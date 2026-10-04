package mobileproto

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestV1PresenceRequiresAllFieldsAndBounds(t *testing.T) {
	for _, input := range []string{`{}`, `{"focused":true,"visible":true,"idle_ms":0,"columns":80}`, `{"focused":null,"visible":true,"idle_ms":0,"columns":80,"rows":24}`, `{"focused":true,"visible":true,"idle_ms":-1,"columns":80,"rows":24}`, `{"focused":true,"visible":true,"idle_ms":0,"columns":513,"rows":24}`, `{"focused":true,"visible":true,"idle_ms":0,"columns":80,"rows":24,"extra":true}`} {
		var p Presence
		if err := json.Unmarshal([]byte(input), &p); err == nil {
			t.Fatalf("accepted %s", input)
		}
	}
	var p Presence
	if err := json.Unmarshal([]byte(`{"focused":false,"visible":false,"idle_ms":0,"columns":80,"rows":24}`), &p); err != nil {
		t.Fatal(err)
	}
}

func TestV1CapabilitiesAndViewerValidation(t *testing.T) {
	if ValidateClientHello(&ClientCapabilities{CoalescedFrames: true}, nil) == nil {
		t.Fatal("coalescing requires reset-free")
	}
	for _, v := range []Viewer{{Kind: "bad", Label: "Phone"}, {Kind: "ios", Label: ""}, {Kind: "browser", Label: "hello\nworld"}, {Kind: "cli", Label: strings.Repeat("x", 129)}} {
		if ValidateClientHello(nil, &v) == nil {
			t.Fatalf("accepted %+v", v)
		}
	}
	if err := ValidateClientHello(&ClientCapabilities{CoalescedFrames: true, ResetFreeFrames: true}, &Viewer{Kind: "ios", Label: "iPhone"}); err != nil {
		t.Fatal(err)
	}
}

func TestV1FixtureManifestAndContract(t *testing.T) {
	root := filepath.Join("..", "..", "testdata", "ui-api", "v1")
	manifest, err := os.ReadFile(filepath.Join(root, "SHA256SUMS"))
	if err != nil {
		t.Fatal(err)
	}
	for _, line := range strings.Split(strings.TrimSpace(string(manifest)), "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			t.Fatalf("manifest %q", line)
		}
		data, e := os.ReadFile(filepath.Join(root, f[1]))
		if e != nil {
			t.Fatal(e)
		}
		hash := sha256.Sum256(data)
		if hex.EncodeToString(hash[:]) != f[0] {
			t.Fatalf("stale checksum %s", f[1])
		}
	}
	data, err := os.ReadFile(filepath.Join(root, "terminal.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	scanner := bufio.NewScanner(bytes.NewReader(data))
	seen := map[string]bool{}
	for scanner.Scan() {
		var r struct {
			Direction string          `json:"direction"`
			Message   json.RawMessage `json:"message"`
		}
		if err := json.Unmarshal(scanner.Bytes(), &r); err != nil {
			t.Fatal(err)
		}
		if r.Direction == "client_to_server" {
			var q Request
			d := json.NewDecoder(bytes.NewReader(r.Message))
			d.DisallowUnknownFields()
			if err := d.Decode(&q); err != nil {
				t.Fatal(err)
			}
			if q.Type == RequestHello {
				if err := ValidateClientHello(q.Capabilities, q.Viewer); err != nil {
					t.Fatal(err)
				}
			}
			if q.Type == RequestPresence {
				if err := ValidatePresence(q.Presence); err != nil {
					t.Fatal(err)
				}
			}
			seen[q.Type] = true
		} else {
			var q Response
			d := json.NewDecoder(bytes.NewReader(r.Message))
			d.DisallowUnknownFields()
			if err := d.Decode(&q); err != nil {
				t.Fatal(err)
			}
			if q.Type == ResponseFrame && (!q.ResetFree || !q.Coalesced) {
				t.Fatal("v1 fixture missing flags")
			}
			seen[q.Type] = true
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	for _, kind := range []string{RequestHello, RequestPresence, RequestPaste, RequestHeartbeat, ResponseHolder, ResponseFrame, ResponseAccepted} {
		if !seen[kind] {
			t.Fatalf("missing fixture %s", kind)
		}
	}
}
