package overview

import (
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
