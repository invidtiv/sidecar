package panereposition

import (
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/ui"
)

// Movable reports whether a leaf of this tree can be repositioned at all.
//
// A tree with fewer than two leaves has nowhere to send one — PlanMove answers
// every destination on it with MoveUnchanged — and a surface drawing a pane
// header with no tree behind it has no leaf to move in the first place. Neither
// offers the control: a button that cannot act is a target the user aims at for
// nothing, and it makes the same header two different widths for a reason
// nothing on screen explains.
//
// Each host asks this once per frame and passes the answer to both its header
// renderer and its region sink, so the drawn glyph and its hit box can never
// disagree.
func Movable(root *panelayout.Node) bool { return panelayout.LeafCount(root) > 1 }

// HeaderReserve is the pane header's shared tab/layout/close geometry. The
// layout control is the first to drop as the pane narrows; close is retained
// until it cannot be drawn whole.
type HeaderReserve struct {
	TabsWidth int
	LayoutCol int
	LayoutW   int
	CloseCol  int
	CloseW    int
	Width     int
}

// ReserveHeader is the one feature-flag check for pane header chrome. Keymap
// availability is the other; hosts derive both painting and hit regions from
// this result instead of checking the flag independently.
//
// It assumes the header belongs to a leaf that can be moved. A surface drawing
// a pane header with no pane tree behind it has nothing to reposition and must
// call ReserveMovableHeader with movable=false instead.
func ReserveHeader(width int, closable bool) HeaderReserve {
	return ReserveMovableHeader(width, true, closable)
}

// ReserveMovableHeader is ReserveHeader for a header that may have no leaf.
// A control that cannot act is worse than an absent one: it occupies a target
// the user will aim at, and it makes the same header two different widths for
// reasons nothing on screen explains.
func ReserveMovableHeader(width int, movable, closable bool) HeaderReserve {
	controls := make([]ui.HeaderControl, 0, 2)
	layoutIndex, closeIndex := -1, -1
	if movable && features.IsEnabled(features.PaneMove.Name) {
		layoutIndex = len(controls)
		controls = append(controls, ui.HeaderControl{Width: ui.LayoutButtonWidth()})
	}
	if closable {
		closeIndex = len(controls)
		controls = append(controls, ui.HeaderControl{Width: ui.CloseButtonWidth()})
	}
	reserved := ui.ReserveHeaderControls(width, controls...)
	result := HeaderReserve{TabsWidth: reserved.TabsWidth, LayoutCol: -1, CloseCol: -1, Width: reserved.Width}
	if layoutIndex >= 0 {
		position := reserved.Controls[layoutIndex]
		result.LayoutCol, result.LayoutW = position.Col, position.Width
	}
	if closeIndex >= 0 {
		position := reserved.Controls[closeIndex]
		result.CloseCol, result.CloseW = position.Col, position.Width
	}
	return result
}

// ComposeHeader joins an already-padded tab row to the controls described by
// ReserveHeader. Hover never changes their measured width.
func ComposeHeader(tabsRow string, width int, closable, layoutHovered, closeHovered bool) string {
	return ComposeMovableHeader(tabsRow, width, true, closable, layoutHovered, closeHovered)
}

// ComposeMovableHeader is ComposeHeader for a header that may have no leaf to
// move. It must be given the same movable value its reserve was.
func ComposeMovableHeader(tabsRow string, width int, movable, closable, layoutHovered, closeHovered bool) string {
	controls := make([]string, 0, 2)
	reserve := ReserveMovableHeader(width, movable, closable)
	if reserve.LayoutW > 0 {
		controls = append(controls, ui.RenderLayoutButton(layoutHovered))
	}
	if reserve.CloseW > 0 {
		controls = append(controls, ui.RenderCloseButton(closeHovered))
	}
	return ui.ComposeHeaderControls(tabsRow, width, controls...)
}
