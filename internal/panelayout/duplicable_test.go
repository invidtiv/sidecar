package panelayout

import "testing"

func terminalShell() *Node {
	return &Node{ID: 3, Split: &Split{Axis: Columns, Ratio: 50,
		A: &Node{ID: 1, Kind: Primary},
		B: &Node{ID: 2, Kind: Shell},
	}}
}

func terminalDocShell() *Node {
	return &Node{ID: 5, Split: &Split{Axis: Columns, Ratio: 50,
		A: &Node{ID: 1, Kind: Primary},
		B: &Node{ID: 4, Split: &Split{Axis: Rows, Ratio: 50,
			A: &Node{ID: 2, Kind: Document},
			B: &Node{ID: 3, Kind: Shell},
		}},
	}}
}

// A Shell open is a new session, so it may never land on a leaf already showing
// a different one. These steps walk the auto rules from an empty workspace: the
// first content pane opens beside the primary terminal, the second stacks in the
// right column, and a later one follows the grid rule into the emptiest column.
func TestPlanOpenShellNeverRetargetsAndFollowsTheAutoRules(t *testing.T) {
	tests := []struct {
		name  string
		root  *Node
		kind  Kind
		boxes map[int]Box
		want  OpenPlan
		ok    bool
	}{
		{
			name: "step 1: first content splits the primary column",
			root: terminalOnly(),
			kind: Shell,
			want: OpenPlan{Split: 1, Axis: Columns},
			ok:   true,
		},
		{
			name: "step 2: the right column stacks",
			root: terminalDoc(),
			kind: Shell,
			want: OpenPlan{Split: 2, Axis: Rows},
			ok:   true,
		},
		{
			name: "step 3: with the right column holding two, the primary column splits (2x2)",
			root: terminalDocIssue(),
			kind: Shell,
			boxes: map[int]Box{
				2: {W: 60, H: 6},
				4: {W: 60, H: 14},
			},
			want: OpenPlan{Split: 1, Axis: Rows},
			ok:   true,
		},
		{
			name: "an existing shell leaf is never retargeted",
			root: &Node{ID: 3, Split: &Split{Axis: Rows, Ratio: 50,
				A: &Node{ID: 1, Kind: Document},
				B: &Node{ID: 2, Kind: Shell},
			}},
			kind: Shell,
			boxes: map[int]Box{
				1: {W: 60, H: 14},
				2: {W: 60, H: 6},
			},
			// One column of two stacks; the emptiest column is that column, so
			// the shell appends below it by splitting the column's subtree.
			want: OpenPlan{Split: 3, Axis: Rows},
			ok:   true,
		},
		{
			name: "a passive kind still retargets its leaf",
			root: terminalDocShell(),
			kind: Document,
			want: OpenPlan{Retarget: 2},
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

// The cap is on live leaves ON SCREEN: the primary terminal plus one peer. The
// third request is refused, and the refusal is legible to the caller so it can
// say so rather than doing nothing.
func TestPlanOpenRefusesPastTheLiveLeafCap(t *testing.T) {
	if LiveLeafCap != 2 {
		t.Fatalf("LiveLeafCap = %d, want 2", LiveLeafCap)
	}
	if got := LiveLeafCount(terminalOnly()); got != 1 {
		t.Fatalf("LiveLeafCount(primary) = %d, want 1", got)
	}
	if got := LiveLeafCount(terminalDocShell()); got != 2 {
		t.Fatalf("LiveLeafCount(primary+shell) = %d, want 2", got)
	}
	if LiveCapReached(terminalDoc()) {
		t.Fatal("primary + document reached the live cap")
	}
	full := terminalShell()
	if !LiveCapReached(full) {
		t.Fatal("primary + shell did not reach the live cap")
	}
	if plan, ok := PlanOpen(full, Shell, nil); ok {
		t.Fatalf("PlanOpen past the cap = %#v, want a refusal", plan)
	}
	// The cap is on live leaves only: passive kinds still open beside them.
	if plan, ok := PlanOpen(full, Document, nil); !ok || plan.Split != 2 {
		t.Fatalf("PlanOpen(Document) = %#v ok=%v, want a split of the shell leaf", plan, ok)
	}
}

// One tmux session is never shown in two leaves: an open naming content already
// on screen retargets that leaf instead of splitting, even for a duplicable kind
// at the live cap.
func TestPlanOpenContentRetargetsTheLeafShowingIt(t *testing.T) {
	root := terminalShell()
	FirstOfKind(root, Shell).ContentID = 7

	plan, ok := PlanOpenContent(root, Shell, 7, nil)
	if !ok || plan.Retarget != 2 || plan.Split != 0 {
		t.Fatalf("PlanOpenContent(same session) = %#v ok=%v, want a retarget of leaf 2", plan, ok)
	}
	if plan, ok := PlanOpenContent(root, Shell, 8, nil); ok {
		t.Fatalf("PlanOpenContent(other session) = %#v, want the cap refusal", plan)
	}
	// Content ids are per kind: a document with the same id is not this session.
	if plan, ok := PlanOpenContent(root, Document, 7, nil); !ok || plan.Retarget != 0 {
		t.Fatalf("PlanOpenContent(Document) = %#v ok=%v, want a split", plan, ok)
	}
}

// Right/Below from the create modal are the same vocabulary as `--split`, so a
// duplicable open takes its axis override unchanged.
func TestApplyAxisOverrideOnADuplicableOpen(t *testing.T) {
	plan, ok := PlanOpen(terminalOnly(), Shell, nil)
	if !ok {
		t.Fatal("premise: a shell opens beside a lone primary terminal")
	}
	if got := ApplyAxisOverride(plan, "below"); got.Axis != Rows || got.Split != 1 {
		t.Fatalf("below = %#v", got)
	}
	if got := ApplyAxisOverride(plan, "right"); got.Axis != Columns || got.Split != 1 {
		t.Fatalf("right = %#v", got)
	}
	if got := ApplyAxisOverride(plan, "auto"); got != plan {
		t.Fatalf("auto = %#v, want the planned placement", got)
	}
}
