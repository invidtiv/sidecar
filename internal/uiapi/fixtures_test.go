package uiapi

import (
	"context"
	"crypto/elliptic"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/marcus/sidecar/internal/mobile"
	"github.com/marcus/sidecar/internal/mobileproto"
)

func fixtureDir() string { return filepath.Join("..", "..", "testdata", "ui-api", "v0") }
func TestUIAPIFixtureCorpus(t *testing.T) {
	dir := fixtureDir()
	now := time.Date(2026, 10, 3, 0, 0, 0, 0, time.UTC)
	identity := mobile.FixtureIdentity("fixture", "local:fixture", "fixture-config", "fixture-project", "fixture-echo", "%1")
	row := mobileproto.CatalogRow{ID: "fixture-shell", OwnerHostID: identity.OwnerHostID, ProjectID: "fixture-project", ProjectName: "Fixture project", WorkspaceID: identity.WorkspaceID, WorkspaceKind: "shell", DisplayName: "Echo terminal", Path: "/workspace/fixture", Provider: "codex", Status: "working", Group: "Working", Session: identity.Session, Pane: identity.Pane, Target: identity.Session, ExpectedTarget: &identity, AttachState: "ready", ObservedAt: now.Format(time.RFC3339), ChangedAt: now.Format(time.RFC3339), Live: true, SemanticStatus: true, AttachmentReady: true}
	catalog := mobileproto.CatalogSnapshot{Generation: "fixture-generation", ObservedAt: row.ObservedAt, HubID: identity.HubID, OwnerHostID: identity.OwnerHostID, OwnerConfigGeneration: identity.OwnerConfigGeneration, Query: mobileproto.CatalogQuery{Sort: "project"}, Hosts: []mobileproto.CatalogHost{{ID: identity.OwnerHostID, Name: "Fixture host", State: "online", Local: true}}, Sections: []mobileproto.CatalogSection{{Key: "fixture-project", Title: "Fixture project", Rows: []mobileproto.CatalogRow{row}}}, Failures: []mobileproto.CatalogFailure{}, Total: 1}
	values := map[string]any{
		"session-proof.json": []any{
			map[string]any{"method": "POST", "path": "/api/v0/pairing/session-proof", "listener": "browser", "origin": "http://127.0.0.1:7861", "request": SessionProofChallengeRequest{RegistrationID: browserRegistrationID("http://127.0.0.1:7861", fixtureBrowserPublicKey())}, "response": SessionProofChallenge{Nonce: "synthetic-nonce", Timestamp: now.UnixMilli(), ExpiresAt: now.Add(pairingCodeTTL)}},
			map[string]any{"method": "POST", "path": "/api/v0/pairing/session-proof/verify", "listener": "browser", "origin": "http://127.0.0.1:7861", "request": SessionProofRequest{RegistrationID: browserRegistrationID("http://127.0.0.1:7861", fixtureBrowserPublicKey()), Nonce: "synthetic-nonce", Timestamp: now.UnixMilli(), Signature: fixtureBrowserSignature}, "response": SessionToken{Token: "synthetic-memory-token", ExpiresAt: now.Add(browserBearerTTL)}},
		},
		"hello.json":    Hello{APIVersion: 0, APIInstance: "api_fixture", ServerVersion: "fixture", Capabilities: []string{"sessions", "status", "terminal", "ws_tickets", "events"}, Terminal: TerminalProtocol{Protocol: "mobile", Version: 0}},
		"sessions.json": catalog,
		"status.json":   Status{APIVersion: 0, APIInstance: "api_fixture", ServerVersion: "fixture", PID: 4242, StartedAt: now, Listeners: []ListenerInfo{{Name: ListenerLocal, Network: "unix", Address: "/tmp/fixture/api.sock"}, {Name: ListenerBrowser, Network: "tcp", Address: "127.0.0.1:7861"}}, Clients: []ClientInfo{}, Terminals: []TerminalInfo{}},
		"error.json":    ErrorBody{Error: ErrorDetail{Code: CodeUnauthenticated, Message: "Pair this browser with sidecar api open."}},
		"pairing.json":  []any{map[string]any{"method": "POST", "path": "/api/v0/pairing/codes", "listener": "local", "request": PairingCodeRequest{Next: "/s/fixture"}, "response": PairingCode{Code: "synthetic-code", URL: "http://127.0.0.1:7861/pair#code=synthetic-code&next=%2Fs%2Ffixture", ExpiresAt: now.Add(time.Minute)}}, map[string]any{"method": "POST", "path": "/api/v0/pairing/exchange", "listener": "browser", "origin": "http://127.0.0.1:7861", "request": PairingExchangeRequest{Code: "synthetic-code", Next: "/s/fixture", PublicKey: fixtureBrowserPublicKey()}, "response": PairingExchange{RegistrationID: browserRegistrationID("http://127.0.0.1:7861", fixtureBrowserPublicKey()), Token: "synthetic-memory-token", ExpiresAt: now.Add(browserBearerTTL), Next: "/s/fixture"}}},
	}
	if os.Getenv("UPDATE_UI_API_FIXTURES") == "1" {
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		for name, value := range values {
			data, err := json.MarshalIndent(value, "", "  ")
			if err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(dir, name), append(data, '\n'), 0644); err != nil {
				t.Fatal(err)
			}
		}
	}
	for name, value := range values {
		data, err := json.MarshalIndent(value, "", "  ")
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(append(data, '\n')) {
			t.Fatalf("stale fixture %s: run ./scripts/update-ui-api-contract.sh", name)
		}
	}
	names, err := filepath.Glob(filepath.Join(dir, "*.json*"))
	if err != nil {
		t.Fatal(err)
	}
	sort.Strings(names)
	var sums strings.Builder
	for _, name := range names {
		data, err := os.ReadFile(name)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Fprintf(&sums, "%x  %s\n", sha256.Sum256(data), filepath.Base(name))
	}
	sumPath := filepath.Join(dir, "SHA256SUMS")
	if os.Getenv("UPDATE_UI_API_FIXTURES") == "1" {
		if err := os.WriteFile(sumPath, []byte(sums.String()), 0644); err != nil {
			t.Fatal(err)
		}
	}
	got, err := os.ReadFile(sumPath)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != sums.String() {
		t.Fatal("UI API SHA256SUMS is stale: run ./scripts/update-ui-api-contract.sh")
	}
	if _, err := LoadFixtures(dir); err != nil {
		t.Fatal(err)
	}
}

