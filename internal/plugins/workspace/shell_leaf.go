package workspace

// The terminal panel is a Shell leaf of the pane tree, and nothing more. Its
// box, its divider, its border and its hit regions are the shared frame's,
// exactly like a document's or a diff's — there is no second split system
// beside the tree, and no arithmetic here that panelayout does not already own.
//
// termPanelVisible remains the panel's LIFECYCLE flag: the tmux session it owns
// outlives a hidden panel, so "is the session ours" and "is a leaf on screen"
// are different questions. syncShellLeaf is the single place the tree is made to
// agree with it — exactly one Shell leaf while the panel is up, none while it is
// not — so no other path can leave a leaf drawn for a panel that was hidden, or
// a panel flagged visible with nowhere to draw.

const (
	// shellSplitDefaultRatio is the primary terminal's share of a freshly opened
	// shell split. A split's ratio is its FIRST child's, and the first child is
	// the primary terminal, so this is the complement of the panel's own half:
	// stating it the other way round is what would put every panel on the far
	// side of its divider.
	shellSplitDefaultRatio = 50

	// shellSplitDefaultAxis is where a shell split opens when nothing has been
	// remembered: below the primary terminal, which is where the panel opened
	// before it was a leaf.
	shellSplitDefaultAxis = SplitRows
)

// shellLeaf is the terminal panel's leaf, or nil when the panel is not in the
// tree.
func (p *Plugin) shellLeaf() *PaneNode { return firstPaneLeafOfKind(p.paneRoot, PaneShell) }

// shellSplitID is the split node dividing the primary terminal from the shell
// leaf. Zero when there is no shell leaf.
func (p *Plugin) shellSplitID() int {
	leaf := p.shellLeaf()
	if leaf == nil {
		return 0
	}
	return parentSplitID(p.paneRoot, leaf.ID)
}

// shellSplitIsColumns reports that the panel sits beside the primary terminal
// rather than below it. It reads the tree, which is the only place the axis
// lives now.
func (p *Plugin) shellSplitIsColumns() bool {
	split := FindPane(p.paneRoot, p.shellSplitID())
	return split != nil && split.Split != nil && split.Split.Axis == SplitCols
}

// rememberShellSplit records the live split's axis and ratio as the shape the
// next ctrl+t opens at. A drag that moves the divider is a preference, not a
// one-off.
func (p *Plugin) rememberShellSplit() {
	split := FindPane(p.paneRoot, p.shellSplitID())
	if split == nil || split.Split == nil {
		return
	}
	p.shellSplitAxis = split.Split.Axis
	p.shellSplitRatio = clampPaneRatio(split.Split.Ratio)
}

// shellSplitShape is the axis and ratio a shell split opens at: what was last
// remembered, or the defaults.
func (p *Plugin) shellSplitShape() (SplitAxis, int) {
	axis := p.shellSplitAxis
	if axis != SplitCols && axis != SplitRows {
		axis = shellSplitDefaultAxis
	}
	ratio := p.shellSplitRatio
	if ratio <= 0 {
		ratio = shellSplitDefaultRatio
	}
	return axis, clampPaneRatio(ratio)
}

// syncShellLeaf makes the pane tree agree with termPanelVisible. It is called
// wherever that flag moves and wherever the tree is rebuilt, so the two cannot
// drift apart for a frame.
//
// It reports whether the tree changed, which is a caller's cue that terminal
// geometry moved.
func (p *Plugin) syncShellLeaf() bool {
	if p.paneRoot == nil {
		return false
	}
	leaf := p.shellLeaf()
	switch {
	case p.termPanelVisible && leaf == nil:
		return p.openShellLeaf()
	case !p.termPanelVisible && leaf != nil:
		p.paneRoot, p.paneFocus = ClosePane(p.paneRoot, leaf.ID)
		return true
	}
	return false
}

// openShellLeaf splits the primary terminal with a Shell leaf at the remembered
// shape. A split the viewport cannot hold is refused rather than squeezed
// (Law 2), and the refusal turns the panel back off so no state claims a leaf
// that is not there.
func (p *Plugin) openShellLeaf() bool {
	terminal := firstPaneLeafOfKind(p.paneRoot, PaneTerminal)
	peer, placed := p.previewPeerBox()
	if terminal == nil || !placed {
		p.termPanelVisible = false
		return false
	}
	axis, ratio := p.shellSplitShape()
	trial, _ := SplitLeaf(clonePaneTree(p.paneRoot), terminal.ID, axis, &PaneNode{Kind: PaneShell})
	if _, _, fits := LayoutPanes(trial, peer, paneTreeFloors()); !fits {
		p.termPanelVisible = false
		return false
	}
	node := &PaneNode{Kind: PaneShell}
	root, focus := SplitLeaf(p.paneRoot, terminal.ID, axis, node)
	if focus != node.ID {
		p.termPanelVisible = false
		return false
	}
	p.paneRoot = root
	p.paneNextID = maxInt(p.paneNextID, maxPaneID(p.paneRoot)+1)
	SetRatio(p.paneRoot, p.shellSplitID(), ratio)
	// Focus follows the panel the same way it did before the panel was a leaf:
	// termPanelFocused says which terminal has the keyboard, and paneFocus stays
	// on the terminal leaf the ring names.
	p.paneFocus = terminalLeafID(p.paneRoot)
	return true
}

// shellLeafBox is the panel's INNER box — header row plus viewport, inside its
// own chrome — or !ok when the panel is not on screen at this size. A layout
// that had to zoom answers with the zoomed leaf alone, so a panel the frame did
// not draw has no box here either.
func (p *Plugin) shellLeafBox() (Box, bool) {
	leaf := p.shellLeaf()
	if leaf == nil {
		return Box{}, false
	}
	geom, ok := p.leafGeometryFor(leaf.ID)
	if !ok || geom.Inner.W <= 0 || geom.Inner.H <= 0 {
		return Box{}, false
	}
	return geom.Inner, true
}

// terminalSlotBox is one terminal surface's inner box, selected by the same
// bool every terminal path is parameterized by. ok is false when that surface is
// not on screen.
func (p *Plugin) terminalSlotBox(termPanel bool) (Box, bool) {
	if termPanel {
		return p.shellLeafBox()
	}
	box, ok := p.terminalLeafBox()
	if !ok || box.W <= 0 || box.H <= 0 {
		return Box{}, false
	}
	return box, true
}

// terminalSlotSize is the tmux size for a slot's box: the box less the one
// header row every embedded terminal spends.
func terminalSlotSize(box Box) (width, height int) {
	return box.W, max(box.H-terminalHeaderRows, 1)
}

// parentSplitID names the split node one leaf hangs from, or zero for a leaf
// that is the whole tree.
func parentSplitID(node *PaneNode, leafID int) int {
	if node == nil || node.Split == nil {
		return 0
	}
	if childLeafID(node.Split.A) == leafID || childLeafID(node.Split.B) == leafID {
		return node.ID
	}
	if id := parentSplitID(node.Split.A, leafID); id != 0 {
		return id
	}
	return parentSplitID(node.Split.B, leafID)
}

func childLeafID(node *PaneNode) int {
	if node == nil || node.Split != nil {
		return 0
	}
	return node.ID
}
