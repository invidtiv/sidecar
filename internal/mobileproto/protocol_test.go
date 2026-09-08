package mobileproto

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/base64"
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

func TestProductionCatalogCorpusCarriesSafeSelectionsAndSharedQueries(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "mobile-protocol", "v0", "sessions-catalog.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema string `json:"schema"`
		Cases  []struct {
			ID       string   `json:"id"`
			Request  Request  `json:"request"`
			Response Response `json:"response"`
		} `json:"cases"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "sidecar.mobile.catalog.v0" || len(fixture.Cases) != 12 {
		t.Fatalf("catalog fixture header = %q, cases=%d", fixture.Schema, len(fixture.Cases))
	}
	wantCases := map[string]bool{"activity": true, "project": true, "recent": true, "name": true, "provider-codex": true, "state-ready": true, "state-ambiguous": true, "state-stale": true, "state-unsupported": true, "search-local-shell": true, "hide-idle-sessions": true, "show-idle-sessions": true}
	states := make(map[string]bool)
	ready := 0
	for _, test := range fixture.Cases {
		delete(wantCases, test.ID)
		if test.Request.Version != Version || test.Request.Type != RequestSessions || test.Request.CatalogQuery == nil || test.Response.Type != ResponseSessions || test.Response.Catalog == nil || test.Response.RequestID != test.Request.RequestID {
			t.Fatalf("invalid %s exchange: request=%+v response=%+v", test.ID, test.Request, test.Response)
		}
		catalog := test.Response.Catalog
		if catalog.HubID == "" || catalog.OwnerHostID == "" || catalog.OwnerConfigGeneration == "" || catalog.Generation == "" || catalog.Total > MaxCatalogRows {
			t.Fatalf("invalid %s catalog authority: %+v", test.ID, catalog)
		}
		noSessionRows := 0
		for _, section := range catalog.Sections {
			for _, row := range section.Rows {
				if row.Group == "No Session" {
					noSessionRows++
				}
				states[row.AttachState] = true
				if row.AttachmentReady {
					ready++
					if row.Target == "" || row.CandidateGeneration == "" || row.ExpectedTarget == nil || row.ExpectedTarget.OwnerHostID != row.OwnerHostID {
						t.Fatalf("ready row lacks exact selection authority: %+v", row)
					}
				} else if row.ExpectedTarget != nil {
					t.Fatalf("refused row carries target authority: %+v", row)
				}
			}
		}
		switch test.ID {
		case "hide-idle-sessions":
			if test.Request.CatalogQuery.ShowIdleSessions == nil || *test.Request.CatalogQuery.ShowIdleSessions || noSessionRows != 0 {
				t.Fatalf("hide idle case = query %+v no_session_rows=%d", test.Request.CatalogQuery, noSessionRows)
			}
		case "show-idle-sessions":
			if test.Request.CatalogQuery.ShowIdleSessions == nil || !*test.Request.CatalogQuery.ShowIdleSessions || noSessionRows == 0 {
				t.Fatalf("show idle case = query %+v no_session_rows=%d", test.Request.CatalogQuery, noSessionRows)
			}
		}
	}
	if len(wantCases) != 0 || ready == 0 {
		t.Fatalf("missing cases=%v ready=%d", wantCases, ready)
	}
	for _, state := range []string{"ready", "unavailable", "ambiguous", "stale", "unknown", "unsupported"} {
		if !states[state] {
			t.Fatalf("catalog corpus does not cover attach state %q", state)
		}
	}
}

func TestProductionHistoryCorpusCarriesFrozenAuthoritativeSnapshot(t *testing.T) {
	path := filepath.Join("..", "..", "testdata", "mobile-protocol", "v0", "history-snapshot.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct {
		Schema   string   `json:"schema"`
		Request  Request  `json:"request"`
		Response Response `json:"response"`
		Expected struct {
			HistoryRows    []string `json:"history_rows"`
			LiveRows       []string `json:"live_rows"`
			NoFinalAdvance bool     `json:"no_final_line_advance"`
		} `json:"expected"`
	}
	if err := json.Unmarshal(data, &fixture); err != nil {
		t.Fatal(err)
	}
	if fixture.Schema != "sidecar.mobile.history-snapshot.v0" || fixture.Request.Type != RequestHistory || fixture.Response.Type != ResponseHistory {
		t.Fatalf("history fixture header = %+v", fixture)
	}
	if fixture.Request.HistoryRows < 1 || fixture.Request.HistoryRows > MaxHistoryRows || fixture.Response.History == nil || fixture.Response.Geometry == nil {
		t.Fatalf("history fixture bounds = %+v", fixture)
	}
	history := fixture.Response.History
	if history.HistoryRows != len(fixture.Expected.HistoryRows) || fixture.Response.Geometry.Rows != len(fixture.Expected.LiveRows) ||
		history.EndLine-history.StartLine != history.HistoryRows || !fixture.Expected.NoFinalAdvance {
		t.Fatalf("history fixture split = response=%+v expected=%+v", fixture.Response, fixture.Expected)
	}
	decoded, err := base64.StdEncoding.DecodeString(history.RenderVTBase64)
	if err != nil || len(decoded) == 0 || len(decoded) > MaxHistoryBytes || bytes.HasSuffix(decoded, []byte("\r\n")) {
		t.Fatalf("history fixture payload bytes=%d err=%v", len(decoded), err)
	}
	if fixture.Response.OutputSequence != fixture.Request.LastOutputSequence || fixture.Response.ResetGeneration != fixture.Request.LastResetGeneration {
		t.Fatalf("history fixture checkpoint = request=%+v response=%+v", fixture.Request, fixture.Response)
	}
	caps := DefaultCapabilities()
	if !caps.HistorySnapshots || caps.MaximumHistoryRows != MaxHistoryRows || caps.MaximumHistoryBytes != MaxHistoryBytes {
		t.Fatalf("history capability = %+v", caps)
	}
}