func TestFixtureServerTerminalUsesRealOrderingAndGuards(t *testing.T) {
	backend, err := LoadFixtures(fixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "u1b-test-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	s, err := Start(Options{StateDir: root, Port: 0, Backend: backend, FixtureStatus: &backend.Status})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := s.Shutdown(ctx); err != nil {
			t.Error(err)
		}
	})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	client := NewLocalClientForSocket(s.Endpoint())
	conn, _, err := websocket.Dial(ctx, strings.Replace(s.BrowserURL(), "http://", "ws://", 1)+terminalPath, &websocket.DialOptions{HTTPHeader: http.Header{"Origin": []string{s.BrowserURL()}}})
	if err != nil {
		t.Fatal(err)
	}
	_, _, err = conn.Read(ctx)
	if websocket.CloseStatus(err) != CloseUnauthenticated {
		t.Fatalf("fixture auth bypass: %v", err)
	}
	_ = conn.CloseNow()
	conn, _, err = websocket.Dial(ctx, strings.Replace(client.URL(terminalPath), "http://", "ws://", 1), &websocket.DialOptions{HTTPClient: client.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = conn.CloseNow() }()
	var transcript strings.Builder
	record := func(direction string, message any) {
		data, _ := json.Marshal(map[string]any{"direction": direction, "message": message})
		transcript.Write(data)
		transcript.WriteByte('\n')
	}
	send := func(req mobileproto.Request) {
		t.Helper()
		req.Version = mobileproto.Version
		data, _ := json.Marshal(req)
		record("client", req)
		if err := conn.Write(ctx, websocket.MessageText, data); err != nil {
			t.Fatal(err)
		}
	}
	read := func() mobileproto.Response {
		t.Helper()
		_, data, err := conn.Read(ctx)
		if err != nil {
			t.Fatal(err)
		}
		var response mobileproto.Response
		if err := json.Unmarshal(data, &response); err != nil {
			t.Fatal(err)
		}
		record("server", response)
		return response
	}
	send(mobileproto.Request{Type: "hello", RequestID: "hello"})
	if r := read(); r.Type != "hello" {
		t.Fatal(r)
	}
	send(mobileproto.Request{Type: "sessions", RequestID: "sessions", CatalogQuery: &mobileproto.CatalogQuery{Sort: "name", Search: "Echo"}})
	if r := read(); r.Type != "sessions" || r.Catalog == nil || r.Catalog.Total != 1 {
		t.Fatal(r)
	}
	expected := backend.targets["fixture-echo"]
	send(mobileproto.Request{Type: "resolve", RequestID: "resolve", Target: "fixture-echo", ExpectedTarget: &expected})
	resolved := read()
	if resolved.Target == nil {
		t.Fatal(resolved)
	}
	send(mobileproto.Request{Type: "open", RequestID: "open", TargetHandle: resolved.Target.Handle, AttachmentID: "fixture-client"})
	opened := read()
	if opened.Type != "opened" {
		t.Fatal(opened)
	}
	frame := read()
	if frame.Type != "frame" || frame.OutputSequence != 1 || frame.ResetGeneration != 1 {
		t.Fatal(frame)
	}
	mutate := func(kind, id string, seq uint64, cols, rows int, data string) {
		send(mobileproto.Request{Type: kind, RequestID: id, AttachmentHandle: opened.AttachmentHandle, OperationSequence: seq, LastResetGeneration: frame.ResetGeneration, LastOutputSequence: frame.OutputSequence, Columns: cols, Rows: rows, DataBase64: base64.StdEncoding.EncodeToString([]byte(data))})
	}
	mutate("control", "control", 1, 40, 10, "")
	control := read()
	if control.Type != "control" || control.ResetGeneration != 2 {
		t.Fatal(control)
	}
	if r := read(); r.Type != "reset" {
		t.Fatal(r)
	}
	frame = read()
	if frame.Geometry.Columns != 40 || frame.Type != "frame" {
		t.Fatal(frame)
	}
	mutate("input", "input", 2, 0, 0, "FIXTURE_ECHO")
	accepted := read()
	if accepted.Type != "accepted" {
		t.Fatal(accepted)
	}
	frame = read()
	vt, err := base64.StdEncoding.DecodeString(frame.RenderVTBase64)
	if err != nil || !strings.Contains(string(vt), "FIXTURE_ECHO") {
		t.Fatalf("echo missing: %q %v", vt, err)
	}
	mutate("input", "gap", 4, 0, 0, "MUST_NOT_ECHO")
	gap := read()
	if gap.Error == nil || gap.Error.Code != mobileproto.ErrorOperationOrder {
		t.Fatal(gap)
	}
	// A refused operation does not consume sequence 3; re-control with 3 works.
	mutate("control", "reclaim", 3, 40, 10, "")
	if r := read(); r.Type != "control" {
		t.Fatal(r)
	}
	mutate("release", "release", 4, 0, 0, "")
	if r := read(); r.Type != "released" {
		t.Fatal(r)
	}
	send(mobileproto.Request{Type: "close", RequestID: "close", AttachmentHandle: opened.AttachmentHandle})
	if r := read(); r.Type != "closed" {
		t.Fatal(r)
	}
	// A fresh connection reconstructs the catalog identity and never replays input.
	if err := conn.Close(websocket.StatusNormalClosure, "reconnect proof"); err != nil {
		t.Fatal(err)
	}
	conn, _, err = websocket.Dial(ctx, strings.Replace(client.URL(terminalPath), "http://", "ws://", 1), &websocket.DialOptions{HTTPClient: client.HTTPClient()})
	if err != nil {
		t.Fatal(err)
	}
	send(mobileproto.Request{Type: "hello", RequestID: "hello"})
	if r := read(); r.Type != "hello" {
		t.Fatal(r)
	}
	send(mobileproto.Request{Type: "reconnect", RequestID: "reconnect", Target: "fixture-echo", ExpectedTarget: &expected, PreviousAttachmentGeneration: 1, AttachmentID: "fixture-client"})
	reconnected := read()
	if reconnected.Type != "reconnected" || reconnected.AttachmentGeneration != 2 {
		t.Fatal(reconnected)
	}
	frame = read()
	if frame.Type != "frame" || frame.OutputSequence != 1 || frame.ResetGeneration != 1 || frame.Control {
		t.Fatal(frame)
	}
	send(mobileproto.Request{Type: "close", RequestID: "close", AttachmentHandle: reconnected.AttachmentHandle})
	if r := read(); r.Type != "closed" {
		t.Fatal(r)
	}
	normalized := regexp.MustCompile(`(api|target|attachment)_[a-f0-9]{32}`).ReplaceAllString(transcript.String(), "${1}_fixture")
	path := filepath.Join(fixtureDir(), "terminal.jsonl")
	if os.Getenv("UPDATE_UI_API_FIXTURES") == "1" {
		if err := os.WriteFile(path, []byte(normalized), 0644); err != nil {
			t.Fatal(err)
		}
	}
	want, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(want) != normalized {
		t.Fatal("terminal transcript differs from the real Service; run ./scripts/update-ui-api-contract.sh")
	}
}

