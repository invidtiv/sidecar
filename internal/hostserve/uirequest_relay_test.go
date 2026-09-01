package hostserve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/tty"
)

func writeRelayRequest(t *testing.T, stateDir, id, action, session, fileHostID string) {
	t.Helper()
	dir := filepath.Join(stateDir, "requests")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := fmt.Sprintf(`{
  "version": 1,
  "id": %q,
  "createdAt": %q,
  "ttlMs": 15000,
  "origin": {"tmuxSession": %q, "hostId": %q, "projectKey": "sidecar", "workDir": "/project"},
  "action": %q,
  "target": {"kind": "file", "value": "README.md", "line": 3},
  "payload": {"mode": "get"}
}`, id, time.Now().UTC().Format(time.RFC3339Nano), session, fileHostID, action)
	path := filepath.Join(dir, id+"-"+action+".json")
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

func uiRequests(t *testing.T, raw string) []hostproto.UIRequest {
	t.Helper()
	var out []hostproto.UIRequest
	for _, msg := range decode(t, raw) {
		if msg.Kind == hostproto.KindUIRequest && msg.UIRequest != nil {
			out = append(out, *msg.UIRequest)
		}
	}
	return out
}

func TestServeWritesViewerPresence(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	project := liveProject(t, runner)
	t.Setenv(tty.ViewerInstanceEnv, "laptop-99")

	opts := baseOptions(&out, runner, time.Now)
	opts.Projects = []Project{project}
	opts.Cycles = 1
	opts.LeaseOwner = func(string) string { return "laptop-99" }

	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	path := filepath.Join(config.StateDir(), "viewers", "laptop-99.json")
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("presence file missing: %v", err)
	}
	var got viewerPresence
	if err := json.Unmarshal(data, &got); err != nil {
		t.Fatalf("presence json: %v", err)
	}
	if got.Instance != "laptop-99" || got.PID != 99 {
		t.Errorf("presence identity = %+v", got)
	}
	if len(got.Capabilities) != 1 || got.Capabilities[0] != viewerCapabilityUIRequestRelayV1 {
		t.Errorf("capabilities = %v", got.Capabilities)
	}
	if got.ExpiresAt.Before(time.Now()) {
		t.Errorf("expiresAt %v is not in the future", got.ExpiresAt)
	}
}

func TestLookupLiveViewerExpiresAndRequiresCapability(t *testing.T) {
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)
	t.Setenv(config.IsolationEnv, "1")
	stateDir := config.StateDir()
	now := time.Now()
	if err := refreshViewerPresence(stateDir, "laptop-7", now, time.Minute); err != nil {
		t.Fatal(err)
	}
	got, ok := LookupLiveViewer(stateDir, "laptop-7", now)
	if !ok || !got.HasCapability(ViewerCapabilityUIRequestRelayV1) {
		t.Fatalf("live presence = %+v ok=%v", got, ok)
	}
	if _, ok := LookupLiveViewer(stateDir, "laptop-7", now.Add(2*time.Minute)); ok {
		t.Fatal("expired presence still looked live")
	}
	if _, ok := LookupLiveViewer(stateDir, "missing", now); ok {
		t.Fatal("missing instance looked live")
	}
}

func TestViewerPresenceRefusesRealStateTree(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv(config.IsolationEnv, "1")
	real := filepath.Join(home, ".local", "state", "sidecar")
	err := refreshViewerPresence(real, "laptop-1", time.Now(), time.Second)
	if err == nil {
		t.Fatal("wrote a viewers file into the real state tree")
	}
	if _, statErr := os.Stat(filepath.Join(real, "viewers", "laptop-1.json")); !os.IsNotExist(statErr) {
		t.Fatal("isolation failure still left a viewers file")
	}
}

