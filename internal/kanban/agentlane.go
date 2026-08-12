package kanban

import (
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

// AgentLane is the one definition of an agent-activity lane, for whichever board
// is drawing it: its label and mark, and the theme's colour for that lane.
//
// Both Kanban boards — the project's and the global browser's — build their
// shared lanes from here, and neither words or colours one itself. Two tables of
// labels and colours is how the same lane came to read "Blocked" on one board
// and "Needs Attention" on the other, in two different greens; a rename or a
// re-theme now lands once.
//
// A board with a lane of its own — the project's Shells column has no global
// counterpart — still defines that one itself. What is shared is the lanes both
// boards have, not the set.
func AgentLane(lane agentstatus.LaneID) Lane {
	return Lane{
		ID:          LaneID(lane),
		Label:       agentLanes[lane],
		HeaderColor: styles.LaneColor(string(lane)),
		State:       CellReady,
	}
}
