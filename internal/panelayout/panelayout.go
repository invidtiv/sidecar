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
	Terminal Kind = iota
	Document
	Issue
	Diff
)

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
	Terminal Floor
	Doc      Floor
	Issue    Floor
	Diff     Floor
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
		floor := floors.Terminal
		switch node.Kind {
		case Document:
			floor = floors.Doc
		case Issue:
			floor = floors.Issue
		case Diff:
			floor = floors.Diff
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
// leaf of the requested kind; otherwise Split names the leaf to divide.
type OpenPlan struct {
	Retarget int
	Split    int
	Axis     Axis
}

// PlanOpen keeps the terminal in a full-height left column: the first content
// opens beside it, a different content kind stacks in the right column, and a
// repeated kind retargets its existing leaf.
func PlanOpen(root *Node, kind Kind) (OpenPlan, bool) {
	if kind == Terminal {
		return OpenPlan{}, false
	}
	if leaf := FirstOfKind(root, kind); leaf != nil {
		return OpenPlan{Retarget: leaf.ID}, true
	}
	if leaf := firstContent(root); leaf != nil {
		return OpenPlan{Split: leaf.ID, Axis: Rows}, true
	}
	if leaf := FirstOfKind(root, Terminal); leaf != nil {
		return OpenPlan{Split: leaf.ID, Axis: Columns}, true
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

func firstContent(node *Node) *Node {
	if node == nil {
		return nil
	}
	if node.Split == nil {
		if node.Kind != Terminal {
			return node
		}
		return nil
	}
	if leaf := firstContent(node.Split.A); leaf != nil {
		return leaf
	}
	return firstContent(node.Split.B)
}

func SplitLeaf(root *Node, leafID int, axis Axis, newLeaf *Node) (*Node, int) {
	rootNodes, rootIDs, valid := inspect(root)
	leaf := rootIDs[leafID]
	if !valid || leaf == nil || leaf.Split != nil || newLeaf == nil || (axis != Columns && axis != Rows) {
		return root, leafID
	}
	candidateNodes, candidateIDs, valid := inspect(newLeaf)
	if !valid || overlaps(rootNodes, candidateNodes) {
		return root, leafID
	}
	if newLeaf.Split != nil {
		for node := range candidateNodes {
			if node.ID <= 0 {
				return root, leafID
			}
		}
	}
	for id := range candidateIDs {
		if rootIDs[id] != nil && newLeaf.Split != nil {
			return root, leafID
		}
	}
	nextID := max(MaxID(root), MaxID(newLeaf)) + 1
	if newLeaf.ID <= 0 || rootIDs[newLeaf.ID] != nil {
		newLeaf.ID = nextID
		nextID++
	} else if newLeaf.ID >= nextID {
		nextID = newLeaf.ID + 1
	}
	existing := *leaf
	*leaf = Node{ID: nextID, Split: &Split{Axis: axis, Ratio: 50, A: &existing, B: newLeaf}}
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
