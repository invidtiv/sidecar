package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/ui"
)

// previewIssueScrollbarPoint reports the screen point of the issue card's
// scrollbar column at view-local row 0.
func previewIssueScrollbarPoint(t *testing.T, m *Model) (int, int) {
	t.Helper()
	m.WorkspacesView(previewWide, previewTall)
	view := m.previewIssueView()
	bar := view.ScrollbarRect()
	if bar.W != 1 {
		t.Fatalf("preview card reports no interactive bar: %+v", bar)
	}
	for _, region := range m.workspacesMouse.HitMap.Regions() {
		if region.Data == previewIssueRegionKind {
			return region.Rect.X + bar.X, region.Rect.Y + termpreview.HeaderRows + bar.Y
		}
	}
	t.Fatal("the preview registered no issue region")
	return 0, 0
}

// The full gesture through this surface's own input path: a bar press arms
// and StartDrags, held motion moves the offset through the shared core, and a
// release far outside the pane settles it with the offset where the pointer
// left it.
func TestPreviewIssueScrollbarDragEndToEndThroughHost(t *testing.T) {
	m, issue := openLongPreviewIssue(t)
	x, y := previewIssueScrollbarPoint(t, m)
	view := issue.view()

	before := view.ScrollOffset()
	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}))
	if !view.ScrollbarDragging() {
		t.Fatal("bar press did not arm the card's gesture")
	}
	if got := m.workspacesMouse.DragRegion(); got != previewIssueScrollbarKind {
		t.Fatalf("bar press started drag %q, want %s", got, previewIssueScrollbarKind)
	}
	if view.ScrollOffset() != before {
		t.Fatalf("thumb grab at rest moved the offset to %d", view.ScrollOffset())
	}

	want := ui.OffsetAtRow(view.ScrollbarParams(), 2)
	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: x, Y: y + 2, Button: tea.MouseLeft}))
	if view.ScrollOffset() != want {
		t.Fatalf("drag to row 2 left offset %d, want %d", view.ScrollOffset(), want)
	}

	run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: 1, Y: 1}))
	if view.ScrollbarDragging() {
		t.Fatal("release outside the pane did not settle the gesture")
	}
	if view.ScrollOffset() != want {
		t.Fatalf("settle moved the offset to %d", view.ScrollOffset())
	}
}

// A release lost off-window recovers on the next button-less motion, which is
// where the shared drag machinery ends a stale gesture on every other surface.
func TestPreviewIssueScrollbarLostReleaseRecoversOnHover(t *testing.T) {
	m, issue := openLongPreviewIssue(t)
	x, y := previewIssueScrollbarPoint(t, m)
	view := issue.view()

	run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y + 1, Button: tea.MouseLeft}))
	if !view.ScrollbarDragging() {
		t.Fatal("press did not arm the gesture")
	}

	run(t, m, m.WorkspacesMouse(tea.MouseMotionMsg{X: x, Y: y + 2}))
	if view.ScrollbarDragging() {
		t.Fatal("lost release left the scrollbar gesture live")
	}
}
