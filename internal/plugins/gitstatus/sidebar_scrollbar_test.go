package gitstatus

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// sidebarTestPlugin renders a status view with count modified files and
// enough height that the files section scrolls.
func sidebarTestPlugin(t *testing.T, count int) *Plugin {
	t.Helper()
	p := &Plugin{
		tree: &FileTree{
			Modified: makeEntries(count, StatusModified),
		},
		recentCommits:  makeCommitsWithHash(40),
		width:          120,
		height:         30,
		sidebarVisible: true,
		mouseHandler:   mouse.NewHandler(),
	}
	return p
}

func findRegion(t *testing.T, p *Plugin, id string) mouse.Rect {
	t.Helper()
	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID == id {
			return r.Rect
		}
	}
	t.Fatalf("no %q region registered in %+v", id, p.mouseHandler.HitMap.Regions())
	return mouse.Rect{}
}

func clickMsg(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func motionMsg(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

func releaseMsg(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg(tea.Mouse{X: x, Y: y, Button: tea.MouseLeft})
}

// The file bar's regions must beat the file rows beneath them: a press on
// the bar hits thumb/track, never regionFile.
func TestSidebarFilesScrollbar_RegionsBeatFileRows(t *testing.T) {
	p := sidebarTestPlugin(t, 40)
	_ = p.renderThreePaneView()

	thumb := findRegion(t, p, ui.RegionScrollbarThumb)
	track := findRegion(t, p, ui.RegionScrollbarTrack)

	if got := p.mouseHandler.HitMap.Test(thumb.X, thumb.Y); got == nil || got.ID != ui.RegionScrollbarThumb {
		t.Fatalf("press on thumb hit %+v, want scrollbar-thumb", got)
	}
	belowThumb := track.Y + track.H - 1
	if belowThumb < thumb.Y+thumb.H {
		t.Fatal("track has no rows below the thumb; test premise broken")
	}
	if got := p.mouseHandler.HitMap.Test(track.X, belowThumb); got == nil || got.ID != ui.RegionScrollbarTrack {
		t.Fatalf("press on track below thumb hit %+v, want scrollbar-track", got)
	}
}

func TestSidebarCommitsScrollbar_RegionsBeatCommitRows(t *testing.T) {
	p := sidebarTestPlugin(t, 4)
	p.cursor = 0
	_ = p.renderThreePaneView()

	thumb := findRegion(t, p, ui.RegionScrollbarThumb)
	if got := p.mouseHandler.HitMap.Test(thumb.X, thumb.Y); got == nil || got.ID != ui.RegionScrollbarThumb {
		t.Fatalf("press on commits thumb hit %+v, want scrollbar-thumb", got)
	}
}

// Dragging the files thumb from its rendered position moves scrollOff with
// the pointer and clamps at both ends of the track without losing gesture.
func TestSidebarFilesScrollbar_ThumbDragEndToEnd(t *testing.T) {
	p := sidebarTestPlugin(t, 40)
	_ = p.renderThreePaneView()

	thumb := findRegion(t, p, ui.RegionScrollbarThumb)
	if _, cmd := p.handleMouse(clickMsg(thumb.X, thumb.Y)); cmd != nil {
		t.Fatal("thumb press produced a command")
	}
	if !p.mouseHandler.IsDragging() || p.mouseHandler.DragRegion() != ui.RegionScrollbarThumb {
		t.Fatal("thumb press did not start a drag")
	}
	startOffset := p.scrollOff

	down := thumb.Y + 10
	if _, _ = p.handleMouse(motionMsg(thumb.X, down)); p.scrollOff <= startOffset {
		t.Fatalf("dragging down left scrollOff at %d (start %d)", p.scrollOff, startOffset)
	}

	// Past the bottom end of the track: clamped, gesture still alive.
	if _, _ = p.handleMouse(motionMsg(thumb.X, thumb.Y+500)); !p.mouseHandler.IsDragging() {
		t.Fatal("dragging past the track lost the gesture")
	}
	params := p.sidebarScroll.drag.params
	if want := params.TotalItems - params.VisibleItems; p.scrollOff != want {
		t.Fatalf("past-end drag offset = %d, want clamped max %d", p.scrollOff, want)
	}

	// Release outside the bar entirely settles cleanly.
	if _, _ = p.handleMouse(releaseMsg(100, 3)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not end the drag")
	}
	if p.sidebarScroll.dragBar != scrollBarNone {
		t.Fatal("release left scrollbar drag state behind")
	}
}

// A track click jumps so the grabbed point becomes the thumb anchor, and the
// same press keeps acting as a drag (macOS jump-to-spot).
func TestSidebarFilesScrollbar_TrackClickAnchorsAndContinues(t *testing.T) {
	p := sidebarTestPlugin(t, 40)
	_ = p.renderThreePaneView()

	track := findRegion(t, p, ui.RegionScrollbarTrack)
	// Grab inside the thumb's travel: rows past the thumb's lowest anchor
	// clamp straight to the last page, which would make this test vacuous.
	grabY := track.Y + track.H/2
	if _, _ = p.handleMouse(clickMsg(track.X, grabY)); !p.mouseHandler.IsDragging() {
		t.Fatal("track click did not continue as a drag")
	}
	want := ui.OffsetAtRow(p.sidebarScroll.drag.params, grabY-track.Y)
	if p.scrollOff != want {
		t.Fatalf("track jump offset = %d, want %d", p.scrollOff, want)
	}
	if want <= 0 {
		t.Fatalf("jump landed at top (%d); grab row should map into the list", want)
	}

	// Continuing to drag maps the pointer straight onto track rows.
	if _, _ = p.handleMouse(motionMsg(track.X, track.Y+1)); p.scrollOff >= want {
		t.Fatalf("drag up after track click offset = %d, want < %d", p.scrollOff, want)
	}
	if _, _ = p.handleMouse(releaseMsg(track.X, track.Y+1)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not end the drag")
	}
}

// Everything fits: no scrollbar regions exist, so nothing intercepts presses
// meant for file rows.
func TestSidebarScrollbar_NoRegionsWhenContentFits(t *testing.T) {
	p := sidebarTestPlugin(t, 3)
	p.recentCommits = makeCommitsWithHash(2)
	_ = p.renderThreePaneView()

	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID == ui.RegionScrollbarThumb || r.ID == ui.RegionScrollbarTrack {
			t.Fatalf("got %q region with all content visible", r.ID)
		}
	}
}

// Hover lights the hovered bar and no other.
func TestSidebarScrollbar_HoverHighlightsOnlyHoveredBar(t *testing.T) {
	p := sidebarTestPlugin(t, 40)
	_ = p.renderThreePaneView()

	filesThumb := findRegion(t, p, ui.RegionScrollbarThumb)
	action := p.mouseHandler.HandleMouse(tea.MouseMotionMsg(tea.Mouse{X: filesThumb.X, Y: filesThumb.Y}))
	if action.Type != mouse.ActionHover {
		t.Fatalf("motion produced %+v, want hover", action.Type)
	}
	p.updateScrollbarHover(action.Region)
	if p.sidebarScroll.hoverBar != scrollBarFiles {
		t.Fatalf("hover bar = %d, want files", p.sidebarScroll.hoverBar)
	}

	action2 := p.mouseHandler.HandleMouse(tea.MouseMotionMsg(tea.Mouse{X: 100, Y: filesThumb.Y}))
	if action2.Type != mouse.ActionHover {
		t.Fatalf("motion produced %+v, want hover", action2.Type)
	}
	p.updateScrollbarHover(action2.Region)
	if p.sidebarScroll.hoverBar != scrollBarNone {
		t.Fatalf("hover away left bar = %d, want none", p.sidebarScroll.hoverBar)
	}
}

// The second press of a rapid double-press arrives as ActionDoubleClick; the
// bar must re-grab it exactly like the first one did instead of swallowing it
// (repeat track-clicks at one cell lose every other press otherwise).
func TestSidebarFilesScrollbar_SecondQuickPressStillGrabsTheBar(t *testing.T) {
	p := sidebarTestPlugin(t, 40)
	_ = p.renderThreePaneView()

	thumb := findRegion(t, p, ui.RegionScrollbarThumb)
	if _, _ = p.handleMouse(clickMsg(thumb.X, thumb.Y)); !p.mouseHandler.IsDragging() {
		t.Fatal("first press did not start a drag")
	}
	if _, _ = p.handleMouse(releaseMsg(thumb.X, thumb.Y)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not settle the first gesture")
	}

	double := tea.MouseClickMsg(tea.Mouse{X: thumb.X, Y: thumb.Y, Button: tea.MouseLeft})
	if _, cmd := p.handleMouse(double); cmd != nil {
		t.Fatal("the second press produced a command")
	}
	if !p.mouseHandler.IsDragging() || p.mouseHandler.DragRegion() != ui.RegionScrollbarThumb {
		t.Fatal("a quick second press on the thumb did not grab the bar")
	}
	if _, _ = p.handleMouse(releaseMsg(thumb.X, thumb.Y)); p.mouseHandler.IsDragging() {
		t.Fatal("release did not settle the re-grabbed gesture")
	}
}
