// Package panelayout owns the presentation-neutral pane tree shared by
// project and global workspace previews. It knows structure, geometry, and the
// default placement policy, but nothing about terminals, documents, issues,
// rendering, or persistence.
package panelayout

import "github.com/marcus/sidecar/internal/termpreview"

const (
	minRatio = 15
	maxRatio = 85
)

type Kind int

const (
	// Primary is the host's owned content. In Workspaces that content is a
	// terminal; app content decks may host any plugin without changing the pane
	// tree's persisted vocabulary.
	Primary Kind = iota
	Document
	Issue
	Diff
	// Resource is the single leaf kind every external terminal resource
	// provider shares. The extension point is which resource is recognized
	// and resolved, not which windows exist, so a Jira ticket and a CI build
	// become tabs in one leaf rather than two pane kinds.
	Resource
	// Shell is a live terminal leaf the user created, distinct from Primary:
	// Primary is the one terminal the host owns, Shell is a peer session the
	// tree hosts beside it. Keeping them apart is what lets a host have more
	// than one live terminal without the tree learning what a terminal is.
	Shell
	// Note is a read-only td note card. Added after Shell so existing numeric
	// Kind values stay stable; persistence uses the string "note".
	Note
)

// Terminal is the persisted-value and source compatibility alias used by the
// existing Workspace hosts. Primary deliberately retains its numeric value.
const Terminal = Primary

type Axis int

const (
	Columns Axis = iota
	Rows
)

type Node struct {
	ID        int
	Kind      Kind
	ContentID int
	Split     *Split
}

type Split struct {
	Axis  Axis
	Ratio int
	A, B  *Node
}

type Box = termpreview.Box

type Placement struct {
	Node *Node
	Box  Box
}

type Divider struct {
	SplitID int
	Axis    Axis
	Box     Box
}

type Floor struct{ Width, Height int }

type Floors struct {
	Primary Floor
	// Terminal is the compatibility field for existing Workspace callers.
	// Primary wins when both are populated.
	Terminal Floor
	Doc      Floor
	Issue    Floor
	Diff     Floor
	Resource Floor
	Shell    Floor
	Note     Floor
}

func (f Floors) primary() Floor {
	if f.Primary != (Floor{}) {
		return f.Primary
	}
	return f.Terminal
}

type Layout struct {
	Leaves   []Placement
	Dividers []Divider
	Zoomed   bool
}

func LayoutTree(root *Node, box Box, floors Floors, focus int) (Layout, bool) {
	if leaves, dividers, fits := LayoutPanes(root, box, floors); fits {
		return Layout{Leaves: leaves, Dividers: dividers}, true
	}
	focused := Find(root, focus)
	if focused == nil || focused.Split != nil {
		return Layout{}, false
	}
	return Layout{Leaves: []Placement{{Node: focused, Box: box}}, Zoomed: true}, true
}

func LayoutPanes(root *Node, box Box, floors Floors) (leaves []Placement, dividers []Divider, fits bool) {
	if root == nil || box.W < 0 || box.H < 0 {
		return nil, nil, false
	}
	minimum := paneMinimum(root, floors)
	if box.W < minimum.Width || box.H < minimum.Height {
		return nil, nil, false
	}
	return layoutNode(root, box, floors, nil, nil)
}

