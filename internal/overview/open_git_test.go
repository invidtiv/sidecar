package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/workspacediff"
)

func TestOOnWorktreeRequestsGitAtWorktreePath(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	m.workspaces.SelectID("a")
	handled, cmd := m.WorkspacesKey(key("O"))
	if !handled {
		t.Fatal("O on a worktree was not handled")
	}
	got := openInGit(t, cmd)
	if got.Path != "/tmp/sidecar-alpha" {
		t.Fatalf("worktree git path = %q, want the worktree Path", got.Path)
	}
}

func TestOOnShellRequestsGitAtProjectRoot(t *testing.T) {
	m, _ := previewModel(t)
	run(t, m, m.SetWorkspacesVisible(true))
	stampShellOwner(m, "c", "/repos/sidecar", "sc-sh", "")
	handled, cmd := m.WorkspacesKey(key("O"))
	if !handled {
		t.Fatal("O on a shell was not handled")
	}
	got := openInGit(t, cmd)
	if got.Path != "/repos/sidecar" {
		t.Fatalf("shell git path = %q, want ProjectRoot", got.Path)
	}
}

func TestOWhileTypingGoesToThePane(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'O', Text: "O"})
	if !handled {
		t.Fatal("interactive O was not handled")
	}
	run(t, m, cmd)
	if len(terminal.keys) == 0 || terminal.keys[len(terminal.keys)-1] != "O" {
		t.Fatalf("pane keys = %v, want O", terminal.keys)
	}
}

func TestGitChipClickJumpsWithoutTyping(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	m.workspaces.SelectID("a")
	press(t, m, ".")
	if m.previewTab != workspacediff.TabDiff {
		t.Fatal("premise: Diff tab should be showing")
	}
	m.WorkspacesView(previewWide, previewTall)

	x, y, ok := gitChipPoint(m)
	if !ok {
		t.Fatal("no Git chip hit region on the Diff tab")
	}
	cmd := m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	got := openInGit(t, cmd)
	if got.Path != "/tmp/sidecar-alpha" {
		t.Fatalf("chip git path = %q", got.Path)
	}
	if m.PreviewInteractive() || terminal.opens != 1 || terminal.IsActive() {
		t.Fatal("clicking Git started typing")
	}
}

func TestGitChipClickWorksWhileTyping(t *testing.T) {
	m, _, terminal := interactiveModel(t)
	enterInteractive(t, m)
	m.WorkspacesView(previewWide, previewTall)
	x, y, ok := gitChipPoint(m)
	if !ok {
		t.Fatal("no Git chip hit region while typing")
	}
	cmd := m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	got := openInGit(t, cmd)
	if got.Path != "/tmp/sidecar-alpha" {
		t.Fatalf("typing-state chip path = %q", got.Path)
	}
	_ = terminal
}

func TestListCommandsAdvertiseGit(t *testing.T) {
	m, _ := previewModel(t)
	m.workspaces.SelectID("a")
	var found bool
	for _, cmd := range m.Commands() {
		if cmd.ID == "open-in-git" && cmd.Name == "Git" {
			found = true
		}
	}
	if !found {
		t.Fatalf("list Commands() omitted Git: %#v", m.Commands())
	}
}

func openInGit(t *testing.T, cmd tea.Cmd) OpenInGitMsg {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected OpenInGitMsg command")
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			if got, found := openInGitFrom(sub); found {
				return got
			}
		}
		t.Fatalf("batch produced %T, want OpenInGitMsg", msg)
	}
	got, ok := msg.(OpenInGitMsg)
	if !ok {
		t.Fatalf("cmd produced %T, want OpenInGitMsg", msg)
	}
	if got.Path == "" {
		t.Fatal("OpenInGitMsg.Path is empty")
	}
	return got
}

func openInGitFrom(cmd tea.Cmd) (OpenInGitMsg, bool) {
	if cmd == nil {
		return OpenInGitMsg{}, false
	}
	got, ok := cmd().(OpenInGitMsg)
	return got, ok && got.Path != ""
}

func gitChipPoint(m *Model) (int, int, bool) {
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if _, ok := region.Data.(previewGitHit); ok {
			return region.Rect.X + 1, region.Rect.Y, true
		}
	}
	return 0, 0, false
}
