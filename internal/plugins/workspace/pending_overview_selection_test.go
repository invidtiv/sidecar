package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
)

func TestPendingOverviewSelectionUsesExactWorktreePathAndShellName(t *testing.T) {
	p := New()
	p.worktreesLoaded = true
	p.shellStartupLoading = false
	p.worktrees = []*Worktree{{Key: "one", Name: "duplicate", Path: "/tmp/one/duplicate"}, {Key: "two", Name: "duplicate", Path: "/tmp/two/duplicate"}}
	p.SetPendingWorkspaceSelection(plugin.PendingWorkspaceSelection{Kind: plugin.WorkspaceSelectionWorktree, Path: "/tmp/two/duplicate"})
	if p.shellSelected || p.selectedIdx != 1 || p.pendingOverviewSelection != nil {
		t.Fatalf("worktree selection = shell:%v index:%d pending:%#v", p.shellSelected, p.selectedIdx, p.pendingOverviewSelection)
	}
	if selected := p.selectedKanbanWorktree(); selected != p.worktrees[1] {
		t.Fatalf("Kanban selection = %#v, want exact second worktree", selected)
	}
	p.shells = []*ShellSession{{Name: "Agent", TmuxName: "same"}, {Name: "Agent", TmuxName: "exact"}}
	p.SetPendingWorkspaceSelection(plugin.PendingWorkspaceSelection{Kind: plugin.WorkspaceSelectionShell, Key: "exact"})
	if !p.shellSelected || p.selectedShellIdx != 1 || p.pendingOverviewSelection != nil {
		t.Fatalf("shell selection = shell:%v index:%d pending:%#v", p.shellSelected, p.selectedShellIdx, p.pendingOverviewSelection)
	}
	if p.kanbanCol != kanbanShellColumnIndex || p.kanbanRow != 1 {
		t.Fatalf("Kanban shell selection = %d,%d", p.kanbanCol, p.kanbanRow)
	}
}

func TestPendingOverviewSelectionStaleDoesNotSelectSimilarItem(t *testing.T) {
	p := New()
	p.worktreesLoaded = true
	p.shellStartupLoading = false
	p.worktrees = []*Worktree{{Name: "target", Path: "/tmp/other/target"}}
	p.SetPendingWorkspaceSelection(plugin.PendingWorkspaceSelection{Kind: plugin.WorkspaceSelectionWorktree, Path: "/tmp/wanted/target"})
	if p.selectedIdx != 0 || p.pendingOverviewSelection != nil || p.toastMessage == "" {
		t.Fatalf("stale selection mutated selection or lacked feedback: index=%d pending=%#v toast=%q", p.selectedIdx, p.pendingOverviewSelection, p.toastMessage)
	}
}
