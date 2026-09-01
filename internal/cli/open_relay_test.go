package cli

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hostserve"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/uirequest"
)

func TestOpenSessionsFlagExclusiveWithShellAndProject(t *testing.T) {
	for _, args := range [][]string{
		{"open", "--sessions", "--shell", "x", "README.md"},
		{"open", "--sessions", "row", "--project", "sidecar", "README.md"},
		{"open", "--sessions=row", "--shell", "x", "README.md"},
	} {
		var out, errOut bytes.Buffer
		handled, code := Run(args, &out, &errOut)
		if !handled || code != 2 {
			t.Fatalf("Run(%v) = handled %v code %d, want usage 2", args, handled, code)
		}
		combined := out.String() + errOut.String()
		if !strings.Contains(combined, "--sessions") {
			t.Fatalf("Run(%v) output %q does not mention --sessions", args, combined)
		}
	}
}

func TestOpenSessionsWritesSessionsOrigin(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	writeProjectShell(t, stateDir, "sidecar", shellstate.Definition{
		TmuxName: "sidecar-sh-sidecar-1", DisplayName: "active task",
	})
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := uirequest.Announce(stateDir, uirequest.Instance{
		PID: os.Getpid(), ProjectKey: "sidecar", Project: "sidecar", WorkDir: workDir,
	}); err != nil {
		t.Fatal(err)
	}

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--sessions", "--wait", "0", "README.md"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("open --sessions = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	req := readWrittenRequest(t, stateDir)
	if !req.Origin.Sessions {
		t.Fatalf("origin.sessions = false: %+v", req.Origin)
	}
	if req.Origin.TmuxSession != "" {
		t.Fatalf("TmuxSession = %q, want empty for --sessions", req.Origin.TmuxSession)
	}
	if req.Action != uirequest.ActionOpen || req.Target.Kind != uirequest.TargetKindFile {
		t.Fatalf("request = %+v", req)
	}
}

func TestOpenFastRefusesMissingViewerPresence(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	writeProjectShell(t, stateDir, "sidecar", shellstate.Definition{
		TmuxName: "sidecar-sh-sidecar-1", DisplayName: "active task", Namespace: "/tmp/sock", WorkDir: workDir,
	})
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	original := sessionLeaseOwner
	t.Cleanup(func() { sessionLeaseOwner = original })
	sessionLeaseOwner = func(string) string { return "laptop-99" }

	started := time.Now()
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--shell", "active task", "--wait", "5s", "README.md"}, &out, &errOut)
	elapsed := time.Since(started)
	if !handled || code != 4 {
		t.Fatalf("missing presence = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	if elapsed > time.Second {
		t.Fatalf("fast-refuse waited %s", elapsed)
	}
	combined := out.String() + errOut.String()
	if !strings.Contains(combined, "laptop-99") || !strings.Contains(combined, "cannot receive pane requests") {
		t.Fatalf("refusal = %q", combined)
	}
	if _, err := os.Stat(filepath.Join(stateDir, "requests")); !os.IsNotExist(err) {
		entries, _ := os.ReadDir(filepath.Join(stateDir, "requests"))
		if len(entries) != 0 {
			t.Fatalf("fast-refuse still wrote a request: %v", entries)
		}
	}
}

func TestOpenWaitsWhenViewerPresenceIsLive(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	workDir := t.TempDir()
	writeProjectMeta(t, stateDir, "sidecar", workDir)
	writeProjectShell(t, stateDir, "sidecar", shellstate.Definition{
		TmuxName: "sidecar-sh-sidecar-1", DisplayName: "active task", Namespace: "/tmp/sock", WorkDir: workDir,
	})
	if err := os.WriteFile(filepath.Join(workDir, "README.md"), []byte("x\n"), 0644); err != nil {
		t.Fatal(err)
	}
	writeLiveViewerPresence(t, stateDir, "laptop-99")
	original := sessionLeaseOwner
	t.Cleanup(func() { sessionLeaseOwner = original })
	sessionLeaseOwner = func(string) string { return "laptop-99" }

	var out, errOut bytes.Buffer
	handled, code := Run([]string{"open", "--shell", "active task", "--wait", "0", "README.md"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("live presence = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	req := readWrittenRequest(t, stateDir)
	if req.Action != uirequest.ActionOpen {
		t.Fatalf("request = %+v", req)
	}
}

func TestRequestAckWritesAckFile(t *testing.T) {
	_, stateDir := setupIsolatedCLI(t)
	req := uirequest.Request{ID: "req-ack-1", Action: uirequest.ActionOpen, CreatedAt: time.Now().UTC(), TTLMs: 5000}
	if _, err := uirequest.WriteRequest(stateDir, req); err != nil {
		t.Fatal(err)
	}
	var out, errOut bytes.Buffer
	handled, code := Run([]string{"request", "ack", "--id", req.ID, "--action", "open", "--status", "opened", "--surface", "shell:foo", "--json"}, &out, &errOut)
	if !handled || code != 0 {
		t.Fatalf("request ack = handled %v code %d stderr %q", handled, code, errOut.String())
	}
	var result uirequest.AckResult
	if err := json.Unmarshal(out.Bytes(), &result); err != nil {
		t.Fatalf("json: %v (%s)", err, out.String())
	}
	if !result.ValidRemoteResult() || result.ID != req.ID || result.Status != uirequest.StatusOpened {
		t.Fatalf("result = %+v", result)
	}
	acks, err := uirequest.ReadAcks(stateDir, req.ID, req.Action)
	if err != nil || len(acks) != 1 || acks[0].Status != uirequest.StatusOpened {
		t.Fatalf("acks = %+v err=%v", acks, err)
	}
}

func writeLiveViewerPresence(t *testing.T, stateDir, instance string) {
	t.Helper()
	got, ok := hostserve.LookupLiveViewer(stateDir, instance, time.Now())
	if ok {
		t.Fatalf("presence already live: %+v", got)
	}
	dir := filepath.Join(stateDir, "viewers")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	body := []byte(`{"instance":"` + instance + `","pid":99,"capabilities":["uiRequestRelayV1"],"expiresAt":"` + time.Now().Add(time.Minute).UTC().Format(time.RFC3339Nano) + `"}`)
	if err := os.WriteFile(filepath.Join(dir, instance+".json"), body, 0o644); err != nil {
		t.Fatal(err)
	}
}
