package styles

import (
	"image/color"
	"strings"

	"charm.land/lipgloss/v2"
)

// resetSeq is the canonical SGR reset we normalize to before re-applying a
// background. Terminals treat "\x1b[m" and "\x1b[0m" identically.
const resetSeq = "\x1b[m"

// FillBackground ensures each line has a uniform background color.
// Inner styled elements emit an SGR reset that clears all attributes including
// the parent container's background, leaving terminal-default black for the
// remainder of the line. We fix this by re-applying the background ANSI
// sequence after every reset, then padding short lines with
// background-colored spaces.
//
// Both reset spellings must be handled: lipgloss v2 emits the implicit-zero
// form "\x1b[m", while other producers emit "\x1b[0m". Matching only the
// latter left every run after a nested styled element - most visibly this
// function's own padding - on the terminal default background, which is what
// made modals look splotchy.
func FillBackground(content string, width int, bgColor color.Color) string {
	if width <= 0 {
		return content
	}
	bgSeq := BgANSISeqFor(bgColor)
	if bgSeq == "" {
		return content
	}

	lines := strings.Split(content, "\n")
	for i, line := range lines {
		// Normalize both reset spellings, then re-apply the background after
		// every reset within the line.
		line = strings.ReplaceAll(line, "\x1b[0m", resetSeq)
		line = strings.ReplaceAll(line, resetSeq, resetSeq+bgSeq)

		// Open the line with the background too, so unstyled leading content
		// does not depend on the enclosing container having set one.
		line = bgSeq + line

		// Pad short lines to target width with background-colored spaces
		w := lipgloss.Width(line)
		if w < width {
			line += strings.Repeat(" ", width-w)
		}

		// Ensure clean reset at end of line so the fill cannot bleed past the
		// content width. Any padding the caller's own container adds after this
		// re-applies its own background.
		if !strings.HasSuffix(line, resetSeq) {
			line += resetSeq
		}

		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

// BgANSISeqFor extracts the raw ANSI escape sequence for the given background
// color by rendering a marker character and taking everything before it.
func BgANSISeqFor(bgColor color.Color) string {
	const marker = "\x01"
	s := lipgloss.NewStyle().Background(bgColor).Render(marker)
	idx := strings.Index(s, marker)
	if idx > 0 {
		return s[:idx]
	}
	return ""
}
