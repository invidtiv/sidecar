// Package panereposition owns the host-independent interaction state for
// repositioning a pane. Structural policy remains in panelayout; surfaces only
// provide their current tree, geometry, persistence, and user-visible notice.
package panereposition

import (
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
)

const (
	Context     = "pane-move"
	CommandMove = "move-pane"

	AreaHiddenReason    = "the pane area is not visible"
	LayoutChangedReason = "the pane layout changed; try again"
)

// Mode identifies the leaf being moved and the active pane tree that owns it.
// Scope must include the host's surface identity and tree generation: leaf IDs
// are deliberately reusable after a project or Sessions-row switch.
type Mode struct {
	scope  string
	leafID int
}

func (m *Mode) Start(scope string, leafID int) bool {
	if m == nil || scope == "" || leafID <= 0 {
		return false
	}
	m.scope, m.leafID = scope, leafID
	return true
}

func (m *Mode) Reset() {
	if m != nil {
		*m = Mode{}
	}
}

// Reconcile keeps the transient mode only while the exact tree still owns the
// source leaf. Hosts call it from their context/command ladders as well as key
// handling so a close or surface replacement cannot leave a stale mode active.
func (m *Mode) Reconcile(scope string, root *panelayout.Node) bool {
	if m == nil || m.scope == "" || m.scope != scope {
		if m != nil && m.scope != "" {
			m.Reset()
		}
		return false
	}
	leaf := panelayout.Find(root, m.leafID)
	if leaf == nil || leaf.Split != nil {
		m.Reset()
		return false
	}
	return true
}

func (m *Mode) Active(scope string, root *panelayout.Node) bool {
	return m != nil && m.scope != "" && m.scope == scope && m.Leaf(root) != nil
}

func (m *Mode) LeafID() int {
	if m == nil {
		return 0
	}
	return m.leafID
}

func (m *Mode) Leaf(root *panelayout.Node) *panelayout.Node {
	if m == nil || m.leafID <= 0 {
		return nil
	}
	leaf := panelayout.Find(root, m.leafID)
	if leaf == nil || leaf.Split != nil {
		return nil
	}
	return leaf
}

type Action struct {
	Direction panelayout.Direction
	Move      bool
	Exit      bool
}

// Decode owns every key while mode is active. An unrecognized key therefore
// returns a zero Action and is swallowed by the surface rather than leaking to
// the pane or list underneath.
func Decode(key string) Action {
	switch key {
	case "h", "left":
		return Action{Direction: panelayout.DirectionLeft, Move: true}
	case "l", "right":
		return Action{Direction: panelayout.DirectionRight, Move: true}
	case "k", "up":
		return Action{Direction: panelayout.DirectionUp, Move: true}
	case "j", "down":
		return Action{Direction: panelayout.DirectionDown, Move: true}
	case "M", "enter", "esc":
		return Action{Exit: true}
	default:
		return Action{}
	}
}

func BoundaryReason(direction panelayout.Direction) string {
	switch direction {
	case panelayout.DirectionUp:
		return "already at the top"
	case panelayout.DirectionDown:
		return "already at the bottom"
	default:
		return panelayout.MoveUnchangedMessage
	}
}

// Reason guarantees every refused/no-op path has something user-visible to
// say even if a future structural outcome forgets its explanatory copy.
func Reason(reason string) string {
	if reason == "" {
		return panelayout.MoveUnchangedMessage
	}
	return reason
}

// Commands is the mode-only footer/help vocabulary shared by both Workspaces
// projections. Bindings remain centralized in internal/keymap.
func Commands() []plugin.Command {
	return []plugin.Command{
		{ID: "move-pane-left", Name: "Left", Description: "Move pane left", Context: Context, Priority: 1},
		{ID: "move-pane-down", Name: "Down", Description: "Move pane down", Context: Context, Priority: 2},
		{ID: "move-pane-up", Name: "Up", Description: "Move pane up", Context: Context, Priority: 3},
		{ID: "move-pane-right", Name: "Right", Description: "Move pane right", Context: Context, Priority: 4},
		{ID: "move-pane-done", Name: "Done", Description: "Leave pane move mode", Context: Context, Priority: 5},
	}
}
