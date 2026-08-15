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

	got := m.renderOutputPreview(previewWide, previewTall)
	stripped := ansi.Strip(got)
	if !strings.Contains(stripped, "Working Tree") && !strings.Contains(stripped, "Loading diff") {
		t.Fatalf("Diff compositor arm drew a blank box:\n%s", stripped)
	}
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf("previewTab = %v, want Output", m.previewTab)
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
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf("premise: previewTab = %v, want Output", m.previewTab)
	}
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
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf(". on focused Diff leaf set previewTab = %v (hid the tree)", m.previewTab)
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
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf(", on focused Diff leaf set previewTab = %v (hid the tree)", m.previewTab)
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

func TestOverviewOpenPreviewDiffForcesOutput(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	m.previewTab = workspacediff.TabDiff
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if m.previewTab != workspacediff.TabOutput {
		t.Fatalf("previewTab = %v, want Output", m.previewTab)
	}
	if m.preview.diff == nil {
		t.Fatal("openPreviewDiff did not open a Diff leaf")
	}
}
