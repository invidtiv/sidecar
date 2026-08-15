package overview

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/mouse"
)

// boardWheelModel builds an activity board with the given per-column card
// counts and registers one hit region per column, as View does.
func boardWheelModel(t *testing.T, counts ...int) *Model {
	t.Helper()
	m := &Model{mouse: mouse.NewHandler()}
	board := kanban.Board{}
	for column, count := range counts {
		lane := kanban.Lane{ID: kanban.LaneID(fmt.Sprintf("lane-%d", column)), Label: "lane"}
		for row := 0; row < count; row++ {
			lane.Cards = append(lane.Cards, kanban.Card{ID: fmt.Sprintf("card-%d-%d", column, row)})
		}
		board.Lanes = append(board.Lanes, lane)
	}
	m.board.SetBoard(board)
	for column := range counts {
		m.mouse.HitMap.AddRect("overview-card", column*20, 0, 20, 10, kanban.HitRegion{
			Kind: kanban.RegionColumnBody, Column: column, X: column * 20, Y: 0, W: 20, H: 10,
		})
	}
	return m
}

func boardWheel(x, y int, up bool) tea.MouseWheelMsg {
	button := tea.MouseWheelDown
	if up {
		button = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
}

// columnX returns a pointer X inside the given column's hit region.
func columnX(column int) int { return column*20 + 5 }

func TestBoardWheelAtBoundary(t *testing.T) {
	tests := []struct {
		name      string
		counts    []int
		selection kanban.Selection
		column    int
		up        bool
		want      bool
	}{
		{name: "top of column, up", counts: []int{5, 5}, selection: kanban.Selection{Column: 0, Row: 0}, column: 0, up: true, want: true},
		{name: "top of column, down", counts: []int{5, 5}, selection: kanban.Selection{Column: 0, Row: 0}, column: 0},
		{name: "middle, up", counts: []int{5, 5}, selection: kanban.Selection{Column: 0, Row: 2}, column: 0, up: true},
		{name: "middle, down", counts: []int{5, 5}, selection: kanban.Selection{Column: 0, Row: 2}, column: 0},
		{name: "bottom, down", counts: []int{5, 5}, selection: kanban.Selection{Column: 0, Row: 4}, column: 0, want: true},
		{name: "bottom, up", counts: []int{5, 5}, selection: kanban.Selection{Column: 0, Row: 4}, column: 0, up: true},
		{name: "empty column is bounded down", counts: []int{0, 5}, selection: kanban.Selection{Column: 0, Row: 0}, column: 0, want: true},
		{name: "empty column is bounded up", counts: []int{0, 5}, selection: kanban.Selection{Column: 0, Row: 0}, column: 0, up: true, want: true},
		{name: "single card column, down", counts: []int{1, 5}, selection: kanban.Selection{Column: 0, Row: 0}, column: 0, want: true},
		// The wheel re-targets the selection to the pointed column, so a
		// different column is movable even at that column's edge.
		{name: "other column at top, up", counts: []int{5, 5}, selection: kanban.Selection{Column: 0, Row: 0}, column: 1, up: true},
		{name: "other column at bottom, down", counts: []int{5, 5}, selection: kanban.Selection{Column: 0, Row: 4}, column: 1},
		// Row clamps into a shorter column: still a column change, so movable.
		{name: "other shorter column", counts: []int{5, 1}, selection: kanban.Selection{Column: 0, Row: 4}, column: 1},
		{name: "empty board", counts: []int{0}, selection: kanban.Selection{}, column: 0, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := boardWheelModel(t, tt.counts...)
			m.board.Select(tt.selection)
			if got := m.BoardWheelAtBoundary(boardWheel(columnX(tt.column), 5, tt.up)); got != tt.want {
				t.Fatalf("BoardWheelAtBoundary = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestBoardWheelReverseAfterBoundary(t *testing.T) {
	m := boardWheelModel(t, 5, 5)
	m.board.Select(kanban.Selection{Column: 0, Row: 4})
	if !m.BoardWheelAtBoundary(boardWheel(columnX(0), 5, false)) {
		t.Fatal("expected bottom boundary")
	}
	if m.BoardWheelAtBoundary(boardWheel(columnX(0), 5, true)) {
		t.Fatal("reverse event after boundary must be movable")
	}
}

func TestBoardWheelUnknownCases(t *testing.T) {
	t.Run("no hit region", func(t *testing.T) {
		m := boardWheelModel(t, 5)
		m.board.Select(kanban.Selection{Column: 0, Row: 0})
		if m.BoardWheelAtBoundary(boardWheel(500, 500, true)) {
			t.Fatal("pointer outside every column must be unknown")
		}
	})
	t.Run("rename modal open", func(t *testing.T) {
		m := boardWheelModel(t, 5)
		m.renameOpen = true
		if m.BoardWheelAtBoundary(boardWheel(columnX(0), 5, true)) {
			t.Fatal("an overlay owning input must be unknown")
		}
	})
	t.Run("view flyout open", func(t *testing.T) {
		m := boardWheelModel(t, 5)
		m.viewFlyoutOpen = true
		if m.BoardWheelAtBoundary(boardWheel(columnX(0), 5, true)) {
			t.Fatal("an overlay owning input must be unknown")
		}
	})
}

// The boundary answer must agree with the movement the board actually applies.
func TestBoardWheelBoundaryMatchesMovement(t *testing.T) {
	for _, row := range []int{0, 1, 3, 4} {
		for _, column := range []int{0, 1} {
			for _, up := range []bool{true, false} {
				m := boardWheelModel(t, 5, 3)
				m.board.Select(kanban.Selection{Column: 0, Row: row})
				before := m.board.Selection()
				bounded := m.BoardWheelAtBoundary(boardWheel(columnX(column), 5, up))
				delta := 3
				if up {
					delta = -3
				}
				m.board.MoveInColumn(column, delta)
				changed := m.board.Selection() != before
				if bounded == changed {
					t.Fatalf("row=%d column=%d up=%v: bounded=%v changed=%v", row, column, up, bounded, changed)
				}
			}
		}
	}
}

// A boundary query must never move the selection.
func TestBoardWheelAtBoundaryIsReadOnly(t *testing.T) {
	m := boardWheelModel(t, 5, 5)
	m.board.Select(kanban.Selection{Column: 0, Row: 4})
	for i := 0; i < 50; i++ {
		m.BoardWheelAtBoundary(boardWheel(columnX(0), 5, false))
	}
	if got := m.board.Selection(); got != (kanban.Selection{Column: 0, Row: 4}) {
		t.Fatalf("selection changed to %+v", got)
	}
}
