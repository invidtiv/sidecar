package panelayout

import "testing"

func planFloors() Floors {
	return Floors{
		Terminal: Floor{Width: 10, Height: 3},
		Doc:      Floor{Width: 10, Height: 3},
		Issue:    Floor{Width: 10, Height: 3},
		Diff:     Floor{Width: 10, Height: 3},
	}
}

func TestPrimaryKeepsTerminalPersistedValueAndFloorCompatibility(t *testing.T) {
	if Primary != Terminal || int(Primary) != 0 {
		t.Fatalf("Primary=%d Terminal=%d, want compatible persisted value 0", Primary, Terminal)
	}
	root := &Node{ID: 1, Kind: Primary}
	for name, floors := range map[string]Floors{
		"primary":  {Primary: Floor{Width: 12, Height: 4}},
		"terminal": {Terminal: Floor{Width: 12, Height: 4}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, _, ok := LayoutPanes(root, Box{W: 11, H: 4}, floors); ok {
				t.Fatal("layout ignored primary floor")
			}
			if _, _, ok := LayoutPanes(root, Box{W: 12, H: 4}, floors); !ok {
				t.Fatal("layout refused exact primary floor")
			}
		})
	}
}

func TestPlanOpen(t *testing.T) {
	stacked := terminalDocIssue()
	tests := []struct {
		name  string
		root  *Node
		kind  Kind
		boxes map[int]Box
		want  OpenPlan
		ok    bool
	}{
		{
			name: "refuse a terminal kind",
			root: terminalOnly(),
			kind: Terminal,
		},
		{
			name: "refuse a nil tree",
			kind: Document,
		},
		{
			name: "first content splits the terminal into columns",
			root: terminalOnly(),
			kind: Document,
			want: OpenPlan{Split: 1, Axis: Columns},
			ok:   true,
		},
		{
			name: "one content leaf is stacked",
			root: terminalDoc(),
			kind: Issue,
			want: OpenPlan{Split: 2, Axis: Rows},
			ok:   true,
		},
		{
			name: "repeated kind retargets",
			root: stacked,
			kind: Document,
			want: OpenPlan{Retarget: 2},
			ok:   true,
		},
		{
			// Two content panes put the right column ahead of the left, so the
			// grid rule splits the primary column and the fourth pane forms a
			// 2×2. Boxes do not choose any more: the emptiest column does.
			name: "a third kind splits the primary column into a 2x2",
			root: stacked,
			kind: Diff,
			want: OpenPlan{Split: 1, Axis: Rows},
			ok:   true,
		},
		{
			name: "boxes cannot talk the grid rule out of the emptiest column",
			root: stacked,
			kind: Diff,
			boxes: map[int]Box{
				2: {W: 60, H: 14},
				4: {W: 60, H: 6},
			},
			want: OpenPlan{Split: 1, Axis: Rows},
			ok:   true,
		},
		{
			name: "nor the other way round",
			root: stacked,
			kind: Diff,
			boxes: map[int]Box{
				2: {W: 60, H: 6},
				4: {W: 60, H: 14},
			},
			want: OpenPlan{Split: 1, Axis: Rows},
			ok:   true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := PlanOpen(tc.root, tc.kind, tc.boxes)
			if ok != tc.ok || got != tc.want {
				t.Fatalf("PlanOpen = %#v ok=%v, want %#v ok=%v", got, ok, tc.want, tc.ok)
			}
		})
	}
}

func TestApplyAxisOverride(t *testing.T) {
	split := OpenPlan{Split: 2, Axis: Rows}
	if got := ApplyAxisOverride(split, "right"); got.Axis != Columns || got.Split != 2 {
		t.Fatalf("right = %#v", got)
	}
	if got := ApplyAxisOverride(split, "below"); got.Axis != Rows || got.Split != 2 {
		t.Fatalf("below = %#v", got)
	}
	if got := ApplyAxisOverride(split, "auto"); got != split {
		t.Fatalf("auto = %#v", got)
	}
	if got := ApplyAxisOverride(split, ""); got != split {
		t.Fatalf("empty = %#v", got)
	}
	retarget := OpenPlan{Retarget: 3, Axis: Rows}
	if got := ApplyAxisOverride(retarget, "right"); got != retarget {
		t.Fatalf("retarget ignored --split: %#v", got)
	}
}

// The terminal-splits plan's B1 rule: once the right column holds two content
// panes, the next open splits the primary column instead of stacking a third
// row, and the four panes land as a 2×2 — two full-height columns of two.
func TestPlanOpenThirdContentFormsATwoByTwoGrid(t *testing.T) {
	root := terminalDocIssue()
	box := Box{W: 120, H: 40}
	leaves, _, fits := LayoutPanes(root, box, planFloors())
	if !fits {
		t.Fatal("premise: File+Issue tree must fit")
	}
	boxes := make(map[int]Box, len(leaves))
	for _, leaf := range leaves {
		boxes[leaf.Node.ID] = leaf.Box
	}

	plan, ok := PlanOpen(root, Diff, boxes)
	if !ok || plan != (OpenPlan{Split: 1, Axis: Rows}) {
		t.Fatalf("PlanOpen = %#v ok=%v, want a split of the primary column (leaf 1)", plan, ok)
	}

	root, focus := SplitLeaf(root, plan.Split, plan.Axis, &Node{ID: 6, Kind: Diff})
	if focus != 6 {
		t.Fatalf("SplitLeaf focus = %d, want the new Diff leaf", focus)
	}

	grid := GridOf(root)
	if grid == nil {
		t.Fatalf("the 2x2 tree escaped the grid vocabulary: %#v", root)
	}
	wantColumns := [][]int{{1, 6}, {2, 4}}
	if grid.ColumnCount() != len(wantColumns) {
		t.Fatalf("grid has %d columns, want %d (%v)", grid.ColumnCount(), len(wantColumns), wantColumns)
	}
	for col, wantIDs := range wantColumns {
		cells := grid.Columns[col].Cells
		if len(cells) != len(wantIDs) {
			t.Fatalf("column %d holds %d cells, want %v", col+1, len(cells), wantIDs)
		}
		for row, wantID := range wantIDs {
			if cells[row].ID != wantID {
				t.Fatalf("cell %d.%d = leaf %d, want %d", col+1, row+1, cells[row].ID, wantID)
			}
		}
	}

	leaves, _, fits = LayoutPanes(root, box, planFloors())
	if !fits || len(leaves) != 4 {
		t.Fatalf("File+Issue+Diff layout leaves=%d fits=%v, want four", len(leaves), fits)
	}
	var terminal, diff Box
	for _, placement := range leaves {
		switch placement.Node.Kind {
		case Terminal:
			terminal = placement.Box
		case Diff:
			diff = placement.Box
		}
	}
	// Both columns span the full height; the primary terminal now shares its
	// column with the new pane instead of keeping it all to itself.
	if terminal.Y != box.Y || diff.Y != terminal.Y+terminal.H+1 {
		t.Fatalf("the new pane is not stacked below the terminal: terminal=%#v diff=%#v box=%#v", terminal, diff, box)
	}
	if terminal.W+1 >= box.W {
		t.Fatalf("terminal kept the whole width: %#v in %#v", terminal, box)
	}
}
