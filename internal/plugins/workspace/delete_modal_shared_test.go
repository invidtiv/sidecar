package workspace

import (
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/worktreedelete"
)

// The project surface and the global Workspaces browser raise one
// confirmation, not two that resemble each other. Each surface proves it
// against internal/worktreedelete directly, so a surface that grows its own
// copy fails its own test (td-2af16d).

func deletePlugin(t *testing.T) (*Plugin, *Worktree) {
	t.Helper()
	root := t.TempDir()
	path := root + "/feature"
	if err := os.MkdirAll(path, 0o755); err != nil {
		t.Fatalf("worktree fixture: %v", err)
	}
	wt := &Worktree{Key: "feature", Name: "feature", Path: path, Branch: "feature-branch"}
	p := &Plugin{
		viewMode:     ViewModeList,
		mouseHandler: mouse.NewHandler(),
		width:        120,
		height:       32,
		sidebarWidth: 40,
		ctx:          &plugin.Context{WorkDir: root, Epoch: 1},
	}
	p.selection.Clear()
	p.worktrees = []*Worktree{wt}
	p.selectedIdx = 0
	return p, wt
}

func TestProjectDeleteConfirmationIsTheSharedOne(t *testing.T) {
	p, wt := deletePlugin(t)

	p.handleListKeys(tea.KeyPressMsg{Code: 'D', Text: "D"})
	if p.viewMode != ViewModeConfirmDelete {
		t.Fatalf("D did not arm the worktree confirmation: viewMode=%v", p.viewMode)
	}

	built := p.deleteConfirm.Modal(p.width)
	if built == nil {
		t.Fatal("the armed confirmation built no modal")
	}
	got := built.Render(p.width, 24, mouse.NewHandler())

	var expected worktreedelete.State
	expected.Open(worktreedelete.Target{Name: wt.Name, Branch: wt.Branch, Path: wt.Path}, false)
	want := expected.Modal(p.width).Render(p.width, 24, mouse.NewHandler())

	if got != want {
		t.Fatalf("the project surface drew a confirmation that is not the shared one.\nwant:\n%s\ngot:\n%s", want, got)
	}
	if !strings.Contains(got, worktreedelete.Title) || !strings.Contains(got, "Delete local branch") {
		t.Fatalf("the shared confirmation's identity is missing:\n%s", got)
	}
}

func TestProjectDeleteConfirmationRoutesThroughTheSharedOutcomes(t *testing.T) {
	p, _ := deletePlugin(t)
	p.handleListKeys(tea.KeyPressMsg{Code: 'D', Text: "D"})

	// Esc cancels and disarms.
	p.handleConfirmDeleteKeys(tea.KeyPressMsg{Code: tea.KeyEscape})
	if p.viewMode != ViewModeList || p.deleteConfirm.Active() || p.deleteConfirmWorktree != nil {
		t.Fatalf("esc left the confirmation armed: mode=%v active=%v", p.viewMode, p.deleteConfirm.Active())
	}

	// The remote branch answer reaches the shared state, which owns the box.
	p.handleListKeys(tea.KeyPressMsg{Code: 'D', Text: "D"})
	p.Update(RemoteCheckDoneMsg{WorkspaceName: "feature", Branch: "feature-branch", Exists: true})
	if !p.deleteConfirm.HasRemote {
		t.Fatal("the remote branch answer did not reach the shared confirmation")
	}
	view := p.deleteConfirm.Modal(p.width).Render(p.width, 24, mouse.NewHandler())
	if !strings.Contains(view, "Delete remote branch") {
		t.Fatalf("the remote branch box is missing after the check landed:\n%s", view)
	}
}