func layoutNode(node *Node, box Box, floors Floors, leaves []Placement, dividers []Divider) ([]Placement, []Divider, bool) {
	if node.Split == nil {
		return append(leaves, Placement{Node: node, Box: box}), dividers, true
	}
	split := node.Split
	aMin, bMin := paneMinimum(split.A, floors), paneMinimum(split.B, floors)
	ratio := ClampRatio(split.Ratio)
	var aBox, bBox, divider Box
	if split.Axis == Rows {
		available := box.H - 1
		aHeight := clamp(box.H*ratio/100, aMin.Height, available-bMin.Height)
		aBox = Box{X: box.X, Y: box.Y, W: box.W, H: aHeight}
		divider = Box{X: box.X, Y: box.Y + aHeight, W: box.W, H: 1}
		bBox = Box{X: box.X, Y: divider.Y + 1, W: box.W, H: available - aHeight}
	} else {
		available := box.W - 1
		aWidth := clamp(box.W*ratio/100, aMin.Width, available-bMin.Width)
		aBox = Box{X: box.X, Y: box.Y, W: aWidth, H: box.H}
		divider = Box{X: box.X + aWidth, Y: box.Y, W: 1, H: box.H}
		bBox = Box{X: divider.X + 1, Y: box.Y, W: available - aWidth, H: box.H}
	}
	dividers = append(dividers, Divider{SplitID: node.ID, Axis: split.Axis, Box: divider})
	leaves, dividers, _ = layoutNode(split.A, aBox, floors, leaves, dividers)
	return layoutNode(split.B, bBox, floors, leaves, dividers)
}

func paneMinimum(node *Node, floors Floors) Floor {
	if node == nil {
		return Floor{}
	}
	if node.Split == nil {
		floor := floors.primary()
		switch node.Kind {
		case Document:
			floor = floors.Doc
		case Issue:
			floor = floors.Issue
		case Diff:
			floor = floors.Diff
		case Resource:
			floor = floors.Resource
		case Shell:
			floor = floors.Shell
		case Note:
			floor = floors.Note
		}
		return Floor{Width: max(floor.Width, 0), Height: max(floor.Height, 0)}
	}
	a, b := paneMinimum(node.Split.A, floors), paneMinimum(node.Split.B, floors)
	if node.Split.Axis == Rows {
		return Floor{Width: max(a.Width, b.Width), Height: a.Height + 1 + b.Height}
	}
	return Floor{Width: a.Width + 1 + b.Width, Height: max(a.Height, b.Height)}
}

// OpenPlan is the shared click-placement answer. Retarget names an existing
// leaf of the requested kind; otherwise Split names the node to divide — a
// leaf for an ordinary split, or a column's subtree (even the root) when the
// grid rule appends to a stack or starts a column. NewFirst puts newLeaf above
// the divided subtree instead of below it, which is how an insert at an
// occupied cell pushes the occupant down.
type OpenPlan struct {
	Retarget int
	Split    int
	Axis     Axis
	NewFirst bool
}

// ApplyAxisOverride rewrites a split plan's axis from the CLI --split flag.
// auto / empty leave PlanOpen's axis alone. A retarget is unchanged: --split
// never forces a second leaf of a kind that already exists, and it never
// retargets the named leaf onto the terminal.
func ApplyAxisOverride(plan OpenPlan, split string) OpenPlan {
	if plan.Retarget != 0 {
		return plan
	}
	switch split {
	case "right":
		plan.Axis = Columns
	case "below":
		plan.Axis = Rows
	}
	return plan
}

// LiveLeafCap is how many live leaves may be on screen in one tree at once:
// the host's primary terminal plus one peer. Live leaves cost a control-mode
// subscription and a tmux resize per geometry change, so the cap is a refusal
// the caller reports, never a silent drop.
const LiveLeafCap = 2

// IsLive reports that a leaf of this kind drives a live terminal session.
func IsLive(kind Kind) bool { return kind == Primary || kind == Shell }

// Duplicable reports that a second leaf of this kind is a second thing, not the
// same thing shown again. Document/Issue/Diff/Resource/Note swap their content
// in place, so opening one twice retargets the leaf that exists; a Shell open
// is a new session, so it always splits.
func Duplicable(kind Kind) bool { return kind == Shell }

// LiveLeafCount is how many live leaves the tree currently holds.
func LiveLeafCount(node *Node) int {
	if node == nil {
		return 0
	}
	if node.Split == nil {
		if IsLive(node.Kind) {
			return 1
		}
		return 0
	}
	return LiveLeafCount(node.Split.A) + LiveLeafCount(node.Split.B)
}

