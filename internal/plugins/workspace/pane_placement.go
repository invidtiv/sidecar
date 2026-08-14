package workspace

// paneOpen is where newly clicked content goes. Retarget names a leaf that
// already holds this kind of content and should be pointed at the new target;
// otherwise Split names the leaf to divide along Axis, with the new leaf taking
// the second half.
type paneOpen struct {
	Retarget int
	Split    int
	Axis     SplitAxis
}

// planPaneOpen is the default placement heuristic for clicked content, and
// deliberately the whole of it: click-driven opening has no window manager
// behind it yet, so the policy is one pure function that M1 of the windowing
// plan replaces outright rather than unpicks from the click paths. Every kind
// of click asks it — a rule that lived in one click path would let the two
// orders of the same two clicks build two different layouts.
//
// The rules, in order:
//   - a leaf of this kind already exists: it is retargeted, the way a second
//     file click retargets the document pane instead of growing the tree;
//   - another content leaf exists: the new leaf splits it along rows, taking
//     the half below it, so the terminal keeps its own column at full height;
//   - otherwise the new leaf splits the terminal along columns, which is the
//     placement the first click into an untouched preview has always got.
//
// ok is false for a tree with no leaf this rule can name, which is a tree
// nothing should be opened into.
func planPaneOpen(root *PaneNode, kind PaneKind) (paneOpen, bool) {
	if kind == PaneTerminal {
		return paneOpen{}, false
	}
	if leaf := firstPaneLeafOfKind(root, kind); leaf != nil {
		return paneOpen{Retarget: leaf.ID}, true
	}
	if leaf := firstContentPaneLeaf(root); leaf != nil {
		return paneOpen{Split: leaf.ID, Axis: SplitRows}, true
	}
	if leaf := firstPaneLeafOfKind(root, PaneTerminal); leaf != nil {
		return paneOpen{Split: leaf.ID, Axis: SplitCols}, true
	}
	return paneOpen{}, false
}

// paneFitMessage names the dimension a refused split needed, because "wider" is
// not advice when the pane would have been stacked.
func paneFitMessage(name string, axis SplitAxis) string {
	if axis == SplitRows {
		return name + " pane needs a taller window; layout left unchanged"
	}
	return name + " pane needs a wider window; layout left unchanged"
}

// firstContentPaneLeaf returns the first non-terminal leaf in placement order.
// The kind-specific rule above has already run, so whatever this finds is
// content of another kind: the leaf the new one stacks under.
func firstContentPaneLeaf(node *PaneNode) *PaneNode {
	if node == nil {
		return nil
	}
	if node.Split == nil {
		if node.Kind != PaneTerminal {
			return node
		}
		return nil
	}
	if leaf := firstContentPaneLeaf(node.Split.A); leaf != nil {
		return leaf
	}
	return firstContentPaneLeaf(node.Split.B)
}

// firstPaneLeafOfKind returns the first leaf of kind in placement order, which
// is the order LayoutPanes walks: A before B at every split.
func firstPaneLeafOfKind(node *PaneNode, kind PaneKind) *PaneNode {
	if node == nil {
		return nil
	}
	if node.Split == nil {
		if node.Kind == kind {
			return node
		}
		return nil
	}
	if leaf := firstPaneLeafOfKind(node.Split.A, kind); leaf != nil {
		return leaf
	}
	return firstPaneLeafOfKind(node.Split.B, kind)
}
