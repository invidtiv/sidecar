package workspace

import "github.com/marcus/sidecar/internal/termpreview"

const (
	paneMinRatio = 15
	paneMaxRatio = 85
)

// PaneKind identifies the content hosted by a pane-tree leaf.
type PaneKind int

const (
	PaneTerminal PaneKind = iota
	PaneDoc
	PaneIssue
)

// SplitAxis identifies how a split divides its box.
type SplitAxis int

const (
	SplitCols SplitAxis = iota
	SplitRows
)

// PaneNode is a leaf when Split is nil and an internal node otherwise.
// ContentID names the content a leaf hosts and nothing about it: the structure
// layer holds an identity the content registry answers, never a document, an
// issue, or a transport.
type PaneNode struct {
	ID        int
	Kind      PaneKind
	ContentID int

	Split *PaneSplit
}

// PaneSplit divides a node between two children. Ratio is the percentage of
// the node's box assigned to A; B receives the remainder after the divider.
type PaneSplit struct {
	Axis  SplitAxis
	Ratio int
	A, B  *PaneNode
}

// Box is a rectangle in preview-content coordinates. It is the shared
// presentation layer's box type, so a leaf this tree places can be handed
// straight to the terminal presentation beneath it without a conversion that
// could quietly disagree.
type Box = termpreview.Box

// Placement associates a leaf with its allocated box.
type Placement struct {
	Node *PaneNode
	Box  Box
}

// Divider is the one-cell boundary owned by an internal pane node.
type Divider struct {
	SplitID int
	Axis    SplitAxis
	Box     Box
}

// PaneFloor is the minimum box size for one kind of leaf.
type PaneFloor struct {
	Width, Height int
}

// Floors contains the minimum box size for each leaf kind.
type Floors struct {
	Terminal PaneFloor
	Doc      PaneFloor
	Issue    PaneFloor
}

// LayoutPanes places every leaf and divider inside box. A false fits result
// means no partial layout should be rendered.
func LayoutPanes(root *PaneNode, box Box, floors Floors) (leaves []Placement, dividers []Divider, fits bool) {
	if root == nil || box.W < 0 || box.H < 0 {
		return nil, nil, false
	}
	minimum := paneMinimum(root, floors)
	if box.W < minimum.Width || box.H < minimum.Height {
		return nil, nil, false
	}

	leaves, dividers = layoutPaneNode(root, box, floors, leaves, dividers)
	return leaves, dividers, true
}

// PaneLayout is one placement of a pane tree in a box. Zoomed means the box
// could not satisfy every leaf's floor and the focused leaf holds all of it:
// Leaves is that one leaf and Dividers is empty.
type PaneLayout struct {
	Leaves   []Placement
	Dividers []Divider
	Zoomed   bool
}

// LayoutPaneTree places root in box, degrading to the focused leaf alone rather
// than to a partial layout. It is the one authority on that degradation: the
// terminal sizers and the split renderer both read Zoomed from here, so a box
// too small for the tree cannot mean one thing to the geometry and another to
// the pixels.
//
// ok is false when there is nothing to draw: no tree, or a box the tree does
// not fit while focus names something that is not a leaf of it.
func LayoutPaneTree(root *PaneNode, box Box, floors Floors, focus int) (PaneLayout, bool) {
	if leaves, dividers, fits := LayoutPanes(root, box, floors); fits {
		return PaneLayout{Leaves: leaves, Dividers: dividers}, true
	}
	focused := FindPane(root, focus)
	if focused == nil || focused.Split != nil {
		return PaneLayout{}, false
	}
	return PaneLayout{Leaves: []Placement{{Node: focused, Box: box}}, Zoomed: true}, true
}

