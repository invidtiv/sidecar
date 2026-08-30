package panelayout

import "fmt"

// Direction is a movement in the projected grid. It is presentation-neutral:
// key bindings, modals, and commands translate their own input into one of
// these values before asking for a destination.
type Direction int

const (
	DirectionLeft Direction = iota
	DirectionRight
	DirectionUp
	DirectionDown
)

// OuterColumnEdge names the two placements Cell cannot express symmetrically.
type OuterColumnEdge int

const (
	NoOuterColumnEdge OuterColumnEdge = iota
	BeforeFirstColumn
	AfterLastColumn
)

// MoveDestination is either a cell in the pre-move grid or an outer column
// edge. Exactly one form must be populated.
type MoveDestination struct {
	Cell      Cell
	OuterEdge OuterColumnEdge
}

type MoveStatus int

const (
	MoveRefused MoveStatus = iota
	MoveUnchanged
	MoveMoved
)

const (
	MoveUnchangedMessage = "that move leaves the layout unchanged"
	MoveFitMessage       = "the window is too small to split"
)

// MovePlan is a structural edit compiled against the tree passed to PlanMove.
// Placement is resolved after source removal; callers must not reinterpret it
// against the pre-move grid.
type MovePlan struct {
	LeafID          int
	Placement       OpenPlan
	CarriedRatio    int
	HasCarriedRatio bool
}

// MoveOutcome says whether a request moves, is already satisfied, or is
// refused. Reason is user-facing for unchanged and refused outcomes.
type MoveOutcome struct {
	Status MoveStatus
	Plan   MovePlan
	Reason string
}

// MoveDirection compiles one directional step against the pre-move grid.
// Vertical boundaries have no destination. Horizontal boundaries retain an
// explicit outer edge so PlanMove can distinguish an identity move from a cap
// refusal after source extraction.
func MoveDirection(root *Node, leafID int, direction Direction) (MoveDestination, bool) {
	grid := GridOf(root)
	if grid == nil {
		return MoveDestination{}, false
	}
	col, row, ok := gridCellOf(grid, leafID)
	if !ok {
		return MoveDestination{}, false
	}
	switch direction {
	case DirectionUp:
		if row == 1 {
			return MoveDestination{}, false
		}
		return MoveDestination{Cell: Cell{Col: col, Row: row - 1}}, true
	case DirectionDown:
		if row == grid.RowCount(col) {
			return MoveDestination{}, false
		}
		return MoveDestination{Cell: Cell{Col: col, Row: row + 1}}, true
	case DirectionLeft:
		if col == 1 {
			return MoveDestination{OuterEdge: BeforeFirstColumn}, true
		}
		return MoveDestination{Cell: Cell{Col: col - 1, Row: grid.RowCount(col-1) + 1}}, true
	case DirectionRight:
		if col == grid.ColumnCount() {
			return MoveDestination{OuterEdge: AfterLastColumn}, true
		}
		return MoveDestination{Cell: Cell{Col: col + 1, Row: grid.RowCount(col+1) + 1}}, true
	default:
		return MoveDestination{}, false
	}
}

// PlanMove extracts a leaf on a clone, translates the destination from the
// visible pre-move grid, and accepts only a result that fits the real surface.
// The input tree is never mutated.
func PlanMove(root *Node, leafID int, destination MoveDestination, box Box, floors Floors) MoveOutcome {
	grid := GridOf(root)
	if grid == nil {
		return refusedMove("the current layout does not resolve to grid columns, so no cell can be addressed")
	}
	sourceCol, sourceRow, ok := gridCellOf(grid, leafID)
	if !ok {
		return refusedMove("the pane to move is not a leaf in the current layout")
	}
	translated, outcome := translateMoveDestination(grid, sourceCol, sourceRow, destination)
	if outcome != nil {
		return *outcome
	}
	if LeafCount(root) == 1 {
		return unchangedMove()
	}

	carried, hasCarried := carriedRatio(root, leafID)
	postRemoval := Clone(root)
	postRemoval, _ = Close(postRemoval, leafID)
	placement, reason := planPlacement(postRemoval, translated)
	if reason != "" {
		return refusedMove(reason)
	}
	plan := MovePlan{
		LeafID:          leafID,
		Placement:       placement,
		CarriedRatio:    carried,
		HasCarriedRatio: hasCarried,
	}
	moved, _ := ApplyMove(Clone(root), plan)
	if idsEqualForMove(gridOfLeafIDs(root), gridOfLeafIDs(moved)) {
		return unchangedMove()
	}
	if _, _, fits := LayoutPanes(moved, box, floors); !fits {
		return refusedMove(MoveFitMessage)
	}
	return MoveOutcome{Status: MoveMoved, Plan: plan}
}

