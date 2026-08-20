package overview

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/terminallink"
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

// The global Sessions browser must answer the braces exactly as the project
// workspace does: { and } cycle Diff target tabs, , and . step files. A key
// that lands on one surface and not the other is a parity bug.
func threeTabPreviewDiff(t *testing.T) *Model {
	t.Helper()
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	if m.preview.diff == nil {
		t.Fatal("openPreviewDiff attached no Diff leaf")
	}
	for _, hash := range []string{"aaaaaaa", "bbbbbbb"} {
		m.preview.diff.tabs.OpenOrFocus(
			workspacediff.Target{Kind: workspacediff.TargetCommit, A: hash},
			&workspacediff.View{},
		)
	}
	if len(m.preview.diff.tabs.Items) != 3 {
		t.Fatalf("tabs = %d, want 3", len(m.preview.diff.tabs.Items))
	}
	m.preview.diff.tabs.Select(0)
	return m
}

func TestBracesCycleDiffTargetTabsInGlobalWorkspaces(t *testing.T) {
	m := threeTabPreviewDiff(t)
	if !m.diffPaneFocused() {
		t.Fatal("premise: Diff leaf should own the keyboard")
	}

	for _, want := range []int{1, 2, 0} { // last step wraps past the end
		handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: '}', Text: "}"})
		if !handled {
			t.Fatal("} was not handled by the focused Diff leaf")
		}
		if m.preview.diff.tabs.Active != want {
			t.Fatalf("} -> active %d, want %d", m.preview.diff.tabs.Active, want)
		}
	}

	for _, want := range []int{2, 1, 0} { // first step wraps past the start
		handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: '{', Text: "{"})
		if !handled {
			t.Fatal("{ was not handled by the focused Diff leaf")
		}
		if m.preview.diff.tabs.Active != want {
			t.Fatalf("{ -> active %d, want %d", m.preview.diff.tabs.Active, want)
		}
	}
}

func TestCommaAndPeriodStepFilesInGlobalWorkspacesDiff(t *testing.T) {
	m := threeTabPreviewDiff(t)
	view := m.preview.diff.view()
	if view == nil {
		t.Fatal("no active Diff view")
	}
	view.State = workspacediff.LoadStateClean
	view.Files = []workspacediff.File{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}}
	view.Cursor = 0

	before := m.preview.diff.tabs.Active
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: '.', Text: "."}); !handled {
		t.Fatal(". was not handled")
	}
	if view.Cursor != 1 {
		t.Fatalf(". -> cursor %d, want 1", view.Cursor)
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: ',', Text: ","}); !handled {
		t.Fatal(", was not handled")
	}
	if view.Cursor != 0 {
		t.Fatalf(", -> cursor %d, want 0", view.Cursor)
	}
	if m.preview.diff.tabs.Active != before {
		t.Fatalf("file stepping moved the active tab to %d", m.preview.diff.tabs.Active)
	}
}

func TestOverviewDiffFileClickSelectsTheRow(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	view := m.preview.diff.view()
	if view == nil {
		t.Fatal("no leaf view")
	}
	view.State = workspacediff.LoadStateReady
	view.Files = []workspacediff.File{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}}
	view.Cursor = 0
	view.Focus = workspacediff.FocusDiff
	view.SetListWidth(40)

	const wide = 450
	m.WorkspacesView(wide, previewTall)

	var fileHit *mouse.Region
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if region.ID != workspacediff.RegionFile {
			continue
		}
		idx, ok := region.Data.(int)
		if ok && idx == 1 {
			copy := region
			fileHit = &copy
		}
	}
	if fileHit == nil {
		t.Fatal("file row 1 was not registered")
	}

	m.WorkspacesMouse(tea.MouseClickMsg{
		X: fileHit.Rect.X + 1, Y: fileHit.Rect.Y, Button: tea.MouseLeft,
	})
	if view.Cursor != 1 {
		t.Fatalf("click selected cursor %d, want 1", view.Cursor)
	}
	if view.Focus != workspacediff.FocusFileList {
		t.Fatalf("click focus = %v, want file list", view.Focus)
	}
}

// WorkspacesKey runs before sidecar's global switch. A focused content leaf
// that claims every leftover key makes @ / ? / digits a no-op. List actions
// (n, D, …) must stay off so they cannot fire under the leaf.
func TestFocusedContentLeavesPassHostGlobals(t *testing.T) {
	stubPreviewTd(t)

	t.Run("diff", func(t *testing.T) {
		m := linkPreviewModel(t, workspaceinventory.KindWorktree)
		run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
		if !m.diffPaneFocused() {
			t.Fatal("premise: Diff leaf should own the keyboard")
		}
		assertHostGlobalsPassThrough(t, m)
		if handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'E', Text: "E"}); !handled || cmd != nil {
			t.Fatalf("E should be swallowed (handled=%v cmd=%v), not start typing", handled, cmd != nil)
		}
		if m.PreviewInteractive() {
			t.Fatal("E on a focused Diff entered the terminal")
		}
	})
	t.Run("issue", func(t *testing.T) {
		m := linkPreviewModel(t, workspaceinventory.KindWorktree)
		run(t, m, m.openPreviewIssue("td-196c42"))
		if !m.issuePaneFocused() {
			t.Fatal("premise: issue leaf should own the keyboard")
		}
		assertHostGlobalsPassThrough(t, m)
		if handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: 'E', Text: "E"}); !handled || cmd != nil {
			t.Fatalf("E should be swallowed (handled=%v cmd=%v), not start typing", handled, cmd != nil)
		}
		if m.PreviewInteractive() || !m.issuePaneFocused() {
			t.Fatal("E on a focused issue entered the terminal or left the leaf")
		}
	})
	t.Run("document", func(t *testing.T) {
		m := linkPreviewModel(t, workspaceinventory.KindWorktree)
		run(t, m, openPreviewDocSpan(m, terminallink.Span{Kind: terminallink.KindFile, Value: "README.md"}))
		if !m.docPaneFocused() {
			t.Fatal("premise: document leaf should own the keyboard")
		}
		assertHostGlobalsPassThrough(t, m)
	})
}

func assertHostGlobalsPassThrough(t *testing.T, m *Model) {
	t.Helper()
	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: '@', Text: "@"})
	if handled {
		t.Fatal("@ was swallowed; the project switcher cannot open")
	}
	if cmd != nil {
		t.Fatal("@ returned a cmd; the host should open the project switcher")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: '?', Text: "?"}); handled {
		t.Fatal("? was swallowed; the command palette cannot open")
	}
	if handled, _ := m.WorkspacesKey(tea.KeyPressMsg{Code: 'n', Text: "n"}); !handled {
		t.Fatal("n should stay with the leaf so it cannot create a worktree")
	}
	if m.CreateOpen() {
		t.Fatal("n on a focused content leaf opened the create flow")
	}
}

func TestFocusedDiffDoesNotStartTypingOnEnter(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	view := m.preview.diff.view()
	if view == nil {
		t.Fatal("no Diff view")
	}
	view.State = workspacediff.LoadStateReady
	view.Files = []workspacediff.File{{Path: "a.go"}}
	view.Focus = workspacediff.FocusDiff

	handled, cmd := m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if !handled || cmd != nil {
		t.Fatalf("enter on hunks: handled=%v cmd=%v; want swallowed", handled, cmd != nil)
	}
	if m.PreviewInteractive() {
		t.Fatal("enter on the Diff hunks entered the terminal")
	}
	if !m.diffPaneFocused() {
		t.Fatal("enter moved focus off the Diff leaf")
	}
}
