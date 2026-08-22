package workspace

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// The project sidebar hosts the same interactive scrollbar as global Sessions:
// thumb drag, track jump-to-spot, hover emphasis and a free-scrolled viewport
// that survives re-renders until the selection moves. These mirror
// internal/overview/workspaces_scrollbar_test.go, driven through this surface's
// own mouse handler exactly as a user's pointer drives it.

// scrollListPlugin is the baseline fixture with n extra worktrees, long enough
// to overflow the sidebar at its test dimensions.
func scrollListPlugin(t *testing.T, n int) *Plugin {
	t.Helper()
	p := sidebarBaselinePlugin(t)
	for i := range n {
		p.worktrees = append(p.worktrees, &Worktree{Name: fmt.Sprintf("scroll-%02d", i), Path: p.ctx.ProjectRoot})
	}
	return p
}

type listBarRegions struct {
	track, thumb *mouse.Region
}

// renderedListBar renders the list view once and reports where the bar's
// regions landed in the hit map.
func renderedListBar(t *testing.T, p *Plugin) listBarRegions {
	t.Helper()
	p.renderListView(p.width, p.height)
	var out listBarRegions
	regions := p.mouseHandler.HitMap.Regions()
	for i := range regions {
		switch r := &regions[i]; r.ID {
		case ui.RegionScrollbarTrack:
			out.track = r
		case ui.RegionScrollbarThumb:
			out.thumb = r
		}
	}
	if out.track == nil || out.thumb == nil {
		t.Fatal("no scrollbar regions registered for the project sidebar")
	}
	return out
}

func pressMouseAt(t *testing.T, p *Plugin, x, y int) {
	t.Helper()
	p.handleMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func dragMouseTo(t *testing.T, p *Plugin, x, y int) {
	t.Helper()
	p.handleMouse(tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft})
}

func releaseMouseAt(t *testing.T, p *Plugin, x, y int) {
	t.Helper()
	p.handleMouse(tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft})
}

// A thumb press grabs where it landed, the drag maps the pointer back onto
// rows, a gesture-chosen viewport survives re-renders, and releasing outside
// the bar settles without losing the position.
func TestSidebarScrollbarThumbDragEndToEnd(t *testing.T) {
	p := scrollListPlugin(t, 60)
	bar := renderedListBar(t, p)
	before := selectionLabel(p)

	pressMouseAt(t, p, bar.track.Rect.X, bar.thumb.Rect.Y+2)
	if !p.mouseHandler.IsDragging() || p.mouseHandler.DragRegion() != string(workspacelist.RegionScrollbarThumb) {
		t.Fatalf("thumb press left dragging=%v region=%q", p.mouseHandler.IsDragging(), p.mouseHandler.DragRegion())
	}
	if !p.sidebarBar.gesture.Active() {
		t.Fatal("thumb press did not begin a scrollbar gesture")
	}
	if got := p.scrollOffset; got != 0 {
		t.Errorf("thumb press moved the offset to %d, want it held at 0", got)
	}
	if sel := selectionLabel(p); sel != before {
		t.Errorf("thumb press selected %q, want %q held", sel, before)
	}

	dragMouseTo(t, p, bar.track.Rect.X, bar.thumb.Rect.Y+6)
	dragged := p.scrollOffset
	if dragged <= 0 {
		t.Fatalf("dragging down left offset %d, want it following the pointer", dragged)
	}

	// A re-render must not drag the viewport back to the selection mid-gesture.
	p.renderListView(p.width, p.height)
	if got := p.scrollOffset; got != dragged {
		t.Errorf("re-render moved the viewport from %d to %d", dragged, got)
	}

	releaseMouseAt(t, p, 1, 1) // released far outside the bar
	if p.mouseHandler.IsDragging() || p.sidebarBar.gesture.Active() {
		t.Error("drag state survived release outside the bar")
	}
	if got := p.scrollOffset; got != dragged {
		t.Errorf("offset = %d after settle, want the dragged position %d", got, dragged)
	}
}

// A track press jumps so the grabbed point anchors the thumb, continues as a
// drag, and clamps past both ends of the track without ending anything.
func TestSidebarScrollbarTrackClickAnchorsAndDrags(t *testing.T) {
	p := scrollListPlugin(t, 60)
	bar := renderedListBar(t, p)

	anchorRow := bar.thumb.Rect.Y + bar.thumb.Rect.H + 2 - bar.track.Rect.Y
	params := p.sidebarBar.bar.Params
	want := ui.OffsetAtRow(params, anchorRow)
	if want == 0 {
		t.Fatalf("test setup: anchor row %d maps to offset 0; pick a lower anchor", anchorRow)
	}

	pressMouseAt(t, p, bar.track.Rect.X, bar.track.Rect.Y+anchorRow)
	if got := p.scrollOffset; got != want {
		t.Errorf("track press scrolled to %d, want %d", got, want)
	}
	if !p.mouseHandler.IsDragging() {
		t.Error("track press did not continue as a drag")
	}

	dragMouseTo(t, p, bar.track.Rect.X, bar.track.Rect.Y+anchorRow-3)
	if got := p.scrollOffset; got >= want {
		t.Errorf("dragging above the anchor left offset %d, want <%d", got, want)
	}

	dragMouseTo(t, p, bar.track.Rect.X, bar.track.Rect.Y+params.TrackHeight+50)
	if got := p.scrollOffset; got != params.TotalItems-params.VisibleItems {
		t.Errorf("dragging far past the bottom left offset %d, want %d", got, params.TotalItems-params.VisibleItems)
	}
	if !p.sidebarBar.gesture.Active() {
		t.Error("clamping ended the gesture")
	}

	releaseMouseAt(t, p, bar.track.Rect.X, bar.track.Rect.Y+anchorRow)
	if p.sidebarBar.gesture.Active() {
		t.Error("release did not end the gesture")
	}
}

