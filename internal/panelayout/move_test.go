package panelayout

import (
	"reflect"
	"strings"
	"testing"
)

var moveTestBox = Box{W: 500, H: 200}

func moveTestFloors() Floors {
	floor := Floor{Width: 4, Height: 2}
	return Floors{
		Primary: floor, Doc: floor, Issue: floor, Diff: floor,
		Resource: floor, Shell: floor, Note: floor,
	}
}

func applyAcceptedMove(t *testing.T, root *Node, leafID int, destination MoveDestination) *Node {
	t.Helper()
	outcome := PlanMove(root, leafID, destination, moveTestBox, moveTestFloors())
	if outcome.Status != MoveMoved {
		t.Fatalf("PlanMove status=%v reason=%q, want moved", outcome.Status, outcome.Reason)
	}
	root, focus := ApplyMove(root, outcome.Plan)
	if focus != leafID {
		t.Fatalf("ApplyMove focus=%d, want %d", focus, leafID)
	}
	return root
}

func TestMoveDirectionAcrossGridVocabulary(t *testing.T) {
	shapes := []struct{ cols, rows int }{{1, 1}, {2, 1}, {2, 2}, {1, 4}, {4, 1}}
	directions := []Direction{DirectionLeft, DirectionRight, DirectionUp, DirectionDown}

	for _, shape := range shapes {
		for col := 1; col <= shape.cols; col++ {
			for row := 1; row <= shape.rows; row++ {
				for _, direction := range directions {
					name := Cell{Col: col, Row: row}.String() + "/" + directionName(direction)
					t.Run(shapeName(shape.cols, shape.rows)+"/"+name, func(t *testing.T) {
						root := gridTree(shape.cols, shape.rows, Document)
						leafID := GridOf(root).Cell(col, row).ID
						destination, ok := MoveDirection(root, leafID, direction)
						verticalBoundary := direction == DirectionUp && row == 1 || direction == DirectionDown && row == shape.rows
						if verticalBoundary {
							if ok {
								t.Fatalf("boundary direction returned %#v", destination)
							}
							return
						}
						if !ok {
							t.Fatal("direction did not compile")
						}
						outcome := PlanMove(root, leafID, destination, moveTestBox, moveTestFloors())
						horizontalIdentity := shape.rows == 1 && ((direction == DirectionLeft && col == 1) || (direction == DirectionRight && col == shape.cols))
						if shape.cols == 1 && shape.rows == 1 || horizontalIdentity {
							if outcome.Status != MoveUnchanged {
								t.Fatalf("identity status=%v reason=%q", outcome.Status, outcome.Reason)
							}
							return
						}
						if outcome.Status != MoveMoved {
							t.Fatalf("status=%v reason=%q, want moved", outcome.Status, outcome.Reason)
						}
						moved, _ := ApplyMove(root, outcome.Plan)
						movedGrid := GridOf(moved)
						gotCol, gotRow, found := gridCellOf(movedGrid, leafID)
						if !found {
							t.Fatal("moved leaf disappeared")
						}
						switch direction {
						case DirectionUp:
							if gotCol != col || gotRow != row-1 {
								t.Fatalf("landed %d.%d, want %d.%d", gotCol, gotRow, col, row-1)
							}
						case DirectionDown:
							if gotCol != col || gotRow != row+1 {
								t.Fatalf("landed %d.%d, want %d.%d", gotCol, gotRow, col, row+1)
							}
						case DirectionLeft:
							if col == 1 {
								if gotCol != 1 || gotRow != 1 {
									t.Fatalf("left edge landed %d.%d, want 1.1", gotCol, gotRow)
								}
							} else {
								wantCol := col - 1
								if gotCol != wantCol || gotRow != movedGrid.RowCount(wantCol) {
									t.Fatalf("left landed %d.%d, want bottom of column %d", gotCol, gotRow, wantCol)
								}
							}
						case DirectionRight:
							if col == shape.cols {
								if gotCol != movedGrid.ColumnCount() || gotRow != 1 {
									t.Fatalf("right edge landed %d.%d, want last column", gotCol, gotRow)
								}
							} else {
								// A one-row source column collapses before the destination.
								wantCol := col + 1
								if shape.rows == 1 {
									wantCol--
								}
								if gotCol != wantCol || gotRow != movedGrid.RowCount(wantCol) {
									t.Fatalf("right landed %d.%d, want bottom of column %d", gotCol, gotRow, wantCol)
								}
							}
						}
					})
				}
			}
		}
	}
}

