package panereposition

import (
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/ui"
)

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
func ReserveHeader(width int, closable bool) HeaderReserve {
	controls := make([]ui.HeaderControl, 0, 2)
	layoutIndex, closeIndex := -1, -1
	if features.IsEnabled(features.PaneMove.Name) {
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
	controls := make([]string, 0, 2)
	reserve := ReserveHeader(width, closable)
	if reserve.LayoutW > 0 {
		controls = append(controls, ui.RenderLayoutButton(layoutHovered))
	}
	if reserve.CloseW > 0 {
		controls = append(controls, ui.RenderCloseButton(closeHovered))
	}
	return ui.ComposeHeaderControls(tabsRow, width, controls...)
}
