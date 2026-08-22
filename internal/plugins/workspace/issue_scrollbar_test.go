package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/ui"
)

// issueScrollbarFixture composes a pane tree whose issue leaf overflows and
// reports the screen point of its scrollbar column, the card, and the box the
// card's row 0 starts at.
func issueScrollbarFixture(t *testing.T) (*Plugin, *issuePane, int, int) {
	t.Helper()
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	steelThreadPaneTree(t, p, root)

	const width, height = 100, 24
	composePaneTree(t, p, width, height)
	box := issueLeafBox(t, p, width, height)
	inner := insetPanelChrome(box)
	topY := inner.Y + terminalHeaderRows

	issue := p.issues[3]
	if issue == nil || issue.view() == nil {
		t.Fatal("fixture has no issue card")
	}
	view := issue.view()
	bar := view.ScrollbarRect()
	if bar.W != 1 || !view.HasScrollbar() {
		t.Fatalf("fixture card reports no interactive bar: rect %+v", bar)
	}
	x := inner.X + bar.X
	return p, issue, x, topY
}

// The full gesture through this host's own input path: a bar press arms and
// StartDrags, held motion moves the offset through the shared core, and a
// release far outside the pane settles it with the offset where the pointer
// left it.
func TestIssueLeafScrollbarDragEndToEndThroughHost(t *testing.T) {
	p, issue, x, topY := issueScrollbarFixture(t)
	view := issue.view()

	if region := p.mouseHandler.HitMap.Test(x, topY); region == nil || region.ID != regionPaneLeaf {
		t.Fatalf("bar column resolves to %+v, want %s", region, regionPaneLeaf)
	}

	before := view.ScrollOffset()
	p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: topY, Button: tea.MouseLeft}))
	if !view.ScrollbarDragging() {
		t.Fatal("bar press did not arm the card's gesture")
	}
	if p.mouseHandler.DragRegion() != regionIssueScrollbar {
		t.Fatalf("bar press started drag %q, want %s", p.mouseHandler.DragRegion(), regionIssueScrollbar)
	}
	if view.ScrollOffset() != before {
		t.Fatalf("thumb grab at rest moved the offset to %d", view.ScrollOffset())
	}

	want := ui.OffsetAtRow(view.ScrollbarParams(), 3)
	p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: x, Y: topY + 3, Button: tea.MouseLeft}))
	if view.ScrollOffset() != want {
		t.Fatalf("drag to row 3 left offset %d, want %d", view.ScrollOffset(), want)
	}

	p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 1, Y: 1}))
	if view.ScrollbarDragging() {
		t.Fatal("release outside the pane did not settle the gesture")
	}
	if view.ScrollOffset() != want {
		t.Fatalf("settle moved the offset to %d", view.ScrollOffset())
	}
	if p.issueScrollLeaf != 0 {
		t.Fatal("settle left the host holding the leaf")
	}
}

// A click on the bar without any following motion — press and release where
// it was — must not leave the thumb rendered pressed.
func TestIssueLeafScrollbarClickOnlyDoesNotStick(t *testing.T) {
	p, issue, x, topY := issueScrollbarFixture(t)
	view := issue.view()

	before := view.View()
	p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: topY, Button: tea.MouseLeft}))
	if !view.ScrollbarDragging() {
		t.Fatal("press did not arm the gesture")
	}
	p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: x, Y: topY, Button: tea.MouseLeft}))
	if view.ScrollbarDragging() {
		t.Fatal("click-only press left the gesture latched")
	}

	// The recovery convention: a hover arriving later proves no drag is held,
	// and the card's output is exactly what it was before the gesture.
	p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: 0, Y: 0}))
	if view.ScrollbarDragging() {
		t.Fatal("hover found a stale gesture still latched")
	}
	if after := view.View(); after != before {
		t.Fatal("click-only gesture left pressed-style bytes behind")
	}
}

// A release lost off-window recovers on the next button-less motion, the same
// boundary every other scrollbar surface uses.
func TestIssueLeafScrollbarLostReleaseRecoversOnHover(t *testing.T) {
	p, issue, x, topY := issueScrollbarFixture(t)
	view := issue.view()

	p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: x, Y: topY + 1, Button: tea.MouseLeft}))
	if !view.ScrollbarDragging() {
		t.Fatal("press did not arm the gesture")
	}

	// No release ever arrives; the shared handler ends the stale drag on the
	// first button-less motion and this host drops its half at that boundary.
	p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: x, Y: topY + 2}))
	if view.ScrollbarDragging() {
		t.Fatal("lost release left the scrollbar gesture live")
	}
}
