package ui

import (
	"image/color"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// RowBackground paints one already-styled row with a uniform background,
// keeping every foreground style the row brought with it.
//
// This is the shared answer to a recurring sidecar bug: a list row is composed
// from pre-styled spans (a source hue, a link, a muted meta column), and each
// of those spans closes itself with an SGR reset — which clears the *whole*
// graphics state, background included. Wrapping such a row in
// `lipgloss.Style.Background(...)` therefore paints only up to the first inner
// span and leaves holes for the rest of the row. The historical workaround was
// to throw the styling away and render the selected row as plain text, which
// makes the selected row look different from every other row in the list.
//
// RowBackground walks the row once and re-asserts the background after
// anything that touches it — a bare or compound reset, an explicit `48;…`
// colour an inner span set for itself, a legacy 40-47/100-107 code. Foreground,
// bold and underline are left exactly as the row wrote them, so the selected
// row is the same row, highlighted.
//
// The row is truncated to width and then padded with background-coloured
// spaces to exactly width, so callers do not need a second padding step whose
// spaces would land outside the highlight. The result is terminated with a
// reset so the background cannot bleed into whatever is appended after it.
//
// Multi-line input is handled line by line; each line is independently
// truncated, padded and terminated.
func RowBackground(row string, width int, bg color.Color) string {
	if width <= 0 {
		return ""
	}
	return RowBackgroundSeq(row, width, styles.BgANSISeqFor(bg))
}

// RowBackgroundSeq is [RowBackground] for callers that already hold the
// background's escape sequence (a captured row, a cached theme colour). An
// empty sequence means "no background", and the row is returned untouched apart
// from truncation and padding.
func RowBackgroundSeq(row string, width int, bgSeq string) string {
	if width <= 0 {
		return ""
	}
	if !strings.Contains(row, "\n") {
		return rowBackgroundLine(row, width, bgSeq)
	}
	lines := strings.Split(row, "\n")
	for i, line := range lines {
		lines[i] = rowBackgroundLine(line, width, bgSeq)
	}
	return strings.Join(lines, "\n")
}

func rowBackgroundLine(line string, width int, bgSeq string) string {
	// Truncate first, with ansi.Truncate so styles that opened before the cut
	// are carried and closed correctly, then measure what is left to pad.
	line = ansi.Truncate(line, width, "")
	gap := width - ansi.StringWidth(line)
	if gap < 0 {
		gap = 0
	}
	if bgSeq == "" {
		return line + strings.Repeat(" ", gap)
	}

	var out strings.Builder
	out.Grow(len(line) + len(bgSeq)*4 + gap + 8)
	// Open with the background so unstyled leading content (indents, the
	// cursor column) is highlighted without depending on an enclosing style.
	out.WriteString(bgSeq)

	state := ansi.NormalState
	remaining := line
	for len(remaining) > 0 {
		seq, _, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			out.WriteString(remaining)
			break
		}
		out.WriteString(seq)
		// Anything that changes the background — a reset that cleared ours, or
		// a colour an inner span set for itself — is immediately overridden, so
		// every cell of the row carries the row background.
		if _, touches := sgrBackground(seq); touches {
			out.WriteString(bgSeq)
		}
		state = newState
		remaining = remaining[n:]
	}

	if gap > 0 {
		out.WriteString(strings.Repeat(" ", gap))
	}
	out.WriteString("\x1b[m")
	return out.String()
}

// SelectedRowBackground is [RowBackground] with the theme's list-selection
// background, which is what a list row's cursor highlight should use unless the
// surface has a reason to differ.
func SelectedRowBackground(row string, width int) string {
	return RowBackgroundSeq(row, width, GetSelectionBgANSI())
}
