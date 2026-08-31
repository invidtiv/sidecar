package workspace

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/shellstate"
)

// newReapManifest builds a manifest holding three shells all marked live under
// one tmux server, which is the state a running Sidecar leaves behind.
func newReapManifest(t *testing.T) *ShellManifest {
	t.Helper()
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &ShellManifest{Version: manifestVersion, path: path}
	for _, name := range []string{"sidecar-sh-one", "sidecar-sh-two", "sidecar-sh-three"} {
		if err := m.AddShell(ShellDefinition{TmuxName: name, DisplayName: name, Namespace: "/tmp/socket"}); err != nil {
			t.Fatal(err)
		}
		if err := m.MarkRestoreEligible(name, "pid=100", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	return m
}

// TestReapShellSurvivesAServerDeath is this surface's copy of the incident
// regression, and it belongs here as well as in shellstate because this surface
// has its own serializer and its own lock.
//
// The project plugin reaps per shell, driven by a capture failing, with no
// listing to apply an empty-listing guard to. A dead tmux server therefore
// arrives here as three independent "this one is gone" verdicts in a row — which
// is precisely the sequence that used to leave shells.json empty.
func TestReapShellSurvivesAServerDeath(t *testing.T) {
	m := newReapManifest(t)
	for _, name := range []string{"sidecar-sh-one", "sidecar-sh-two", "sidecar-sh-three"} {
		outcome, err := m.ReapShell(name, shellstate.ServerGone()) // no tmux server is running
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		if outcome != shellstate.ReapPreserved {
			t.Fatalf("%s: outcome %s, want %s", name, outcome, shellstate.ReapPreserved)
		}
	}
	reloaded, err := LoadShellManifest(m.path)
	if err != nil {
		t.Fatal(err)
	}
	if len(reloaded.Shells) != 3 {
		t.Fatalf("a server death left %d of 3 shell records on disk", len(reloaded.Shells))
	}
	if len(reloaded.Tombstones) != 0 {
		t.Fatalf("a server death must tombstone nothing, got %d", len(reloaded.Tombstones))
	}
	for _, s := range reloaded.Shells {
		if s.Restore == nil || !s.Restore.Eligible {
			t.Errorf("%s survived but is not a restore candidate", s.TmuxName)
		}
	}
}

// TestReapShellPreservesAcrossAReplacedServer covers the case where a new server
// is already running by the time the verdict lands.
func TestReapShellPreservesAcrossAReplacedServer(t *testing.T) {
	m := newReapManifest(t)
	outcome, err := m.ReapShell("sidecar-sh-one", shellstate.ServerRunning("pid=200"))
	if err != nil {
		t.Fatal(err)
	}
	if outcome != shellstate.ReapPreserved {
		t.Fatalf("outcome %s, want %s", outcome, shellstate.ReapPreserved)
	}
	if m.FindShell("sidecar-sh-one") == nil {
		t.Fatal("a shell that died with its old server had its record deleted")
	}
}

// TestReapShellStillTombstonesAClosedTerminal proves the fix did not turn
// reaping off. A shell that exited inside the server that is still running is
// still tombstoned, and still recoverable by `sidecar shell restore`.
func TestReapShellStillTombstonesAClosedTerminal(t *testing.T) {
	m := newReapManifest(t)
	outcome, err := m.ReapShell("sidecar-sh-one", shellstate.ServerRunning("pid=100"))
	if err != nil {
		t.Fatal(err)
	}
	if outcome != shellstate.ReapTombstoned {
		t.Fatalf("outcome %s, want %s", outcome, shellstate.ReapTombstoned)
	}
	if m.FindShell("sidecar-sh-one") != nil {
		t.Fatal("the closed shell is still listed")
	}
	if len(m.Tombstones) != 1 || m.Tombstones[0].TmuxName != "sidecar-sh-one" {
		t.Fatalf("the closed shell must stay recoverable, tombstones: %+v", m.Tombstones)
	}
}

// TestMarkRestoreEligibleWritesOnlyOnTransition is the write-amplification guard
// for the marker on this surface, where it is reached from every capture.
func TestMarkRestoreEligibleWritesOnlyOnTransition(t *testing.T) {
	path := filepath.Join(t.TempDir(), ".sidecar", "shells.json")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	m := &ShellManifest{Version: manifestVersion, path: path}
	if err := m.AddShell(ShellDefinition{TmuxName: "sidecar-sh-one", Namespace: "/tmp/socket"}); err != nil {
		t.Fatal(err)
	}
	if err := m.MarkRestoreEligible("sidecar-sh-one", "pid=100", time.Now().UTC()); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 5; i++ {
		if err := m.MarkRestoreEligible("sidecar-sh-one", "pid=100", time.Now().UTC()); err != nil {
			t.Fatal(err)
		}
	}
	after, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(after) {
		t.Fatal("repeated marking of an unchanged fact rewrote shells.json")
	}
}
