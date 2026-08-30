package layoutapply

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/panereposition"
	"github.com/marcus/sidecar/internal/uirequest"
)

// MoveNoGridReason explains a tree with no grid answer, so a move request never
// reports a bare empty cell beside a verdict.
const MoveNoGridReason = "the current layout does not resolve to grid columns, so no cell can be addressed"

// MoveNoFocusReason is --focused with no focused pane to move.
const MoveNoFocusReason = "no pane is focused on that surface; name the pane by cell instead"

// applyMove is the third mode beside batch and spec: reposition ONE pane that
// is already open, through panelayout.PlanMove — the same planner the modal and
// the keyboard call. Nothing here decides placement for itself.
//
// Like the other two modes it never queues, and it is all-or-nothing by
// construction: a refused plan leaves the tree byte-for-byte untouched.
func applyMove(h Host, req uirequest.Request, payload uirequest.LayoutPayload, surface string) tea.Cmd {
	if payload.Move == nil {
		return declineMove(h, req, surface, "move payload carries no move record")
	}
	move := *payload.Move
	if err := uirequest.ValidateLayoutMove(move); err != nil {
		return declineMove(h, req, surface, err.Error())
	}
	target, err := uirequest.ParseLayoutMoveTo(move.To)
	if err != nil {
		return declineMove(h, req, surface, err.Error())
	}

	root := h.PaneRoot()
	grid := panelayout.GridOf(root)
	if root == nil || grid == nil {
		return declineMove(h, req, surface, MoveNoGridReason)
	}
	leafID, reason := moveSourceLeaf(h, grid, move)
	if reason != "" {
		return declineMove(h, req, surface, reason)
	}
	destination, noop, reason := moveDestination(root, grid, leafID, target)
	if reason != "" {
		return declineMove(h, req, surface, reason)
	}
	if noop != "" {
		return ackMoveUnchanged(h, req, surface, leafID, noop)
	}

	peer, placed := h.PeerBox()
	if !placed {
		return declineMove(h, req, surface, tooSmall)
	}
	outcome := panelayout.PlanMove(root, leafID, destination, peer, h.Floors())
	switch outcome.Status {
	case panelayout.MoveUnchanged:
		return ackMoveUnchanged(h, req, surface, leafID, panereposition.Reason(outcome.Reason))
	case panelayout.MoveMoved:
		refusal, cmd := h.CommitMove(outcome.Plan)
		if refusal != "" {
			return declineMove(h, req, surface, refusal)
		}
		h.Ack(req, uirequest.StatusMoved, "", []uirequest.AckItem{{
			Verdict: uirequest.ItemVerdictMoved,
			Surface: surface,
			Pane:    leafID,
			Cell:    moveCellOf(h.PaneRoot(), leafID),
		}}, nil)
		return cmd
	default:
		return declineMove(h, req, surface, panereposition.Reason(outcome.Reason))
	}
}

// moveSourceLeaf names the pane to move: the surface's focused leaf, or the
// occupant of a pre-move cell. Both are read against the tree as it stands,
// which is the addressing rule the whole verb is stated in.
func moveSourceLeaf(h Host, grid *panelayout.Grid, move uirequest.LayoutMove) (int, string) {
	if move.Focused {
		leafID := h.FocusedLeaf()
		if leafID == 0 {
			return 0, MoveNoFocusReason
		}
		if _, ok := moveGridCell(grid, leafID); !ok {
			return 0, MoveNoFocusReason
		}
		return leafID, ""
	}
	cell, _ := panelayout.ParseCell(move.From)
	if cell.Col > grid.ColumnCount() {
		return 0, fmt.Sprintf("column %d is out of range; the layout has %d", cell.Col, grid.ColumnCount())
	}
	if cell.Row > grid.RowCount(cell.Col) {
		return 0, fmt.Sprintf("cell %s holds no pane; column %d holds %d", cell.String(), cell.Col, grid.RowCount(cell.Col))
	}
	return grid.Cell(cell.Col, cell.Row).ID, ""
}

// moveDestination compiles a validated --to form into a MoveDestination.
//
// The direction form goes through panelayout.MoveDirection — the same call the
// modal's h/j/k/l make — so the CLI and the keyboard cannot drift into two
// answers about what "right" means, including the new outer columns at either
// edge that a Cell cannot name.
//
// A non-empty second return is an accepted no-op with its reason; a non-empty
// third is a refusal.
func moveDestination(root *panelayout.Node, grid *panelayout.Grid, leafID int, target uirequest.LayoutMoveTarget) (panelayout.MoveDestination, string, string) {
	switch target.Form {
	case uirequest.LayoutMoveCell:
		return panelayout.MoveDestination{Cell: target.Cell}, "", ""
	case uirequest.LayoutMoveColumn:
		column := target.Column
		if column > grid.ColumnCount() {
			if column != grid.ColumnCount()+1 {
				return panelayout.MoveDestination{}, "", fmt.Sprintf("column %d is out of range; the layout has %d", column, grid.ColumnCount())
			}
			return panelayout.MoveDestination{Cell: panelayout.Cell{Col: column, Row: 1}}, "", ""
		}
		// Appending at the bottom of the destination column is what l and h do,
		// and for the same reason: it makes repeated moves walk a pane across
		// the layout instead of shuffling the destination's occupants.
		return panelayout.MoveDestination{Cell: panelayout.Cell{Col: column, Row: grid.RowCount(column) + 1}}, "", ""
	default:
		destination, ok := panelayout.MoveDirection(root, leafID, target.Direction)
		if !ok {
			return panelayout.MoveDestination{}, panereposition.BoundaryReason(target.Direction), ""
		}
		return destination, "", ""
	}
}

func moveGridCell(grid *panelayout.Grid, leafID int) (panelayout.Cell, bool) {
	if grid == nil {
		return panelayout.Cell{}, false
	}
	for col := 1; col <= grid.ColumnCount(); col++ {
		for row := 1; row <= grid.RowCount(col); row++ {
			if leaf := grid.Cell(col, row); leaf != nil && leaf.ID == leafID {
				return panelayout.Cell{Col: col, Row: row}, true
			}
		}
	}
	return panelayout.Cell{}, false
}

func moveCellOf(root *panelayout.Node, leafID int) string {
	return layoutCells(root)[leafID]
}

func ackMoveUnchanged(h Host, req uirequest.Request, surface string, leafID int, reason string) tea.Cmd {
	h.Ack(req, uirequest.StatusUnchanged, reason, []uirequest.AckItem{{
		Verdict: uirequest.ItemVerdictUnchanged,
		Surface: surface,
		Pane:    leafID,
		Cell:    moveCellOf(h.PaneRoot(), leafID),
		Reason:  reason,
	}}, nil)
	return nil
}

func declineMove(h Host, req uirequest.Request, surface, reason string) tea.Cmd {
	h.Ack(req, uirequest.StatusDeclined, reason, []uirequest.AckItem{{
		Verdict: uirequest.ItemVerdictDeclined,
		Surface: surface,
		Reason:  reason,
	}}, nil)
	return nil
}