func TestServeAnnouncesOpenRequestForLeaseHolder(t *testing.T) {
	out := &syncBuilder{}
	runner := &fakeRunner{}
	project := liveProject(t, runner)
	const viewer = "laptop-7"
	t.Setenv(tty.ViewerInstanceEnv, viewer)

	opts := baseOptions(nil, runner, time.Now)
	opts.Out = out
	opts.Projects = []Project{project}
	opts.Cycles = 0
	opts.InventoryEvery = time.Hour
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Hour, time.Hour, time.Hour
	opts.LeaseOwner = func(session string) string {
		if session == "spike-claude" {
			return viewer
		}
		return "other-viewer"
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, opts) }()

	waitFor(t, "the first snapshot", func() bool { return snapshotCount(t, out.String()) == 1 })
	writeRelayRequest(t, config.StateDir(), "req-open-1", "open", "spike-claude", "evil-host")
	waitFor(t, "the ui request announcement", func() bool { return len(uiRequests(t, out.String())) >= 1 })

	got := uiRequests(t, out.String())
	if len(got) != 1 {
		t.Fatalf("announcements = %+v", got)
	}
	if got[0].ID != "req-open-1" || got[0].Action != hostproto.UIRequestActionOpen {
		t.Errorf("announcement = %+v", got[0])
	}
	if got[0].Origin.HostID != "testhost" {
		t.Errorf("HostID = %q, want the connection host, not the request file", got[0].Origin.HostID)
	}
	if got[0].Origin.TmuxSession != "spike-claude" {
		t.Errorf("session = %q", got[0].Origin.TmuxSession)
	}
	if got[0].Target.Value != "README.md" {
		t.Errorf("target = %+v", got[0].Target)
	}
	if snapshotCount(t, out.String()) != 1 {
		t.Error("a request signal started an inventory collection of its own")
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeDoesNotAnnounceForeignLease(t *testing.T) {
	out := &syncBuilder{}
	runner := &fakeRunner{}
	project := liveProject(t, runner)
	t.Setenv(tty.ViewerInstanceEnv, "laptop-7")

	opts := baseOptions(nil, runner, time.Now)
	opts.Out = out
	opts.Projects = []Project{project}
	opts.Cycles = 0
	opts.InventoryEvery = time.Hour
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = 20*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond
	opts.LeaseOwner = func(string) string { return "other-viewer" }
	var cycles atomic.Uint64
	opts.OnCycle = func(generation uint64, _ time.Duration, _ int64) { cycles.Store(generation) }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, opts) }()

	waitFor(t, "the first snapshot", func() bool { return snapshotCount(t, out.String()) == 1 })
	writeRelayRequest(t, config.StateDir(), "req-foreign-1", "open", "spike-claude", "testhost")
	waitFor(t, "a later cycle", func() bool { return cycles.Load() >= 3 })
	if got := uiRequests(t, out.String()); len(got) != 0 {
		t.Fatalf("foreign lease was announced: %+v", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeIgnoresAcksAndOtherActions(t *testing.T) {
	out := &syncBuilder{}
	runner := &fakeRunner{}
	project := liveProject(t, runner)
	const viewer = "laptop-7"
	t.Setenv(tty.ViewerInstanceEnv, viewer)

	opts := baseOptions(nil, runner, time.Now)
	opts.Out = out
	opts.Projects = []Project{project}
	opts.Cycles = 0
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = 20*time.Millisecond, 20*time.Millisecond, 20*time.Millisecond
	opts.LeaseOwner = func(string) string { return viewer }

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, opts) }()

	waitFor(t, "the first snapshot", func() bool { return snapshotCount(t, out.String()) == 1 })

	stateDir := config.StateDir()
	writeRelayRequest(t, stateDir, "req-rename-1", "rename-shell", "spike-claude", "testhost")
	acks := filepath.Join(stateDir, "requests", "req-rename-1-rename-shell.acks")
	if err := os.MkdirAll(acks, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(acks, "inst.json"), []byte(`{"instance":"x"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	writeRelayRequest(t, stateDir, "req-layout-1", "layout", "spike-claude", "testhost")
	waitFor(t, "the layout announcement", func() bool { return len(uiRequests(t, out.String())) >= 1 })

	got := uiRequests(t, out.String())
	if len(got) != 1 || got[0].ID != "req-layout-1" || got[0].Action != hostproto.UIRequestActionLayout {
		t.Fatalf("announcements = %+v", got)
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

func TestServeIsReadOnlyWithViewerRelay(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	project := liveProject(t, runner)
	t.Setenv(tty.ViewerInstanceEnv, "laptop-7")

	opts := baseOptions(&out, runner, time.Now)
	opts.Projects = []Project{project}
	opts.Cycles = 2
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Millisecond, time.Millisecond, time.Millisecond
	opts.InventoryEvery = time.Nanosecond
	opts.LeaseOwner = func(string) string { return "laptop-7" }
	opts.Capture = func(string, int) (string, tty.PaneState, error) {
		return "esc to interrupt\n> working", tty.PaneState{}, nil
	}
	opts.OnCycle = func(generation uint64, _ time.Duration, _ int64) {
		if generation == 1 {
			writeRelayRequest(t, config.StateDir(), "req-ro-1", "open", "spike-claude", "testhost")
		}
	}

	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	forbidden := []string{
		"resize-window", "resize-pane", "send-keys", "kill-session", "kill-pane",
		"kill-server", "set-option", "new-session", "respawn-pane", "rename-session",
		"set-buffer", "paste-buffer", "split-window", "kill-window",
	}
	runner.mu.Lock()
	calls := append([]string(nil), runner.calls...)
	runner.mu.Unlock()
	for _, call := range calls {
		for _, verb := range forbidden {
			if strings.Contains(call, verb) {
				t.Errorf("serve issued a mutating command: %q", call)
			}
		}
	}
	if _, err := os.Stat(filepath.Join(config.StateDir(), "requests", "req-ro-1-open.acks")); !os.IsNotExist(err) {
		t.Fatal("serve wrote an ack file")
	}
}
