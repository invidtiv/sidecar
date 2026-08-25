package panelayout

import "testing"

// gridOfIDs projects a tree to bare leaf IDs per column, the shape the
// projection round-trip assertions compare against.
func gridOfIDs(root *Node) [][]int {
	grid := GridOf(root)
	if grid == nil {
		return nil
	}
	ids := make([][]int, len(grid.Columns))
	for i, column := range grid.Columns {
		ids[i] = make([]int, len(column.Cells))
		for j, cell := range column.Cells {
			ids[i][j] = cell.ID
		}
	}
	return ids
}

func idsEqual(a, b [][]int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if len(a[i]) != len(b[i]) {
			return false
		}
		for j := range a[i] {
			if a[i][j] != b[i][j] {
				return false
			}
		}
	}
	return true
}

// gridTree builds an exact columns-of-rows shape with sequential IDs for the
// states no run of real opens can reach (the deck keeps passive kinds to one
// leaf each, so the deep grid fills are constructed, not walked).
func gridTree(cols, rows int, kind Kind) *Node {
	nextID := 1
	var root *Node
	for c := 0; c < cols; c++ {
		column := leaf(nextID, kind)
		nextID++
		for r := 1; r < rows; r++ {
			column = split(nextID, Rows, 50, column, leaf(nextID+1, kind))
			nextID += 2
		}
		if root == nil {
			root = column
			continue
		}
		root = split(nextID, Columns, 50, root, column)
		nextID++
	}
	return root
}

// The projection flattens same-axis nesting and refuses everything the
// vocabulary has no name for.
func TestGridOfProjectsColumnsOfRows(t *testing.T) {
	tests := []struct {
		name string
		root *Node
		want [][]int // nil means GridOf returns nil
	}{
		{
			name: "nil tree",
			root: nil,
			want: nil,
		},
		{
			name: "a lone leaf is one column of one",
			root: terminalOnly(),
			want: [][]int{{1}},
		},
		{
			name: "terminal beside a document",
			root: terminalDoc(),
			want: [][]int{{1}, {2}},
		},
		{
			name: "the everyday stacked shape",
			root: terminalDocIssue(),
			want: [][]int{{1}, {2, 4}},
		},
		{
			name: "a three-pane column is Rows splits chained inside Rows",
			root: split(9, Rows, 50,
				split(5, Rows, 50, leaf(2, Document), leaf(3, Issue)),
				leaf(4, Diff),
			),
			want: [][]int{{2, 3, 4}},
		},
		{
			name: "columns chain inside columns too",
			root: split(9, Columns, 50,
				split(7, Columns, 50, leaf(1, Primary), leaf(2, Shell)),
				leaf(3, Document),
			),
			want: [][]int{{1}, {2}, {3}},
		},
		{
			name: "row stacks on both sides of the root columns split",
			root: split(9, Columns, 50,
				split(5, Rows, 50, leaf(1, Primary), leaf(2, Document)),
				split(6, Rows, 50, leaf(3, Issue), leaf(4, Diff)),
			),
			want: [][]int{{1, 2}, {3, 4}},
		},
		{
			name: "a columns split nested inside a row stack escapes",
			root: split(9, Columns, 50,
				leaf(1, Primary),
				split(5, Rows, 50,
					leaf(2, Document),
					split(6, Columns, 50, leaf(3, Issue), leaf(4, Diff)),
				),
			),
			want: nil,
		},
		{
			name: "columns above rows below the seam escapes",
			root: split(9, Rows, 50,
				split(7, Columns, 50, leaf(1, Terminal), leaf(2, Document)),
				split(8, Columns, 50, leaf(3, Issue), leaf(4, Document)),
			),
			want: nil,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := gridOfIDs(tc.root)
			if tc.want == nil {
				if got != nil {
					t.Fatalf("GridOf = %v, want nil", got)
				}
				return
			}
			if !idsEqual(got, tc.want) {
				t.Fatalf("GridOf = %v, want %v", got, tc.want)
			}
		})
	}
}