// LeafCount is how many content leaves the tree holds. It is the number a
// sidebar row's layout badge is derived from, so a live tree and the tree the
// same workspace persisted are counted by one rule.
func LeafCount(node *Node) int {
	if node == nil {
		return 0
	}
	if node.Split == nil {
		return 1
	}
	return LeafCount(node.Split.A) + LeafCount(node.Split.B)
}

// LiveCapReached reports that no further live leaf fits in this tree. Hosts use
// it to explain the refusal instead of leaving PlanOpen's false unexplained.
func LiveCapReached(root *Node) bool { return LiveLeafCount(root) >= LiveLeafCap }

// FirstOfContent names the leaf already showing one content id of a kind, or
// nil. A live session is never shown in two leaves, so an open that names a
// session already on screen retargets it rather than splitting.
func FirstOfContent(node *Node, kind Kind, contentID int) *Node {
	if node == nil || contentID == 0 {
		return nil
	}
	if node.Split == nil {
		if node.Kind == kind && node.ContentID == contentID {
			return node
		}
		return nil
	}
	if leaf := FirstOfContent(node.Split.A, kind, contentID); leaf != nil {
		return leaf
	}
	return FirstOfContent(node.Split.B, kind, contentID)
}

// PlanOpen keeps the primary content in a full-height left column: the first
// content opens beside it, a different content kind stacks in the right
// column, and once two content panes are on screen the grid rule takes over —
// the emptiest column takes the next pane (so the fourth pane forms a 2×2
// rather than a third stacked row), full columns give way to a new column,
// and the caps refuse. boxes may be nil; they only break ties for trees that
// escape the grid vocabulary, where the legacy largest-content-leaf rule
// still answers.
func PlanOpen(root *Node, kind Kind, boxes map[int]Box) (OpenPlan, bool) {
	return PlanOpenContent(root, kind, 0, boxes)
}

// PlanOpenContent is PlanOpen for a named piece of content. A duplicable kind
// never retargets a leaf showing something else — every open is a new session —
// but it does retarget the leaf already showing this exact content, which is
// what keeps one tmux session out of two leaves.
func PlanOpenContent(root *Node, kind Kind, contentID int, boxes map[int]Box) (OpenPlan, bool) {
	if kind == Primary {
		return OpenPlan{}, false
	}
	if leaf := FirstOfContent(root, kind, contentID); leaf != nil {
		return OpenPlan{Retarget: leaf.ID}, true
	}
	if Duplicable(kind) {
		if IsLive(kind) && LiveCapReached(root) {
			return OpenPlan{}, false
		}
	} else if leaf := FirstOfKind(root, kind); leaf != nil {
		return OpenPlan{Retarget: leaf.ID}, true
	}
	contents := contentLeaves(root)
	switch {
	case len(contents) == 0:
		if leaf := FirstOfKind(root, Primary); leaf != nil {
			return OpenPlan{Split: leaf.ID, Axis: Columns}, true
		}
	case len(contents) == 1:
		return OpenPlan{Split: contents[0].ID, Axis: Rows}, true
	default:
		grid := GridOf(root)
		if grid == nil {
			// A tree that escapes the grid vocabulary has no columns to fill;
			// the legacy largest-leaf answer still gives it a placement.
			if leaf := largestContentLeaf(contents, boxes); leaf != nil {
				return OpenPlan{Split: leaf.ID, Axis: Rows}, true
			}
			return OpenPlan{}, false
		}
		return planFillEmptiestColumn(grid)
	}
	return OpenPlan{}, false
}

