package overview

import (
	"github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/mouse"
)

// Lane-bar gestures on the Agent Overview board.
//
// The board component owns everything about a lane's scrollbar — its regions,
// its press-time mapping, which lane a drag moves — so what lives here is only
// routing: arming PressScrollbar on a press inside a registered bar region,
// starting the shared handler's drag so motions come back by their source, and
// settling ReleaseScrollbar on the release or on the first button-less motion
// that betrays a lost one. Nothing is persisted; scroll offsets are view state.

// isBoardBarRegion reports that a hit-tested region belongs to a lane's bar
// rather than to a card or column.
func isBoardBarRegion(region kanban.HitRegion) bool {
	return region.Kind == kanban.RegionScrollbarThumb || region.Kind == kanban.RegionScrollbarTrack
}

// isBoardScrollbarDragID reports that a drag started in one of the board's bar
// regions, so its motion belongs to that gesture rather than to whatever the
// pointer is over now.
func isBoardScrollbarDragID(id string) bool {
	return id == string(kanban.RegionScrollbarThumb) ||
		id == string(kanban.RegionScrollbarTrack)
}

// pressBoardScrollbar begins the grabbed lane's gesture. The component refuses
// anything that is not a live bar — including a fitting lane's spacer column —
// and only then does the shared handler's drag start.
func (m *Model) pressBoardScrollbar(region kanban.HitRegion, action mouse.MouseAction) {
	if m.board.PressScrollbar(region, action.Y) {
		m.mouse.StartDrag(action.X, action.Y, string(region.Kind), 0)
	}
}
