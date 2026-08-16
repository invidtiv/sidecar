package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestOverviewCompositorDiffArmIsNotBlank(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if m.preview.diff == nil || m.preview.diff.view() == nil {
		t.Fatal("openPreviewDiff did not attach a Diff view")
	}
	if panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Diff) == nil {
		t.Fatal("tree has no Diff leaf")
	}

	// A loaded empty snapshot still paints "Working Tree" chrome. Without an
	// explicit compositor arm the box would be blank.
	view := m.preview.diff.view()
	view.State = workspacediff.LoadStateClean
	view.ApplySnapshot()

	// Through the surface's own view, because placing and framing the leaves is
	// the shared frame's job now, not the terminal renderer's.
	stripped := ansi.Strip(m.WorkspacesView(previewWide, previewTall))
	if !strings.Contains(stripped, "Working Tree") && !strings.Contains(stripped, "Loading diff") {
		t.Fatalf("Diff compositor arm drew a blank box:\n%s", stripped)
	}
}

func TestGlobalPreviewDiffWheelStopsAtRenderedBoundaries(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	view := m.preview.diff.view()
	if view == nil {
		t.Fatal("openPreviewDiff did not attach a Diff view")
	}
	view.Content = "diff --git a/a b/a\none\ntwo\nthree\nfour\nfive"
	view.Files = []workspacediff.File{{Path: "a", Raw: "one\ntwo\nthree\nfour\nfive"}}
	m.WorkspacesView(previewWide, previewTall)

	var x, y int
	found := false
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if kind, ok := region.Data.(string); ok && kind == previewDiffRegionKind {
			x, y = region.Rect.X+region.Rect.W/2, region.Rect.Y+region.Rect.H-2
			found = true
			break
		}
	}
	if !found {
		t.Fatal("diff leaf region was not registered")
	}
	if !m.WorkspacesWheelAtBoundary(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelUp}) {
		t.Fatal("global diff top wheel was not bounded")
	}
	view.ScrollContent(1000, view.Height())
	if view.DiffScroll != view.ContentMaxScroll(view.Height()) {
		t.Fatalf("global diff overscrolled to %d", view.DiffScroll)
	}
	if !m.WorkspacesWheelAtBoundary(tea.MouseWheelMsg{X: x, Y: y, Button: tea.MouseWheelDown}) {
		t.Fatal("global diff bottom wheel was not bounded")
	}
}

func TestOverviewDiffFocusContextIsNotRoot(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if got := m.WorkspaceFocusContext(); got != ctxGlobalWorkspacesDiff {
		t.Fatalf("context = %q, want %q", got, ctxGlobalWorkspacesDiff)
	}
	handled, cmd := m.previewDiffPaneKey(tea.KeyPressMsg{Code: 'q', Text: "q"})
	if !handled {
		t.Fatal("q was not handled")
	}
	if cmd != nil {
		run(t, m, cmd)
	}
	if m.preview.diff != nil {
		t.Fatal("q did not hide the Diff leaf")
	}
	if m.WorkspaceFocusContext() == ctxGlobalWorkspacesDiff {
		t.Fatal("hidden Diff kept global-workspaces-diff context")
	}
}

func TestWorkspacesKeyDotOnFocusedDiffLeafKeepsTree(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if !m.diffPaneFocused() {
		t.Fatal("premise: Diff leaf should own the keyboard")
	}

	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: '.', Text: "."})
	if !handled {
		t.Fatal(". was not handled")
	}
	if cmd != nil {
		run(t, m, cmd)
	}
	if m.preview.diff == nil || panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Diff) == nil {
		t.Fatal(". hid the Diff leaf")
	}
	if !m.diffPaneFocused() {
		t.Fatal(". moved focus off the Diff leaf")
	}

	handled, cmd = m.WorkspacesKey(tea.KeyPressMsg{Code: ',', Text: ","})
	if !handled {
		t.Fatal(", was not handled")
	}
	if cmd != nil {
		run(t, m, cmd)
	}
}

