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
	p.previewTab = PreviewTabOutput
	p.sidebarVisible = false

	clickPreviewActionChip(t, p, previewActionDiff)

	if p.previewTab == PreviewTabDiff {
		t.Fatal("Diff chip set previewTab to Diff")
	}
	if p.previewTab != PreviewTabOutput {
		t.Fatalf("previewTab = %v, want Output", p.previewTab)
	}
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

func TestClickDiffChipOnTopicTaskTabOpensLeaf(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	p.worktrees[0].TaskID = "td-abcd12"
	p.previewTab = PreviewTabTask
	p.sidebarVisible = false
	if p.paneTreeShowing() {
		t.Fatal("premise: Task tab hides the tree")
	}

	clickPreviewActionChip(t, p, previewActionDiff)

	if p.previewTab == PreviewTabDiff {
		t.Fatal("Diff chip set previewTab to Diff")
	}
	if p.previewTab != PreviewTabOutput {
		t.Fatalf("previewTab = %v, want Output", p.previewTab)
	}
	if !p.paneTreeShowing() {
		t.Fatal("tree is not showing after Diff chip click from Task tab")
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
	p.previewTab = PreviewTabDiff
	p.sidebarVisible = false

	clickPreviewActionChip(t, p, previewActionTask)

	if p.previewTab == PreviewTabDiff {
		t.Fatal("Task chip left previewTab on Diff")
	}
	if p.previewTab != PreviewTabOutput {
		t.Fatalf("previewTab = %v, want Output", p.previewTab)
	}
	issue, _ := p.activeIssuePane()
	if issue == nil || issue.view() == nil || issue.view().IssueID() != "td-abcd12" {
		t.Fatalf("Task chip did not open Issues leaf on TaskID: %#v", issue)
	}
	if p.viewMode == ViewModeInteractive {
		t.Fatal("Task chip started typing")
	}
}
