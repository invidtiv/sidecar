package overview

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Shells are one model with two projections. A shell that closes in the project
// plugin but lingers in the global browser is the same bug reported twice, so
// the global surface reaps on its own evidence rather than waiting for the
// other surface to rewrite shells.json (td-6a4100).

const (
	reapProject = "/tmp/project"
	reapSession = "sidecar-sh-project-1"
)

func reapModel(t *testing.T) *Model {
	t.Helper()
	m := New(workspaceinventory.Collector{})
	shell := workspaceinventory.Workspace{
		ID: reapProject + ":shell:" + reapSession, ProjectKey: reapProject, ProjectRoot: reapProject,
		Kind: workspaceinventory.KindShell, Key: reapSession, Name: "Shell 1", Path: reapProject,
		TmuxName: reapSession, Namespace: tmuxenv.Namespace(),
	}
	m.results[reapProject] = workspaceinventory.ProjectResult{
		ProjectKey: reapProject, ProjectRoot: reapProject, ProjectName: "project",
		Workspaces: []workspaceinventory.Workspace{shell},
	}
	// One cycle in which tmux listed the shell's pane: that is the positive
	// liveness this surface needs before any later absence can mean anything.
	m.currentPanes = []workspaceinventory.Pane{{ID: "%1", Session: reapSession, Path: reapProject}}
	if cmd := m.reapDeadShells(); cmd != nil {
		t.Fatal("a live shell was probed")
	}
	return m
}

func stubReap(t *testing.T, verdict shellliveness.Verdict) *[]string {
	t.Helper()
	return stubReapSequence(t, verdict, verdict)
}

// stubReapSequence answers the suspicion probe with first and every later probe
// — including the one taken immediately before the manifest write — with rest.
func stubReapSequence(t *testing.T, first, rest shellliveness.Verdict) *[]string {
	t.Helper()
	forgotten := []string{}
	calls := 0
	previousProbe, previousForget := shellLivenessProbe, forgetShell
	shellLivenessProbe = func(string) shellliveness.Verdict {
		calls++
		if calls == 1 {
			return first
		}
		return rest
	}
	forgetShell = func(_, session, _ string, _ time.Time, _ tmuxserver.Incarnation) error {
		forgotten = append(forgotten, session)
		return nil
	}
	t.Cleanup(func() { shellLivenessProbe, forgetShell = previousProbe, previousForget })
	return &forgotten
}

func runReap(t *testing.T, m *Model, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got nil")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("command produced no message")
	}
	return m.update(msg)
}

// otherPanes is a tmux inventory that answered and simply does not contain the
// shell under test — a live server, one dead session.
func otherPanes() []workspaceinventory.Pane {
	return []workspaceinventory.Pane{{ID: "%9", Session: "unrelated-session", Path: "/tmp"}}
}

func shellRows(m *Model) int {
	count := 0
	for _, result := range m.results {
		for _, workspace := range result.Workspaces {
			if workspace.Kind == workspaceinventory.KindShell {
				count++
			}
		}
	}
	return count
}

func TestGlobalBrowserClosesAShellWhoseSessionIsGone(t *testing.T) {
	m := reapModel(t)
	forgotten := stubReap(t, shellliveness.Gone)

	// The next cycle's tmux inventory still lists panes — the server is up —
	// but this shell's pane is no longer among them.
	m.currentPanes = otherPanes()
	next := runReap(t, m, m.reapDeadShells())

	if shellRows(m) != 0 {
		t.Fatal("the dead shell is still listed in the global browser")
	}
	runReap(t, m, next)
	if len(*forgotten) != 1 || (*forgotten)[0] != reapSession {
		t.Fatalf("manifest entries forgotten = %v, want exactly the dead shell", *forgotten)
	}
}

func TestGlobalBrowserKeepsAShellWhenTmuxCannotAnswer(t *testing.T) {
	m := reapModel(t)
	forgotten := stubReap(t, shellliveness.Unknown)

	m.currentPanes = otherPanes()
	if cmd := m.reapDeadShells(); cmd != nil {
		if next := runReap(t, m, cmd); next != nil {
			runReap(t, m, next)
		}
	}

	if shellRows(m) != 1 {
		t.Fatal("an unreachable tmux removed a shell from the global browser")
	}
	if len(*forgotten) != 0 {
		t.Fatalf("an unconfirmed death wrote the manifest: %v", *forgotten)
	}
}

