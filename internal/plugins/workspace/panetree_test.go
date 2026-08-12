package workspace

import "testing"

var testPaneFloors = Floors{
	Terminal: PaneFloor{Width: 10, Height: 3},
	Doc:      PaneFloor{Width: 30, Height: 3},
}

func TestLayoutPanesSingleLeafUsesWholeBox(t *testing.T) {
	root := &PaneNode{ID: 1, Kind: PaneTerminal}
	box := Box{X: 7, Y: 11, W: 80, H: 24}
	leaves, dividers, fits := LayoutPanes(root, box, testPaneFloors)
	if !fits || len(leaves) != 1 || len(dividers) != 0 {
		t.Fatalf("layout = (%+v, %+v, %v), want one leaf and no divider", leaves, dividers, fits)
	}
	if leaves[0].Node != root || leaves[0].Box != box {
		t.Fatalf("placement = %+v, want node %p in %+v", leaves[0], root, box)
	}
}

func TestLayoutPanesSplitRatiosAccountForEveryCell(t *testing.T) {
	tests := []struct {
		name    string
		axis    SplitAxis
		ratio   int
		box     Box
		wantA   Box
		wantDiv Box
		wantB   Box
	}{
		{
			name: "columns round down without losing a cell", axis: SplitCols, ratio: 33,
			box:   Box{X: 3, Y: 4, W: 102, H: 20},
			wantA: Box{X: 3, Y: 4, W: 33, H: 20}, wantDiv: Box{X: 36, Y: 4, W: 1, H: 20}, wantB: Box{X: 37, Y: 4, W: 68, H: 20},
		},
		{
			name: "rows round down without losing a cell", axis: SplitRows, ratio: 67,
			box:   Box{X: 3, Y: 4, W: 80, H: 32},
			wantA: Box{X: 3, Y: 4, W: 80, H: 21}, wantDiv: Box{X: 3, Y: 25, W: 80, H: 1}, wantB: Box{X: 3, Y: 26, W: 80, H: 10},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root := splitNode(10, tt.axis, tt.ratio,
				&PaneNode{ID: 1, Kind: PaneTerminal},
				&PaneNode{ID: 2, Kind: PaneTerminal})
			leaves, dividers, fits := LayoutPanes(root, tt.box, testPaneFloors)
			if !fits || len(leaves) != 2 || len(dividers) != 1 {
				t.Fatalf("layout = (%+v, %+v, %v)", leaves, dividers, fits)
			}
			if leaves[0].Box != tt.wantA || dividers[0].Box != tt.wantDiv || leaves[1].Box != tt.wantB {
				t.Fatalf("boxes = (%+v, %+v, %+v), want (%+v, %+v, %+v)", leaves[0].Box, dividers[0].Box, leaves[1].Box, tt.wantA, tt.wantDiv, tt.wantB)
			}
		})
	}
}

func TestLayoutPanesClampsRatioAndHonorsFloors(t *testing.T) {
	root := splitNode(10, SplitCols, -20,
		&PaneNode{ID: 1, Kind: PaneTerminal},
		&PaneNode{ID: 2, Kind: PaneDoc})
	leaves, _, fits := LayoutPanes(root, Box{W: 101, H: 10}, testPaneFloors)
	if !fits {
		t.Fatal("layout unexpectedly did not fit")
	}
	if leaves[0].Box.W != 15 || leaves[1].Box.W != 85 {
		t.Fatalf("widths = %d/%d, want clamped 15/85", leaves[0].Box.W, leaves[1].Box.W)
	}

	root.Split.Ratio = 99
	leaves, _, fits = LayoutPanes(root, Box{W: 51, H: 10}, testPaneFloors)
	if !fits {
		t.Fatal("floor-adjusted layout unexpectedly did not fit")
	}
	if leaves[0].Box.W != 20 || leaves[1].Box.W != 30 {
		t.Fatalf("widths = %d/%d, want terminal remainder 20 and doc floor 30", leaves[0].Box.W, leaves[1].Box.W)
	}
}

func TestLayoutPanesReturnsNoPartialLayoutWhenFloorsDoNotFit(t *testing.T) {
	root := splitNode(10, SplitCols, 50,
		&PaneNode{ID: 1, Kind: PaneTerminal},
		&PaneNode{ID: 2, Kind: PaneDoc})
	leaves, dividers, fits := LayoutPanes(root, Box{W: 40, H: 10}, testPaneFloors)
	if fits || leaves != nil || dividers != nil {
		t.Fatalf("layout = (%+v, %+v, %v), want no partial layout", leaves, dividers, fits)
	}
}

