package overview

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// boardScrollbarRegion returns the hit region of one lane's bar part, as the
// last View registered it.
func boardScrollbarRegion(t *testing.T, m *Model, id string, column int) mouse.Region {
	t.Helper()
	for _, region := range m.mouse.HitMap.Regions() {
		if region.ID != id {
			continue
		}
		hit, ok := region.Data.(kanban.HitRegion)
		if ok && (hit.Kind == kanban.RegionScrollbarThumb || hit.Kind == kanban.RegionScrollbarTrack) && hit.Column == column {
			return region
		}
	}
	t.Fatalf("no %s region for lane %d", id, column)
	return mouse.Region{}
}

// boardScrollbarModel builds a board whose first two lanes overflow, renders
// it, and returns the model with lane 0's thumb geometry resolved.
func boardScrollbarModel(t *testing.T) (*Model, mouse.Region, mouse.Region) {
	t.Helper()
	m := compactOverviewModel(9)
	m.board.SetBoard(kanban.Board{Lanes: []kanban.Lane{
		{ID: "idle", Label: "Idle", Cards: overflowCards("idle", 9)},
		{ID: "working", Label: "Working", Cards: overflowCards("work", 9)},
		{ID: "blocked", Label: "Needs attention"},
		{ID: "done", Label: "Done"},
		{ID: "paused", Label: "Paused"},
	}})
	m.View(200, 15)
	return m,
		boardScrollbarRegion(t, m, ui.RegionScrollbarThumb, 0),
		boardScrollbarRegion(t, m, ui.RegionScrollbarTrack, 0)
}

func overflowCards(prefix string, count int) []kanban.Card {
	cards := make([]kanban.Card, count)
	for i := range cards {
		cards[i] = kanban.Card{ID: fmt.Sprintf("%s-%d", prefix, i)}
	}
	return cards
}

// The full gesture through this surface's own input path: a thumb press arms
// and StartDrags without moving anything, held motion moves that lane's
// viewport — and only that lane's — and a release far away settles with the
// offset where the pointer left it.
func TestBoardLaneThumbDragEndToEndMovesOnlyThatLane(t *testing.T) {
	m, thumb, track := boardScrollbarModel(t)

	m.Update(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if got := m.mouse.DragRegion(); got != ui.RegionScrollbarThumb {
		t.Fatalf("bar press started drag %q, want %s", got, ui.RegionScrollbarThumb)
	}
	if !m.board.DraggingScrollbar() {
		t.Fatal("bar press did not arm the component's gesture")
	}

	visible := 0
	for _, region := range m.mouse.HitMap.Regions() {
		if hit, ok := region.Data.(kanban.HitRegion); ok && hit.Kind == kanban.RegionCard && hit.Column == 0 {
			visible++
		}
	}
	params := ui.ScrollbarParams{TotalItems: 9, ScrollOffset: 0, VisibleItems: visible, TrackHeight: track.Rect.H}
	const rows = 3
	want := ui.OffsetAtRow(params, rows)

	m.Update(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + rows, Button: tea.MouseLeft})
	if !m.board.DraggingScrollbar() {
		t.Fatal("motion ended the gesture")
	}

	// Re-render through the same entry the frame uses: the dragged lane's
	// thumb lands on the shared core's row for the new offset, and the
	// neighbour's bar has not moved.
	m.View(200, 15)
	dragged := boardScrollbarRegion(t, m, ui.RegionScrollbarThumb, 0)
	neighbour := boardScrollbarRegion(t, m, ui.RegionScrollbarThumb, 1)
	wantY := track.Rect.Y + ui.RowForOffset(ui.ScrollbarParams{
		TotalItems: params.TotalItems, ScrollOffset: want,
		VisibleItems: params.VisibleItems, TrackHeight: params.TrackHeight,
	}, want)
	if dragged.Rect.Y != wantY {
		t.Fatalf("dragged thumb Y = %d, want %d (offset %d)", dragged.Rect.Y, wantY, want)
	}
	if neighbour.Rect.Y != thumb.Rect.Y {
		t.Fatalf("lane 1 thumb moved to %d, want %d", neighbour.Rect.Y, thumb.Rect.Y)
	}

	m.Update(tea.MouseReleaseMsg{X: 1, Y: 1})
	if m.board.DraggingScrollbar() {
		t.Fatal("release did not settle the gesture")
	}
}

