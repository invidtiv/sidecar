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
	body := strings.Repeat(bar, length)
	if vertical {
		body = strings.TrimSuffix(strings.Repeat(bar+"\n", length), "\n")
	}
	return handleStyle(state).Render(body)
}

// RenderDivider renders an idle vertical handle of height cells.
// Compatibility wrapper for callers that have not migrated to RenderHandle.
func RenderDivider(height int) string {
	return RenderHandle(height, true, HandleIdle)
}

func handleStyle(state HandleState) lipgloss.Style {
	color := styles.BorderNormal
	switch state {
	case HandleHover:
		color = lipgloss.Color(handleHoverColor)
	case HandleDrag:
		color = lipgloss.Color(handleDragColor)
	}
	return lipgloss.NewStyle().Foreground(color)
}
