package conversations

import (
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

func sessionListTestPlugin(t *testing.T, count int) *Plugin {
	t.Helper()
	p := New()
	// A registered adapter keeps View on the two-pane path.
	p.adapters = map[string]adapter.Adapter{"mock": &mockAdapter{}}
	now := time.Now()
	p.sessions = make([]adapter.Session, count)
	for i := range p.sessions {
		p.sessions[i] = adapter.Session{
			ID:        "s" + string(rune('a'+i)),
			Name:      "session",
			UpdatedAt: now.Add(-time.Duration(i) * time.Hour),
		}
	}
	p.width = 150
	p.height = 24
	p.activePane = PaneSidebar
	p.cursor = 0
	p.sidebarVisible = true
	return p
}

func convClickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func convMotionMsg(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func convReleaseMsg(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func findConvRegion(t *testing.T, p *Plugin, id string) mouse.Rect {
	t.Helper()
	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID == id {
			return r.Rect
		}
	}
	t.Fatalf("no %q region registered", id)
	return mouse.Rect{}
}

// The bar's regions must beat the session rows beneath them: a press on the
// track hits the scrollbar, never regionSessionItem.
func TestSessionScrollbar_RegionsBeatSessionRows(t *testing.T) {
	p := sessionListTestPlugin(t, 30)
	_ = p.View(p.width, p.height)

	thumb := findConvRegion(t, p, ui.RegionScrollbarThumb)
	if got := p.mouseHandler.HitMap.Test(thumb.X, thumb.Y); got == nil || got.ID != ui.RegionScrollbarThumb {
		t.Fatalf("press on thumb hit %+v, want scrollbar-thumb", got)
	}
	track := findConvRegion(t, p, ui.RegionScrollbarTrack)
	below := track.Y + track.H - 1
	if below >= thumb.Y+thumb.H {
		if got := p.mouseHandler.HitMap.Test(track.X, below); got == nil || got.ID != ui.RegionScrollbarTrack {
			t.Fatalf("press below thumb hit %+v, want scrollbar-track", got)
		}
	}
}

// Dragging the thumb scrolls the session list with the pointer and clamps at
// the ends without losing the gesture; nothing is persisted on release.
func TestSessionScrollbar_ThumbDragEndToEnd(t *testing.T) {
	p := sessionListTestPlugin(t, 30)
	_ = p.View(p.width, p.height)

	thumb := findConvRegion(t, p, ui.RegionScrollbarThumb)
	if _, _ = p.handleMouse(convClickMsg(thumb.X, thumb.Y)); !p.mouseHandler.IsDragging() {
		t.Fatal("thumb press did not start a drag")
	}
	startOffset := p.scrollOff

	if _, _ = p.handleMouse(convMotionMsg(thumb.X, thumb.Y+6)); p.scrollOff <= startOffset {
		t.Fatalf("dragging down left scrollOff at %d (start %d)", p.scrollOff, startOffset)
	}
	if _, _ = p.handleMouse(convMotionMsg(thumb.X, thumb.Y+500)); !p.mouseHandler.IsDragging() {
		t.Fatal("dragging past the end lost the gesture")
	}
	params := p.listScroll.dragging.params
	maxOffset := params.TotalItems - params.VisibleItems
	if p.scrollOff != maxOffset {
		t.Fatalf("past-end offset = %d, want clamped %d", p.scrollOff, maxOffset)
	}

	if _, _ = p.handleMouse(convReleaseMsg(100, 2)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not end the drag")
	}
	if p.listScroll.grabDelta != 0 || p.listScroll.dragging.has {
		t.Fatal("release left scrollbar gesture state behind")
	}
}

// A track click jumps so the grabbed point anchors the thumb, then keeps
// acting as a drag.
func TestSessionScrollbar_TrackClickAnchorsAndContinues(t *testing.T) {
	p := sessionListTestPlugin(t, 30)
	_ = p.View(p.width, p.height)

	track := findConvRegion(t, p, ui.RegionScrollbarTrack)
	thumb := findConvRegion(t, p, ui.RegionScrollbarThumb)
	// Grab on the track BELOW the thumb: the jump anchors the grabbed point
	// at the thumb top (clamped to the last page when past its travel).
	grabY := thumb.Y + thumb.H + 2
	if grabY >= track.Y+track.H {
		t.Fatal("no free track rows below the thumb; test premise broken")
	}
	if _, _ = p.handleMouse(convClickMsg(track.X, grabY)); !p.mouseHandler.IsDragging() {
		t.Fatal("track click did not continue as a drag")
	}
	want := ui.OffsetAtRow(p.listScroll.dragging.params, grabY-track.Y)
	if p.scrollOff != want {
		t.Fatalf("track jump offset = %d, want %d", p.scrollOff, want)
	}
	if want <= 0 {
		t.Fatalf("jump landed at top (%d); test premise broken", want)
	}

	if _, _ = p.handleMouse(convMotionMsg(track.X, track.Y+1)); p.scrollOff >= want {
		t.Fatalf("drag up after jump offset = %d, want < %d", p.scrollOff, want)
	}
	if _, _ = p.handleMouse(convReleaseMsg(track.X, track.Y+1)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not end the drag")
	}
}

// Everything fits: no regions registered, and the spacer column stays inert.
func TestSessionScrollbar_NoRegionsWhenContentFits(t *testing.T) {
	p := sessionListTestPlugin(t, 3)
	_ = p.View(p.width, p.height)

	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID == ui.RegionScrollbarThumb || r.ID == ui.RegionScrollbarTrack {
			t.Fatalf("got %q region with all sessions visible", r.ID)
		}
	}
}

// The second press of a rapid double-press arrives as ActionDoubleClick; the
// bar must re-grab it exactly like the first one did instead of swallowing it
// (repeat track-clicks at one cell lose every other press otherwise).
func TestSessionScrollbar_SecondQuickPressStillGrabsTheBar(t *testing.T) {
	p := sessionListTestPlugin(t, 30)
	_ = p.View(p.width, p.height)

	thumb := findConvRegion(t, p, ui.RegionScrollbarThumb)
	if _, _ = p.handleMouse(convClickMsg(thumb.X, thumb.Y)); !p.mouseHandler.IsDragging() {
		t.Fatal("first press did not start a drag")
	}
	if _, _ = p.handleMouse(convReleaseMsg(thumb.X, thumb.Y)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not settle the first gesture")
	}

	double := tea.MouseClickMsg(tea.Mouse{X: thumb.X, Y: thumb.Y, Button: tea.MouseLeft})
	if _, cmd := p.handleMouse(double); cmd != nil {
		t.Fatal("the second press produced a command")
	}
	if !p.mouseHandler.IsDragging() || p.mouseHandler.DragRegion() != ui.RegionScrollbarThumb {
		t.Fatal("a quick second press on the thumb did not grab the bar")
	}
	if _, _ = p.handleMouse(convReleaseMsg(thumb.X, thumb.Y)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not settle the re-grabbed gesture")
	}
}