func layoutPaneNode(node *PaneNode, box Box, floors Floors, leaves []Placement, dividers []Divider) ([]Placement, []Divider) {
	if node.Split == nil {
		return append(leaves, Placement{Node: node, Box: box}), dividers
	}

	split := node.Split
	aMinimum := paneMinimum(split.A, floors)
	bMinimum := paneMinimum(split.B, floors)
	ratio := clampPaneRatio(split.Ratio)

	var aBox, bBox, dividerBox Box
	switch split.Axis {
	case SplitRows:
		available := box.H - 1
		aHeight := clampInt(box.H*ratio/100, aMinimum.Height, available-bMinimum.Height)
		aBox = Box{X: box.X, Y: box.Y, W: box.W, H: aHeight}
		dividerBox = Box{X: box.X, Y: box.Y + aHeight, W: box.W, H: 1}
		bBox = Box{X: box.X, Y: dividerBox.Y + 1, W: box.W, H: available - aHeight}
	default:
		available := box.W - 1
		aWidth := clampInt(box.W*ratio/100, aMinimum.Width, available-bMinimum.Width)
		aBox = Box{X: box.X, Y: box.Y, W: aWidth, H: box.H}
		dividerBox = Box{X: box.X + aWidth, Y: box.Y, W: 1, H: box.H}
		bBox = Box{X: dividerBox.X + 1, Y: box.Y, W: available - aWidth, H: box.H}
	}

	dividers = append(dividers, Divider{SplitID: node.ID, Axis: split.Axis, Box: dividerBox})
	leaves, dividers = layoutPaneNode(split.A, aBox, floors, leaves, dividers)
	return layoutPaneNode(split.B, bBox, floors, leaves, dividers)
}

func paneMinimum(node *PaneNode, floors Floors) PaneFloor {
	if node == nil {
		return PaneFloor{}
	}
	if node.Split == nil {
		switch node.Kind {
		case PaneDoc:
			return nonNegativeFloor(floors.Doc)
		case PaneIssue:
			return nonNegativeFloor(floors.Issue)
		default:
			return nonNegativeFloor(floors.Terminal)
		}
	}

	a := paneMinimum(node.Split.A, floors)
	b := paneMinimum(node.Split.B, floors)
	if node.Split.Axis == SplitRows {
		return PaneFloor{Width: maxInt(a.Width, b.Width), Height: a.Height + 1 + b.Height}
	}
	return PaneFloor{Width: a.Width + 1 + b.Width, Height: maxInt(a.Height, b.Height)}
}

func nonNegativeFloor(floor PaneFloor) PaneFloor {
	floor.Width = maxInt(floor.Width, 0)
	floor.Height = maxInt(floor.Height, 0)
	return floor
}

// SplitLeaf replaces leafID with an internal node whose first child preserves
// the existing leaf identity. A single new leaf receives a unique positive ID
// when its supplied ID is missing or already in use; a subtree must already
// have stable, non-colliding IDs. The returned focus target is the new subtree's
// first leaf.
func SplitLeaf(root *PaneNode, leafID int, axis SplitAxis, newLeaf *PaneNode) (*PaneNode, int) {
	rootNodes, rootIDs, valid := inspectPaneTree(root)
	leaf := rootIDs[leafID]
	if !valid || leaf == nil || leaf.Split != nil || newLeaf == nil || (axis != SplitCols && axis != SplitRows) {
		return root, leafID
	}
	candidateNodes, candidateIDs, valid := inspectPaneTree(newLeaf)
	if !valid || paneTreesOverlap(rootNodes, candidateNodes) {
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
		if rootIDs[id] != nil {
			// Preserve the convenient leaf API: a single new leaf may omit its
			// identity or collide and will receive the next available ID below.
			// Subtrees must arrive with stable, non-colliding identities.
			if newLeaf.Split != nil {
				return root, leafID
			}
		}
	}

	nextID := maxInt(maxPaneID(root), maxPaneID(newLeaf)) + 1
	if newLeaf.ID <= 0 || rootIDs[newLeaf.ID] != nil {
		newLeaf.ID = nextID
		nextID++
	} else if newLeaf.ID >= nextID {
		nextID = newLeaf.ID + 1
	}

	existingLeaf := *leaf
	*leaf = PaneNode{
		ID: nextID,
		Split: &PaneSplit{
			Axis:  axis,
			Ratio: 50,
			A:     &existingLeaf,
			B:     newLeaf,
		},
	}
	return root, firstLeaf(newLeaf).ID
}

