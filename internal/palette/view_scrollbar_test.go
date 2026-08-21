package palette

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

func scrollbarTestModel(t *testing.T, entries int) *Model {
	t.Helper()
	m := New()
	m.SetSize(120, 40)
	m.filtered = make([]PaletteEntry, entries)
	for i := range m.filtered {
		m.filtered[i] = PaletteEntry{CommandID: "cmd"}
	}
	return &m
}

func regionByID(t *testing.T, h *mouse.Handler, id string) mouse.Region {
	t.Helper()
	for _, r := range h.HitMap.Regions() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no %q region in %+v", id, h.HitMap.Regions())
	return mouse.Region{}
}

func hasRegion(h *mouse.Handler, id string) bool {
	for _, r := range h.HitMap.Regions() {
		if r.ID == id {
			return true
		}
	}
	return false
}

func leftClick(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func dragTo(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// The bar's regions are appended after the entry rows they overlap, and the
// reverse scan of HitMap.Test must give them every cell of the bar column.
func TestPaletteScrollbarRegionsWinOverRowsBeneath(t *testing.T) {
	m := scrollbarTestModel(t, 40)
	m.View()

	thumb := regionByID(t, m.mouseHandler, ui.RegionScrollbarThumb)
	track := regionByID(t, m.mouseHandler, ui.RegionScrollbarTrack)

	if track.Rect.W != 1 || thumb.Rect.X != track.Rect.X {
		t.Fatalf("track = %+v, thumb = %+v: the bar is not one column", track.Rect, thumb.Rect)
	}
	if !track.Rect.Contains(thumb.Rect.X, thumb.Rect.Y) ||
		!track.Rect.Contains(thumb.Rect.X, thumb.Rect.Y+thumb.Rect.H-1) {
		t.Fatalf("thumb %+v escapes track %+v", thumb.Rect, track.Rect)
	}

	item := regionByID(t, m.mouseHandler, fmt.Sprintf("%s0", paletteItemPrefix))
	if !item.Rect.Contains(thumb.Rect.X, thumb.Rect.Y) {
		t.Fatal("test premise: the thumb does not sit over an entry row")
	}
	if got := m.mouseHandler.HitMap.Test(thumb.Rect.X, thumb.Rect.Y); got == nil || got.ID != ui.RegionScrollbarThumb {
		t.Fatalf("thumb cell resolved to %+v, want the thumb", got)
	}
	belowThumb := thumb.Rect.Y + thumb.Rect.H
	if belowThumb >= track.Rect.Y+track.Rect.H {
		t.Fatal("test premise: no pure track row below the thumb")
	}
	if got := m.mouseHandler.HitMap.Test(track.Rect.X, belowThumb); got == nil || got.ID != ui.RegionScrollbarTrack {
		t.Fatalf("track cell resolved to %+v, want the track", got)
	}
}

func TestPaletteScrollbarRegistersNothingWhenEverythingFits(t *testing.T) {
	m := scrollbarTestModel(t, 5)
	m.View()
	if hasRegion(m.mouseHandler, ui.RegionScrollbarThumb) || hasRegion(m.mouseHandler, ui.RegionScrollbarTrack) {
		t.Fatal("a fitting list registered scrollbar regions")
	}
	if _, ok := m.listScrollbarParams(); ok {
		t.Fatal("pointer params exist for a fitting list")
	}
	if m.handleScrollbarPointer(leftClick(70, m.height/2)) {
		t.Fatal("a fitting list claimed a pointer event")
	}
	if m.mouseHandler.IsDragging() {
		t.Fatal("a fitting list started a drag")
	}
}

// Press thumb → drag → the offset tracks ui.OffsetAtRow of the pointer,
// clamps at both ends without losing the gesture, and a release anywhere
// settles it.
func TestPaletteThumbDragMovesTheWindowEndToEnd(t *testing.T) {
	m := scrollbarTestModel(t, 40)
	m.View()
	visibleCount, _ := m.listWindow()
	params := ui.ScrollbarParams{
		TotalItems:   len(m.filtered),
		ScrollOffset: 0,
		VisibleItems: visibleCount,
		TrackHeight:  visibleCount,
	}

	thumb := regionByID(t, m.mouseHandler, ui.RegionScrollbarThumb)
	pressY := thumb.Rect.Y
	if _, cmd := m.handleMouse(leftClick(thumb.Rect.X, pressY)); cmd != nil {
		t.Fatal("pressing the thumb produced a command")
	}
	if !m.mouseHandler.IsDragging() || m.mouseHandler.DragRegion() != ui.RegionScrollbarThumb {
		t.Fatal("pressing the thumb did not start a drag")
	}

	down := pressY + 4
	if _, cmd := m.handleMouse(dragTo(thumb.Rect.X, down)); cmd != nil {
		t.Fatal("dragging the thumb produced a command")
	}
	want := ui.OffsetAtRow(params, down-pressY)
	if m.offset != want {
		t.Fatalf("offset after dragging down = %d, want %d", m.offset, want)
	}
	if m.cursor != want {
		t.Fatalf("cursor after dragging down = %d, want it carried with the window to %d", m.cursor, want)
	}

	maxOffset := params.TotalItems - params.VisibleItems
	m.handleMouse(dragTo(thumb.Rect.X, pressY+500))
	if m.offset != maxOffset {
		t.Fatalf("offset past the end = %d, want the clamp at %d", m.offset, maxOffset)
	}
	if !m.mouseHandler.IsDragging() {
		t.Fatal("clamping at the end lost the gesture")
	}
	m.handleMouse(dragTo(thumb.Rect.X, pressY-500))
	if want := 0; m.offset != want {
		t.Fatalf("offset above the start = %d, want the clamp at %d", m.offset, want)
	}

	m.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1, Button: tea.MouseLeft})
	if m.mouseHandler.IsDragging() {
		t.Fatal("releasing away from the bar did not settle the drag")
	}
	m.handleMouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: pressY + 9})
	if m.offset != 0 {
		t.Fatalf("plain hover after release moved the window to %d", m.offset)
	}
}

