package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestWaitingWorktreeDoesNotAdvertiseApprovalCommands(t *testing.T) {
	p := New()
	p.worktrees = []*Worktree{{
		Name:   "waiting",
		Path:   t.TempDir(),
		Status: StatusWaiting,
		Agent:  &Agent{},
	}}

	for _, pane := range []FocusPane{PaneSidebar, PanePreview} {
		p.activePane = pane
		for _, cmd := range p.Commands() {
			switch cmd.ID {
			case "approve", "approve-all", "reject":
				t.Fatalf("%v advertised approval command %q in %s", pane, cmd.ID, cmd.Context)
			}
		}
	}
}

func TestApprovalKeysDoNothingInWorkspaceList(t *testing.T) {
	p := New()
	p.worktrees = []*Worktree{{
		Name:   "waiting",
		Path:   t.TempDir(),
		Status: StatusWaiting,
		Agent:  &Agent{},
	}}

	for _, key := range []string{"y", "Y", "N"} {
		if cmd := p.handleListKeys(tea.KeyPressMsg{Code: []rune(key)[0], Text: key}); cmd != nil {
			t.Fatalf("%q still invokes an approval action in Workspaces", key)
		}
	}
}
