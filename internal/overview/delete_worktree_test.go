package overview

import (
	"context"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/worktreedelete"
)

// D acts on the selection's kind here exactly as it does on the project
// surface, and a worktree raises the one shared confirmation rather than a
// second one that resembles it (td-2af16d).

// sharedConfirmation renders the confirmation internal/worktreedelete builds
// for a target. A surface that ever grows its own copy stops matching this.
func sharedConfirmation(t *testing.T, width int, target worktreedelete.Target, isMain bool) string {
	t.Helper()
	var expected worktreedelete.State
	expected.Open(target, isMain)
	built := expected.Modal(width)
	if built == nil {
		t.Fatal("the shared confirmation built nothing")
	}
	return built.Render(width, 24, mouse.NewHandler())
}

// containsBlock reports that every non-blank line of block appears in view.
func containsBlock(view, block string) bool {
	for _, line := range strings.Split(block, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if !strings.Contains(view, strings.TrimRight(line, " ")) {
			return false
		}
	}
	return true
}

func selectWorkspace(t *testing.T, m *Model, id string) workspaceinventory.Workspace {
	t.Helper()
	if !m.workspaces.SelectID(id) {
		t.Fatalf("row %q is not selectable", id)
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.ID != id {
		t.Fatalf("selection is %#v, want %q", workspace, id)
	}
	return workspace
}

func TestGlobalDeleteOnAWorktreeRaisesTheSharedConfirmation(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	workspace := selectWorkspace(t, m, "a")

	handled, _ := m.WorkspacesKey(key("D"))
	if !handled {
		t.Fatal("D was not answered for a selected worktree")
	}
	if !m.DeleteOpen() || !m.DeletingWorktree() {
		t.Fatalf("D did not raise the worktree confirmation (open=%v worktree=%v)", m.DeleteOpen(), m.DeletingWorktree())
	}

	view := m.WorkspacesView(120, 24)
	expected := sharedConfirmation(t, m.width, worktreedelete.Target{
		Name: workspace.Name, Branch: workspace.Branch, Path: workspace.Path,
	}, false)
	if !containsBlock(view, expected) {
		t.Fatalf("the global surface drew a confirmation that is not the shared one.\nwant lines of:\n%s\ngot:\n%s", expected, view)
	}
	if !strings.Contains(view, worktreedelete.Title) || !strings.Contains(view, "Delete local branch") {
		t.Fatalf("the shared confirmation's identity is missing:\n%s", view)
	}

	// Esc closes it and leaves nothing armed.
	m.WorkspacesKey(key("esc"))
	if m.DeleteOpen() || m.DeletingWorktree() {
		t.Fatal("esc left the worktree confirmation open")
	}
}

func TestGlobalDeleteOnAShellStillDeletesTheShell(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	selectWorkspace(t, m, "c")

	handled, _ := m.WorkspacesKey(key("D"))
	if !handled || !m.DeleteOpen() {
		t.Fatal("D did not raise the shell confirmation")
	}
	if m.DeletingWorktree() {
		t.Fatal("a selected shell raised the worktree confirmation")
	}
	view := m.WorkspacesView(120, 24)
	if !strings.Contains(view, "Delete Shell") || strings.Contains(view, worktreedelete.Title) {
		t.Fatalf("the shell confirmation is not the one on screen:\n%s", view)
	}
}

func TestConfirmingAWorktreeDeleteRunsTheSharedDeletePath(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	workspace := selectWorkspace(t, m, "a")

	type call struct{ dir, arg string }
	var deleted, branches []call
	restoreDelete, restoreBranch := execDeleteWorktree, execDeleteLocalBranch
	execDeleteWorktree = func(ctx context.Context, workDir, path string, isMissing bool) error {
		deleted = append(deleted, call{workDir, path})
		return nil
	}
	execDeleteLocalBranch = func(ctx context.Context, workDir, branch string) error {
		branches = append(branches, call{workDir, branch})
		return nil
	}
	t.Cleanup(func() { execDeleteWorktree, execDeleteLocalBranch = restoreDelete, restoreBranch })

	if handled, _ := m.WorkspacesKey(key("D")); !handled {
		t.Fatal("D was not answered for a selected worktree")
	}
	m.worktreeDelete.DeleteLocal = true

	cmd := m.RunDeleteCommand("confirm-delete")
	if cmd == nil {
		t.Fatal("confirming produced no work")
	}
	if m.DeleteOpen() {
		t.Fatal("the confirmation stayed open after confirming")
	}
	msg, ok := cmd().(globalWorktreeDeleteDoneMsg)
	if !ok {
		t.Fatalf("delete produced %#v", cmd())
	}
	if msg.Err != nil {
		t.Fatalf("delete reported %v", msg.Err)
	}
	if len(deleted) != 1 || deleted[0].dir != "/tmp/sidecar" || deleted[0].arg != workspace.Path {
		t.Fatalf("worktree removal ran as %#v, want the owning project and the selected path", deleted)
	}
	if len(branches) != 1 || branches[0].arg != workspace.Branch {
		t.Fatalf("branch cleanup ran as %#v, want the selected branch", branches)
	}
}

func TestWorktreeDeleteIsDiscoverableForAWorktreeSelection(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	selectWorkspace(t, m, "a")

	if !hasCommand(m, "delete-worktree") {
		t.Fatalf("the worktree selection does not advertise delete-worktree: %#v", m.Commands())
	}
	if hasCommand(m, "delete-shell") {
		t.Fatal("a worktree selection advertises the shell's delete")
	}

	selectWorkspace(t, m, "c")
	if !hasCommand(m, "delete-shell") || hasCommand(m, "delete-worktree") {
		t.Fatalf("the shell selection advertises the wrong delete: %#v", m.Commands())
	}

	// While the confirmation is open the footer names what is being deleted.
	if handled, _ := m.WorkspacesKey(key("D")); !handled {
		t.Fatal("D was not answered for a selected shell")
	}
	for _, cmd := range m.Commands() {
		if cmd.ID == "confirm-delete" && !strings.Contains(cmd.Description, "shell") {
			t.Fatalf("the shell confirmation's footer reads %q", cmd.Description)
		}
	}
	m.closeDelete()

	selectWorkspace(t, m, "a")
	if handled, _ := m.WorkspacesKey(key("D")); !handled {
		t.Fatal("D was not answered for a selected worktree")
	}
	found := false
	for _, cmd := range m.Commands() {
		if cmd.ID == "confirm-delete" {
			found = true
			if !strings.Contains(cmd.Description, "worktree") {
				t.Fatalf("the worktree confirmation's footer reads %q", cmd.Description)
			}
		}
	}
	if !found {
		t.Fatal("the open confirmation advertises no confirm command")
	}
}
