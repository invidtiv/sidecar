package docview

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/marcus/sidecar/internal/ui"
)

// wrapLine wraps one (possibly ANSI-styled) line to width at plain-text
// breakpoints and slices the original so styling survives.
func wrapLine(line string, width int) []string {
	if width < 1 {
		return []string{""}
	}

	expanded := ui.ExpandTabs(line, tabStopWidth)
	plain := ansi.Strip(expanded)
	wrappedPlain := cellbuf.Wrap(plain, width, "")
	plainSegments := strings.Split(wrappedPlain, "\n")

	wrapped := make([]string, 0, len(plainSegments))
	offset := 0
	for _, seg := range plainSegments {
		segWidth := ansi.StringWidth(seg)
		if segWidth == 0 {
			wrapped = append(wrapped, "")
			continue
		}
		slice := ansi.TruncateLeft(expanded, offset, "")
		slice = ansi.Truncate(slice, segWidth, "")
		wrapped = append(wrapped, slice)
		offset += segWidth
	}
	if len(wrapped) == 0 {
		return []string{""}
	}
	return wrapped
}