// ApplyMove applies a previously accepted plan while retaining the exact leaf
// pointer and ID. Host-owned state keyed by either identity therefore follows
// the pane instead of being reconstructed.
func ApplyMove(root *Node, plan MovePlan) (*Node, int) {
	leaf := Find(root, plan.LeafID)
	if leaf == nil || leaf.Split != nil || LeafCount(root) < 2 || plan.Placement.Retarget != 0 {
		return root, plan.LeafID
	}
	// An accepted plan names the post-removal tree. Check that target before
	// touching the live tree so a stale or fabricated plan cannot turn a move
	// into a close.
	trial := Clone(root)
	trialLeaf := Find(trial, plan.LeafID)
	trial, _ = Close(trial, plan.LeafID)
	trial, trialFocus := ApplyPlan(trial, plan.Placement, trialLeaf)
	if trialFocus != plan.LeafID || Find(trial, plan.LeafID) != trialLeaf {
		return root, plan.LeafID
	}
	root, _ = Close(root, plan.LeafID)
	if Find(root, plan.LeafID) != nil {
		return root, plan.LeafID
	}
	root, focus := ApplyPlan(root, plan.Placement, leaf)
	if Find(root, plan.LeafID) != leaf {
		return root, focus
	}
	if plan.HasCarriedRatio {
		if parent, first := leafParent(root, plan.LeafID); parent != nil {
			ratio := plan.CarriedRatio
			if !first {
				ratio = 100 - ratio
			}
			parent.Split.Ratio = ClampRatio(ratio)
		}
	}
	return root, focus
}

func translateMoveDestination(grid *Grid, sourceCol, sourceRow int, destination MoveDestination) (MoveDestination, *MoveOutcome) {
	if destination.OuterEdge != NoOuterColumnEdge {
		if destination.Cell != (Cell{}) || (destination.OuterEdge != BeforeFirstColumn && destination.OuterEdge != AfterLastColumn) {
			outcome := refusedMove("the move destination is invalid")
			return MoveDestination{}, &outcome
		}
		return destination, nil
	}
	cell := destination.Cell
	if cell.Col < 1 || cell.Row < 1 || cell.Col > MaxGridColumns || cell.Row > MaxGridRows {
		outcome := refusedMove(outsideGridReason(cell))
		return MoveDestination{}, &outcome
	}
	if cell.Col > grid.ColumnCount() {
		if cell.Col != grid.ColumnCount()+1 || cell.Row != 1 {
			outcome := refusedMove(columnRangeReason(cell.Col, grid.ColumnCount()))
			return MoveDestination{}, &outcome
		}
	} else if cell.Row > grid.RowCount(cell.Col)+1 {
		outcome := refusedMove(cellRangeReason(cell, grid.RowCount(cell.Col)))
		return MoveDestination{}, &outcome
	}
	if cell.Col == sourceCol && cell.Row == sourceRow {
		outcome := unchangedMove()
		return MoveDestination{}, &outcome
	}

	translated := cell
	sourceColumnCollapses := grid.RowCount(sourceCol) == 1
	if sourceColumnCollapses {
		if cell.Col == sourceCol {
			outcome := unchangedMove()
			return MoveDestination{}, &outcome
		}
		if sourceCol < cell.Col {
			translated.Col--
		}
	} else if cell.Col == sourceCol && cell.Row == grid.RowCount(sourceCol)+1 {
		// Appending to the source column remains an append after extraction.
		translated.Row--
	}
	return MoveDestination{Cell: translated}, nil
}

func gridCellOf(grid *Grid, leafID int) (int, int, bool) {
	if grid == nil {
		return 0, 0, false
	}
	for col, column := range grid.Columns {
		for row, leaf := range column.Cells {
			if leaf.ID == leafID {
				return col + 1, row + 1, true
			}
		}
	}
	return 0, 0, false
}

func carriedRatio(root *Node, leafID int) (int, bool) {
	parent, first := leafParent(root, leafID)
	if parent == nil {
		return 0, false
	}
	if first {
		return parent.Split.Ratio, true
	}
	return 100 - parent.Split.Ratio, true
}

func leafParent(node *Node, leafID int) (*Node, bool) {
	if node == nil || node.Split == nil {
		return nil, false
	}
	if node.Split.A.Split == nil && node.Split.A.ID == leafID {
		return node, true
	}
	if node.Split.B.Split == nil && node.Split.B.ID == leafID {
		return node, false
	}
	if parent, first := leafParent(node.Split.A, leafID); parent != nil {
		return parent, first
	}
	return leafParent(node.Split.B, leafID)
}

func gridOfLeafIDs(root *Node) [][]int {
	grid := GridOf(root)
	if grid == nil {
		return nil
	}
	ids := make([][]int, len(grid.Columns))
	for col, column := range grid.Columns {
		ids[col] = make([]int, len(column.Cells))
		for row, leaf := range column.Cells {
			ids[col][row] = leaf.ID
		}
	}
	return ids
}

func idsEqualForMove(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for col := range a {
		if len(a[col]) != len(b[col]) {
			return false
		}
		for row := range a[col] {
			if a[col][row] != b[col][row] {
				return false
			}
		}
	}
	return true
}

func outsideGridReason(cell Cell) string {
	return fmt.Sprintf("cell %s is outside the %dx%d layout grid", cell.String(), MaxGridColumns, MaxGridRows)
}

func columnRangeReason(col, count int) string {
	return fmt.Sprintf("column %d is out of range; the layout has %d", col, count)
}

func cellRangeReason(cell Cell, rows int) string {
	return fmt.Sprintf("cell %s is out of range; column %d holds %d", cell.String(), cell.Col, rows)
}

func refusedMove(reason string) MoveOutcome {
	return MoveOutcome{Status: MoveRefused, Reason: reason}
}

func unchangedMove() MoveOutcome {
	return MoveOutcome{Status: MoveUnchanged, Reason: MoveUnchangedMessage}
}