// inspectPaneTree validates structural and identity invariants without
// recursion, so malformed external input cannot loop forever. The returned
// maps are useful to prove a proposed subtree is detached before mutation.
func inspectPaneTree(root *PaneNode) (nodes map[*PaneNode]struct{}, ids map[int]*PaneNode, valid bool) {
	nodes = make(map[*PaneNode]struct{})
	ids = make(map[int]*PaneNode)
	if root == nil {
		return nodes, ids, false
	}

	stack := []*PaneNode{root}
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

		if node.Split == nil {
			continue
		}
		if node.Split.Axis != SplitCols && node.Split.Axis != SplitRows || node.Split.A == nil || node.Split.B == nil {
			return nodes, ids, false
		}
		stack = append(stack, node.Split.B, node.Split.A)
	}
	return nodes, ids, true
}

func paneTreesOverlap(a, b map[*PaneNode]struct{}) bool {
	for node := range b {
		if _, exists := a[node]; exists {
			return true
		}
	}
	return false
}

// ClosePane removes a leaf and collapses its parent into the sibling subtree.
// The returned focus target is the first leaf in that sibling subtree. The only
// leaf in a tree cannot be closed.
func ClosePane(root *PaneNode, leafID int) (*PaneNode, int) {
	if root == nil {
		return nil, 0
	}
	if root.Split == nil {
		return root, root.ID
	}

	parent, sibling := paneParentAndSibling(root, leafID)
	if parent == nil || sibling == nil {
		return root, leafID
	}
	focus := firstLeaf(sibling)
	*parent = *sibling
	return root, focus.ID
}

func paneParentAndSibling(node *PaneNode, leafID int) (parent, sibling *PaneNode) {
	if node == nil || node.Split == nil {
		return nil, nil
	}
	if node.Split.A != nil && node.Split.A.Split == nil && node.Split.A.ID == leafID {
		return node, node.Split.B
	}
	if node.Split.B != nil && node.Split.B.Split == nil && node.Split.B.ID == leafID {
		return node, node.Split.A
	}
	if parent, sibling = paneParentAndSibling(node.Split.A, leafID); parent != nil {
		return parent, sibling
	}
	return paneParentAndSibling(node.Split.B, leafID)
}

func firstLeaf(node *PaneNode) *PaneNode {
	for node != nil && node.Split != nil {
		node = node.Split.A
	}
	return node
}

// FindPane returns the leaf or internal node with id.
func FindPane(root *PaneNode, id int) *PaneNode {
	if root == nil {
		return nil
	}
	if root.ID == id {
		return root
	}
	if root.Split == nil {
		return nil
	}
	if node := FindPane(root.Split.A, id); node != nil {
		return node
	}
	return FindPane(root.Split.B, id)
}

// SetRatio updates an internal node's ratio after clamping it to the supported
// range. It returns false when splitID does not identify an internal node.
func SetRatio(root *PaneNode, splitID, ratio int) bool {
	node := FindPane(root, splitID)
	if node == nil || node.Split == nil {
		return false
	}
	node.Split.Ratio = clampPaneRatio(ratio)
	return true
}

func maxPaneID(node *PaneNode) int {
	if node == nil {
		return 0
	}
	maximum := node.ID
	if node.Split != nil {
		maximum = maxInt(maximum, maxPaneID(node.Split.A))
		maximum = maxInt(maximum, maxPaneID(node.Split.B))
	}
	return maximum
}

func clampPaneRatio(ratio int) int {
	return clampInt(ratio, paneMinRatio, paneMaxRatio)
}

func clampInt(value, minimum, maximum int) int {
	if value < minimum {
		return minimum
	}
	if value > maximum {
		return maximum
	}
	return value
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}
