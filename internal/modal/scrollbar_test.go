package modal

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/scroll"
	"github.com/marcus/sidecar/internal/styles"
)

// overflowModal builds a modal whose body is tall enough to scroll, plus the
// handler its regions were registered into.
func overflowModal(t *testing.T) (*Modal, *mouse.Handler) {
	t.Helper()
	m := New("Test", WithWidth(40))
	for i := 0; i < 30; i++ {
		m.AddSection(Text(fmt.Sprintf("Line %d", i)))
	}
	handler := mouse.NewHandler()
	m.Render(80, 24, handler)
	return m, handler
}

// barRegions finds the registered scrollbar track and thumb regions.
func barRegions(t *testing.T, h *mouse.Handler) (track, thumb *mouse.Region) {
	t.Helper()
	for i := range h.HitMap.Regions() {
		r := &h.HitMap.Regions()[i]
		switch r.ID {
		case RegionScrollbarTrack:
			track = r
		case RegionScrollbarThumb:
			thumb = r
		}
	}
	if track == nil || thumb == nil {
		t.Fatal("expected scrollbar track and thumb regions to be registered")
	}
	return track, thumb
}

func clickAt(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func motionAt(x, y int) tea.MouseMotionMsg {
	// A live drag arrives as left-button motion; button-less motion means
	// "no buttons held", which the mouse handler reads as a lost release.
	return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func bareMotionAt(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y}
}

func releaseAt(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// viewportParams reconstructs the ScrollbarParams the framework's viewport bar
// was drawn with: one row of content per row of thumb math.
func (m *Modal) viewportParams() (total, visible int) {
	total = 0
	for _, s := range m.sections {
		res := s.Render(40, "", "")
		total += measureHeight(res.Content)
	}
	visible = m.lastViewportH
	return total, max(1, visible)
}

func TestViewportThumbDragEndToEnd(t *testing.T) {
	m, h := overflowModal(t)
	track, thumb := barRegions(t, h)

	// Press the middle of the thumb.
	grabY := thumb.Rect.Y + thumb.Rect.H/2
	m.HandleMouse(clickAt(thumb.Rect.X, grabY), h)
	if !h.IsDragging() || h.DragRegion() != RegionScrollbarThumb {
		t.Fatalf("thumb press must start a drag, dragging=%v region=%q", h.IsDragging(), h.DragRegion())
	}

	grabDelta := grabY - track.Rect.Y - scroll.RowForOffset(track.Rect.H, track.Rect.H, track.Rect.H, m.scrollOffset)
	if grabDelta != grabY-thumb.Rect.Y {
		// The offset starts at 0, so the anchor is exactly where the render
		// placed the thumb; recompute through the shared math for honesty.
		grabDelta = grabY - track.Rect.Y - scroll.RowForOffset(30, track.Rect.H, track.Rect.H, m.scrollOffset)
	}

	// Drag down five rows and expect the shared inverse mapping.
	dragY := grabY + 5
	m.HandleMouse(motionAt(thumb.Rect.X, dragY), h)
	want := scroll.OffsetAtRow(30, track.Rect.H, track.Rect.H, dragY-track.Rect.Y-grabDelta)
	if m.scrollOffset != want {
		t.Errorf("after dragging to row %d, offset = %d, want %d", dragY, m.scrollOffset, want)
	}

	// Dragging past the bottom clamps without ending the gesture.
	m.HandleMouse(motionAt(thumb.Rect.X, track.Rect.Y+track.Rect.H+50), h)
	maxOff := 30 - track.Rect.H
	if m.scrollOffset != maxOff {
		t.Errorf("past-end clamp: offset = %d, want %d", m.scrollOffset, maxOff)
	}
	if !h.IsDragging() {
		t.Error("clamping past the end must not lose the gesture")
	}

	// Release settles cleanly and keeps the position.
	m.HandleMouse(releaseAt(0, 0), h)
	if h.IsDragging() {
		t.Error("drag still live after release")
	}
	if m.scrollOffset != maxOff {
		t.Errorf("offset after release = %d, want %d", m.scrollOffset, maxOff)
	}
}

func TestViewportTrackClickAnchorsAndContinuesDragging(t *testing.T) {
	m, h := overflowModal(t)
	track, thumb := barRegions(t, h)

	// Click a track cell well below the thumb.
	clickRow := thumb.Rect.Y + thumb.Rect.H + 3
	if clickRow >= track.Rect.Y+track.Rect.H {
		clickRow = track.Rect.Y + track.Rect.H - 2
	}
	m.HandleMouse(clickAt(track.Rect.X, clickRow), h)

	row := clickRow - track.Rect.Y
	want := scroll.OffsetAtRow(30, track.Rect.H, track.Rect.H, row)
	if m.scrollOffset != want {
		t.Fatalf("track click must jump so the grabbed point anchors the thumb: offset = %d, want %d", m.scrollOffset, want)
	}
	if !h.IsDragging() {
		t.Fatal("a track click continues as a drag")
	}

	// The continuing gesture maps the pointer straight onto rows (anchor 0).
	further := clickRow + 4
	m.HandleMouse(motionAt(track.Rect.X, further), h)
	want = scroll.OffsetAtRow(30, track.Rect.H, track.Rect.H, further-track.Rect.Y)
	if m.scrollOffset != want {
		t.Errorf("post-jump drag: offset = %d, want %d", m.scrollOffset, want)
	}

	m.HandleMouse(releaseAt(99, 99), h)
	if h.IsDragging() {
		t.Error("release outside the window must still end the drag")
	}
}

// TestBarRegionsReachableThroughActiveModalRouting answers the plan's open
// question at the library level: update.go routes every mouse event for an
// open modal straight into Modal.HandleMouse with that modal's own handler —
// there is no gate between them that could swallow a scrollbar region hit.
// This drives a full gesture exclusively through that one entry point and
// requires the body to move; it also asserts the bar never leaks an item
// action or a cancel.
func TestBarRegionsReachableThroughActiveModalRouting(t *testing.T) {
	m, h := overflowModal(t)
	_, thumb := barRegions(t, h)

	active := true // stands in for activeModal() != ModalNone
	route := func(msg tea.MouseMsg) string {
		if !active {
			t.Fatal("event routed while no modal is active")
		}
		return m.HandleMouse(msg, h)
	}

	startScroll := m.scrollOffset
	if action := route(clickAt(thumb.Rect.X, thumb.Rect.Y)); action != "" {
		t.Fatalf("scrollbar press leaked the action %q", action)
	}
	route(motionAt(thumb.Rect.X, thumb.Rect.Y+6))
	if action := route(releaseAt(thumb.Rect.X, thumb.Rect.Y+6)); action != "" {
		t.Fatalf("scrollbar release leaked the action %q", action)
	}
	if m.scrollOffset <= startScroll {
		t.Errorf("gesture through the active-modal gate moved nothing: %d -> %d", startScroll, m.scrollOffset)
	}
}

func TestScrollbarRegionsRegisteredAfterContent(t *testing.T) {
	_, h := overflowModal(t)
	regions := h.HitMap.Regions()
	if len(regions) < 2 ||
		regions[len(regions)-2].ID != RegionScrollbarTrack ||
		regions[len(regions)-1].ID != RegionScrollbarThumb {
		var ids []string
		for _, r := range regions {
			ids = append(ids, r.ID)
		}
		t.Fatalf("scrollbar regions must come after all content regions (reverse-scan priority); got %v", ids)
	}
}

func TestHasThumbFalseRegistersNothing(t *testing.T) {
	m := New("Test", WithWidth(40)).
		AddSection(Text("short")).
		AddSection(Text("content"))
	handler := mouse.NewHandler()
	m.Render(80, 40, handler)

	for _, r := range handler.HitMap.Regions() {
		if r.ID == RegionScrollbarThumb || r.ID == RegionScrollbarTrack {
			t.Fatalf("fitting content must register no scrollbar regions, found %q", r.ID)
		}
	}

	// A press in the column where the bar would be is absorbed as body, and
	// starts no gesture. Find a body point clear of other regions.
	x, y := -1, -1
	for _, r := range handler.HitMap.Regions() {
		if r.ID == "modal-body" {
			x, y = r.Rect.X+r.Rect.W-1, r.Rect.Y+r.Rect.H/2
			break
		}
	}
	if x < 0 {
		t.Fatal("no modal-body region")
	}
	if action := m.HandleMouse(clickAt(x, y), handler); action != "" {
		t.Errorf("body click returned %q", action)
	}
	if handler.IsDragging() {
		t.Error("clicking where no thumb exists must not start a drag")
	}
}

// TestIdleViewportBarBytesMatchLegacyRenderer pins idle byte parity against the
// draw-only renderer this replaced, so adopting interactivity cannot change
// what an untouched frame looks like.
func TestIdleViewportBarBytesMatchLegacyRenderer(t *testing.T) {
	legacy := func(total, offset, height int) string {
		loc := scroll.ThumbLocFor(total, offset, height, height)
		trackStyle := lipgloss.NewStyle().Foreground(styles.ScrollbarTrackColor).Background(styles.BgSecondary)
		thumbStyle := lipgloss.NewStyle().Foreground(styles.ScrollbarThumbColor).Background(styles.BgSecondary)
		lines := make([]string, height)
		for i := range height {
			if i >= loc.Pos && i < loc.Pos+loc.Size {
				lines[i] = thumbStyle.Render("┃")
			} else {
				lines[i] = trackStyle.Render("│")
			}
		}
		return strings.Join(lines, "\n")
	}

	m := New("Test")
	for _, tc := range []struct{ total, offset, height int }{
		{20, 0, 10}, {100, 37, 8}, {7, 3, 5}, {11, 9, 3}, {50, 25, 25},
	} {
		got, _ := m.renderViewportBar(nil, tc.total, tc.offset, tc.height)
		if want := legacy(tc.total, tc.offset, tc.height); got != want {
			t.Errorf("idle bar bytes changed for total=%d offset=%d height=%d", tc.total, tc.offset, tc.height)
		}
	}
}

// TestSectionDeclaredScrollbarRegistrationAndOwnership exercises the switcher
// shape: a Custom section draws its own list bar and declares it. The library
// places the regions at the declared column after all content, absorbs their
// presses so they can never select a row underneath, and answers SectionBarAt
// with everything an owning mouse handler needs to run the gesture itself.
func TestSectionDeclaredScrollbarRegistrationAndOwnership(t *testing.T) {
	const totalRows = 16
	const visibleRows = 8

	list := Custom(func(contentWidth int, focusID, hoverID string) RenderedSection {
		rows := make([]string, visibleRows)
		for i := range rows {
			rows[i] = strings.Repeat("x", contentWidth-scrollbarCol)
		}
		return RenderedSection{
			Content: strings.Join(rows, "\n"),
			Scrollbar: &SectionScrollbar{
				TotalItems:   totalRows,
				ScrollOffset: 0,
				VisibleItems: visibleRows,
				TrackHeight:  visibleRows,
				LocalX:       contentWidth - scrollbarCol,
			},
		}
	}, nil)

	m := New("Test", WithWidth(60), WithHints(false)).AddSection(list)
	handler := mouse.NewHandler()
	m.Render(80, 40, handler)

	track, thumb := barRegions(t, handler)
	if track.Rect.X != thumb.Rect.X || track.Rect.H != visibleRows {
		t.Fatalf("declared bar geometry wrong: track=%+v thumb=%+v", track.Rect, thumb.Rect)
	}

	// On the thumb: full declaration plus absolute (unclipped) track origin.
	declared, _, trackY, onThumb, ok := m.SectionBarAt(thumb.Rect.X, thumb.Rect.Y, handler)
	if !ok || !onThumb {
		t.Fatalf("SectionBarAt missed the thumb: ok=%v onThumb=%v", ok, onThumb)
	}
	if declared.TotalItems != totalRows || declared.VisibleItems != visibleRows || declared.TrackHeight != visibleRows {
		t.Errorf("declaration mismatch: %+v", declared)
	}
	if trackY != track.Rect.Y {
		t.Errorf("unclipped track top = %d, want %d", trackY, track.Rect.Y)
	}

	// On the track below the thumb: same bar, not the thumb.
	if _, _, _, onThumb, ok := m.SectionBarAt(track.Rect.X, track.Rect.Y+visibleRows-1, handler); !ok || onThumb {
		t.Errorf("track point: ok=%v onThumb=%v", ok, onThumb)
	}

	// A press on the bar is absorbed by HandleMouse — never an item action,
	// never a cancel, and never a fall-through to the row underneath.
	if action := m.HandleMouse(clickAt(thumb.Rect.X, thumb.Rect.Y), handler); action != "" {
		t.Errorf("section-bar press leaked the action %q", action)
	}
	if handler.IsDragging() {
		t.Error("section bars are not the library's gestures to start")
	}

	// Anywhere else in the modal reports no section bar.
	if _, _, _, _, ok := m.SectionBarAt(1, 1, handler); ok {
		t.Error("backdrop reported as a section bar")
	}
}

// scrollbarCol is the width a declared list bar occupies at the right edge.
const scrollbarCol = 1

func TestSectionDeclaredScrollbarInertWhenItFits(t *testing.T) {
	list := Custom(func(contentWidth int, focusID, hoverID string) RenderedSection {
		return RenderedSection{
			Content: "tiny",
			Scrollbar: &SectionScrollbar{
				TotalItems:   3,
				VisibleItems: 8,
				TrackHeight:  8,
			},
		}
	}, nil)
	m := New("Test", WithWidth(60), WithHints(false)).AddSection(list)
	handler := mouse.NewHandler()
	m.Render(80, 40, handler)

	for _, r := range handler.HitMap.Regions() {
		if r.ID == RegionScrollbarThumb || r.ID == RegionScrollbarTrack {
			t.Fatalf("declared bar with no thumb registered a region: %+v", r)
		}
	}
	if _, _, _, _, ok := m.SectionBarAt(0, 0, handler); ok {
		t.Error("fitting content must leave no section bar to claim")
	}
}

// TestModalResetMidGestureSettlesCleanly covers closing a modal while a
// scrollbar drag is live: nothing afterwards may resurrect the gesture or move
// the (freshly reset) scroll state.
func TestModalResetMidGestureSettlesCleanly(t *testing.T) {
	m, h := overflowModal(t)
	_, thumb := barRegions(t, h)

	m.HandleMouse(clickAt(thumb.Rect.X, thumb.Rect.Y), h)
	if !h.IsDragging() {
		t.Fatal("precondition: drag live before close")
	}

	m.Reset()

	// Stray motion/release from the dead gesture must be harmless.
	m.HandleMouse(motionAt(thumb.Rect.X, thumb.Rect.Y+9), h)
	m.HandleMouse(releaseAt(thumb.Rect.X, thumb.Rect.Y+9), h)
	if m.scrollOffset != 0 {
		t.Errorf("post-reset motions moved scroll to %d, want 0", m.scrollOffset)
	}

	// A fresh open renders and gestures again from scratch.
	m.Render(80, 24, h)
	_, thumb2 := barRegions(t, h)
	m.HandleMouse(clickAt(thumb2.Rect.X, thumb2.Rect.Y), h)
	if !h.IsDragging() {
		t.Error("a reopened modal must accept new scrollbar gestures")
	}
	m.HandleMouse(releaseAt(thumb2.Rect.X, thumb2.Rect.Y), h)
}
