// Package ui provides shared UI components and helpers for the TUI.
package ui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// DimStyle applies a dim gray color to background content behind modals.
// We strip existing ANSI codes and apply gray because SGR 2 (faint) doesn't
// reliably combine with existing color codes in most terminals.
//
// This is a function, not a var: styles.TextMuted is a package-level variable
// that ApplyTheme reassigns, so a var here would be evaluated at init — before
// any theme is applied — and every modal backdrop would keep dimming in the
// default theme's grey no matter which theme is active. See internal/themecheck.
func DimStyle() lipgloss.Style {
	return lipgloss.NewStyle().Foreground(styles.TextMuted)
}

// DimSequence and ResetSequence are the raw ANSI codes used by DimStyle.
// Exported for testing.
const (
	DimSequence     = "\x1b[2m"
	ResetSequence   = "\x1b[0m"
	overlayTabWidth = 4
)

// normalizeOverlayLine removes terminal-dependent tab expansion before any
// cell geometry is calculated, then clips and pads using ANSI-aware cell
// widths. Overlay callers commonly pass plugin views containing literal tabs;
// leaving them intact makes capture width disagree with the terminal and can
// split a modal border onto the following row.
func clipOverlayLine(line string, width int) string {
	if width <= 0 {
		return ""
	}
	line = strings.ReplaceAll(line, "\t", strings.Repeat(" ", overlayTabWidth))
	return ansi.Truncate(line, width, "")
}

func normalizeOverlayLine(line string, width int) string {
	return fitLineWidth(clipOverlayLine(line, width), width)
}

// fitLineWidth clips and pads one line to exactly width cells, counting cells
// rather than bytes. A line already that wide is returned untouched: the
// compositor's inputs are styled content, and re-truncating a line that fits
// would rewrite the escape sequences it arrived with.
func fitLineWidth(line string, width int) string {
	if width <= 0 {
		return ""
	}
	lineWidth := ansi.StringWidth(line)
	if lineWidth == width {
		return line
	}
	if lineWidth > width {
		line = ansi.Truncate(line, width, "")
		lineWidth = ansi.StringWidth(line)
	}
	if lineWidth < width {
		line += strings.Repeat(" ", width-lineWidth)
	}
	return line
}

// maxLineWidth returns the maximum visual width of the given lines.
func maxLineWidth(lines []string) int {
	maxWidth := 0
	for _, line := range lines {
		w := ansi.StringWidth(line)
		if w > maxWidth {
			maxWidth = w
		}
	}
	return maxWidth
}

// dimLine strips ANSI codes and applies dim gray styling.
func dimLine(s string, width int) string {
	return DimStyle().Render(normalizeOverlayLine(ansi.Strip(s), width))
}

// compositeRow overlays modalLine onto bgLine at position modalStartX.
// Returns: dimmed-left-segment + modalLine + dimmed-right-segment
func compositeRow(bgLine, modalLine string, modalStartX, modalWidth, totalWidth int) string {
	var result strings.Builder
	dim := DimStyle()

	// Expand tabs before measuring or slicing. Background styling is stripped
	// for consistent dimming; modal styling is retained.
	stripped := normalizeOverlayLine(ansi.Strip(bgLine), totalWidth)
	modalLine = normalizeOverlayLine(modalLine, modalWidth)
	bgWidth := ansi.StringWidth(stripped)

	// Left segment: dimmed background from 0 to modalStartX
	if modalStartX > 0 {
		// Use ansi.Truncate to get visual-width-based substring
		leftSeg := ansi.Truncate(stripped, modalStartX, "")
		leftWidth := ansi.StringWidth(leftSeg)
		result.WriteString(dim.Render(leftSeg))
		// Pad if background is shorter than modal position
		if leftWidth < modalStartX {
			result.WriteString(strings.Repeat(" ", modalStartX-leftWidth))
		}
	}

	// Modal content (not dimmed)
	result.WriteString(modalLine)

	// Right segment: dimmed background after modal
	rightStartX := modalStartX + modalWidth
	if rightStartX < totalWidth && bgWidth > rightStartX {
		// Use ansi.Cut to get visual-width-based substring from position
		rightSeg := ansi.Cut(stripped, rightStartX, bgWidth)
		result.WriteString(dim.Render(rightSeg))
	}

	return normalizeOverlayLine(result.String(), totalWidth)
}

// OverlayModal composites a modal on top of a dimmed background.
// The modal is centered, with dimmed background visible on all sides.
func OverlayModal(background, modal string, width, height int) string {
	if width < 0 {
		width = 0
	}
	if height < 0 {
		height = 0
	}
	bgLines := strings.Split(background, "\n")
	modalLines := strings.Split(modal, "\n")
	for i := range modalLines {
		modalLines[i] = clipOverlayLine(modalLines[i], width)
	}

	// Calculate modal dimensions and position
	modalWidth := maxLineWidth(modalLines)
	modalHeight := len(modalLines)
	if modalWidth > 0 && modalWidth+2 <= width {
		// A blank column either side. Without it the dimmed text behind the
		// modal runs straight into the box's border — "runes := []r╭───" reads
		// as two overlapping strings rather than as a box over a page — and the
		// gutter costs a column the box was never going to use. The box's own
		// position is unchanged: padding both sides moves the centre by nothing.
		for i := range modalLines {
			pad := modalWidth - ansi.StringWidth(modalLines[i])
			if pad < 0 {
				pad = 0
			}
			modalLines[i] = " " + modalLines[i] + strings.Repeat(" ", pad) + " "
		}
		modalWidth += 2
	}
	startX := (width - modalWidth) / 2
	startY := (height - modalHeight) / 2
	if startX < 0 {
		startX = 0
	}
	if startY < 0 {
		startY = 0
	}

	// Ensure we have enough background lines
	for len(bgLines) < height {
		bgLines = append(bgLines, "")
	}

	// Build result with compositing
	result := make([]string, 0, height)
	for y := 0; y < height; y++ {
		bgLine := ""
		if y < len(bgLines) {
			bgLine = bgLines[y]
		}

		modalRowIdx := y - startY
		if modalRowIdx >= 0 && modalRowIdx < modalHeight {
			// Composite: dimmed-left + modal + dimmed-right
			result = append(result, compositeRow(bgLine, modalLines[modalRowIdx], startX, modalWidth, width))
		} else {
			// Pure dimmed background (above or below modal)
			result = append(result, dimLine(bgLine, width))
		}
	}

	return strings.Join(result, "\n")
}