// planFillEmptiestColumn is the auto rule once two content panes are on
// screen. The emptiest column takes the next pane — which is what makes the
// fourth pane form a 2×2 beside the primary terminal instead of stacking a
// third row (the right column is then the fuller one) — and ties go to the
// leftmost column so the answer stays deterministic. When every column is
// full, a new column opens while the vocabulary allows one; past the caps the
// caller reports the refusal rather than squeezing.
func planFillEmptiestColumn(grid *Grid) (OpenPlan, bool) {
	best := 0
	for i := range grid.Columns {
		if len(grid.Columns[i].Cells) < len(grid.Columns[best].Cells) {
			best = i
		}
	}
	if !grid.RowsAtCap(best + 1) {
		return OpenPlan{Split: grid.Columns[best].Node.ID, Axis: Rows}, true
	}
	if !grid.ColumnsAtCap() {
		return OpenPlan{Split: grid.Root().ID, Axis: Columns}, true
	}
	return OpenPlan{}, false
}

func FirstOfKind(node *Node, kind Kind) *Node {
	if node == nil {
		return nil
	}
	if node.Split == nil {
		if node.Kind == kind {
			return node
		}
		return nil
	}
	if leaf := FirstOfKind(node.Split.A, kind); leaf != nil {
		return leaf
	}
	return FirstOfKind(node.Split.B, kind)
}

func contentLeaves(node *Node) []*Node {
	if node == nil {
		return nil
	}
	if node.Split == nil {
		if node.Kind != Primary {
			return []*Node{node}
		}
		return nil
	}
	return append(contentLeaves(node.Split.A), contentLeaves(node.Split.B)...)
}

func largestContentLeaf(contents []*Node, boxes map[int]Box) *Node {
	var best *Node
	bestArea := -1
	for _, leaf := range contents {
		if leaf == nil {
			continue
		}
		area := 0
		if box, ok := boxes[leaf.ID]; ok {
			area = box.W * box.H
		}
		if best == nil || area > bestArea {
			best = leaf
			bestArea = area
		}
	}
	return best
}

// SplitLeaf divides the node targetID names — a leaf for the ordinary split,
// or an internal node (a column's subtree, the root) when a placement appends
// to a stack or starts a column. The divided subtree becomes the split's first
// child and newLeaf its second; the tree is spliced in place and the new
// leaf's ID is returned as the focus. Anything invalid — an unknown target, a
// malformed or overlapping candidate, colliding IDs — leaves the tree
// untouched and returns the target unchanged.
func SplitLeaf(root *Node, targetID int, axis Axis, newLeaf *Node) (*Node, int) {
	return splitNode(root, targetID, axis, newLeaf, false)
}

// ApplyPlan grafts newLeaf where plan says: retargets are the caller's to
// honor, so this is SplitLeaf with the plan's own axis and ordering.
func ApplyPlan(root *Node, plan OpenPlan, newLeaf *Node) (*Node, int) {
	return splitNode(root, plan.Split, plan.Axis, newLeaf, plan.NewFirst)
}

func splitNode(root *Node, targetID int, axis Axis, newLeaf *Node, newFirst bool) (*Node, int) {
	rootNodes, rootIDs, valid := inspect(root)
	target := rootIDs[targetID]
	if !valid || target == nil || newLeaf == nil || (axis != Columns && axis != Rows) {
		return root, targetID
	}
	candidateNodes, candidateIDs, valid := inspect(newLeaf)
	if !valid || overlaps(rootNodes, candidateNodes) {
		return root, targetID
	}
	if newLeaf.Split != nil {
		for node := range candidateNodes {
			if node.ID <= 0 {
				return root, targetID
			}
		}
	}
	for id := range candidateIDs {
		if rootIDs[id] != nil && newLeaf.Split != nil {
			return root, targetID
		}
	}
	nextID := max(MaxID(root), MaxID(newLeaf)) + 1
	if newLeaf.ID <= 0 || rootIDs[newLeaf.ID] != nil {
		newLeaf.ID = nextID
		nextID++
	} else if newLeaf.ID >= nextID {
		nextID = newLeaf.ID + 1
	}
	existing := *target
	first, second := &existing, newLeaf
	if newFirst {
		first, second = newLeaf, &existing
	}
	*target = Node{ID: nextID, Split: &Split{Axis: axis, Ratio: 50, A: first, B: second}}
	return root, firstLeaf(newLeaf).ID
}