// A failed tmux inventory is not evidence that anything died, and it arrives
// for every project at once — the exact shape that would empty the board.
func TestFailedTmuxInventoryReapsNothing(t *testing.T) {
	m := reapModel(t)
	forgotten := stubReap(t, shellliveness.Gone)

	m.currentPanes = otherPanes()
	m.tmuxErr = errTmuxUnavailable
	if cmd := m.reapDeadShells(); cmd != nil {
		t.Fatal("a failed tmux inventory probed for deaths")
	}
	if shellRows(m) != 1 || len(*forgotten) != 0 {
		t.Fatalf("a failed inventory closed shells: rows=%d forgotten=%v", shellRows(m), *forgotten)
	}
}

// A manifest entry this browser never saw running is what survives a reboot.
// Auto-close must leave it for the recreate path.
func TestNeverObservedShellSurvives(t *testing.T) {
	m := New(workspaceinventory.Collector{})
	m.results[reapProject] = workspaceinventory.ProjectResult{
		ProjectKey: reapProject, ProjectRoot: reapProject, ProjectName: "project",
		Workspaces: []workspaceinventory.Workspace{{
			ProjectKey: reapProject, ProjectRoot: reapProject, Kind: workspaceinventory.KindShell,
			Key: reapSession, Name: "Shell 1", TmuxName: reapSession, Namespace: tmuxenv.Namespace(),
		}},
	}
	forgotten := stubReap(t, shellliveness.Gone)

	if cmd := m.reapDeadShells(); cmd != nil {
		t.Fatal("a shell that was cold from the start was probed")
	}
	if shellRows(m) != 1 || len(*forgotten) != 0 {
		t.Fatalf("an offline row was auto-closed: rows=%d forgotten=%v", shellRows(m), *forgotten)
	}
}

// Shells on another tmux server are invisible to this listing, so their absence
// from it says nothing at all.
func TestForeignNamespaceShellIsNeverJudged(t *testing.T) {
	m := reapModel(t)
	result := m.results[reapProject]
	result.Workspaces[0].Namespace = "/tmp/some-other-socket/default"
	m.results[reapProject] = result
	forgotten := stubReap(t, shellliveness.Gone)

	m.currentPanes = otherPanes()
	if cmd := m.reapDeadShells(); cmd != nil {
		t.Fatal("a shell on another tmux server was probed")
	}
	if shellRows(m) != 1 || len(*forgotten) != 0 {
		t.Fatalf("a foreign-namespace shell was closed: rows=%d forgotten=%v", shellRows(m), *forgotten)
	}
}

var errTmuxUnavailable = tmuxUnavailableError{}

type tmuxUnavailableError struct{}

func (tmuxUnavailableError) Error() string { return "tmux inventory failed" }

// Finding 3's guard. The collector reports "no server running" as zero panes
// and no error, so the tmuxErr check alone never fires for the case it was
// written for: a tmux restart, where every shell at once looks missing. The
// probe would answer Unknown, but the pass must not start at all.
func TestEmptyTmuxInventoryReapsNothing(t *testing.T) {
	m := reapModel(t)
	forgotten := stubReap(t, shellliveness.Gone)

	m.currentPanes = nil
	if cmd := m.reapDeadShells(); cmd != nil {
		t.Fatal("an empty tmux inventory — a server that is not running — probed for deaths")
	}
	if shellRows(m) != 1 || len(*forgotten) != 0 {
		t.Fatalf("a vanished tmux server closed shells: rows=%d forgotten=%v", shellRows(m), *forgotten)
	}
}

// Finding 1, the surface half. The verdict was true when taken and false by the
// time it was applied, because the user brought the session back in between.
// The re-probe at the point of the write is what notices.
func TestResurrectedShellIsNotForgotten(t *testing.T) {
	m := reapModel(t)
	// Gone when suspected, Alive by the time the write is about to happen.
	forgotten := stubReapSequence(t, shellliveness.Gone, shellliveness.Alive)

	m.currentPanes = otherPanes()
	next := runReap(t, m, m.reapDeadShells())
	runReap(t, m, next)

	if len(*forgotten) != 0 {
		t.Fatalf("the manifest entry of a resurrected shell was deleted: %v", *forgotten)
	}
}