// A press on a bar never selects or activates what is drawn under it, and the
// second press of a rapid double-press re-grabs exactly like the first one
// did instead of activating the card beneath.
func TestBoardBarPressNeverSelectsAndDoublePressReGrabs(t *testing.T) {
	m, thumb, _ := boardScrollbarModel(t)
	// Keep the selection clear of the pressed lane, so nothing a bar gesture
	// legitimately does to its own lane can masquerade as a stray selection.
	m.board.Select(kanban.Selection{Column: 1, Row: 0})

	click := tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft}
	if cmd := m.Update(click); cmd != nil {
		t.Fatal("bar press produced a command")
	}
	if got := (m.board.Selection()); got != (kanban.Selection{Column: 1, Row: 0}) {
		t.Fatalf("bar press moved selection to %#v", got)
	}

	m.Update(tea.MouseReleaseMsg{X: thumb.Rect.X, Y: thumb.Rect.Y})
	if m.board.DraggingScrollbar() {
		t.Fatal("release did not settle the first grab")
	}

	// The second press arrives as ActionDoubleClick; it must re-arm.
	if cmd := m.Update(click); cmd != nil {
		t.Fatal("double-click on a bar produced an activation command")
	}
	if !m.board.DraggingScrollbar() {
		t.Fatal("a rapid second press on the bar did not re-grab it")
	}
	if got := m.mouse.DragRegion(); got != ui.RegionScrollbarThumb {
		t.Fatalf("second press started drag %q, want %s", got, ui.RegionScrollbarThumb)
	}
	m.Update(tea.MouseReleaseMsg{X: 1, Y: 1})

	// The bar wins the column it shares with cards: the point pressed above
	// resolves to the bar even though card regions span the full lane width.
	hit := m.mouse.HitMap.Test(thumb.Rect.X, thumb.Rect.Y)
	bar, ok := hit.Data.(kanban.HitRegion)
	if !ok || (bar.Kind != kanban.RegionScrollbarThumb && bar.Kind != kanban.RegionScrollbarTrack) {
		t.Fatalf("point under the bar resolved to %#v", hit.Data)
	}
}

// A release lost off-window recovers on the next button-less motion, which is
// where the shared drag machinery ends a stale gesture on every other surface.
func TestBoardBarLostReleaseSettlesOnHover(t *testing.T) {
	m, thumb, _ := boardScrollbarModel(t)

	m.Update(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if !m.board.DraggingScrollbar() {
		t.Fatal("press did not arm the gesture")
	}

	m.Update(tea.MouseMotionMsg{X: thumb.Rect.X, Y: thumb.Rect.Y + 2})
	if m.board.DraggingScrollbar() {
		t.Fatal("lost release left the lane-bar gesture live")
	}

	// And nothing moved behind the pointer's back: a fresh grab still starts
	// from where the board was.
	m.Update(tea.MouseClickMsg{X: thumb.Rect.X, Y: thumb.Rect.Y, Button: tea.MouseLeft})
	if !m.board.DraggingScrollbar() {
		t.Fatal("fresh grab after recovery refused")
	}
	m.Update(tea.MouseReleaseMsg{X: 1, Y: 1})
}

// Lanes whose cards fit draw no bar and register no regions: their reserved
// column is an anti-jitter spacer, not a control.
func TestBoardFittingLanesRegisterNoBarRegions(t *testing.T) {
	m := compactOverviewModel(2)
	m.View(200, 15)
	for _, region := range m.mouse.HitMap.Regions() {
		if region.ID == ui.RegionScrollbarThumb || region.ID == ui.RegionScrollbarTrack {
			t.Fatalf("fitting board registered %s at %#v", region.ID, region.Rect)
		}
	}
}