// The caps are constants a caller can name and predicates it can ask before
// attempting an open, exactly as LiveLeafCap is.
func TestGridCapsAreConstantsWithPredicates(t *testing.T) {
	if MaxGridColumns != 4 || MaxGridRows != 4 {
		t.Fatalf("caps = %d x %d, want 4x4", MaxGridColumns, MaxGridRows)
	}
	fullColumn := gridTree(1, 4, Document)
	if GridColumnsAtCap(fullColumn) {
		t.Fatal("one full column did not leave room for another")
	}
	if !GridRowAtCap(fullColumn, 1) {
		t.Fatal("a four-pane column read as under the row cap")
	}
	fourColumns := gridTree(4, 2, Document)
	if !GridColumnsAtCap(fourColumns) {
		t.Fatal("four columns read as under the column cap")
	}
	if GridRowAtCap(fourColumns, 3) {
		t.Fatal("a two-row column read as at the row cap")
	}
	if GridColumnsAtCap(nil) || GridRowAtCap(nil, 1) {
		t.Fatal("a nil tree tripped a cap predicate")
	}
	if GridRowAtCap(fourColumns, 0) || GridRowAtCap(fourColumns, 5) {
		t.Fatal("an out-of-range column index tripped the row cap predicate")
	}
	if GridColumnCapMessage == "" || GridRowCapMessage == "" || LiveCapMessage == "" {
		t.Fatal("cap refusals must say why")
	}
}

// The auto rules walked pane by pane. Each step plans against the previous
// step's tree, so this is the real progression a run of opens produces —
// beside, stack, the 2×2 (terminal-splits B1), then emptiest-column fills.
func TestPlanOpenAutoWalksTheEmptiestColumn(t *testing.T) {
	next := map[Kind]Kind{Document: Issue, Issue: Diff, Diff: Note, Note: Resource}
	root := leaf(1, Primary)
	kind := Document

	steps := []struct {
		want OpenPlan
		cols [][]int
	}{
		{OpenPlan{Split: 1, Axis: Columns}, [][]int{{1}, {2}}},
		{OpenPlan{Split: 2, Axis: Rows}, [][]int{{1}, {2, 4}}},
		// The right column holds two content panes: the primary column
		// splits and the fourth pane completes the 2×2.
		{OpenPlan{Split: 1, Axis: Rows}, [][]int{{1, 6}, {2, 4}}},
		// Beyond four the emptiest column wins; ties go to the leftmost.
		// Column 1 is an internal subtree now, and it splits fine.
		{OpenPlan{Split: 7, Axis: Rows}, [][]int{{1, 6, 8}, {2, 4}}},
		{OpenPlan{Split: 5, Axis: Rows}, [][]int{{1, 6, 8}, {2, 4, 10}}},
	}

	for i, step := range steps {
		plan, ok := PlanOpen(root, kind, nil)
		if !ok || plan != step.want {
			t.Fatalf("open %d = %#v ok=%v, want %#v (%v)", i+1, plan, ok, step.want, gridOfIDs(root))
		}
		var focus int
		root, focus = ApplyPlan(root, plan, &Node{Kind: kind})
		if focus <= 0 || Find(root, focus) == nil {
			t.Fatalf("open %d applied to no leaf: focus=%d", i+1, focus)
		}
		if got := gridOfIDs(root); !idsEqual(got, step.cols) {
			t.Fatalf("open %d produced %v, want %v", i+1, got, step.cols)
		}
		kind = next[kind]
	}

	// Deeper states hand-built from one kind, planned with an absent kind:
	// uneven columns pick the emptiest, full columns give way to a new one,
	// and the 4×4 cap refuses.
	plan, ok := PlanOpen(gridTree(2, 4, Document), Issue, nil)
	if !ok || plan.Axis != Columns || plan.Split != GridOf(gridTree(2, 4, Document)).Root().ID {
		t.Fatalf("two full columns planned %#v ok=%v, want the root split into a new column", plan, ok)
	}

	uneven := split(MaxID(gridTree(1, 4, Document))+1, Columns, 50,
		gridTree(1, 4, Document),
		split(MaxID(gridTree(1, 4, Document))+2, Rows, 50,
			leaf(MaxID(gridTree(1, 4, Document))+3, Shell),
			leaf(MaxID(gridTree(1, 4, Document))+4, Shell),
		),
	)
	plan, ok = PlanOpen(uneven, Issue, nil)
	if !ok || plan.Axis != Rows || plan.Split != GridOf(uneven).Columns[1].Node.ID {
		t.Fatalf("a four-row column beside a two-row one planned %#v, want the shorter column filled", plan)
	}

	full := gridTree(4, 4, Document)
	if plan, ok := PlanOpen(full, Issue, nil); ok {
		t.Fatalf("the 4x4-full grid planned %#v (%v), want a refusal", plan, gridOfIDs(full))
	}
}

// A tree that escapes the grid vocabulary still gets the legacy largest-leaf
// answer, so restores of odd shapes keep opening panes instead of refusing.
func TestPlanOpenFallsBackToLargestLeafOffTheGrid(t *testing.T) {
	root := split(9, Rows, 50,
		split(7, Columns, 50, leaf(1, Primary), leaf(2, Document)),
		leaf(3, Issue),
	)
	if gridOfIDs(root) != nil {
		t.Fatalf("premise: %v should not project", gridOfIDs(root))
	}
	plan, ok := PlanOpen(root, Diff, nil)
	if !ok || plan.Split == 0 || plan.NewFirst {
		t.Fatalf("PlanOpen off-grid = %#v ok=%v, want a legacy split plan", plan, ok)
	}
}

