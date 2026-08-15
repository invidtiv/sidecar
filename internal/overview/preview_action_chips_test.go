package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func setOverviewTaskID(t *testing.T, m *Model, id string) {
	t.Helper()
	ws, ok := m.catalog["a"]
	if !ok {
		t.Fatal("catalog missing a")
	}
	ws.TaskID = id
	m.catalog["a"] = ws
	if result, ok := m.results["sidecar"]; ok && len(result.Workspaces) > 0 {
		result.Workspaces[0].TaskID = id
		m.results["sidecar"] = result
	}
}

func clickOverviewAction(t *testing.T, m *Model, hit previewActionHit) {
	t.Helper()
	m.WorkspacesView(previewWide, previewTall)
	var region *mouse.Region
	for _, r := range m.workspacesMouse.HitMap.Regions() {
		if got, ok := r.Data.(previewActionHit); ok && got == hit {
			copy := r
			region = &copy
			break
		}
	}
	if region == nil {
		t.Fatalf("no action chip hit region for %v", hit)
	}
	cmd := m.workspacesRegionMouse(mouse.MouseAction{
		Type:   mouse.ActionClick,
		X:      region.Rect.X + region.Rect.W/2,
		Y:      region.Rect.Y,
		Region: region,
	})
	run(t, m, cmd)
}

func TestOverviewClickDiffChipOnMainOpensLeaf(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	ws := m.catalog["a"]
	ws.IsMain = true
	m.catalog["a"] = ws
	m.syncBoard()
	m.workspaces.SelectID("a")
	run(t, m, m.previewSelect())

	clickOverviewAction(t, m, previewActionDiff)

	if m.preview.diff == nil || panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Diff) == nil {
		t.Fatal("Diff chip did not open a Diff leaf")
	}
	if m.PreviewInteractive() {
		t.Fatal("Diff chip started typing")
	}
}

func TestOverviewClickDiffChipOnTopicOpensLeaf(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	setOverviewTaskID(t, m, "td-196c42")

	clickOverviewAction(t, m, previewActionDiff)

	if m.preview.diff == nil {
		t.Fatal("Diff chip did not open a Diff leaf")
	}
	if m.PreviewInteractive() {
		t.Fatal("Diff chip started typing")
	}
}

func TestOverviewClickTaskChipOpensIssueLeaf(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	setOverviewTaskID(t, m, "td-196c42")

	clickOverviewAction(t, m, previewActionTask)

	if m.preview.issue == nil || m.preview.issue.view() == nil || m.preview.issue.view().IssueID() != "td-196c42" {
		t.Fatal("Task chip did not open the Issues leaf on TaskID")
	}
	if m.PreviewInteractive() {
		t.Fatal("Task chip started typing")
	}
}