func TestPlanMoveOuterEdgesCapsAndEscapedGrid(t *testing.T) {
	root := gridTree(2, 2, Document)
	leafID := 1
	left := applyAcceptedMove(t, Clone(root), leafID, MoveDestination{OuterEdge: BeforeFirstColumn})
	if col, row, _ := gridCellOf(GridOf(left), leafID); col != 1 || row != 1 {
		t.Fatalf("before-first landed %d.%d", col, row)
	}
	right := applyAcceptedMove(t, Clone(root), leafID, MoveDestination{OuterEdge: AfterLastColumn})
	if col, row, _ := gridCellOf(GridOf(right), leafID); col != GridOf(right).ColumnCount() || row != 1 {
		t.Fatalf("after-last landed %d.%d", col, row)
	}
	leftIdentity := gridTree(2, 1, Document)
	if outcome := PlanMove(leftIdentity, 1, MoveDestination{OuterEdge: BeforeFirstColumn}, moveTestBox, moveTestFloors()); outcome.Status != MoveUnchanged {
		t.Fatalf("before-first identity = %#v, want unchanged", outcome)
	}
	rightIdentity := gridTree(2, 1, Document)
	if outcome := PlanMove(rightIdentity, 2, MoveDestination{OuterEdge: AfterLastColumn}, moveTestBox, moveTestFloors()); outcome.Status != MoveUnchanged {
		t.Fatalf("after-last identity = %#v, want unchanged", outcome)
	}

	fourColumns := gridTree(4, 2, Document)
	outcome := PlanMove(fourColumns, 1, MoveDestination{OuterEdge: BeforeFirstColumn}, moveTestBox, moveTestFloors())
	if outcome.Status != MoveRefused || outcome.Reason != GridColumnCapMessage {
		t.Fatalf("column cap = status %v reason %q", outcome.Status, outcome.Reason)
	}
	// The fourth column's cells are 12 and 14 in gridTree(4, 2).
	outcome = PlanMove(fourColumns, 12, MoveDestination{OuterEdge: AfterLastColumn}, moveTestBox, moveTestFloors())
	if outcome.Status != MoveRefused || outcome.Reason != GridColumnCapMessage {
		t.Fatalf("mirrored column cap = status %v reason %q", outcome.Status, outcome.Reason)
	}
	fullTarget := split(100, Columns, 50, leaf(90, Document), gridTree(1, 4, Issue))
	outcome = PlanMove(fullTarget, 90, MoveDestination{Cell: Cell{Col: 2, Row: 2}}, moveTestBox, moveTestFloors())
	if outcome.Status != MoveRefused || outcome.Reason != GridRowCapMessage {
		t.Fatalf("row cap = status %v reason %q", outcome.Status, outcome.Reason)
	}

	escaped := split(9, Rows, 50,
		split(7, Columns, 50, leaf(1, Primary), leaf(2, Document)),
		leaf(3, Issue),
	)
	outcome = PlanMove(escaped, 1, MoveDestination{Cell: Cell{Col: 1, Row: 1}}, moveTestBox, moveTestFloors())
	if outcome.Status != MoveRefused || !strings.Contains(outcome.Reason, "does not resolve to grid columns") {
		t.Fatalf("escaped grid = status %v reason %q", outcome.Status, outcome.Reason)
	}
}

func TestPlanMoveTranslatesPreRemovalAddresses(t *testing.T) {
	column := gridTree(1, 4, Document)
	const first = 1
	moved := applyAcceptedMove(t, column, first, MoveDestination{Cell: Cell{Col: 1, Row: 3}})
	// The destination is a final cell, not the target leaf's shifted cell after
	// extraction: removing row 1 does not turn a requested final row 3 into 2.
	if got, want := gridOfLeafIDs(moved), [][]int{{3, 5, first, 7}}; !idsEqualForMove(got, want) {
		t.Fatalf("row-before-destination translation = %v, want %v", got, want)
	}

	column = gridTree(1, 3, Document)
	moved = applyAcceptedMove(t, column, first, MoveDestination{Cell: Cell{Col: 1, Row: 4}})
	if col, row, _ := gridCellOf(GridOf(moved), first); col != 1 || row != 3 {
		t.Fatalf("source-column append landed %d.%d, want 1.3", col, row)
	}

	columns := gridTree(3, 1, Document)
	const source = 1
	moved = applyAcceptedMove(t, columns, source, MoveDestination{Cell: Cell{Col: 3, Row: 1}})
	if got, want := gridOfLeafIDs(moved), [][]int{{2}, {source, 4}}; !idsEqualForMove(got, want) {
		t.Fatalf("collapsed-column translation = %v, want %v", got, want)
	}

	identity := gridTree(2, 1, Document)
	outcome := PlanMove(identity, source, MoveDestination{Cell: Cell{Col: 1, Row: 2}}, moveTestBox, moveTestFloors())
	if outcome.Status != MoveUnchanged {
		t.Fatalf("disappearing destination = status %v reason %q", outcome.Status, outcome.Reason)
	}
}

