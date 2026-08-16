package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/styles"
)

// HandleState is the pointer state of a 1-cell drag handle.
type HandleState int

const (
	HandleIdle HandleState = iota
	HandleHover
	HandleDrag
)

// TD divider hover/drag precedent: styles/td_renderers.go cyan / orange.
const (
	handleHoverColor = "#00BCD4"
	handleDragColor  = "#FF9800"
	// Keep idle handles subordinate to unfocused borders without dropping all
	// the way to the theme's muted-border contrast.
	handleIdleBorderMix = 0.30
)

const (
	handleBarV = "┃"
	handleBarH = "━"
)

// HandleStateFrom reports drag over hover over idle.
func HandleStateFrom(hovering, dragging bool) HandleState {
	if dragging {
		return HandleDrag
	}
	if hovering {
		return HandleHover
	}
	return HandleIdle
}

// RenderHandle draws a 1-cell-thick handle.
// vertical=true: length is height; false: length is width.
func RenderHandle(length int, vertical bool, state HandleState) string {
	if length <= 0 {
		return ""
	}
	bar := handleBarH
	if vertical {
		bar = handleBarV
	}
	var body string
	if vertical {
		// Keep the divider's allocated geometry stable while stopping the
		// visible bar one row short of the surrounding panels at each end.
		lines := make([]string, length)
		for i := range lines {
			if i == 0 || i == length-1 {
				lines[i] = " "
			} else {
				lines[i] = bar
			}
		}
		body = strings.Join(lines, "\n")
	} else if length == 1 {
		body = " "
	} else {
		// Horizontal handles follow the same inset rule without changing the
		// row width owned by split layout and hit testing.
		body = " " + strings.Repeat(bar, length-2) + " "
	}
	return handleStyle(state).Render(body)
}

// RenderDivider renders an idle vertical handle of height cells.
// Compatibility wrapper for callers that have not migrated to RenderHandle.
func RenderDivider(height int) string {
	return RenderHandle(height, true, HandleIdle)
}

func handleStyle(state HandleState) lipgloss.Style {
	theme := styles.GetCurrentTheme()
	color := lipgloss.Color(styles.Blend(theme.Colors.BorderMuted, theme.Colors.BorderNormal, handleIdleBorderMix))
	switch state {
	case HandleHover:
		color = lipgloss.Color(handleHoverColor)
	case HandleDrag:
		color = lipgloss.Color(handleDragColor)
	}
	return lipgloss.NewStyle().Foreground(color)
}
