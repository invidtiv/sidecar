// Package panereposition owns the host-independent interaction state for
// repositioning a pane. Structural policy remains in panelayout; surfaces only
// provide their current tree, geometry, persistence, and user-visible notice.
package panereposition

import (
	"github.com/marcus/sidecar/internal/panelayout"
)

const (
	CommandMove = "move-pane"

	LayoutChangedReason = "the pane layout changed; try again"
)

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
