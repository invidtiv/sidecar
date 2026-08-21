package workspace

import "github.com/marcus/sidecar/internal/termpreview"

// The terminal panel is the first shell leaf. It is still switched by the
// `termPanel bool` that threads through several hundred call sites, so rather
// than rewrite those, the bool is given a leaf to mean: `false` names the
// primary terminal's slot, `true` names the shell slot. Everything that asks
// where a terminal surface is drawn now asks the tree for that slot's box
// instead of walking offsets of its own.
//
// The slots are the IDs of a sub-tree rooted at the primary terminal leaf's
// inner box. It is a real panelayout tree — one Primary leaf, one Shell leaf,
// one split whose axis is the panel's layout — so the leaf kind, the floors and
// the structural vocabulary are the shared ones. Only its box arithmetic is
// still the panel's own (termPanelSplitBoxes), because the panel's clamp order
// and its fits rule are not panelayout's and this slice may not move a rendered
// cell. That arithmetic dies with the renderer in the next slice, at which point
// the sub-tree collapses into the real pane tree.
const (
	// shellSlotPrimary is the primary agent/shell terminal's leaf.
	shellSlotPrimary = 1
	// shellSlotShell is the panel terminal's leaf.
	shellSlotShell = 2
	// shellSlotSplit is the split node above them.
	shellSlotSplit = 3
)

// shellSlotFor maps the legacy termPanel bool onto a slot ID.
func shellSlotFor(termPanel bool) int {
	if termPanel {
		return shellSlotShell
	}
	return shellSlotPrimary
}

// shellPaneSubtree is the terminal leaf's own tree: the primary terminal alone
// when no panel is up, otherwise the primary terminal beside (right layout) or
// above (bottom layout) a Shell leaf.
func (p *Plugin) shellPaneSubtree() *PaneNode {
	primary := &PaneNode{ID: shellSlotPrimary, Kind: PaneTerminal}
	if !p.termPanelVisible {
		return primary
	}
	axis := SplitRows
	if p.termPanelLayout == TermPanelRight {
		axis = SplitCols
	}
	return &PaneNode{ID: shellSlotSplit, Split: &PaneSplit{
		Axis:  axis,
		Ratio: clampPaneRatio(100 - p.termPanelEffectiveSize()),
		A:     primary,
		B:     &PaneNode{ID: shellSlotShell, Kind: PaneShell},
	}}
}

// shellPaneBoxes places the sub-tree's leaves inside the primary terminal
// leaf's inner box, keyed by slot. Each box includes that surface's own header
// row, exactly as the pane tree's leaf boxes do.
//
// A split too small to draw has no shell leaf at all — the panel is not
// rendered, so there is nothing to place — and the primary terminal gets the
// whole box, which is the fallback every renderer already takes.
func (p *Plugin) shellPaneBoxes() map[int]Box {
	// Sizes come from the preview dimensions, which carry the term.GetSize
	// fallback a not-yet-sized plugin needs; the origin is the placed leaf's,
	// and callers that need a position check for one themselves.
	width, height := p.calculatePreviewDimensions()
	container := Box{W: width, H: termPanelContainerHeight(height)}
	if leaf, ok := p.terminalLeafBox(); ok {
		if placed := termpreview.SurfaceIn(leaf); placed.OK {
			container.X, container.Y = placed.X, placed.HeaderY
		}
	}

	root := p.shellPaneSubtree()
	if root.Split == nil {
		return map[int]Box{shellSlotPrimary: container}
	}
	outputBox, termBox, fits := p.termPanelSplitBoxes()
	if !fits {
		return map[int]Box{shellSlotPrimary: container}
	}
	if root.Split.Axis == SplitCols {
		return map[int]Box{
			shellSlotPrimary: {X: container.X, Y: container.Y, W: outputBox, H: container.H},
			shellSlotShell:   {X: container.X + outputBox + termPanelDividerCols, Y: container.Y, W: termBox, H: container.H},
		}
	}
	return map[int]Box{
		shellSlotPrimary: {X: container.X, Y: container.Y, W: container.W, H: outputBox},
		shellSlotShell:   {X: container.X, Y: container.Y + outputBox + termPanelDividerRows, W: container.W, H: termBox},
	}
}

// shellSlotBox is one terminal surface's box, selected by the legacy bool.
// ok is false when that surface is not on screen.
func (p *Plugin) shellSlotBox(termPanel bool) (Box, bool) {
	box, ok := p.shellPaneBoxes()[shellSlotFor(termPanel)]
	if !ok || box.W <= 0 || box.H <= 0 {
		return Box{}, false
	}
	return box, true
}

// shellSlotTerminalSize is the tmux size for a slot's box: the box less the one
// header row every embedded terminal spends.
func shellSlotTerminalSize(box Box) (width, height int) {
	return box.W, max(box.H-terminalHeaderRows, 1)
}