func TestOverviewDiffLeafRegistersFileHitsAndDragsLeafDivider(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	view := m.preview.diff.view()
	if view == nil {
		t.Fatal("no leaf view")
	}
	view.State = workspacediff.LoadStateClean
	view.Files = []workspacediff.File{{Path: "a.go"}}
	view.SetListWidth(40)
	m.diff.SetListWidth(99)

	const wide = 450
	m.WorkspacesView(wide, previewTall)

	var fileHit, divider *mouse.Region
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		switch region.ID {
		case workspacediff.RegionFile:
			copy := region
			fileHit = &copy
		case previewDiffDividerKind:
			copy := region
			divider = &copy
		}
	}
	if fileHit == nil {
		t.Fatal("leaf FileHits were not registered")
	}
	if divider == nil {
		t.Fatal("leaf divider was not registered")
	}
	if body, ok := m.previewDiffLeafBody(); !ok || body.W < workspacediff.CollapseThreshold {
		t.Fatalf("leaf body too narrow for an internal divider: %#v ok=%v", body, ok)
	}

	x, y := divider.Rect.X+1, divider.Rect.Y+1
	m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	m.WorkspacesMouse(tea.MouseMotionMsg{X: x + 8, Y: y, Button: tea.MouseLeft})
	if view.ListWidth() == 40 {
		t.Fatal("divider drag did not mutate the leaf view")
	}
	if m.diff.ListWidth() != 99 {
		t.Fatalf("divider drag mutated the tab view: listWidth=%d, want 99", m.diff.ListWidth())
	}
}

func TestOverviewOpenPreviewDiffOpensLeaf(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if m.preview.diff == nil {
		t.Fatal("openPreviewDiff did not open a Diff leaf")
	}
}

func TestOverviewCommitTabApplyLeavesLoading(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.MustParse("abc1234")))
	view := m.preview.diff.view()
	if view == nil {
		t.Fatal("no commit view")
	}
	run(t, m, func() tea.Msg {
		return workspacediff.CommitDetailMsg{
			Epoch: view.Epoch, WorkspaceID: view.WorkspaceID, Identity: "c:abc1234",
			Hash: "abc1234",
			Commit: &workspacediff.CommitDetail{
				Hash: "abc1234", ShortHash: "abc1234", Subject: "one",
				Files: []workspacediff.CommitFile{{Path: "a.go"}},
			},
		}
	})
	if view.CommitDetail == nil || view.State == workspacediff.LoadStateLoading {
		t.Fatalf("after apply: detail=%#v state=%v", view.CommitDetail, view.State)
	}
	if view.Focus != workspacediff.FocusCommitFiles {
		t.Fatalf("focus = %v, want commit file list", view.Focus)
	}
	got := view.Render(80, 10, workspacediff.RenderOpts{})
	if strings.Contains(got, "Loading diff…") || strings.Contains(got, "Working Tree vs HEAD") {
		t.Fatalf("render = %q", got)
	}
}

func TestOverviewRangeTabApplyRefusesSnapshot(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.MustParse("aaa1111..bbb2222")))
	view := m.preview.diff.view()
	if view == nil {
		t.Fatal("no range view")
	}
	run(t, m, func() tea.Msg {
		return workspacediff.RangeMsg{
			Epoch: view.Epoch, WorkspaceID: view.WorkspaceID, Identity: "r:aaa1111..bbb2222",
			Raw: "diff --git a/a.go b/a.go\n--- a/a.go\n+++ b/a.go\n@@ -0,0 +1 @@\n+hi\n",
		}
	})
	if view.State == workspacediff.LoadStateLoading || len(view.Files) != 1 {
		t.Fatalf("range apply: state=%v files=%#v", view.State, view.Files)
	}
	run(t, m, func() tea.Msg {
		return workspacediff.SnapshotMsg{
			Epoch: view.Epoch, WorkspaceID: view.WorkspaceID, Identity: "r:aaa1111..bbb2222",
			Snapshot: &workspacediff.Snapshot{State: workspacediff.LoadStateReady, WorkingTree: "wt"},
		}
	})
	if view.Snapshot != nil || (len(view.Files) == 1 && view.Files[0].Path != "a.go") {
		t.Fatalf("snapshot landed on range tab: snapshot=%v files=%#v", view.Snapshot != nil, view.Files)
	}
}