func TestLayoutPanesNestedSplitsPreserveEveryCell(t *testing.T) {
	right := splitNode(11, SplitRows, 50,
		&PaneNode{ID: 2, Kind: PaneDoc},
		&PaneNode{ID: 3, Kind: PaneTerminal})
	root := splitNode(10, SplitCols, 50, &PaneNode{ID: 1, Kind: PaneTerminal}, right)
	box := Box{X: 5, Y: 7, W: 81, H: 25}
	leaves, dividers, fits := LayoutPanes(root, box, testPaneFloors)
	if !fits || len(leaves) != 3 || len(dividers) != 2 {
		t.Fatalf("layout = (%+v, %+v, %v)", leaves, dividers, fits)
	}

	want := []Box{
		{X: 5, Y: 7, W: 40, H: 25},
		{X: 46, Y: 7, W: 40, H: 12},
		{X: 46, Y: 20, W: 40, H: 12},
	}
	for i := range want {
		if leaves[i].Box != want[i] {
			t.Fatalf("leaf %d box = %+v, want %+v", i, leaves[i].Box, want[i])
		}
	}
	if got := leaves[0].Box.W + dividers[0].Box.W + leaves[1].Box.W; got != box.W {
		t.Fatalf("top row accounts for %d columns, want %d", got, box.W)
	}
	if got := leaves[1].Box.H + dividers[1].Box.H + leaves[2].Box.H; got != box.H {
		t.Fatalf("right column accounts for %d rows, want %d", got, box.H)
	}
}

func TestPaneTreeMutatorsPreserveLeafIDsAndCollapseOnClose(t *testing.T) {
	root := &PaneNode{ID: 1, Kind: PaneTerminal}
	newLeaf := &PaneNode{ID: 2, Kind: PaneDoc, DocID: 8}
	root, focus := SplitLeaf(root, 1, SplitCols, newLeaf)
	if focus != 2 || root.Split == nil {
		t.Fatalf("split focus/root = %d/%+v", focus, root)
	}
	if root.ID == 1 || root.Split.A.ID != 1 || root.Split.B.ID != 2 {
		t.Fatalf("split did not preserve leaf IDs: %+v", root)
	}
	if FindPane(root, 1) == nil || FindPane(root, 2) != newLeaf || FindPane(root, root.ID) != root {
		t.Fatal("FindPane did not find stable leaf and split IDs")
	}

	root, focus = ClosePane(root, 2)
	if focus != 1 || root.Split != nil || root.ID != 1 || root.Kind != PaneTerminal {
		t.Fatalf("close result = focus %d, root %+v; want terminal leaf 1", focus, root)
	}
}

func TestClosePaneFocusesFirstLeafOfSiblingSubtree(t *testing.T) {
	sibling := splitNode(11, SplitRows, 50,
		&PaneNode{ID: 2, Kind: PaneDoc},
		&PaneNode{ID: 3, Kind: PaneTerminal})
	root := splitNode(10, SplitCols, 50, &PaneNode{ID: 1, Kind: PaneTerminal}, sibling)
	root, focus := ClosePane(root, 1)
	if focus != 2 || root.ID != 11 || root.Split == nil {
		t.Fatalf("close result = focus %d, root %+v; want sibling subtree focused at leaf 2", focus, root)
	}
}

func TestSplitLeafAllocatesUniqueIDsAndSetRatioClamps(t *testing.T) {
	root := &PaneNode{ID: 4, Kind: PaneTerminal}
	newLeaf := &PaneNode{ID: 4, Kind: PaneDoc}
	root, focus := SplitLeaf(root, 4, SplitRows, newLeaf)
	if focus == 4 || newLeaf.ID != focus || root.ID == focus || root.ID == 4 {
		t.Fatalf("IDs after split = root %d, old 4, new %d", root.ID, focus)
	}
	if !SetRatio(root, root.ID, 100) || root.Split.Ratio != paneMaxRatio {
		t.Fatalf("ratio = %d, want %d", root.Split.Ratio, paneMaxRatio)
	}
	if !SetRatio(root, root.ID, 0) || root.Split.Ratio != paneMinRatio {
		t.Fatalf("ratio = %d, want %d", root.Split.Ratio, paneMinRatio)
	}
	if SetRatio(root, 4, 50) {
		t.Fatal("SetRatio accepted a leaf ID")
	}
}

func splitNode(id int, axis SplitAxis, ratio int, a, b *PaneNode) *PaneNode {
	return &PaneNode{ID: id, Split: &PaneSplit{Axis: axis, Ratio: ratio, A: a, B: b}}
}
