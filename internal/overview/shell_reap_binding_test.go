package overview

import (
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// The binding between this surface and the shell writer, tested through the
// real call path rather than through the writer's own arguments.
//
// The reap decision depends entirely on which tmux server the writer is told
// about, and this surface used to tell it `tmuxserver.Socket()` — a socket stat,
// which is Present with pid 0. The writer could not identify that server, read
// every call as "the server died", and so this surface never tombstoned
// anything: a shell the user deliberately closed was preserved, marked as a
// cold-restore candidate, and recreated after the next tmux restart.
//
// The unit tests missed it completely, and it is worth being precise about why,
// because the shape of the miss is more instructive than the bug. They called
// shellliveness.ReapShell directly with a pid-qualified incarnation built in the
// test, so they proved the plumbing carried a server id and never proved this
// surface supplies one. The fixtures also had no ServerPID on their panes, so
// there was nothing for a correct implementation to pick up. These tests fix
// both halves: the panes carry a server pid the way real tmux now reports it,
// and the assertion is about what arrives at the writer from the real path.

const bindingServerPID = 4242

// panesWithServer is a tmux listing that answered, does not contain the shell
// under test, and reports the server pid — which is what `tmux list-panes -a`
// with #{pid} returns in production.
func panesWithServer(pid int) []workspaceinventory.Pane {
	return []workspaceinventory.Pane{{ID: "%9", Session: "unrelated-session", Path: "/tmp", ServerPID: pid}}
}

// capturedServer drives the real reap path to completion and returns the tmux
// identity that actually reached the writer.
func capturedServer(t *testing.T, pid int) tmuxserver.Incarnation {
	t.Helper()
	m := reapModelUnderServer(t, pid)

	var got tmuxserver.Incarnation
	seen := false
	previousForget := forgetShell
	forgetShell = func(_, _, _ string, _ time.Time, server tmuxserver.Incarnation) error {
		got, seen = server, true
		return nil
	}
	t.Cleanup(func() { forgetShell = previousForget })

	driveReap(t, m, pid)
	if !seen {
		t.Fatal("the reap path never reached the writer")
	}
	return got
}

// driveReap runs one full cycle in which tmux answered, reported the server pid,
// and did not list the shell under test — then carries the probe and the write
// through exactly as the runtime would.
func driveReap(t *testing.T, m *Model, pid int) {
	t.Helper()
	m.currentPanes = panesWithServer(pid)
	next := runReap(t, m, m.reapDeadShells())
	runReap(t, m, next)
}

// reapModelUnderServer establishes the shell as alive under a *named* server.
//
// The positive observation has to name the same server the later absence does.
// Observing it alive under an unidentified server and then absent under
// pid=4242 is a server transition, and the tracker correctly clears prior
// liveness across one — a shell never seen alive in the current server is not
// something this surface will act on. Getting that wrong the first time is a
// good sign the fence works.
func reapModelUnderServer(t *testing.T, pid int) *Model {
	t.Helper()
	m := reapModel(t)
	m.currentPanes = []workspaceinventory.Pane{{ID: "%1", Session: reapSession, Path: reapProject, ServerPID: pid}}
	if cmd := m.reapDeadShells(); cmd != nil {
		t.Fatal("a live shell was probed")
	}
	return m
}

// TestReapWriterReceivesAnIdentifiedServer is the regression test for the
// blocker. It asserts the one property the bug violated: what reaches the writer
// must be a server the writer can identify.
func TestReapWriterReceivesAnIdentifiedServer(t *testing.T) {
	stubReap(t, shellliveness.Gone)
	server := capturedServer(t, bindingServerPID)

	state := workspaceops.ServerStateOf(server)
	if !state.Known() {
		t.Fatal("the writer was handed an unidentifiable server; it cannot tell a closed shell from a dead server")
	}
	if !state.Running() {
		t.Fatalf("a live tmux server reached the writer as not running: %+v", server)
	}
	if want := "pid=4242"; state.ID() != want {
		t.Fatalf("server id %q, want %q — the writer must receive the pid the listing reported", state.ID(), want)
	}
}

// TestReapTombstonesAShellClosedInALiveServer is the end-to-end proof through
// the real writer: the record leaves shells.json and becomes a tombstone.
func TestReapTombstonesAShellClosedInALiveServer(t *testing.T) {
	root := seedReapManifest(t, "pid=4242", true)
	stubReap(t, shellliveness.Gone)
	useRealWriter(t)

	driveReap(t, reapModelUnderServer(t, bindingServerPID), bindingServerPID)

	shells, tombs := readReapManifest(t, root)
	if len(shells) != 0 {
		t.Fatalf("a shell closed inside a live server was not tombstoned: %+v", shells)
	}
	if len(tombs) != 1 || tombs[0].TmuxName != reapSession {
		t.Fatalf("the closed shell must stay recoverable, tombstones: %+v", tombs)
	}
}

// TestReapIssuesNoWriteAfterAServerReplacement is what actually protects records
// on this surface when a tmux server is replaced, and it is not the writer.
//
// Reaching the writer's preserve branch from here turns out to be impossible by
// construction, which is worth stating rather than pretending otherwise. This
// surface only probes a shell it has seen alive in the current server, and
// seeing it alive is exactly what re-stamps that shell's marker to that server —
// so if a probe ever happens, the marker names the running server and absence
// really does mean someone closed it. The replacement case is caught one level
// earlier: a changed server identity resets the tracker's liveness, no probe is
// issued, and no write of any kind occurs. The record survives because nothing
// touched it.
//
// The writer's preserve branch still earns its place, for the project workspace
// plugin, which reaps per shell off a capture failing with no listing to reset
// against. Recording where each defence actually applies is the point of this
// test: the earlier version asserted the writer preserved here, and only passed
// in the reviewer's imagination and mine.
func TestReapIssuesNoWriteAfterAServerReplacement(t *testing.T) {
	root := seedReapManifest(t, "pid=1111", false)
	stubReap(t, shellliveness.Gone)
	useRealWriter(t)

	// Alive under the old server, then a cycle under a different one in which
	// the shell is no longer listed.
	m := reapModelUnderServer(t, 1111)
	m.currentPanes = panesWithServer(bindingServerPID)
	if cmd := m.reapDeadShells(); cmd != nil {
		t.Fatal("a server replacement must not produce a probe; prior liveness belonged to the old server")
	}

	shells, tombs := readReapManifest(t, root)
	if len(shells) != 1 {
		t.Fatalf("a shell that died with its old server was deleted: %+v", shells)
	}
	if len(tombs) != 0 {
		t.Fatalf("nothing should have been tombstoned, got %+v", tombs)
	}
	if got := shells[0].Restore.LastSeenServer; got != "pid=1111" {
		t.Fatalf("the untouched record's marker changed to %q", got)
	}
}

// TestReapDefersWhenTheServerCannotBeIdentified pins the third outcome: with no
// server pid on the listing the surface cannot say which server it is looking
// at, and the record is kept without being marked either way.
func TestReapDefersWhenTheServerCannotBeIdentified(t *testing.T) {
	root := seedReapManifest(t, "pid=4242", false)
	stubReap(t, shellliveness.Gone)
	useRealWriter(t)

	// panes without a ServerPID, as an older tmux would report
	m := reapModel(t)
	m.currentPanes = otherPanes()
	next := runReap(t, m, m.reapDeadShells())
	runReap(t, m, next)

	shells, tombs := readReapManifest(t, root)
	if len(shells) != 1 {
		t.Fatalf("an unidentifiable server must not cause a deletion: %+v", shells)
	}
	if len(tombs) != 0 {
		t.Fatalf("an unidentifiable server must not tombstone: %+v", tombs)
	}
	if shells[0].Restore != nil && shells[0].Restore.Eligible {
		t.Fatal("an unidentifiable server must not mark the record restorable either")
	}
}

// useRealWriter points the surface at the production writer for the duration of
// one test, which is what makes these tests binding tests rather than another
// round of asserting on a stub's arguments.
func useRealWriter(t *testing.T) {
	t.Helper()
	previous := forgetShell
	forgetShell = workspaceops.ReapManagedShellFunc
	t.Cleanup(func() { forgetShell = previous })
}

// seedReapManifest writes a real shells.json for the reap fixture's project,
// carrying the marker that says which server the shell was last alive in.
func seedReapManifest(t *testing.T, lastSeenServer string, eligible bool) string {
	t.Helper()
	stateHome := t.TempDir()
	t.Setenv("XDG_STATE_HOME", stateHome)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")

	dir, err := projectdir.Resolve(reapProject)
	if err != nil {
		t.Fatal(err)
	}
	def := shellstate.Definition{
		TmuxName:    reapSession,
		DisplayName: "Shell 1",
		Namespace:   tmuxenv.Namespace(),
		CreatedAt:   time.Now().Add(-time.Hour),
		WorkDir:     reapProject,
		Restore:     &shellstate.RestoreState{Eligible: eligible, LastSeenServer: lastSeenServer},
	}
	if err := shellstate.AddAtPath(filepath.Join(dir, "shells.json"), def); err != nil {
		t.Fatal(err)
	}
	return reapProject
}

func readReapManifest(t *testing.T, root string) ([]shellstate.Definition, []shellstate.Tombstone) {
	t.Helper()
	dir, err := projectdir.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "shells.json")
	shells, err := shellstate.ListAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	tombs, err := shellstate.ListTombstonesAtPath(path)
	if err != nil {
		t.Fatal(err)
	}
	return shells, tombs
}
