package panelayout

import "testing"

// leaf builds a bare leaf node.
func leaf(id int, kind Kind) *Node { return &Node{ID: id, Kind: kind} }

func split(id int, axis Axis, ratio int, a, b *Node) *Node {
	return &Node{ID: id, Split: &Split{Axis: axis, Ratio: ratio, A: a, B: b}}
}

// terminalOnly, terminalDoc and terminalDocIssue mirror the shapes the
// workspace surfaces build: a full-height terminal column, with content stacked
// in the right column as it is opened.
func terminalOnly() *Node { return leaf(1, Terminal) }

func terminalDoc() *Node {
	return split(3, Columns, 50, leaf(1, Terminal), leaf(2, Document))
}

func terminalDocIssue() *Node {
	return split(3, Columns, 50,
		leaf(1, Terminal),
		split(5, Rows, 50, leaf(2, Document), leaf(4, Issue)),
	)
}

func terminalDocIssueShell() *Node {
	return split(7, Columns, 50, terminalDocIssue(), leaf(6, Shell))
}

func TestRingLeafOrderMatchesLayoutPlacement(t *testing.T) {
	tests := []struct {
		name string
		root *Node
	}{
		{name: "single terminal", root: terminalOnly()},
		{name: "terminal beside doc", root: terminalDoc()},
		{name: "terminal beside stacked doc and issue", root: terminalDocIssue()},
		{name: "deep left nesting", root: split(9, Rows, 50,
			split(7, Columns, 50, leaf(1, Terminal), leaf(2, Document)),
			split(8, Columns, 50, leaf(3, Issue), leaf(4, Document)),
		)},
	}
	box := Box{X: 0, Y: 0, W: 120, H: 40}
	floors := Floors{Terminal: Floor{Width: 10, Height: 3}, Doc: Floor{Width: 10, Height: 3}, Issue: Floor{Width: 10, Height: 3}}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			placements, _, fits := LayoutPanes(test.root, box, floors)
			if !fits {
				t.Fatalf("layout did not fit; fixture is unusable")
			}
			want := make([]Target, 0, len(placements))
			for _, placement := range placements {
				want = append(want, Target{Kind: TargetLeaf, Leaf: placement.Node.ID})
			}
			got := Ring(test.root, false)
			if len(got) != len(want) {
				t.Fatalf("ring = %v, want placement order %v", got, want)
			}
			for i := range want {
				if got[i] != want[i] {
					t.Fatalf("ring = %v, want placement order %v", got, want)
				}
			}
		})
	}
}

func TestRingVisibility(t *testing.T) {
	tests := []struct {
		name    string
		root    *Node
		sidebar bool
		want    []Target
	}{
		{
			name: "leaves only",
			root: terminalDocIssue(),
			want: []Target{{Kind: TargetLeaf, Leaf: 1}, {Kind: TargetLeaf, Leaf: 2}, {Kind: TargetLeaf, Leaf: 4}},
		},
		{
			name:    "sidebar first",
			root:    terminalDoc(),
			sidebar: true,
			want:    []Target{{Kind: TargetSidebar}, {Kind: TargetLeaf, Leaf: 1}, {Kind: TargetLeaf, Leaf: 2}},
		},
		{
			name: "shell leaf last",
			root: split(3, Columns, 50, terminalOnly(), leaf(2, Shell)),
			want: []Target{{Kind: TargetLeaf, Leaf: 1}, {Kind: TargetLeaf, Leaf: 2}},
		},
		{
			name:    "sidebar leaves panel",
			root:    terminalDocIssueShell(),
			sidebar: true,
			want: []Target{
				{Kind: TargetSidebar},
				{Kind: TargetLeaf, Leaf: 1},
				{Kind: TargetLeaf, Leaf: 2},
				{Kind: TargetLeaf, Leaf: 4},
				{Kind: TargetLeaf, Leaf: 6},
			},
		},
		{
			name:    "nil tree keeps sidebar",
			root:    nil,
			sidebar: true,
			want:    []Target{{Kind: TargetSidebar}},
		},
		{
			name: "nil tree with nothing visible is empty",
			root: nil,
			want: nil,
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			got := Ring(test.root, test.sidebar)
			if len(got) != len(test.want) {
				t.Fatalf("Ring = %v, want %v", got, test.want)
			}
			for i := range test.want {
				if got[i] != test.want[i] {
					t.Fatalf("Ring = %v, want %v", got, test.want)
				}
			}
		})
	}
}

