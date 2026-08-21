package workspace

import "github.com/marcus/sidecar/internal/state"

// The terminal panel used to persist three keys of its own: which side it was
// on, how much of the preview it took, and whether it was up. It is a Shell leaf
// of the pane tree now, so those facts belong to the persisted layout. This file
// is the one-time conversion between the two, and it exists as pure functions
// because getting the ratio's direction wrong would silently flip every existing
// user's panel to the other side of its divider on upgrade — which is a thing to
// prove in a table, not to read carefully.

// termPanelPrefs is one user's legacy panel preference, as read from state.
type termPanelPrefs struct {
	Visible bool
	// Side is "bottom" or "right"; anything else reads as bottom, which is
	// what an unset key meant.
	Side string
	// Size is the percentage the PANEL occupied, 0 meaning the old default.
	Size int
}

// legacyTermPanelDefaultSize is the share the panel took when its key was
// unset.
const legacyTermPanelDefaultSize = 50

// termPanelSplitShape converts a legacy preference into the pane tree's
// vocabulary: bottom is a Rows split, right is a Columns split, and the panel's
// own percentage becomes the split's ratio INVERTED, because a split's ratio is
// its first child's share and the first child is the primary terminal — the
// panel is the second. Stating the complement anywhere else is how the two
// would disagree.
func termPanelSplitShape(prefs termPanelPrefs) (axis SplitAxis, ratio int) {
	axis = SplitRows
	if prefs.Side == "right" {
		axis = SplitCols
	}
	size := prefs.Size
	if size <= 0 {
		size = legacyTermPanelDefaultSize
	}
	return axis, clampPaneRatio(100 - size)
}

// migrateTermPanelIntoLayout splices the legacy panel into a persisted layout as a
// Shell leaf beside its terminal leaf, at the axis and ratio the preference
// names. A layout that already has a Shell leaf, a layout with no terminal leaf,
// and a preference that was not visible are all returned untouched: this
// conversion adds a panel that existed, it never invents one.
func migrateTermPanelIntoLayout(layout *state.PaneLayoutJSON, prefs termPanelPrefs) *state.PaneLayoutJSON {
	if layout == nil || !prefs.Visible || paneLayoutHasKind(layout, contentKindShell) {
		return layout
	}
	axis, ratio := termPanelSplitShape(prefs)
	axisName := "rows"
	if axis == SplitCols {
		axisName = "cols"
	}
	spliced, ok := spliceShellBesideTerminal(layout, axisName, ratio)
	if !ok {
		return layout
	}
	return spliced
}

// spliceShellBesideTerminal replaces the terminal leaf with a split of the
// terminal and a new shell leaf. The terminal stays the FIRST child, which is
// what makes the ratio above mean what it says.
func spliceShellBesideTerminal(node *state.PaneLayoutJSON, axis string, ratio int) (*state.PaneLayoutJSON, bool) {
	if node == nil {
		return nil, false
	}
	if node.Split == nil {
		if node.Kind != contentKindTerminal {
			return node, false
		}
		terminal := *node
		terminal.Root, terminal.Surface = "", ""
		wrapped := *node
		wrapped.Kind = ""
		wrapped.Split = &state.PaneSplitJSON{
			Axis:  axis,
			Ratio: ratio,
			A:     &terminal,
			B:     &state.PaneLayoutJSON{Kind: contentKindShell},
		}
		return &wrapped, true
	}
	if a, ok := spliceShellBesideTerminal(node.Split.A, axis, ratio); ok {
		wrapped := *node
		split := *node.Split
		split.A = a
		wrapped.Split = &split
		return &wrapped, true
	}
	if b, ok := spliceShellBesideTerminal(node.Split.B, axis, ratio); ok {
		wrapped := *node
		split := *node.Split
		split.B = b
		wrapped.Split = &split
		return &wrapped, true
	}
	return node, false
}

func paneLayoutHasKind(node *state.PaneLayoutJSON, kind string) bool {
	if node == nil {
		return false
	}
	if node.Split == nil {
		return node.Kind == kind
	}
	return paneLayoutHasKind(node.Split.A, kind) || paneLayoutHasKind(node.Split.B, kind)
}

// takeLegacyTermPanelPrefs reads the legacy keys once and clears them, so a
// panel converted into a layout cannot be converted into a second one on the
// next launch.
func (p *Plugin) takeLegacyTermPanelPrefs() termPanelPrefs {
	if p.legacyTermPanelTaken {
		return termPanelPrefs{}
	}
	p.legacyTermPanelTaken = true
	visible, side, size := state.LegacyTermPanel()
	prefs := termPanelPrefs{Visible: visible, Side: side, Size: size}
	if !visible && side == "" && size == 0 {
		return prefs
	}
	// The remembered shape outlives the conversion: it is what ctrl+t opens a
	// split at, including for a user whose panel was hidden at exit.
	p.shellSplitAxis, p.shellSplitRatio = termPanelSplitShape(prefs)
	_ = state.ClearLegacyTermPanel()
	return prefs
}