// Track click = jump-to-spot anchored at the grab row, and the same gesture
// keeps going when the pointer moves again.
func TestPaletteTrackClickJumpsAnchoredAndKeepsDragging(t *testing.T) {
	m := scrollbarTestModel(t, 40)
	m.View()
	visibleCount, _ := m.listWindow()
	params := ui.ScrollbarParams{
		TotalItems:   len(m.filtered),
		ScrollOffset: 0,
		VisibleItems: visibleCount,
		TrackHeight:  visibleCount,
	}

	track := regionByID(t, m.mouseHandler, ui.RegionScrollbarTrack)
	grabRow := track.Rect.H / 2
	grabY := track.Rect.Y + grabRow
	m.handleMouse(leftClick(track.Rect.X, grabY))

	if want := ui.OffsetAtRow(params, grabRow); m.offset != want {
		t.Fatalf("offset after track click = %d, want %d", m.offset, want)
	}
	if !m.mouseHandler.IsDragging() || m.mouseHandler.DragRegion() != ui.RegionScrollbarTrack {
		t.Fatal("track click did not start a continuing drag")
	}

	upY := grabY - 2
	m.handleMouse(dragTo(track.Rect.X, upY))
	if want := ui.OffsetAtRow(params, upY-track.Rect.Y); m.offset != want {
		t.Fatalf("offset after continued drag = %d, want %d", m.offset, want)
	}
}

// While travel (track minus thumb) does not exceed maxOffset, re-rendering the
// jumped-to offset pins the thumb top back onto the grabbed row exactly.
func TestPaletteTrackClickPinsTheThumbToTheGrabbedRow(t *testing.T) {
	m := scrollbarTestModel(t, 40)
	m.View()

	track := regionByID(t, m.mouseHandler, ui.RegionScrollbarTrack)
	grabRow := 3
	m.handleMouse(leftClick(track.Rect.X, track.Rect.Y+grabRow))

	m.clearModal()
	m.View()
	thumb := regionByID(t, m.mouseHandler, ui.RegionScrollbarThumb)
	if got := thumb.Rect.Y - track.Rect.Y; got != grabRow {
		t.Fatalf("thumb top landed on track row %d, want the grabbed row %d", got, grabRow)
	}
}

// A second press inside the double-click window arrives as ActionDoubleClick,
// which the modal framework drops. The bar must treat it as a fresh grab.
func TestPaletteSecondQuickPressStillGrabsTheBar(t *testing.T) {
	m := scrollbarTestModel(t, 40)
	m.View()

	thumb := regionByID(t, m.mouseHandler, ui.RegionScrollbarThumb)
	m.handleMouse(leftClick(thumb.Rect.X, thumb.Rect.Y))
	m.handleMouse(tea.MouseReleaseMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})

	// Press again immediately at the same cell: the handler reports a
	// double-click and Modal.HandleMouse swallows it, but the gesture must
	// start all the same.
	if _, cmd := m.handleMouse(leftClick(thumb.Rect.X, thumb.Rect.Y)); cmd != nil {
		t.Fatal("the second press produced a command")
	}
	if !m.mouseHandler.IsDragging() || m.mouseHandler.DragRegion() != ui.RegionScrollbarThumb {
		t.Fatal("a quick second press on the thumb did not grab the bar")
	}
}

// Idle output is untouched; hover and an active drag light the bar up through
// the core's state hooks.
func TestPaletteScrollbarStylingFollowsPointerState(t *testing.T) {
	m := scrollbarTestModel(t, 40)
	m.View()
	section := m.listSection()
	const cw = 60

	idle := section.Render(cw, "", "").Content
	hoverThumb := section.Render(cw, "", ui.RegionScrollbarThumb).Content
	hoverTrack := section.Render(cw, "", ui.RegionScrollbarTrack).Content
	if idle == hoverThumb || idle == hoverTrack {
		t.Fatal("hovering the bar did not restyle it")
	}

	m.mouseHandler.StartDrag(0, 0, ui.RegionScrollbarThumb, 0)
	dragging := section.Render(cw, "", ui.RegionScrollbarThumb).Content
	if dragging == hoverThumb {
		t.Fatal("an active drag renders identically to hover")
	}
}
