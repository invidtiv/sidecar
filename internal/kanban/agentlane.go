package kanban

import (
	"image/color"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/styles"
)

// agentLanes is the one table of lane wording and marks that every Kanban board
// draws its shared lanes from.
//
// The words are short deliberately. A lane column is as narrow as 16 columns on
// the project board, and the component appends a count to whatever it is given,
// so the list's longer heading for the same lane ("Needs Attention") does not
// fit here — which is why a board words a lane for itself rather than reading
// the list's group name. It words it once for both boards, not once per board.
var agentLanes = map[agentstatus.LaneID]string{
	agentstatus.LaneWorking: "● Working",
	agentstatus.LaneBlocked: "◆ Blocked",
	agentstatus.LaneDone:    "✓ Done",
	agentstatus.LaneIdle:    "○ Idle",
	agentstatus.LanePaused:  "⏸ Paused",
}

// LanePalette answers the colour a board draws a lane's header in.
//
// Colour is not part of a lane's identity here: the two boards arrived with
// hues of their own, and sharing the definition of what a lane *is* must not
// re-theme either of them. A board states its palette; ThemeLanePalette is the
// theme's own lane colours, for a board that wants them.
type LanePalette func(agentstatus.LaneID) color.Color

// ThemeLanePalette is the theme's lane colours, as the global browser draws.
func ThemeLanePalette(lane agentstatus.LaneID) color.Color {
	return styles.LaneColor(string(lane))
}

// AgentLane is the one definition of an agent-activity lane, for whichever board
// is drawing it: its identity, its label and mark, and the header colour the
// caller's palette gives it.
//
// Both Kanban boards — the project's and the global browser's — build their
// shared lanes from here, and neither words one itself. Two tables of labels is
// how the same lane came to read "Blocked" on one board and "Needs Attention"
// on the other; a rename now lands once.
//
// [Lane.State] is left zero: whether a lane is ready, loading or errored is
// what a particular board knows about a particular refresh, not something the
// lane is. A board that draws empty lanes differently sets it.
//
// A board with a lane of its own — the project's Shells column has no global
// counterpart — still defines that one itself. What is shared is the lanes both
// boards have, not the set.
func AgentLane(lane agentstatus.LaneID, palette LanePalette) Lane {
	if palette == nil {
		palette = ThemeLanePalette
	}
	return Lane{
		ID:          LaneID(lane),
		Label:       agentLanes[lane],
		HeaderColor: palette(lane),
	}
}
