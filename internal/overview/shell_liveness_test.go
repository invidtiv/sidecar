package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/tmuxenv"
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
	forgotten := []string{}
	previousProbe, previousForget := shellLivenessProbe, forgetShell
	shellLivenessProbe = func(string) shellliveness.Verdict { return verdict }
	forgetShell = func(_, session, _ string) error {
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

	// The next cycle's tmux inventory no longer has the shell's pane.
	m.currentPanes = nil
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

	m.currentPanes = nil
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

	m.currentPanes = nil
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

	m.currentPanes = nil
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