func TestFixtureCatalogUsesSharedQueryAndRejectsMalformedAuthority(t *testing.T) {
	backend, err := LoadFixtures(fixtureDir())
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct {
		query mobileproto.CatalogQuery
		total int
	}{
		{mobileproto.CatalogQuery{Sort: "name", Search: "echo"}, 1},
		{mobileproto.CatalogQuery{Providers: []string{"claude"}}, 0},
		{mobileproto.CatalogQuery{States: []string{"ready"}}, 1},
		{mobileproto.CatalogQuery{Hosts: []string{"absent"}}, 0},
	} {
		got, err := backend.Sessions(context.Background(), tc.query)
		if err != nil || got.Total != tc.total {
			t.Fatalf("%+v: %+v %v", tc.query, got, err)
		}
	}
	if _, err := backend.Sessions(context.Background(), mobileproto.CatalogQuery{Sort: "unknown"}); err == nil {
		t.Fatal("invalid sort accepted")
	}
	dir := t.TempDir()
	for _, name := range []string{"sessions.json", "status.json"} {
		data, err := os.ReadFile(filepath.Join(fixtureDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	var catalog mobileproto.CatalogSnapshot
	data, err := os.ReadFile(filepath.Join(dir, "sessions.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(data, &catalog); err != nil {
		t.Fatal(err)
	}
	catalog.Sections[0].Rows[0].ExpectedTarget.TargetGeneration = "arbitrary-real-target"
	data, err = json.Marshal(catalog)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sessions.json"), data, 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := LoadFixtures(dir); err == nil {
		t.Fatal("non-fixture identity accepted")
	}
}

func TestFixtureLoaderRejectsDocumentBeyondByteBound(t *testing.T) {
	dir := t.TempDir()
	for _, name := range []string{"sessions.json", "status.json"} {
		data, err := os.ReadFile(filepath.Join(fixtureDir(), name))
		if err != nil {
			t.Fatal(err)
		}
		if name == "status.json" {
			// The decoder sees a valid first document and whitespace until
			// the limit, hiding the second document beyond its artificial EOF.
			data = append(data, []byte(strings.Repeat(" ", mobileproto.MaxLineBytes))...)
			data = append(data, []byte(`{"pid":999}`)...)
		}
		if err := os.WriteFile(filepath.Join(dir, name), data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := LoadFixtures(dir); err == nil {
		t.Fatal("oversized fixture with hidden second JSON document accepted")
	}
}

func fixtureBrowserPublicKey() BrowserPublicKey {
	curve := elliptic.P256().Params()
	return BrowserPublicKey{Kty: "EC", Crv: "P-256", X: base64.RawURLEncoding.EncodeToString(curve.Gx.FillBytes(make([]byte, 32))), Y: base64.RawURLEncoding.EncodeToString(curve.Gy.FillBytes(make([]byte, 32)))}
}

// Fixed valid P-256 vector for the synthetic generator-point public key and
// the fixed nonce/timestamp above. This is example material, never a credential.
const fixtureBrowserSignature = "axfR8uEsQkf4vOblY6RA8ncDfYEt6zOg9KE5RdiYwpYaO0QJAFWXamzPiK7epCFH5TubNJwuXLt-rtW_T7JfBw"
