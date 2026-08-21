package panelayout

import "testing"

// A Shell leaf is a live terminal peer, not the host's Primary terminal, so it
// carries its own floor. These tables assert that the floor is folded up the
// tree on both axes — a Shell leaf that fell back to the zero floor would let a
// tree claim to fit at a size where the terminal has no rows.
func TestShellLeafFoldsItsOwnFloor(t *testing.T) {
	floors := Floors{
		Primary:  Floor{Width: 14, Height: 5},
		Shell:    Floor{Width: 10, Height: 3},
		Doc:      Floor{Width: 20, Height: 3},
		Issue:    Floor{Width: 20, Height: 3},
		Diff:     Floor{Width: 20, Height: 3},
		Resource: Floor{Width: 20, Height: 3},
	}

	tests := []struct {
		name string
		root *Node
		want Floor
	}{
		{
			name: "lone shell leaf",
			root: &Node{ID: 1, Kind: Shell},
			want: Floor{Width: 10, Height: 3},
		},
		{
			name: "primary beside a shell",
			root: &Node{ID: 3, Split: &Split{Axis: Columns, Ratio: 50,
				A: &Node{ID: 1, Kind: Primary},
				B: &Node{ID: 2, Kind: Shell},
			}},
			want: Floor{Width: 14 + 1 + 10, Height: 5},
		},
		{
			name: "primary above a shell",
			root: &Node{ID: 3, Split: &Split{Axis: Rows, Ratio: 50,
				A: &Node{ID: 1, Kind: Primary},
				B: &Node{ID: 2, Kind: Shell},
			}},
			want: Floor{Width: 14, Height: 5 + 1 + 3},
		},
		{
			name: "shell stacked beside a document column",
			root: &Node{ID: 5, Split: &Split{Axis: Columns, Ratio: 50,
				A: &Node{ID: 1, Kind: Primary},
				B: &Node{ID: 4, Split: &Split{Axis: Rows, Ratio: 50,
					A: &Node{ID: 2, Kind: Shell},
					B: &Node{ID: 3, Kind: Document},
				}},
			}},
			// The stacked column needs both its leaves plus their divider,
			// which is taller than the primary terminal beside it.
			want: Floor{Width: 14 + 1 + 20, Height: 3 + 1 + 3},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := paneMinimum(tc.root, floors); got != tc.want {
				t.Fatalf("paneMinimum = %+v, want %+v", got, tc.want)
			}
			// The floor is the exact boundary: one cell under it in either
			// dimension is a refusal, and the floor itself lays out.
			if _, _, fits := LayoutPanes(tc.root, Box{W: tc.want.Width, H: tc.want.Height}, floors); !fits {
				t.Fatalf("tree refused its own floor %+v", tc.want)
			}
			if _, _, fits := LayoutPanes(tc.root, Box{W: tc.want.Width - 1, H: tc.want.Height}, floors); fits {
				t.Fatal("tree fits one column under its floor")
			}
			if _, _, fits := LayoutPanes(tc.root, Box{W: tc.want.Width, H: tc.want.Height - 1}, floors); fits {
				t.Fatal("tree fits one row under its floor")
			}
		})
	}
}

// Shell is a content kind, not the host's Primary: a leaf of it must not be
// mistaken for the terminal the tree is built around.
func TestShellIsDistinctFromPrimary(t *testing.T) {
	if Shell == Primary {
		t.Fatal("Shell must not share Primary's value")
	}
	root := &Node{ID: 3, Split: &Split{Axis: Columns, Ratio: 50,
		A: &Node{ID: 1, Kind: Primary},
		B: &Node{ID: 2, Kind: Shell},
	}}
	if leaf := FirstOfKind(root, Primary); leaf == nil || leaf.ID != 1 {
		t.Fatalf("FirstOfKind(Primary) = %+v, want leaf 1", leaf)
	}
	if leaf := FirstOfKind(root, Shell); leaf == nil || leaf.ID != 2 {
		t.Fatalf("FirstOfKind(Shell) = %+v, want leaf 2", leaf)
	}
	if leaves := contentLeaves(root); len(leaves) != 1 || leaves[0].ID != 2 {
		t.Fatalf("contentLeaves = %+v, want only the shell leaf", leaves)
	}
}
