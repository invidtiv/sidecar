package overview

import (
	"strings"
	"testing"
)

// The host's half of a card activation: the named workspace becomes the
// selected row of the global Workspaces list, with the list holding the
// keyboard (td-16b473).
func TestRevealSelectsTheNamedRowInTheWorkspacesList(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	selectWorkspace(t, m, "a")

	shell, ok := m.catalog["c"]
	if !ok {
		t.Fatal("fixture is missing the shell row")
	}
	run(t, m, m.RevealWorkspace(shell))

	selected, ok := m.SelectedWorkspace()
	if !ok || selected.ID != "c" {
		t.Fatalf("reveal selected %#v, want the named shell", selected)
	}
	if m.PreviewFocused() {
		t.Fatal("reveal left the keyboard in the preview instead of the list")
	}
}

// A row the list is currently hiding is still a row the user asked for.
func TestRevealShowsAHiddenIdleWorktree(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.showIdleWorktrees = false
	m.syncWorkspaces()

	idle, ok := m.catalog["d"]
	if !ok {
		t.Fatal("fixture is missing the idle worktree")
	}
	if m.workspaces.SelectID(idle.ID) {
		t.Skip("the fixture's idle worktree is not hidden in this configuration")
	}

	run(t, m, m.RevealWorkspace(idle))
	selected, ok := m.SelectedWorkspace()
	if !ok || selected.ID != idle.ID {
		t.Fatalf("reveal selected %#v, want the hidden idle worktree", selected)
	}
}

// A narrowing query is the other reason the asked-for row is not on screen.
// Landing silently on somebody else's row is the dangerous outcome, because D
// acts on the selection (td-16b473, M3).
func TestRevealClearsAFilterThatHidesTheTarget(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))

	target, ok := m.catalog["c"]
	if !ok {
		t.Fatal("fixture is missing the shell row")
	}
	m.workspaces.Filter().SetQuery("alpha")
	m.workspaces.Reproject()
	if m.workspaces.SelectID(target.ID) {
		t.Skip("the query does not hide the target in this fixture")
	}
	before := m.workspaces.SelectedID()

	run(t, m, m.RevealWorkspace(target))

	selected, ok := m.SelectedWorkspace()
	if !ok || selected.ID != target.ID {
		t.Fatalf("reveal left the selection on %q (was %q), want the named row", selected.ID, before)
	}
	if m.workspaces.Filter().Active() {
		t.Fatalf("the query %q still narrows the list", m.workspaces.Filter().Query())
	}
}

// A workspace that has left the catalog cannot be revealed; say so rather than
// leaving the previous selection looking like the answer.
func TestRevealingAVanishedWorkspaceSaysSoInsteadOfSelectingAnotherRow(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	selectWorkspace(t, m, "a")

	gone := m.catalog["c"]
	gone.ID = "sidecar:shell:vanished"
	gone.Name = "vanished"
	cmd := m.RevealWorkspace(gone)

	toast, ok := toastFrom(t, cmd)
	if !ok || !strings.Contains(toast.Message, "vanished") {
		t.Fatalf("reveal of a vanished workspace produced %#v", toast)
	}
	if selected, _ := m.SelectedWorkspace(); selected.ID != "a" {
		t.Fatalf("the selection moved to %q", selected.ID)
	}
}

// Showing hidden idle worktrees is a persisted choice everywhere else it is
// made, so the fly-out's checkbox must not silently revert on restart (L4).
func TestRevealPersistsUnhidingIdleWorktrees(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.showIdleWorktrees = false
	m.syncWorkspaces()

	saved := 0
	original := saveShowIdleWorktrees
	saveShowIdleWorktrees = func(show bool) error {
		if show {
			saved++
		}
		return nil
	}
	t.Cleanup(func() { saveShowIdleWorktrees = original })

	idle, ok := m.catalog["d"]
	if !ok {
		t.Fatal("fixture is missing the idle worktree")
	}
	if m.workspaces.SelectID(idle.ID) {
		t.Skip("the fixture's idle worktree is not hidden in this configuration")
	}

	run(t, m, m.RevealWorkspace(idle))
	if saved == 0 {
		t.Fatal("unhiding idle worktrees during a reveal was not persisted")
	}
}
