package workspace

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	boardkanban "github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// kanbanScrollbarRegion returns the hit region of one lane's bar part, as the
// last render registered it.
func kanbanScrollbarRegion(t *testing.T, p *Plugin, kind boardkanban.RegionKind, column int) mouse.Region {
	t.Helper()
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionKanbanScrollbar {
			continue
		}
		hit, ok := region.Data.(boardkanban.HitRegion)
		if ok && hit.Kind == kind && hit.Column == column {
			return region
		}
	}
	t.Fatalf("no %s region for lane %d", kind, column)
	return mouse.Region{}
}

// kanbanOverflowPlugin builds a board whose working and blocked lanes both
// overflow the viewport, renders it, and returns the plugin.
func kanbanOverflowPlugin(t *testing.T) (*Plugin, int) {
	t.Helper()
	p := New()
	for i := range 8 {
		p.worktrees = append(p.worktrees, &Worktree{Key: fmt.Sprintf("/repo/a%d", i), Name: fmt.Sprintf("active-%d", i), Status: StatusActive})
	}
	for i := range 9 {
		p.worktrees = append(p.worktrees, &Worktree{Key: fmt.Sprintf("/repo/w%d", i), Name: fmt.Sprintf("waiting-%d", i), Status: StatusWaiting})
	}
	const width, height = 200, 18
	p.mouseHandler.HitMap.Clear()
	p.renderKanbanView(width, height)
	return p, width
}

// The full gesture through this plugin's own input path: a thumb press arms
// and StartDrags without moving anything, held motion moves that lane's
// viewport — and only that lane's — and a release far away settles with the
// offset where the pointer left it.
func TestKanbanLaneThumbDragEndToEndMovesOnlyThatLane(t *testing.T) {
	p, _ := kanbanOverflowPlugin(t)
	thumb := kanbanScrollbarRegion(t, p, boardkanban.RegionScrollbarThumb, 1)
	track := kanbanScrollbarRegion(t, p, boardkanban.RegionScrollbarTrack, 1)

	p.handleMouse(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if got := p.mouseHandler.DragRegion(); got != regionKanbanScrollbar {
		t.Fatalf("bar press started drag %q, want %s", got, regionKanbanScrollbar)
	}
	if !p.kanban.DraggingScrollbar() {
		t.Fatal("bar press did not arm the component's gesture")
	}

	visible := 0
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != regionKanbanCard {
			continue
		}
		if hit, ok := region.Data.(boardkanban.HitRegion); ok && hit.Column == 1 {
			visible++
		}
	}
	params := ui.ScrollbarParams{TotalItems: 8, ScrollOffset: 0, VisibleItems: visible, TrackHeight: track.Rect.H}
	const rows = 3
	want := ui.OffsetAtRow(params, rows)

	p.handleMouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + rows, Button: tea.MouseLeft})
	if !p.kanban.DraggingScrollbar() {
		t.Fatal("motion ended the gesture")
	}

	// Re-render through the same entry the frame uses — clearing the frame's
	// hit map first, exactly as the View pass does, so no stale region from
	// the pre-drag frame survives: the dragged lane's thumb lands on the
	// shared core's row for the new offset, and the neighbour lane's bar has
	// not moved.
	p.mouseHandler.HitMap.Clear()
	p.renderKanbanView(200, 18)
	dragged := kanbanScrollbarRegion(t, p, boardkanban.RegionScrollbarThumb, 1)
	neighbour := kanbanScrollbarRegion(t, p, boardkanban.RegionScrollbarThumb, 2)
	wantY := track.Rect.Y + ui.RowForOffset(ui.ScrollbarParams{
		TotalItems: params.TotalItems, ScrollOffset: want,
		VisibleItems: params.VisibleItems, TrackHeight: params.TrackHeight,
	}, want)
	if dragged.Rect.Y != wantY {
		t.Fatalf("dragged thumb Y = %d, want %d (offset %d)", dragged.Rect.Y, wantY, want)
	}
	if neighbour.Rect.Y != track.Rect.Y {
		t.Fatalf("lane 2 thumb moved to %d, want its rest row %d", neighbour.Rect.Y, track.Rect.Y)
	}

	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
	if p.kanban.DraggingScrollbar() {
		t.Fatal("release did not settle the gesture")
	}
}