// Explicit cells resolve against the current grid: occupied inserts push down,
// one-past-the-end appends, further addresses refuse.
func TestPlanOpenAtResolvesEveryCellClass(t *testing.T) {
	// terminal | doc / issue  →  [[1],[2,4]]
	root := terminalDocIssue()

	tests := []struct {
		name    string
		cell    Cell
		want    OpenPlan
		reason  string
		project [][]int // projection after applying the plan; nil skips
	}{
		{
			name:    "occupied top cell takes the new pane and pushes the stack down",
			cell:    Cell{Col: 2, Row: 1},
			want:    OpenPlan{Split: 2, Axis: Rows, NewFirst: true},
			project: [][]int{{1}, {99, 2, 4}},
		},
		{
			name:    "occupied mid cell splits only what is below it",
			cell:    Cell{Col: 2, Row: 2},
			want:    OpenPlan{Split: 4, Axis: Rows, NewFirst: true},
			project: [][]int{{1}, {2, 99, 4}},
		},
		{
			name:    "one past the end of a column appends at its bottom",
			cell:    Cell{Col: 2, Row: 3},
			want:    OpenPlan{Split: 5, Axis: Rows},
			project: [][]int{{1}, {2, 4, 99}},
		},
		{
			name:    "one past the last column starts a new one",
			cell:    Cell{Col: 3, Row: 1},
			want:    OpenPlan{Split: 3, Axis: Columns},
			project: [][]int{{1}, {2, 4}, {99}},
		},
		{
			name:   "one past the last column needs row 1",
			cell:   Cell{Col: 3, Row: 2},
			reason: "column 3 is out of range; the layout has 2",
		},
		{
			name:   "further out of range is refused with the size",
			cell:   Cell{Col: 4, Row: 1},
			reason: "column 4 is out of range; the layout has 2",
		},
		{
			name:   "a row past one-past-the-end is refused",
			cell:   Cell{Col: 2, Row: 4},
			reason: "cell 2.4 is out of range; column 2 holds 2",
		},
		{
			name:   "zero cells are outside the vocabulary",
			cell:   Cell{Col: 0, Row: 1},
			reason: "cell 0.1 is outside the 4x4 layout grid",
		},
		{
			name:   "cells beyond the caps are outside the vocabulary",
			cell:   Cell{Col: 5, Row: 1},
			reason: "cell 5.1 is outside the 4x4 layout grid",
		},
		{
			name:   "row 5 is never addressable",
			cell:   Cell{Col: 1, Row: 5},
			reason: "cell 1.5 is outside the 4x4 layout grid",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			plan, reason := PlanOpenAt(root, Note, 0, tc.cell)
			if tc.reason != "" {
				if reason != tc.reason {
					t.Fatalf("PlanOpenAt(%d.%d) reason = %q, want %q", tc.cell.Col, tc.cell.Row, reason, tc.reason)
				}
				return
			}
			if reason != "" || plan != tc.want {
				t.Fatalf("PlanOpenAt(%d.%d) = %#v reason=%q, want %#v", tc.cell.Col, tc.cell.Row, plan, reason, tc.want)
			}
			applied, focus := ApplyPlan(Clone(root), plan, &Node{ID: 99})
			if focus != 99 {
				t.Fatalf("applied plan focused leaf %d, want the new pane", focus)
			}
			if got := gridOfIDs(applied); !idsEqual(got, tc.project) {
				t.Fatalf("applied plan projected %v, want %v", got, tc.project)
			}
		})
	}
}