// The same race one layer down: the tracker refuses a verdict tagged with a
// life that ended, so a sighting between dispatch and delivery is enough on its
// own — no second probe required.
func TestVerdictFromAPreviousLifeDoesNotDropTheRow(t *testing.T) {
	m := reapModel(t)
	forgotten := stubReap(t, shellliveness.Gone)

	m.currentPanes = otherPanes()
	cmd := m.reapDeadShells()
	if cmd == nil {
		t.Fatal("the missing shell was not probed")
	}
	probed, ok := cmd().(shellProbedMsg)
	if !ok {
		t.Fatalf("probe produced %T, want shellProbedMsg", cmd())
	}

	// The session comes back and the next cycle sees its pane before the
	// verdict is applied.
	m.currentPanes = append(otherPanes(), workspaceinventory.Pane{ID: "%2", Session: reapSession, Path: reapProject})
	m.reapDeadShells()

	m.update(probed)

	if shellRows(m) != 1 {
		t.Fatal("a verdict about a previous life dropped a live shell's row")
	}
	if len(*forgotten) != 0 {
		t.Fatalf("a verdict about a previous life deleted a live shell's entry: %v", *forgotten)
	}
}

func stubLivenessServer(t *testing.T, inc tmuxserver.Incarnation) *tmuxserver.Incarnation {
	t.Helper()
	current := inc
	previous := shellLivenessServer
	shellLivenessServer = func() tmuxserver.Incarnation { return current }
	t.Cleanup(func() { shellLivenessServer = previous })
	return &current
}

// Sidecar running outside tmux sees the new server on the next inventory pass.
// ObserveServer must fire on that transition — not only at tracker construction —
// so a listing that simply does not contain the old shells is not a mass reap.
func TestServerRestartWhileRunningReapsNothing(t *testing.T) {
	server := stubLivenessServer(t, tmuxserver.Present(1, 2, 3))
	m := reapModel(t)
	forgotten := stubReap(t, shellliveness.Gone)

	if !m.shellLivenessTracker().SeenAlive(reapSession) {
		t.Fatal("precondition: the shell was seen alive on the first server")
	}

	*server = tmuxserver.Present(9, 10, 11)
	m.currentPanes = otherPanes()
	if cmd := m.reapDeadShells(); cmd != nil {
		t.Fatal("a server restart probed for deaths")
	}
	if m.shellLivenessTracker().SeenAlive(reapSession) {
		t.Fatal("overview binding did not reset seenAlive on the live transition")
	}
	if shellRows(m) != 1 || len(*forgotten) != 0 {
		t.Fatalf("a server restart closed shells: rows=%d forgotten=%v", shellRows(m), *forgotten)
	}
}

func TestStaleServerVerdictDoesNotDropTheRow(t *testing.T) {
	server := stubLivenessServer(t, tmuxserver.Present(1, 2, 3))
	m := reapModel(t)
	forgotten := stubReap(t, shellliveness.Gone)

	m.currentPanes = otherPanes()
	cmd := m.reapDeadShells()
	if cmd == nil {
		t.Fatal("the missing shell was not probed")
	}
	probed, ok := cmd().(shellProbedMsg)
	if !ok {
		t.Fatalf("probe produced %T, want shellProbedMsg", cmd())
	}

	*server = tmuxserver.Present(9, 10, 11)
	m.observeTmuxServer(*server)
	m.currentPanes = append(otherPanes(), workspaceinventory.Pane{ID: "%2", Session: reapSession, Path: reapProject})
	m.reapDeadShells()

	m.update(probed)

	if shellRows(m) != 1 {
		t.Fatal("a verdict from a previous server dropped a live shell's row")
	}
	if len(*forgotten) != 0 {
		t.Fatalf("a verdict from a previous server deleted a live shell's entry: %v", *forgotten)
	}
}
