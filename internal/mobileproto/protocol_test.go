package mobileproto

import (
	"bufio"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestProductionCorpusManifestAndFreshProcessReconnect(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "mobile-protocol", "v0")
	manifest, err := os.ReadFile(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		fields := strings.Fields(line)
		if len(fields) != 2 {
			t.Fatalf("invalid manifest line %q", line)
		}
		data, err := os.ReadFile(filepath.Join(root, fields[1]))
		if err != nil {
			t.Fatal(err)
		}
		sum := sha256.Sum256(data)
		if got := hex.EncodeToString(sum[:]); got != fields[0] {
			t.Fatalf("%s checksum = %s, want %s", fields[1], got, fields[0])
		}
	}

	file, err := os.Open(filepath.Join(root, "ssh-fresh-reconnect.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := file.Close(); err != nil {
			t.Errorf("close corpus: %v", err)
		}
	})
	var hellos []Response
	var resolved Target
	var reconnectRequest Request
	var reconnected Response
	mutationAcks := 0
	scanner := bufio.NewScanner(file)
	scanner.Buffer(make([]byte, 64<<10), MaxLineBytes)
	for scanner.Scan() {
		var record struct {
			Direction string          `json:"direction"`
			Message   json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			t.Fatal(err)
		}
		if record.Direction == "client_to_server" {
			var request Request
			if err := json.Unmarshal(record.Message, &request); err != nil || request.Version != Version {
				t.Fatalf("request: %s, %v", record.Message, err)
			}
			if request.Type == RequestReconnect {
				reconnectRequest = request
			}
			continue
		}
		var response Response
		if err := json.Unmarshal(record.Message, &response); err != nil || response.Version != Version {
			t.Fatalf("response: %s, %v", record.Message, err)
		}
		switch response.Type {
		case ResponseHello:
			hellos = append(hellos, response)
		case ResponseResolved:
			resolved = *response.Target
		case ResponseReconnected:
			reconnected = response
		case ResponseControl, ResponseAccepted, ResponseResized, ResponseHeartbeat, ResponseReleased:
			mutationAcks++
			if response.OperationSequence == 0 || response.OutputSequence == 0 || response.ResetGeneration == 0 {
				t.Fatalf("mutation acknowledgment omitted fence: %+v", response)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	if len(hellos) != 2 || hellos[0].APIInstance == hellos[1].APIInstance {
		t.Fatalf("hello instances = %+v", hellos)
	}
	if reconnectRequest.ExpectedTarget == nil || *reconnectRequest.ExpectedTarget != resolved.Identity() {
		t.Fatalf("reconnect identity = %+v, resolved = %+v", reconnectRequest.ExpectedTarget, resolved.Identity())
	}
	if reconnected.Target == nil || reconnected.Target.Handle == resolved.Handle ||
		reconnected.Target.Identity() != resolved.Identity() || reconnected.AttachmentGeneration != 2 {
		t.Fatalf("reconnected = %+v", reconnected)
	}
	if mutationAcks < 6 {
		t.Fatalf("mutation acknowledgments = %d", mutationAcks)
	}
}
