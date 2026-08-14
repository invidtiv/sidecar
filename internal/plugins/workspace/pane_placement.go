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

// planIssueOpen is the default placement heuristic for a clicked td issue, and
// deliberately the whole of it: click-driven opening has no window manager
// behind it yet, so the policy is one pure function that M1 of the windowing
// plan replaces outright rather than unpicks from the click paths.
//
// The rules, in order:
//   - an issue leaf already exists: it is retargeted, the way a second file
//     click retargets the document pane instead of growing the tree;
//   - a document leaf exists: the issue splits it along rows — document above,
//     issue below — so the terminal keeps its own column at full height;
//   - otherwise the issue splits the terminal along columns, which is exactly
//     the placement a file click gets.
//
// ok is false for a tree with no leaf this rule can name, which is a tree
// nothing should be opened into.
func planIssueOpen(root *PaneNode) (paneOpen, bool) {
	if leaf := firstPaneLeafOfKind(root, PaneIssue); leaf != nil {
		return paneOpen{Retarget: leaf.ID}, true
	}
	if leaf := firstPaneLeafOfKind(root, PaneDoc); leaf != nil {
		return paneOpen{Split: leaf.ID, Axis: SplitRows}, true
	}
	if leaf := firstPaneLeafOfKind(root, PaneTerminal); leaf != nil {
		return paneOpen{Split: leaf.ID, Axis: SplitCols}, true
	}
	return paneOpen{}, false
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
