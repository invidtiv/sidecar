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
			name: "nil boxes on two content leaves split DFS-A",
			root: stacked,
			kind: Diff,
			want: OpenPlan{Split: 2, Axis: Rows},
			ok:   true,
		},
		{
			name: "equal areas split DFS-A",
			root: stacked,
			kind: Diff,
			boxes: map[int]Box{
				1: {W: 60, H: 20},
				2: {W: 60, H: 10},
				4: {W: 60, H: 10},
			},
			want: OpenPlan{Split: 2, Axis: Rows},
			ok:   true,
		},
		{
			name: "larger document wins",
			root: stacked,
			kind: Diff,
			boxes: map[int]Box{
				2: {W: 60, H: 14},
				4: {W: 60, H: 6},
			},
			want: OpenPlan{Split: 2, Axis: Rows},
			ok:   true,
		},
		{
			name: "larger issue wins",
			root: stacked,
			kind: Diff,
			boxes: map[int]Box{
				2: {W: 60, H: 6},
				4: {W: 60, H: 14},
			},
			want: OpenPlan{Split: 4, Axis: Rows},
			ok:   true,
		},
		{
			name: "missing boxes fall back to DFS-A",
			root: stacked,
			kind: Diff,
			boxes: map[int]Box{
				1: {W: 60, H: 20},
			},
			want: OpenPlan{Split: 2, Axis: Rows},
			ok:   true,
		},
		{
			name: "terminal box is never chosen",
			root: stacked,
			kind: Diff,
			boxes: map[int]Box{
				1: {W: 200, H: 200},
				2: {W: 10, H: 10},
				4: {W: 10, H: 10},
			},
			want: OpenPlan{Split: 2, Axis: Rows},
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

func TestPlanOpenThirdContentKeepsTerminalFullHeight(t *testing.T) {
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
	if !ok || plan.Retarget != 0 || plan.Axis != Rows || plan.Split == 1 {
		t.Fatalf("PlanOpen = %#v ok=%v, want a content-leaf row split", plan, ok)
	}

	root, focus := SplitLeaf(root, plan.Split, plan.Axis, &Node{ID: 6, Kind: Diff})
	if focus != 6 {
		t.Fatalf("SplitLeaf focus = %d, want the new Diff leaf", focus)
	}

	leaves, _, fits = LayoutPanes(root, box, planFloors())
	if !fits || len(leaves) != 4 {
		t.Fatalf("File+Issue+Diff layout leaves=%d fits=%v, want four", len(leaves), fits)
	}
	var terminal Box
	for _, leaf := range leaves {
		if leaf.Node.Kind == Terminal {
			terminal = leaf.Box
		}
	}
	if terminal.H != box.H || terminal.Y != box.Y {
		t.Fatalf("terminal box %#v, want the left column at the full height of %#v", terminal, box)
	}
}