// A bar press never selects the card drawn under it, and the second press of
// a rapid double-press re-grabs exactly like the first one did instead of
// activating the card beneath (double-press parity).
func TestKanbanBarPressNeverSelectsAndDoublePressReGrabs(t *testing.T) {
	p, _ := kanbanOverflowPlugin(t)
	thumb := kanbanScrollbarRegion(t, p, boardkanban.RegionScrollbarThumb, 1)

	click := tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft}
	if cmd := p.handleMouse(click); cmd != nil {
		t.Fatal("bar press produced a command")
	}
	if sel := p.kanban.Selection(); sel.Column != 0 || sel.Row != 0 {
		t.Fatalf("bar press moved selection to %#v", sel)
	}

	p.handleMouse(tea.MouseReleaseMsg{X: thumb.Rect.X, Y: thumb.Rect.Y})
	if p.kanban.DraggingScrollbar() {
		t.Fatal("release did not settle the first grab")
	}

	// Two clicks at one cell inside the double-click window: the second
	// arrives as ActionDoubleClick and must re-arm rather than activate.
	if cmd := p.handleMouse(click); cmd != nil {
		t.Fatal("second press on a bar produced an activation command")
	}
	if !p.kanban.DraggingScrollbar() {
		t.Fatal("a rapid second press on the bar did not re-grab it")
	}
	if got := p.mouseHandler.DragRegion(); got != regionKanbanScrollbar {
		t.Fatalf("second press started drag %q, want %s", got, regionKanbanScrollbar)
	}
	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})

	// The bar wins the column it shares with cards: the pressed point
	// resolves to the bar even though card regions span the full lane width.
	hit := p.mouseHandler.HitMap.Test(thumb.Rect.X, thumb.Rect.Y)
	bar, ok := hit.Data.(boardkanban.HitRegion)
	if !ok || (bar.Kind != boardkanban.RegionScrollbarThumb && bar.Kind != boardkanban.RegionScrollbarTrack) {
		t.Fatalf("point under the bar resolved to %#v", hit.Data)
	}
}

// A release lost off-window recovers on the next button-less motion, which is
// where the shared drag machinery ends a stale gesture on every other surface.
func TestKanbanBarLostReleaseSettlesOnHover(t *testing.T) {
	p, _ := kanbanOverflowPlugin(t)
	thumb := kanbanScrollbarRegion(t, p, boardkanban.RegionScrollbarThumb, 1)

	p.handleMouse(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if !p.kanban.DraggingScrollbar() {
		t.Fatal("press did not arm the gesture")
	}

	p.handleMouse(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + 2})
	if p.kanban.DraggingScrollbar() {
		t.Fatal("lost release left the lane-bar gesture live")
	}

	// And the component keeps working: a fresh grab arms again.
	p.handleMouse(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if !p.kanban.DraggingScrollbar() {
		t.Fatal("fresh grab after recovery refused")
	}
	p.handleMouse(tea.MouseReleaseMsg{X: 1, Y: 1})
}

// Lanes whose cards fit draw no bar and register no regions: their reserved
// column is an anti-jitter spacer, not a control.
func TestKanbanFittingBoardRegistersNoBarRegions(t *testing.T) {
	p := New()
	p.worktrees = []*Worktree{
		{Key: "/repo/a", Name: "active", Status: StatusActive},
		{Key: "/repo/b", Name: "waiting", Status: StatusWaiting},
	}
	p.renderKanbanView(200, 18)
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID == regionKanbanScrollbar {
			t.Fatalf("fitting board registered a bar at %#v", region.Rect)
		}
	}
}