func inspect(root *Node) (map[*Node]struct{}, map[int]*Node, bool) {
	nodes, ids := make(map[*Node]struct{}), make(map[int]*Node)
	if root == nil {
		return nodes, ids, false
	}
	stack := []*Node{root}
	for len(stack) > 0 {
		node := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if node == nil {
			return nodes, ids, false
		}
		if _, exists := nodes[node]; exists {
			return nodes, ids, false
		}
		nodes[node] = struct{}{}
		if node.ID > 0 {
			if ids[node.ID] != nil {
				return nodes, ids, false
			}
			ids[node.ID] = node
		} else if node.Split != nil {
			return nodes, ids, false
		}
		if node.Split != nil {
			if (node.Split.Axis != Columns && node.Split.Axis != Rows) || node.Split.A == nil || node.Split.B == nil {
				return nodes, ids, false
			}
			stack = append(stack, node.Split.B, node.Split.A)
		}
	}
	return nodes, ids, true
}

func overlaps(a, b map[*Node]struct{}) bool {
	for node := range b {
		if _, ok := a[node]; ok {
			return true
		}
	}
	return false
}

func Close(root *Node, leafID int) (*Node, int) {
	if root == nil {
		return nil, 0
	}
	if root.Split == nil {
		return root, root.ID
	}
	parent, sibling := parentAndSibling(root, leafID)
	if parent == nil || sibling == nil {
		return root, leafID
	}
	focus := firstLeaf(sibling)
	*parent = *sibling
	return root, focus.ID
}

func parentAndSibling(node *Node, leafID int) (*Node, *Node) {
	if node == nil || node.Split == nil {
		return nil, nil
	}
	if node.Split.A.Split == nil && node.Split.A.ID == leafID {
		return node, node.Split.B
	}
	if node.Split.B.Split == nil && node.Split.B.ID == leafID {
		return node, node.Split.A
	}
	if parent, sibling := parentAndSibling(node.Split.A, leafID); parent != nil {
		return parent, sibling
	}
	return parentAndSibling(node.Split.B, leafID)
}

func firstLeaf(node *Node) *Node {
	for node != nil && node.Split != nil {
		node = node.Split.A
	}
	return node
}

func Find(root *Node, id int) *Node {
	if root == nil || root.ID == id {
		return root
	}
	if root.Split == nil {
		return nil
	}
	if node := Find(root.Split.A, id); node != nil {
		return node
	}
	return Find(root.Split.B, id)
}

func SetRatio(root *Node, splitID, ratio int) bool {
	node := Find(root, splitID)
	if node == nil || node.Split == nil {
		return false
	}
	node.Split.Ratio = ClampRatio(ratio)
	return true
}

func MaxID(node *Node) int {
	if node == nil {
		return 0
	}
	maximum := node.ID
	if node.Split != nil {
		maximum = max(maximum, MaxID(node.Split.A))
		maximum = max(maximum, MaxID(node.Split.B))
	}
	return maximum
}

// Clone returns a detached copy suitable for trial layout before committing a
// split that may not fit the current viewport.
func Clone(node *Node) *Node {
	if node == nil {
		return nil
	}
	clone := &Node{ID: node.ID, Kind: node.Kind, ContentID: node.ContentID}
	if node.Split != nil {
		clone.Split = &Split{Axis: node.Split.Axis, Ratio: node.Split.Ratio, A: Clone(node.Split.A), B: Clone(node.Split.B)}
	}
	return clone
}

func ClampRatio(ratio int) int { return clamp(ratio, minRatio, maxRatio) }

func clamp(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}