// The bar's column answers to the bar even though rows and the whole-sidebar
// background reach under it: HitMap.Test scans reverse and the bar registered
// last. Pressing there starts a scrollbar gesture, never a row activation.
func TestSidebarScrollbarRegionWinsItsColumn(t *testing.T) {
	p := scrollListPlugin(t, 60)
	bar := renderedListBar(t, p)

	hit := p.mouseHandler.HitMap.Test(bar.track.Rect.X, bar.track.Rect.Y+bar.track.Rect.H/2)
	if hit == nil || (hit.ID != ui.RegionScrollbarThumb && hit.ID != ui.RegionScrollbarTrack) {
		t.Fatalf("bar column hit %#v, want a scrollbar region", hit)
	}

	before := selectionLabel(p)
	pressMouseAt(t, p, bar.track.Rect.X, bar.track.Rect.Y+bar.track.Rect.H/2)
	if id := p.mouseHandler.DragRegion(); !isListScrollbarID(id) {
		t.Errorf("press in the bar column started drag %q, want a scrollbar region", id)
	}
	if sel := selectionLabel(p); sel != before {
		t.Errorf("pressing the bar selected %q underneath it, want %q held", sel, before)
	}

	// A wheel notch over the bar column belongs to the list, exactly as over
	// the row beside it — both sit over the sidebar background.
	onBar := tea.MouseWheelMsg{X: bar.track.Rect.X, Y: bar.track.Rect.Y + 2, Button: tea.MouseWheelDown}
	beside := tea.MouseWheelMsg{X: 4, Y: onBar.Y, Button: tea.MouseWheelDown}
	if got, want := p.WheelAtBoundary(onBar), p.WheelAtBoundary(beside); got != want {
		t.Errorf("wheel at the bar column bounded=%v, want the row beside it (%v)", got, want)
	}
	releaseMouseAt(t, p, bar.track.Rect.X, bar.track.Rect.Y+2)
}

// Content that fits registers no bar regions at all.
func TestSidebarNoScrollbarRegionsWhenContentFits(t *testing.T) {
	p := sidebarBaselinePlugin(t) // two shells, three worktrees
	p.renderListView(p.width, p.height)
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if isListScrollbarID(region.ID) {
			t.Errorf("scrollbar region %q registered for fitting content", region.ID)
		}
	}
	if p.sidebarBar.bar.Has {
		t.Fatal("fitting content reported a scrollbar")
	}
}

// Hover lights the bar, and moving away restores byte-identical idle output.
func TestSidebarScrollbarIdleByteParityAcrossHover(t *testing.T) {
	p := scrollListPlugin(t, 60)
	bar := renderedListBar(t, p)
	idle := p.renderListView(p.width, p.height)

	dragMouseTo(t, p, bar.thumb.Rect.X, bar.thumb.Rect.Y) // button-less motion is a hover
	lit := p.renderListView(p.width, p.height)
	if lit == idle {
		t.Fatal("hovering the bar produced no visible emphasis")
	}

	dragMouseTo(t, p, 1, 1)
	back := p.renderListView(p.width, p.height)
	if back != idle {
		t.Fatal("idle output drifted after a hover round trip")
	}
}

// A free-scrolled viewport survives a re-render, and moving the selection hands
// following back to the keyboard.
func TestSidebarFreeScrollSurvivesRenderUntilSelectionMoves(t *testing.T) {
	p := scrollListPlugin(t, 60)
	renderedListBar(t, p) // renders once so the bar snapshot exists

	const dragged = 5
	p.setListViewport(dragged)
	p.renderListView(p.width, p.height)
	if !p.freeScroll {
		t.Fatal("the free-scroll latch did not survive into the render")
	}
	if got := p.sidebarBar.bar.Params.ScrollOffset; got != dragged {
		t.Errorf("re-rendered viewport = %d, want the gesture position %d", got, dragged)
	}

	p.moveCursor(-1) // any selection move owns the viewport again
	if p.freeScroll {
		t.Fatal("moving the selection left the free-scroll latch set")
	}
	p.renderListView(p.width, p.height)
	if got := p.sidebarBar.bar.Params.ScrollOffset; got >= dragged {
		t.Errorf("viewport = %d after the selection moved, want it following again (<%d)", got, dragged)
	}
}