func TestPlanMoveValidatesSingletonDestinationBeforeIdentity(t *testing.T) {
	root := leaf(1, Primary)
	tests := []struct {
		name        string
		destination MoveDestination
		wantReason  string
	}{
		{
			name:        "cell outside grid",
			destination: MoveDestination{Cell: Cell{Col: 5, Row: 1}},
			wantReason:  "cell 5.1 is outside the 4x4 layout grid",
		},
		{
			name: "mixed cell and outer edge",
			destination: MoveDestination{
				Cell:      Cell{Col: 1, Row: 1},
				OuterEdge: BeforeFirstColumn,
			},
			wantReason: "the move destination is invalid",
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			outcome := PlanMove(root, 1, tc.destination, moveTestBox, moveTestFloors())
			if outcome.Status != MoveRefused || outcome.Reason != tc.wantReason {
				t.Fatalf("PlanMove = %#v, want refused %q", outcome, tc.wantReason)
			}
		})
	}
	if outcome := PlanMove(root, 1, MoveDestination{OuterEdge: AfterLastColumn}, moveTestBox, moveTestFloors()); outcome.Status != MoveUnchanged {
		t.Fatalf("valid singleton identity = %#v, want unchanged", outcome)
	}
}

func TestPlanMoveOutcomesAndFitRefusalDoNotMutate(t *testing.T) {
	root := gridTree(2, 2, Document)
	before := Clone(root)
	leafID := GridOf(root).Cell(1, 1).ID

	moved := PlanMove(root, leafID, MoveDestination{OuterEdge: BeforeFirstColumn}, moveTestBox, moveTestFloors())
	if moved.Status != MoveMoved || moved.Plan.LeafID != leafID || moved.Reason != "" {
		t.Fatalf("moved outcome = %#v", moved)
	}
	unchanged := PlanMove(root, leafID, MoveDestination{Cell: Cell{Col: 1, Row: 1}}, moveTestBox, moveTestFloors())
	if unchanged.Status != MoveUnchanged || unchanged.Reason == "" || unchanged.Plan != (MovePlan{}) {
		t.Fatalf("unchanged outcome = %#v", unchanged)
	}
	refused := PlanMove(root, 999, MoveDestination{Cell: Cell{Col: 1, Row: 1}}, moveTestBox, moveTestFloors())
	if refused.Status != MoveRefused || refused.Reason == "" || refused.Plan != (MovePlan{}) {
		t.Fatalf("refused outcome = %#v", refused)
	}

	// The 2x2 needs 9x5. Moving one first-column leaf to an outer edge
	// produces three columns needing 14x5, so 13x5 proves the move itself is
	// what fails rather than merely rediscovering an already-unfitting tree.
	tight := Box{W: 13, H: 5}
	if _, _, fits := LayoutPanes(root, tight, moveTestFloors()); !fits {
		t.Fatal("premise: original tree must fit the refusal box")
	}
	refused = PlanMove(root, leafID, MoveDestination{OuterEdge: BeforeFirstColumn}, tight, moveTestFloors())
	if refused.Status != MoveRefused || refused.Reason != MoveFitMessage {
		t.Fatalf("fit refusal = %#v", refused)
	}
	if !reflect.DeepEqual(root, before) {
		t.Fatalf("planning mutated the tree:\n got %#v\nwant %#v", root, before)
	}
}

