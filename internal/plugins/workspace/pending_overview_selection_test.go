package workspace

import (
	"os"
	"path/filepath"
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

func TestPendingOverviewSelectionCanonicalizesWorktreePath(t *testing.T) {
	realPath := filepath.Join(t.TempDir(), "topic")
	if err := os.Mkdir(realPath, 0o755); err != nil {
		t.Fatal(err)
	}
	aliasPath := filepath.Join(t.TempDir(), "topic-alias")
	if err := os.Symlink(realPath, aliasPath); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.worktreesLoaded = true
	p.worktrees = []*Worktree{{Name: "topic", Path: aliasPath}}
	p.SetPendingWorkspaceSelection(plugin.PendingWorkspaceSelection{Kind: plugin.WorkspaceSelectionWorktree, Path: realPath})
	if p.selectedIdx != 0 || p.pendingOverviewSelection != nil || p.toastMessage != "" {
		t.Fatalf("canonical selection: index=%d pending=%#v toast=%q", p.selectedIdx, p.pendingOverviewSelection, p.toastMessage)
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

func TestPendingOverviewMergeQueuesExistingStrategyWorkflow(t *testing.T) {
	path := t.TempDir()
	p := New()
	p.ctx = &plugin.Context{WorkDir: path, ProjectRoot: path, Epoch: 1}
	p.worktreesLoaded = true
	p.worktrees = []*Worktree{{Name: "topic", Path: path, Branch: "topic"}}
	p.SetPendingWorkspaceSelection(plugin.PendingWorkspaceSelection{Kind: plugin.WorkspaceSelectionWorktree, Path: path, Action: "merge"})
	if cmd := p.TakePendingWorkspaceAction(); cmd == nil {
		t.Fatal("global merge navigation did not queue the project merge workflow")
	}
	if cmd := p.TakePendingWorkspaceAction(); cmd != nil {
		t.Fatal("pending merge workflow was delivered more than once")
	}
}
