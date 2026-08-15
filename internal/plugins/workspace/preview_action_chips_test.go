package workspace

import (
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

func TestClickDiffChipOnMainWorktreeOpensLeaf(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	p.worktrees[0].IsMain = true
	p.sidebarVisible = false

	clickPreviewActionChip(t, p, previewActionDiff)

	if !p.paneTreeShowing() {
		t.Fatal("tree is not showing after Diff chip click")
	}
	if diff, _ := p.activeDiffPane(); diff == nil {
		t.Fatal("Diff chip did not open a Diff leaf")
	}
	if p.viewMode == ViewModeInteractive {
		t.Fatal("Diff chip started typing")
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