func TestMoveCarriesRatioAcrossExtractionLandingAxisAndRepeatedMoves(t *testing.T) {
	tests := []struct {
		name       string
		ratio      int
		sourceID   int
		dest       MoveDestination
		wantShare  int
		wantFirst  bool
		wantParent int
	}{
		{"A extraction A landing", 70, 1, MoveDestination{OuterEdge: BeforeFirstColumn}, 70, true, 70},
		{"A extraction B landing", 70, 1, MoveDestination{OuterEdge: AfterLastColumn}, 70, false, 30},
		{"B extraction A landing", 30, 2, MoveDestination{OuterEdge: BeforeFirstColumn}, 70, true, 70},
		{"B extraction B landing", 30, 2, MoveDestination{OuterEdge: AfterLastColumn}, 70, false, 30},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			root := split(10, Columns, 50,
				split(9, Rows, tc.ratio, leaf(1, Document), leaf(2, Issue)),
				leaf(3, Diff),
			)
			outcome := PlanMove(root, tc.sourceID, tc.dest, moveTestBox, moveTestFloors())
			if outcome.Status != MoveMoved || !outcome.Plan.HasCarriedRatio || outcome.Plan.CarriedRatio != tc.wantShare {
				t.Fatalf("plan = %#v, want carried share %d", outcome, tc.wantShare)
			}
			root, _ = ApplyMove(root, outcome.Plan)
			parent, first := leafParent(root, tc.sourceID)
			if parent == nil || first != tc.wantFirst || parent.Split.Ratio != tc.wantParent {
				t.Fatalf("landing parent=%#v first=%v, want first=%v ratio=%d", parent, first, tc.wantFirst, tc.wantParent)
			}
			if parent.Split.Axis != Columns {
				t.Fatalf("axis change did not land in a column split: %v", parent.Split.Axis)
			}
		})
	}

	root := split(10, Columns, 50,
		split(9, Rows, 65, leaf(1, Document), leaf(2, Issue)),
		leaf(3, Diff),
	)
	for step, direction := range []Direction{DirectionRight, DirectionLeft, DirectionRight, DirectionLeft} {
		destination, ok := MoveDirection(root, 1, direction)
		if !ok {
			t.Fatalf("step %d direction did not compile", step+1)
		}
		outcome := PlanMove(root, 1, destination, moveTestBox, moveTestFloors())
		if outcome.Status != MoveMoved || outcome.Plan.CarriedRatio != 65 {
			t.Fatalf("step %d plan=%#v, want carried ratio 65", step+1, outcome)
		}
		root, _ = ApplyMove(root, outcome.Plan)
		parent, first := leafParent(root, 1)
		share := parent.Split.Ratio
		if !first {
			share = 100 - share
		}
		if share != 65 {
			t.Fatalf("step %d landed share=%d, want 65", step+1, share)
		}
	}

	clamped := split(10, Columns, 50,
		split(9, Rows, 5, leaf(1, Document), leaf(2, Issue)),
		leaf(3, Diff),
	)
	outcome := PlanMove(clamped, 1, MoveDestination{OuterEdge: AfterLastColumn}, moveTestBox, moveTestFloors())
	if outcome.Plan.CarriedRatio != 5 {
		t.Fatalf("carried ratio=%d, want raw request 5", outcome.Plan.CarriedRatio)
	}
	clamped, _ = ApplyMove(clamped, outcome.Plan)
	parent, first := leafParent(clamped, 1)
	if first || parent.Split.Ratio != 85 {
		t.Fatalf("clamped B landing parent=%#v first=%v, want ratio 85", parent, first)
	}
}

func TestMovePreservesPrimaryAndShellIdentityAndLiveCount(t *testing.T) {
	primary := leaf(1, Primary)
	shell := leaf(2, Shell)
	document := leaf(3, Document)
	root := split(10, Columns, 50, primary, split(9, Rows, 50, shell, document))
	wantPrimaryID, wantShellID := primary.ID, shell.ID
	wantLive := LiveLeafCount(root)

	root = applyAcceptedMove(t, root, wantPrimaryID, MoveDestination{Cell: Cell{Col: 2, Row: 2}})
	if Find(root, wantPrimaryID) != primary || primary.ID != wantPrimaryID || LiveLeafCount(root) != wantLive {
		t.Fatalf("primary identity/live count changed: ptr=%p want=%p id=%d want=%d live=%d want=%d", Find(root, wantPrimaryID), primary, primary.ID, wantPrimaryID, LiveLeafCount(root), wantLive)
	}
	root = applyAcceptedMove(t, root, wantShellID, MoveDestination{OuterEdge: BeforeFirstColumn})
	if Find(root, wantShellID) != shell || shell.ID != wantShellID || LiveLeafCount(root) != wantLive {
		t.Fatalf("shell identity/live count changed: ptr=%p want=%p id=%d want=%d live=%d want=%d", Find(root, wantShellID), shell, shell.ID, wantShellID, LiveLeafCount(root), wantLive)
	}
}

func TestApplyMoveRejectsAStaleTargetWithoutClosingTheLeaf(t *testing.T) {
	root := gridTree(2, 2, Document)
	before := Clone(root)
	leafID := GridOf(root).Cell(1, 1).ID
	got, focus := ApplyMove(root, MovePlan{
		LeafID:    leafID,
		Placement: OpenPlan{Split: 999, Axis: Rows},
	})
	if focus != leafID || !reflect.DeepEqual(got, before) {
		t.Fatalf("stale plan changed tree or focus: focus=%d tree=%#v", focus, got)
	}
}

func directionName(direction Direction) string {
	switch direction {
	case DirectionLeft:
		return "left"
	case DirectionRight:
		return "right"
	case DirectionUp:
		return "up"
	case DirectionDown:
		return "down"
	default:
		return "unknown"
	}
}

func shapeName(cols, rows int) string { return Cell{Col: cols, Row: rows}.String() }
