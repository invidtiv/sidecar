package workspace

import (
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/workspacediff"
)

func clickPreviewActionChip(t *testing.T, p *Plugin, hit previewActionHit) {
	t.Helper()
	_ = p.View(p.width, p.height)
	var region *mouse.Region
	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID != regionPreviewAction {
			continue
		}
		if got, ok := r.Data.(previewActionHit); ok && got == hit {
			copy := r
			region = &copy
			break
		}
	}
	if region == nil {
		t.Fatalf("no action chip hit region for %v", hit)
	}
	_ = p.handleMouseClick(mouse.MouseAction{
		Type:   mouse.ActionClick,
		X:      region.Rect.X + region.Rect.W/2,
		Y:      region.Rect.Y,
		Region: region,
	})
}

// The main checkout is the project's primary working directory, not a
// workspace: nothing in this list creates, deletes, merges, or pushes it, and
// its preview was a static explainer rather than a terminal. It is no longer
// offered as a row, so it is no longer a surface with a chip header either.
func TestMainWorktreeIsNotOfferedAsARow(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	p.worktrees[0].IsMain = true

	for _, index := range p.visibleWorktreeIndices() {
		if p.worktrees[index].IsMain {
			t.Fatal("the main checkout is still listed as a workspace row")
		}
	}
	if wt := p.selectedWorktree(); wt != nil && wt.IsMain {
		t.Fatal("the main checkout is still the selected surface")
	}

	_ = p.View(p.width, p.height)
	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID == regionPreviewAction {
			t.Fatal("the main checkout still draws preview action chips")
		}
	}
}

// A main checkout that is hosting shells keeps its row: Sidecar is running from
// inside a worktree, so those sessions have nowhere else to appear, and hiding
// their parent would take them off the surface entirely.
func TestMainWorktreeKeepsItsRowWhileItHostsShells(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	main := p.worktrees[0]
	main.IsMain = true
	// Sidecar is running somewhere else, so main's shells nest under its row.
	p.ctx.WorkDir = t.TempDir()
	p.nestedByWorkDir = map[string][]*ShellSession{
		filepath.Clean(main.Path): {{Name: "in main", TmuxName: "shell-main"}},
	}

	listed := false
	for _, index := range p.visibleWorktreeIndices() {
		if p.worktrees[index].IsMain {
			listed = true
		}
	}
	if !listed {
		t.Fatal("hiding the main checkout took its shells off the list with it")
	}
}

func TestClickDiffChipOnTopicWorktreeOpensLeaf(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	p.worktrees[0].TaskID = "td-abcd12"
	p.sidebarVisible = false

	clickPreviewActionChip(t, p, previewActionDiff)

	if !p.paneTreeShowing() {
		t.Fatal("tree is not showing after Diff chip click")
	}
	if diff, _ := p.activeDiffPane(); diff == nil || diff.view() == nil || diff.view().Target.Identity() != workspacediff.IdentityWorkingTree {
		t.Fatal("Diff chip did not open a working-tree Diff leaf")
	}
	if p.viewMode == ViewModeInteractive {
		t.Fatal("Diff chip started typing")
	}
}

func TestClickTaskChipOpensIssueLeaf(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	p.worktrees[0].TaskID = "td-abcd12"
	p.sidebarVisible = false

	clickPreviewActionChip(t, p, previewActionTask)

	issue, _ := p.activeIssuePane()
	if issue == nil || issue.view() == nil || issue.view().IssueID() != "td-abcd12" {
		t.Fatalf("Task chip did not open Issues leaf on TaskID: %#v", issue)
	}
	if p.viewMode == ViewModeInteractive {
		t.Fatal("Task chip started typing")
	}
}
