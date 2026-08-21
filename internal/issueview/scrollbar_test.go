package issueview

import (
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// overflowIssueModel installs sample data in a card small enough that its
// rows overflow: a 40x10 box leaves a live thumb.
func overflowIssueModel(t *testing.T) *Model {
	t.Helper()
	m := New(nil)
	m.SetSize(40, 10)
	apply(t, m, sample(), nil)
	return m
}

func barRect(t *testing.T, m *Model) mouse.Rect {
	t.Helper()
	rect := m.ScrollbarRect()
	if !m.HasScrollbar() || rect.W != 1 || rect.H != m.height {
		t.Fatalf("rect=%+v hasScrollbar=%v", rect, m.HasScrollbar())
	}
	if rect.X != m.leftPadding()+m.contentWidth() {
		t.Fatalf("bar column X = %d, want %d", rect.X, m.leftPadding()+m.contentWidth())
	}
	return rect
}

// A thumb press at the card's own input seam arms a gesture; motions map
// through the shared core; ending settles it where the pointer left it. A
// bar press never activates the card or navigates anywhere.
func TestIssueScrollbar_DragEndToEndAtInputSeam(t *testing.T) {
	m := overflowIssueModel(t)
	rect := barRect(t, m)
	params := m.ScrollbarParams()

	kind, cmd := m.HandleClick(rect.X, rect.Y)
	if kind != HitScrollbar || cmd != nil {
		t.Fatalf("thumb click = kind %v cmd %v", kind, cmd)
	}
	if !m.ScrollbarDragging() {
		t.Fatal("thumb press did not arm a drag")
	}
	if m.Active() || m.Focused() {
		t.Fatal("scrollbar press activated or focused the card")
	}
	if m.IssueID() != "td-abc123" {
		t.Fatalf("scrollbar press navigated to %q", m.IssueID())
	}

	for _, row := range []int{1, 2, 3} {
		if !m.ScrollbarDrag(rect.Y + row) {
			t.Fatalf("drag to row %d found no live gesture", row)
		}
		if want := ui.OffsetAtRow(params, row); m.scroll != want {
			t.Fatalf("drag to row %d left scroll %d, want %d", row, m.scroll, want)
		}
	}

	m.ScrollbarDragEnd()
	if m.ScrollbarDragging() {
		t.Fatal("drag end did not settle the gesture")
	}
	if m.scroll != ui.OffsetAtRow(params, 3) {
		t.Fatalf("settle moved the offset to %d", m.scroll)
	}
}

// Pressing the track jumps so the grabbed point becomes the thumb anchor,
// and the same gesture keeps dragging from there (macOS track-click).
func TestIssueScrollbar_TrackClickJumpsAndAnchors(t *testing.T) {
	m := overflowIssueModel(t)
	rect := barRect(t, m)
	params := m.ScrollbarParams()

	const grabRow = 6 // below the thumb at scroll 0
	kind, _ := m.HandleClick(rect.X, rect.Y+grabRow)
	if kind != HitScrollbar {
		t.Fatalf("track click = %v", kind)
	}
	if got := ui.RowForOffset(params, m.scroll); got != grabRow {
		t.Fatalf("track click anchored thumb top at row %d, want %d", got, grabRow)
	}

	if !m.ScrollbarDrag(rect.Y + grabRow - 2) {
		t.Fatal("post-jump drag found no live gesture")
	}
	if want := ui.OffsetAtRow(params, grabRow-2); m.scroll != want {
		t.Fatalf("post-jump drag left scroll %d, want %d", m.scroll, want)
	}
	m.ScrollbarDragEnd()
}

// Dragging past either end clamps at that end without losing the gesture.
func TestIssueScrollbar_DragClampsWithoutEndingGesture(t *testing.T) {
	m := overflowIssueModel(t)
	rect := barRect(t, m)

	m.HandleClick(rect.X, rect.Y)
	if !m.ScrollbarDrag(-50) || !m.ScrollbarDragging() {
		t.Fatal("dragging above the track lost the gesture")
	}
	if m.scroll != 0 {
		t.Fatalf("above-track drag left scroll %d, want 0", m.scroll)
	}
	maxScroll := m.maxScroll()
	if maxScroll == 0 {
		t.Fatal("fixture does not scroll")
	}
	if !m.ScrollbarDrag(500) || !m.ScrollbarDragging() {
		t.Fatal("dragging below the track lost the gesture")
	}
	if m.scroll != maxScroll {
		t.Fatalf("below-track drag left scroll %d, want %d", m.scroll, maxScroll)
	}
	m.ScrollbarDragEnd()
}

// The bar wins its column over the card's action buttons: a click on the
// bar's cell of a parent/subtask row begins a scrollbar gesture and never
// opens the row, hits never reach into the column, and registering the bar
// after the row regions makes HitMap agree.
func TestIssueScrollbar_RegionPriorityOverActionRows(t *testing.T) {
	m := overflowIssueModel(t)
	rect := barRect(t, m)
	_ = m.View()

	hits := m.Hits()
	var parent *Hit
	for i := range hits {
		if hits[i].Kind == HitParent {
			parent = &hits[i]
			break
		}
	}
	if parent == nil {
		t.Fatal("fixture has no parent row hit")
	}
	for _, h := range hits {
		if h.X < rect.X && h.X+h.W > rect.X {
			t.Fatalf("hit %+v reaches into the bar column %d", h, rect.X)
		}
	}

	kind, cmd := m.HandleClick(rect.X, parent.Y)
	if kind != HitScrollbar || cmd != nil {
		t.Fatalf("bar click on an action row = kind %v cmd %v", kind, cmd)
	}
	if m.IssueID() != "td-abc123" || m.SelectedID() != "td-abc123" {
		t.Fatalf("bar click opened %q/%q", m.SelectedID(), m.IssueID())
	}
	if m.cursor != -1 {
		t.Fatalf("bar click moved the cursor to %d", m.cursor)
	}
	m.ScrollbarDragEnd()

	// Registration order hosts follow: content regions first, bar second.
	hm := mouse.NewHitMap()
	for _, h := range hits {
		hm.AddRect("issue-row", h.X, h.Y, h.W, h.H, h.ID)
	}
	_, geom := ui.RenderScrollbarWithGeometry(m.ScrollbarParams())
	if !geom.HasThumb {
		t.Fatal("fixture lost its thumb")
	}
	hm.AddRect(ui.RegionScrollbarTrack, rect.X, rect.Y, rect.W, rect.H, nil)
	hm.AddRect(ui.RegionScrollbarThumb, rect.X, rect.Y+geom.ThumbRect.Min.Y, rect.W, geom.ThumbRect.Dy(), nil)

	if got := hm.Test(parent.X, parent.Y); got == nil || got.ID != "issue-row" {
		t.Fatalf("row point resolved to %+v, want issue-row", got)
	}
	if got := hm.Test(rect.X, parent.Y); got == nil ||
		(got.ID != ui.RegionScrollbarThumb && got.ID != ui.RegionScrollbarTrack) {
		t.Fatalf("bar point resolved to %+v, want the scrollbar region", got)
	}
}

// Content that fits leaves the reserved column inert: no geometry, and a
// click there behaves exactly as it did before the bar went interactive.
func TestIssueScrollbar_NoRegionsWhenContentFits(t *testing.T) {
	m := New(nil)
	m.SetSize(40, 10)
	apply(t, m, &Data{ID: "td-tiny", Title: "tiny"}, nil)
	if m.HasScrollbar() {
		t.Fatal("fitting card reports an interactive bar")
	}
	if rect := m.ScrollbarRect(); rect.W != 0 || rect.H != 0 {
		t.Fatalf("fitting card reports bar geometry %+v", rect)
	}

	x := m.leftPadding() + m.contentWidth()
	kind, cmd := m.HandleClick(x, 0)
	if kind != HitBody || cmd != nil || !m.Active() {
		t.Fatalf("spacer click = kind %v cmd %v active %v, want the plain body click", kind, cmd, m.Active())
	}
}

// Idle output is byte-identical across a hover round trip and across a full
// press-drag-release gesture that ends where it began.
func TestIssueScrollbar_IdleByteParityAcrossHoverRoundTrip(t *testing.T) {
	m := overflowIssueModel(t)
	rect := barRect(t, m)
	params := m.ScrollbarParams()

	idle := m.View()
	m.HandleHover(rect.X, 0)
	if hover := m.View(); hover == idle {
		t.Fatal("hovering the bar changed no bytes")
	}
	m.HandleHover(-1, -1)
	if back := m.View(); back != idle {
		t.Fatal("hover round trip did not restore idle bytes")
	}

	// Full gesture: grab the thumb, hold without moving, release, clear hover.
	thumbRow := ui.RowForOffset(params, m.scroll)
	m.HandleClick(rect.X, thumbRow)
	if dragged := m.View(); dragged == idle {
		t.Fatal("an active drag changed no bytes")
	}
	m.ScrollbarDrag(thumbRow)
	m.ScrollbarDragEnd()
	m.HandleHover(-1, -1)
	if back := m.View(); back != idle {
		t.Fatal("gesture round trip did not restore idle bytes")
	}
}

// Bar hover and nav-row hover are exclusive: hovering one clears the other,
// and the stale-clearing (-1,-1) call hosts use resets both.
func TestIssueScrollbar_HoverExclusivity(t *testing.T) {
	m := overflowIssueModel(t)
	rect := barRect(t, m)
	_ = m.View()

	var child *Hit
	for i := range m.Hits() {
		h := m.Hits()[i]
		if h.Kind == HitChild {
			child = &h
			break
		}
	}
	if child == nil {
		// Only visible rows carry hits; fall back to whatever nav row is on
		// screen (the parent, near the top of this fixture).
		for i := range m.Hits() {
			h := m.Hits()[i]
			if h.Cursor >= 0 {
				child = &h
				break
			}
		}
	}
	if child == nil {
		t.Fatal("fixture has no navigable row to hover")
	}

	m.HandleHover(child.X+1, child.Y)
	if m.hover < 0 || m.scrollbarHover {
		t.Fatalf("row hover = cursor %d bar %v", m.hover, m.scrollbarHover)
	}
	m.HandleHover(rect.X, child.Y)
	if m.hover != -1 || !m.scrollbarHover {
		t.Fatalf("bar hover = cursor %d bar %v", m.hover, m.scrollbarHover)
	}
	m.HandleHover(child.X+1, child.Y)
	if m.hover < 0 || m.scrollbarHover {
		t.Fatalf("returning to the row = cursor %d bar %v", m.hover, m.scrollbarHover)
	}
	m.HandleHover(-1, -1)
	if m.hover != -1 || m.scrollbarHover {
		t.Fatalf("clearing hover = cursor %d bar %v", m.hover, m.scrollbarHover)
	}
}
