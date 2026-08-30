package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
)

// CloseButtonLabel is the glyph drawn on pane-header close buttons.
const CloseButtonLabel = "×"

// LayoutButtonLabel is the glyph drawn on pane-header layout buttons.
const LayoutButtonLabel = "⊞"

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

// RenderLayoutButton paints the shared header layout control. It deliberately
// uses the same padding and rest/hover styles as the close button so adding the
// second control does not invent another header vocabulary.
func RenderLayoutButton(hovered bool) string {
	hoverIdx := -1
	if hovered {
		hoverIdx = 0
	}
	return ResolveButtonStyle(-1, hoverIdx, 0).UnsetPadding().Padding(0, 1).Render(LayoutButtonLabel)
}

// LayoutButtonWidth is the columns the layout control occupies at rest.
func LayoutButtonWidth() int { return lipgloss.Width(RenderLayoutButton(false)) }

// HeaderControl is one whole right-edge control. Controls are supplied in
// drop order: the first control gives way first as the row narrows. The
// surviving controls retain their input order from left to right.
type HeaderControl struct {
	Width int
}

// HeaderControlReserve is one control's placement in a header row. Col is -1
// and Width is zero when the control was dropped.
type HeaderControlReserve struct {
	Col   int
	Width int
}

// HeaderControls is the result of reserving a tab strip plus zero or more
// whole right-edge controls.
type HeaderControls struct {
	TabsWidth int
	Controls  []HeaderControlReserve
	Width     int
}

// ReserveHeaderControls keeps every visible control whole. When they do not
// all fit, controls drop in argument order until the remainder fits; this is
// what lets the layout button yield before the close button on narrow panes.
func ReserveHeaderControls(width int, controls ...HeaderControl) HeaderControls {
	result := HeaderControls{Controls: make([]HeaderControlReserve, len(controls))}
	for i := range result.Controls {
		result.Controls[i].Col = -1
	}
	if width < 1 {
		return result
	}
	result.Width = width
	first := 0
	total := 0
	for _, control := range controls {
		total += max(control.Width, 0)
	}
	for first < len(controls) && total > width {
		total -= max(controls[first].Width, 0)
		first++
	}
	result.TabsWidth = width - total
	col := result.TabsWidth
	for i := first; i < len(controls); i++ {
		controlWidth := max(controls[i].Width, 0)
		if controlWidth == 0 {
			continue
		}
		result.Controls[i] = HeaderControlReserve{Col: col, Width: controlWidth}
		col += controlWidth
	}
	return result
}

// ComposeHeaderControls joins an already-padded tab row to rendered controls.
// Controls must be in the same drop order used to reserve the tab row.
func ComposeHeaderControls(tabsRow string, width int, controls ...string) string {
	specs := make([]HeaderControl, len(controls))
	for i, control := range controls {
		specs[i].Width = lipgloss.Width(control)
	}
	reserve := ReserveHeaderControls(width, specs...)
	row := tabsRow
	for i, control := range controls {
		position := reserve.Controls[i]
		if position.Width == 0 {
			continue
		}
		gap := position.Col - lipgloss.Width(row)
		if gap > 0 {
			row += strings.Repeat(" ", gap)
		}
		row += control
	}
	return row
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
	reserved := ReserveHeaderControls(width, HeaderControl{Width: CloseButtonWidth()})
	close := HeaderControlReserve{Col: -1}
	if len(reserved.Controls) > 0 {
		close = reserved.Controls[0]
	}
	return HeaderClose{
		TabsWidth: reserved.TabsWidth,
		CloseCol:  close.Col,
		CloseW:    close.Width,
		Width:     reserved.Width,
	}
}

// ComposeHeaderClose joins an already-padded tab row to the close button.
// tabsRow must already be TabsWidth columns; the result is Width columns.
func ComposeHeaderClose(tabsRow string, width int, hovered bool) string {
	return ComposeHeaderControls(tabsRow, width, RenderCloseButton(hovered))
}
