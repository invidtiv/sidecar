package docview

import (
	"fmt"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// overflowDocModel loads a raw document tall enough to scroll in a 30x4 box
// drawn at origin (10, 5), so scrollbar geometry has somewhere real to sit.
func overflowDocModel(t *testing.T, lines int) *Model {
	t.Helper()
	m := newTestModel(t)
	m.SetSize(30, 4)
	m.loading = false
	m.rendered = false
	body := make([]string, lines)
	for i := range body {
		body[i] = fmt.Sprintf("line %d", i+1)
	}
	m.result = filepreview.PreviewResult{
		Content: strings.Join(body, "\n"),
		Lines:   body,
	}
	m.invalidateRender()
	m.SetOrigin(10, 5)
	return m
}

func clickAt(x, y int) mouse.MouseAction {
	return mouse.MouseAction{Type: mouse.ActionClick, X: x, Y: y}
}

func dragTo(x, y int) mouse.MouseAction {
	return mouse.MouseAction{Type: mouse.ActionDrag, X: x, Y: y}
}

func releaseAt(x, y int) mouse.MouseAction {
	return mouse.MouseAction{Type: mouse.ActionDragEnd, X: x, Y: y}
}

// A thumb press at the model's own input seam arms a gesture; every motion
// maps through the shared core; releasing anywhere — including far outside
// the bar — settles it with the offset where the pointer left it.
func TestDocScrollbar_DragEndToEndAtInputSeam(t *testing.T) {
	m := overflowDocModel(t, 20)
	rect := m.ScrollbarRect()
	params := m.ScrollbarParams()
	if !m.HasScrollbar() || rect.W != 1 || rect.H != 4 {
		t.Fatalf("rect=%+v hasScrollbar=%v", rect, m.HasScrollbar())
	}

	if res := m.HandleSelectionMouse(clickAt(rect.X, rect.Y)); !res.Handled {
		t.Fatal("thumb press was not consumed at the input seam")
	}
	if !m.ScrollbarDragging() {
		t.Fatal("thumb press did not arm a drag")
	}
	if m.HasSelection() {
		t.Fatal("scrollbar press started a selection")
	}

	for _, row := range []int{1, 2, 3} {
		if res := m.HandleSelectionMouse(dragTo(rect.X, rect.Y+row)); !res.Handled {
			t.Fatalf("drag to row %d was not consumed", row)
		}
		want := ui.OffsetAtRow(params, row)
		if m.scroll != want {
			t.Fatalf("drag to row %d left scroll %d, want %d", row, m.scroll, want)
		}
	}

	// Release anywhere settles; the offset is view state and stays.
	if res := m.HandleSelectionMouse(releaseAt(rect.X+5, rect.Y+9)); !res.Handled {
		t.Fatal("release outside the bar was not consumed")
	}
	if m.ScrollbarDragging() {
		t.Fatal("release did not settle the gesture")
	}
	if m.scroll != ui.OffsetAtRow(params, 3) {
		t.Fatalf("settle moved the offset to %d", m.scroll)
	}
}

// Pressing the track jumps so the grabbed point becomes the thumb anchor,
// and the same gesture keeps dragging from there (macOS track-click).
func TestDocScrollbar_TrackClickJumpsAndAnchors(t *testing.T) {
	m := overflowDocModel(t, 20)
	rect := m.ScrollbarRect()
	params := m.ScrollbarParams()

	if res := m.HandleSelectionMouse(clickAt(rect.X, rect.Y+3)); !res.Handled {
		t.Fatal("track press was not consumed")
	}
	if got := ui.RowForOffset(params, m.scroll); got != 3 {
		t.Fatalf("track click anchored thumb top at row %d, want 3", got)
	}

	// The continuing drag maps the pointer straight onto track rows.
	if res := m.HandleSelectionMouse(dragTo(rect.X, rect.Y+1)); !res.Handled {
		t.Fatal("post-jump drag was not consumed")
	}
	if m.scroll != ui.OffsetAtRow(params, 1) {
		t.Fatalf("post-jump drag left scroll %d, want %d", m.scroll, ui.OffsetAtRow(params, 1))
	}
	m.HandleSelectionMouse(releaseAt(rect.X, rect.Y+1))
}

// Dragging past either end clamps at that end without losing the gesture.
func TestDocScrollbar_DragClampsWithoutEndingGesture(t *testing.T) {
	m := overflowDocModel(t, 20)
	rect := m.ScrollbarRect()

	m.HandleSelectionMouse(clickAt(rect.X, rect.Y))
	if res := m.HandleSelectionMouse(dragTo(rect.X, rect.Y-50)); !res.Handled || !m.ScrollbarDragging() {
		t.Fatal("dragging above the track lost the gesture")
	}
	if m.scroll != 0 {
		t.Fatalf("above-track drag left scroll %d, want 0", m.scroll)
	}
	if res := m.HandleSelectionMouse(dragTo(rect.X, rect.Y+50)); !res.Handled || !m.ScrollbarDragging() {
		t.Fatal("dragging below the track lost the gesture")
	}
	maxScroll := m.maxScroll()
	if maxScroll == 0 || m.scroll != maxScroll {
		t.Fatalf("below-track drag left scroll %d, want %d", m.scroll, maxScroll)
	}
	m.HandleSelectionMouse(releaseAt(rect.X, rect.Y))
}

// The bar wins its column: content links are never scanned into the bar's
// cells, a bar press never reaches the selection engine even on a link row,
// and registering the bar after link regions makes HitMap agree.
func TestDocScrollbar_RegionPriorityOverLinks(t *testing.T) {
	m := overflowDocModel(t, 12)
	// Tokens sit early in the row so truncation cannot hide them; the URL is
	// activatable without a resolution index.
	m.result.Lines[2] = "https://example.com/x and internal/ui/scrollbar.go"
	m.result.Content = strings.Join(m.result.Lines, "\n")
	m.invalidateRender()

	rect := m.ScrollbarRect()
	frame := m.ScanContentLinks(m.View(), contentlink.FrameOptions{})
	if len(frame.Hits) == 0 {
		t.Fatal("fixture produced no link hits to prioritize against")
	}
	for _, hit := range frame.Hits {
		if hit.Rect.X+hit.Rect.W > rect.X {
			t.Fatalf("link hit %v reaches into the bar column %d", hit.Rect, rect.X)
		}
	}

	// A press on the bar is consumed at the seam no matter what sits beside
	// it: no selection, no click-through for a host to activate a link with.
	rowY := rect.Y + 2 // the line carrying the links
	res := m.HandleSelectionMouse(clickAt(rect.X, rowY))
	if !res.Handled || res.ClickThrough {
		t.Fatalf("bar press = %+v, want consumed without click-through", res)
	}
	if m.HasSelection() {
		t.Fatal("bar press started a selection over a link row")
	}
	m.HandleSelectionMouse(releaseAt(rect.X, rowY))

	// Registration order the hosts follow: content regions first, bar second,
	// and reverse-scan priority resolves the shared column to the bar.
	hm := mouse.NewHitMap()
	for i, hit := range frame.Hits {
		hm.AddRect("doc-link", hit.Rect.X, hit.Rect.Y, hit.Rect.W, hit.Rect.H, i)
	}
	_, geom := ui.RenderScrollbarWithGeometry(m.ScrollbarParams())
	if !geom.HasThumb {
		t.Fatal("fixture lost its thumb")
	}
	hm.AddRect(ui.RegionScrollbarTrack, rect.X, rect.Y, rect.W, rect.H, nil)
	hm.AddRect(ui.RegionScrollbarThumb, rect.X, rect.Y+geom.ThumbRect.Min.Y, rect.W, geom.ThumbRect.Dy(), nil)

	linkPoint := frame.Hits[0].Rect
	if got := hm.Test(linkPoint.X, linkPoint.Y); got == nil || got.ID != "doc-link" {
		t.Fatalf("link point resolved to %+v, want doc-link", got)
	}
	if got := hm.Test(rect.X, rowY); got == nil ||
		(got.ID != ui.RegionScrollbarThumb && got.ID != ui.RegionScrollbarTrack) {
		t.Fatalf("bar point resolved to %+v, want the scrollbar region", got)
	}
}

// Content that fits leaves the reserved column inert: no geometry, no regions
// implied, presses fall through untouched.
func TestDocScrollbar_NoRegionsWhenContentFits(t *testing.T) {
	m := overflowDocModel(t, 2)
	if m.HasScrollbar() {
		t.Fatal("fitting content reports an interactive bar")
	}
	if rect := m.ScrollbarRect(); rect.W != 0 || rect.H != 0 {
		t.Fatalf("fitting content reports bar geometry %+v", rect)
	}
	before := m.View()
	if res := m.HandleSelectionMouse(clickAt(m.originX+m.contentWidth(), m.originY)); res.Handled {
		t.Fatal("spacer-column press was claimed by the scrollbar")
	}
	if after := m.View(); after != before {
		t.Fatal("spacer-column press changed the render")
	}
}

// Idle output is byte-identical across a hover round trip and across a full
// press-drag-release gesture that ends where it began.
func TestDocScrollbar_IdleByteParityAcrossHoverRoundTrip(t *testing.T) {
	m := overflowDocModel(t, 20)
	rect := m.ScrollbarRect()

	idle := m.View()
	m.HandleSelectionMouse(mouse.MouseAction{Type: mouse.ActionHover, X: rect.X, Y: rect.Y})
	if hover := m.View(); hover == idle {
		t.Fatal("hovering the bar changed no bytes")
	}
	m.HandleSelectionMouse(mouse.MouseAction{Type: mouse.ActionHover, X: rect.X - 3, Y: rect.Y})
	if back := m.View(); back != idle {
		t.Fatal("hover round trip did not restore idle bytes")
	}

	// Full gesture ending where it began: grab the thumb, hold, release.
	_, geom := ui.RenderScrollbarWithGeometry(m.ScrollbarParams())
	thumbRow := rect.Y + geom.ThumbRect.Min.Y
	m.HandleSelectionMouse(clickAt(rect.X, thumbRow))
	if dragged := m.View(); dragged == idle {
		t.Fatal("an active drag changed no bytes")
	}
	m.HandleSelectionMouse(dragTo(rect.X, thumbRow))
	m.HandleSelectionMouse(releaseAt(rect.X, thumbRow))
	m.HandleSelectionMouse(mouse.MouseAction{Type: mouse.ActionHover, X: rect.X - 3, Y: rect.Y})
	if back := m.View(); back != idle {
		t.Fatal("gesture round trip did not restore idle bytes")
	}
}

// A selection drag that crosses the bar column mid-gesture stays a selection:
// only gestures that STARTED on the bar are scrollbar drags.
func TestDocScrollbar_SelectionDragCrossingBarStaysSelection(t *testing.T) {
	m := overflowDocModel(t, 20)
	rect := m.ScrollbarRect()

	if res := m.HandleSelectionMouse(clickAt(m.originX+m.display().gutterWidth, m.originY)); !res.Handled {
		t.Fatal("content press was not handled by the selection engine")
	}
	before := m.scroll
	if res := m.HandleSelectionMouse(dragTo(rect.X, m.originY+1)); !res.Handled {
		t.Fatal("selection drag crossing the bar was dropped")
	}
	if m.ScrollbarDragging() {
		t.Fatal("a crossing selection drag armed the scrollbar")
	}
	if m.scroll != before {
		t.Fatalf("crossing selection drag scrolled the document to %d", m.scroll)
	}
	m.HandleSelectionMouse(releaseAt(rect.X, m.originY+1))
}

// A lost release — pointer left the window, modal opened — abandons the
// gesture instead of leaving it live.
func TestDocScrollbar_AbandonSettlesGesture(t *testing.T) {
	m := overflowDocModel(t, 20)
	rect := m.ScrollbarRect()

	m.HandleSelectionMouse(clickAt(rect.X, rect.Y))
	if !m.ScrollbarDragging() {
		t.Fatal("press did not arm the gesture")
	}
	m.AbandonSelection()
	if m.ScrollbarDragging() {
		t.Fatal("abandon left the scrollbar gesture live")
	}
}
