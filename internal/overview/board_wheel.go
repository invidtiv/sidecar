package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/mouse"
)

// BoardWheelAtBoundary mirrors the activity board's wheel routing in Update
// without moving the selection or rendering. It answers true only when the
// event cannot change the board selection: the pointer is over the column that
// already owns the selection and that column's selected row cannot move (an
// empty column is bounded in both directions).
//
// A wheel over a different column is movable, because kanban.MoveInColumn
// re-targets the selection to that column before moving the row.
func (m *Model) BoardWheelAtBoundary(msg tea.MouseWheelMsg) bool {
	if m == nil || m.mouse == nil || m.renameOpen || m.viewFlyoutOpen {
		return false
	}
	action := m.mouse.HandleMouse(msg)
	if action.Type != mouse.ActionScrollUp && action.Type != mouse.ActionScrollDown {
		return false
	}
	if action.Region == nil {
		return false
	}
	region, ok := action.Region.Data.(kanban.HitRegion)
	if !ok {
		return false
	}
	delta := action.Delta
	if delta == 0 {
		if action.Type == mouse.ActionScrollUp {
			delta = -1
		} else {
			delta = 1
		}
	}
	// Reuse the board's own movement rules rather than re-deriving lane sizes.
	board := m.board.Board()
	before := m.board.Selection()
	after := board.MoveRow(board.Clamp(kanban.Selection{Column: region.Column, Row: before.Row}), delta)
	return after == before
}
