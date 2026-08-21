package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// CloseButtonLabel is the glyph drawn on pane-header close buttons.
const CloseButtonLabel = "×"

// RenderCloseButton paints the shared header close control. It uses the same
// rest/hover styles as every other button so a pane X does not invent a third
// look.
func RenderCloseButton(hovered bool) string {
	hoverIdx := -1
	if hovered {
		hoverIdx = 0
	}
	// Header chrome keeps one cell of padding so the X stays a real click
	// target without the modal button's two-cell gutters, which would steal
	// the last tab's filename.
	return ResolveButtonStyle(-1, hoverIdx, 0).UnsetPadding().Padding(0, 1).Render(CloseButtonLabel)
}

// CloseButtonWidth is the columns the close control occupies at rest. Hover
// must not change it, or the tab strip would reflow under the pointer.
func CloseButtonWidth() int {
	return lipgloss.Width(RenderCloseButton(false))
}

// RenderTabCloseHover paints the per-tab × the pointer is over. It fills
// exactly width columns — the cells the × already occupies inside the pill —
// so the highlight replaces them without reflowing the strip. The look is the
// shared hover button, the same as the pane-header X, because they are the
// same control at two scales.
func RenderTabCloseHover(width int) string {
	if width < 1 {
		return ""
	}
	label := CloseButtonLabel
	if pad := width - lipgloss.Width(label); pad > 0 {
		label = strings.Repeat(" ", pad) + label
	}
	return ResolveButtonStyle(-1, 0, 0).UnsetPadding().Render(label)
}

// HeaderClose is the reserved right-edge close control on a pane header.
type HeaderClose struct {
	// TabsWidth is the columns left for the tab strip. Equals Width when the
	// button does not fit.
	TabsWidth int
	// CloseCol is the column of the button relative to the header origin.
	// -1 when the button is not drawn.
	CloseCol int
	CloseW   int
	Width    int
}

// ReserveHeaderClose keeps the close button whole on the right and gives the
// rest of the row to the tab strip. A row too narrow to hold the button draws
// no button: a clipped X is a target whose meaning cannot be recovered.
func ReserveHeaderClose(width int) HeaderClose {
	if width < 1 {
		return HeaderClose{CloseCol: -1}
	}
	btnW := CloseButtonWidth()
	if width < btnW {
		return HeaderClose{TabsWidth: width, CloseCol: -1, Width: width}
	}
	return HeaderClose{
		TabsWidth: width - btnW,
		CloseCol:  width - btnW,
		CloseW:    btnW,
		Width:     width,
	}
}

// ComposeHeaderClose joins an already-padded tab row to the close button.
// tabsRow must already be TabsWidth columns; the result is Width columns.
func ComposeHeaderClose(tabsRow string, width int, hovered bool) string {
	reserve := ReserveHeaderClose(width)
	if reserve.CloseW == 0 {
		return tabsRow
	}
	btn := RenderCloseButton(hovered)
	gap := reserve.CloseCol - lipgloss.Width(tabsRow)
	if gap < 0 {
		gap = 0
	}
	return tabsRow + strings.Repeat(" ", gap) + btn
}