// --at expresses a requirement, so a kind that would retarget is an error
// rather than a quiet move, and the live cap still binds shell cells.
func TestPlanOpenAtRefusesRetargetsAndPastTheLiveCap(t *testing.T) {
	stacked := terminalDocIssue()
	FirstOfKind(stacked, Document).ContentID = 7

	if _, reason := PlanOpenAt(stacked, Document, 7, Cell{Col: 2, Row: 3}); reason == "" {
		t.Fatal("same-content document planned a cell, want a retarget refusal")
	}
	if _, reason := PlanOpenAt(stacked, Issue, 0, Cell{Col: 2, Row: 3}); reason == "" {
		t.Fatal("second issue kind planned a cell, want a retarget refusal")
	}

	shellFull := terminalShell()
	if _, reason := PlanOpenAt(shellFull, Shell, 9, Cell{Col: 2, Row: 2}); reason != LiveCapMessage {
		t.Fatalf("shell cell past the live cap reason = %q, want %q", reason, LiveCapMessage)
	}
	plan, reason := PlanOpenAt(shellFull, Document, 0, Cell{Col: 2, Row: 2})
	if reason != "" || plan != (OpenPlan{Split: 2, Axis: Rows}) {
		t.Fatalf("document cell beside a full live pair = %#v %q, want a split of the shell leaf", plan, reason)
	}

	if _, reason := PlanOpenAt(stacked, Primary, 0, Cell{Col: 2, Row: 1}); reason == "" {
		t.Fatal("primary planned a cell, want a refusal")
	}
	if _, reason := PlanOpenAt(nil, Document, 0, Cell{Col: 1, Row: 1}); reason == "" {
		t.Fatal("a nil tree planned a cell, want an escape refusal")
	}
	offGrid := split(9, Rows, 50,
		split(7, Columns, 50, leaf(1, Primary), leaf(2, Document)),
		leaf(3, Issue),
	)
	if _, reason := PlanOpenAt(offGrid, Note, 0, Cell{Col: 1, Row: 2}); reason == "" {
		t.Fatal("an off-grid tree planned a cell, want an escape refusal")
	}
}

// Occupied-cell inserts overflow a full column no matter which occupied row
// was named; one-past-the-end rows cannot reach a full column because row 5
// is already outside the vocabulary.
func TestPlanOpenAtRefusesOverflowPastTheRowCap(t *testing.T) {
	fullColumn := gridTree(1, 4, Document)
	for _, row := range []int{1, 2, 3, 4} {
		_, reason := PlanOpenAt(fullColumn, Note, 0, Cell{Col: 1, Row: row})
		if reason != GridRowCapMessage {
			t.Fatalf("insert at 1.%d reason = %q, want %q", row, reason, GridRowCapMessage)
		}
	}
}

// Splitting internal nodes is the primitive the column appends stand on: the
// divided subtree keeps its place and identity, and refusals leave the tree
// untouched.
func TestSplitLeafSplitsInternalNodes(t *testing.T) {
	root := terminalDocIssue()
	column := GridOf(root).Columns[1].Node
	if column.Split == nil {
		t.Fatal("premise: the right column is an internal node")
	}
	root, focus := SplitLeaf(root, column.ID, Rows, &Node{ID: 6, Kind: Diff})
	if focus != 6 {
		t.Fatalf("focus = %d, want the new leaf", focus)
	}
	if got, want := gridOfIDs(root), [][]int{{1}, {2, 4, 6}}; !idsEqual(got, want) {
		t.Fatalf("column append produced %v, want %v", got, want)
	}
	if Find(root, MaxID(root)) == nil {
		t.Fatal("the divided column subtree lost its split node")
	}

	// The root itself splits into a new trailing column.
	root, focus = SplitLeaf(root, root.ID, Columns, &Node{ID: 20, Kind: Note})
	if focus != 20 {
		t.Fatalf("root split focus = %d, want the new leaf", focus)
	}
	if got, want := gridOfIDs(root), [][]int{{1}, {2, 4, 6}, {20}}; !idsEqual(got, want) {
		t.Fatalf("root append produced %v, want %v", got, want)
	}

	gotRoot, focus := SplitLeaf(root, 999, Rows, &Node{ID: 8, Kind: Note})
	if focus != 999 || !idsEqual(gridOfIDs(gotRoot), gridOfIDs(root)) {
		t.Fatal("an unknown target mutated the tree")
	}
}

func TestApplyPlanHonorsNewFirstOrdering(t *testing.T) {
	root := terminalDocIssue()

	applied, focus := ApplyPlan(Clone(root), OpenPlan{Split: 2, Axis: Rows, NewFirst: true}, &Node{ID: 9, Kind: Diff})
	if focus != 9 {
		t.Fatalf("focus = %d, want the inserted leaf", focus)
	}
	if got, want := gridOfIDs(applied), [][]int{{1}, {9, 2, 4}}; !idsEqual(got, want) {
		t.Fatalf("NewFirst insert produced %v, want %v", got, want)
	}

	applied, focus = ApplyPlan(Clone(root), OpenPlan{Split: 2, Axis: Rows}, &Node{ID: 9, Kind: Diff})
	if focus != 9 {
		t.Fatalf("focus = %d, want the appended leaf", focus)
	}
	// Without NewFirst the new leaf goes BELOW the divided node: splitting a
	// stack's top leaf lands mid-column, which is why one-past-the-end rows
	// plan against the column's subtree instead.
	if got, want := gridOfIDs(applied), [][]int{{1}, {2, 9, 4}}; !idsEqual(got, want) {
		t.Fatalf("plain insert produced %v, want %v", got, want)
	}
}