func TestCycleTarget(t *testing.T) {
	full := Ring(terminalDocIssueShell(), true)
	tests := []struct {
		name    string
		ring    []Target
		current Target
		reverse bool
		want    Target
	}{
		{name: "sidebar to first leaf", ring: full, current: Target{Kind: TargetSidebar}, want: Target{Kind: TargetLeaf, Leaf: 1}},
		{name: "leaf to leaf", ring: full, current: Target{Kind: TargetLeaf, Leaf: 1}, want: Target{Kind: TargetLeaf, Leaf: 2}},
		{name: "last passive leaf to shell", ring: full, current: Target{Kind: TargetLeaf, Leaf: 4}, want: Target{Kind: TargetLeaf, Leaf: 6}},
		{name: "shell wraps to sidebar", ring: full, current: Target{Kind: TargetLeaf, Leaf: 6}, want: Target{Kind: TargetSidebar}},
		{name: "reverse sidebar wraps to shell", ring: full, current: Target{Kind: TargetSidebar}, reverse: true, want: Target{Kind: TargetLeaf, Leaf: 6}},
		{name: "reverse shell to last passive leaf", ring: full, current: Target{Kind: TargetLeaf, Leaf: 6}, reverse: true, want: Target{Kind: TargetLeaf, Leaf: 4}},
		{name: "reverse first leaf to sidebar", ring: full, current: Target{Kind: TargetLeaf, Leaf: 1}, reverse: true, want: Target{Kind: TargetSidebar}},
		{name: "unknown current starts at first", ring: full, current: Target{Kind: TargetLeaf, Leaf: 99}, want: Target{Kind: TargetSidebar}},
		{name: "unknown current reverse starts at last", ring: full, current: Target{Kind: TargetLeaf, Leaf: 99}, reverse: true, want: Target{Kind: TargetLeaf, Leaf: 6}},
		{name: "single entry ring stays put", ring: []Target{{Kind: TargetSidebar}}, current: Target{Kind: TargetSidebar}, want: Target{Kind: TargetSidebar}},
		{name: "empty ring returns current", ring: nil, current: Target{Kind: TargetLeaf, Leaf: 2}, want: Target{Kind: TargetLeaf, Leaf: 2}},
		{name: "empty ring reverse returns current", ring: nil, current: Target{Kind: TargetLeaf, Leaf: 6}, reverse: true, want: Target{Kind: TargetLeaf, Leaf: 6}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := CycleTarget(test.ring, test.current, test.reverse); got != test.want {
				t.Fatalf("CycleTarget(%v, reverse=%v) = %v, want %v", test.current, test.reverse, got, test.want)
			}
		})
	}
}

func TestCycleTargetWalksWholeRing(t *testing.T) {
	ring := Ring(terminalDocIssueShell(), true)
	for _, reverse := range []bool{false, true} {
		current := ring[0]
		seen := map[Target]bool{current: true}
		for range len(ring) - 1 {
			current = CycleTarget(ring, current, reverse)
			if seen[current] {
				t.Fatalf("reverse=%v: revisited %v before covering the ring %v", reverse, current, ring)
			}
			seen[current] = true
		}
		if got := CycleTarget(ring, current, reverse); got != ring[0] {
			t.Fatalf("reverse=%v: wrap returned %v, want %v", reverse, got, ring[0])
		}
	}
}

func TestAtRingEndAndRingStart(t *testing.T) {
	ring := []Target{{Kind: TargetSidebar}, {Kind: TargetLeaf, Leaf: 1}, {Kind: TargetLeaf, Leaf: 2}}
	if AtRingEnd(ring, Target{Kind: TargetLeaf, Leaf: 1}, false) {
		t.Fatal("a middle window reported the end of the ring")
	}
	if !AtRingEnd(ring, Target{Kind: TargetLeaf, Leaf: 2}, false) {
		t.Fatal("the last window did not report the end of the ring")
	}
	if !AtRingEnd(ring, Target{Kind: TargetSidebar}, true) {
		t.Fatal("the first window is the end going backwards")
	}
	if AtRingEnd(ring, Target{Kind: TargetLeaf, Leaf: 9}, false) {
		t.Fatal("a target the ring does not contain is not at its end")
	}
	if AtRingEnd(nil, Target{Kind: TargetSidebar}, false) {
		t.Fatal("an empty ring has no end")
	}

	if start, ok := RingStart(ring, false); !ok || start != ring[0] {
		t.Fatalf("RingStart forward = %v (%v), want %v", start, ok, ring[0])
	}
	if start, ok := RingStart(ring, true); !ok || start != ring[len(ring)-1] {
		t.Fatalf("RingStart reverse = %v (%v), want %v", start, ok, ring[len(ring)-1])
	}
	if _, ok := RingStart(nil, false); ok {
		t.Fatal("an empty ring has no start")
	}
}

// TwoPaneRing is the tree ring's answer for the surfaces that have no tree: a
// window that is not drawn is not a stop, in either position.
func TestTwoPaneRing(t *testing.T) {
	both := TwoPaneRing(true, true)
	if len(both) != 2 || both[0].Kind != TargetSidebar || both[1] != ContentPaneTarget {
		t.Fatalf("ring = %v, want the list then the content pane", both)
	}
	if got := TwoPaneRing(true, false); len(got) != 1 || got[0].Kind != TargetSidebar {
		t.Fatalf("ring without a content pane = %v, want the list alone", got)
	}
	if got := TwoPaneRing(false, true); len(got) != 1 || got[0] != ContentPaneTarget {
		t.Fatalf("ring with the list hidden = %v, want the content pane alone", got)
	}
	if got := TwoPaneRing(false, false); len(got) != 0 {
		t.Fatalf("ring with nothing drawn = %v, want no stops", got)
	}

	// The wrap points of the two-window ring are its two ends.
	if !AtRingEnd(both, ContentPaneTarget, false) || AtRingEnd(both, ContentPaneTarget, true) {
		t.Fatal("the content pane is the forward wrap point and only that")
	}
	if start, ok := RingStart(both, false); !ok || start.Kind != TargetSidebar {
		t.Fatalf("forward restart = %v, want the list", start)
	}
}
