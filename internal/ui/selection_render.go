package ui

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// AnsiResetRe matches ANSI reset sequences (both \x1b[0m and \x1b[m).
var AnsiResetRe = regexp.MustCompile(`\x1b\[0?m`)

// GetSelectionBgANSI returns the ANSI 24-bit background code for selection highlight
// based on the current theme's SelectionBg (falling back to BgTertiary).
func GetSelectionBgANSI() string {
	theme := styles.GetCurrentTheme()
	hex := theme.Colors.SelectionBg
	if hex == "" {
		hex = theme.Colors.BgTertiary
	}
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		r, g, b = 79, 89, 100
	}
	return fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
}

// InjectSelectionBackground adds a selection background to a whole fragment,
// surviving any background the fragment sets for itself, and fully resets at the
// end so the highlight cannot leak into whatever is appended after it.
func InjectSelectionBackground(s string) string {
	return injectRangeBackground(s, 0, -1, "\x1b[0m")
}

// ExpandTabs replaces tabs with spaces, preserving ANSI sequences and column widths.
func ExpandTabs(line string, tabWidth int) string {
	if tabWidth <= 0 || !strings.Contains(line, "\t") {
		return line
	}

	var sb strings.Builder
	sb.Grow(len(line))

	state := ansi.NormalState
	column := 0
	for len(line) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(line, state, nil)
		if n <= 0 {
			sb.WriteString(line)
			break
		}
		if seq == "\t" && width == 0 {
			spaces := tabWidth - (column % tabWidth)
			if spaces == 0 {
				spaces = tabWidth
			}
			sb.WriteString(strings.Repeat(" ", spaces))
			column += spaces
		} else {
			sb.WriteString(seq)
			column += width
		}
		state = newState
		line = line[n:]
	}

	return sb.String()
}

// VisualSubstring extracts a substring by visual column range [startCol, endCol).
// endCol is EXCLUSIVE (one past last included column).
// Handles ANSI escape codes (skipped in column counting).
// If endCol is -1, extracts to end of string.
// Returns plain text (ANSI stripped) for clipboard use.
func VisualSubstring(s string, startCol, endCol int) string {
	if s == "" {
		return ""
	}

	var sb strings.Builder
	state := ansi.NormalState
	cumWidth := 0

	remaining := s
	for len(remaining) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			break
		}
		if width > 0 {
			charStart := cumWidth
			charEnd := cumWidth + width
			cumWidth = charEnd

			// Check if this character is within range
			inRange := false
			if endCol == -1 {
				inRange = charEnd > startCol
			} else {
				inRange = charStart < endCol && charEnd > startCol
			}
			if inRange {
				sb.WriteString(seq)
			}
			if endCol >= 0 && cumWidth >= endCol {
				break
			}
		}
		// Skip ANSI sequences (width == 0, not a visible character)
		state = newState
		remaining = remaining[n:]
	}

	return sb.String()
}

// InjectCharacterRangeBackground applies selection background to visual columns
// [startCol, endCol] (inclusive) within the line. startCol and endCol are in
// absolute visual space (post-tab-expansion). Handles ANSI codes correctly.
// If endCol is -1, highlights to end of line.
func InjectCharacterRangeBackground(line string, startCol, endCol int) string {
	return injectRangeBackground(line, startCol, endCol, "")
}

// injectRangeBackground walks the line once, holding the selection background
// against anything the line does to its own background.
//
// Whole-line ranges used to take a shortcut that prepended the highlight and
// only re-applied it after a bare reset. Apps that paint each row with their own
// background (grok's panel styling) overrode it with their first SGR, so the
// middle lines of a multi-line selection lost their highlight entirely while the
// first and last — which took this walk — kept theirs.
//
// closeAtEOL is what to emit if the line ends inside the selection: callers
// highlighting a fragment that will be concatenated with other text need a full
// reset, while a rendered row restores its own background instead.
func injectRangeBackground(line string, startCol, endCol int, closeAtEOL string) string {
	selBg := GetSelectionBgANSI()
	var sb strings.Builder
	sb.Grow(len(line) + 64)

	state := ansi.NormalState
	cumWidth := 0
	inSelection := false
	// The background the line itself is carrying, to hand back when the
	// selection ends. Blanket \x1b[49m would drop a styled row's own background
	// for the text after the selection.
	lineBg := "\x1b[49m"

	remaining := line
	for len(remaining) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			sb.WriteString(remaining)
			break
		}

		if width > 0 {
			// Visible character
			charInRange := false
			if endCol == -1 {
				charInRange = cumWidth >= startCol
			} else {
				charInRange = cumWidth >= startCol && cumWidth <= endCol
			}

			if charInRange && !inSelection {
				sb.WriteString(selBg)
				inSelection = true
			} else if !charInRange && inSelection {
				sb.WriteString(lineBg)
				inSelection = false
			}

			sb.WriteString(seq)
			cumWidth += width

			// Check if we've passed the end of selection
			if endCol >= 0 && cumWidth > endCol && inSelection {
				sb.WriteString(lineBg)
				inSelection = false
			}
		} else {
			// ANSI sequence or control character
			sb.WriteString(seq)
			if bg, touches := sgrBackground(seq); touches {
				lineBg = bg
				// The line just set its own background, which would paint over the
				// highlight for the rest of the span.
				if inSelection {
					sb.WriteString(selBg)
				}
			}
		}

		state = newState
		remaining = remaining[n:]
	}

	if inSelection {
		if closeAtEOL != "" {
			sb.WriteString(closeAtEOL)
		} else {
			sb.WriteString(lineBg)
		}
	}

	return sb.String()
}

// sgrBackground reports whether an escape sequence changes the background and,
// if so, the minimal sequence that reproduces the background it leaves behind.
// Colour parameters consume their own arguments, so an SGR like 38;2;49;0;0
// (a foreground colour that happens to contain 49) is not read as a background
// reset.
func sgrBackground(seq string) (string, bool) {
	if !strings.HasPrefix(seq, "\x1b[") || !strings.HasSuffix(seq, "m") {
		return "", false
	}
	params := strings.Split(seq[2:len(seq)-1], ";")

	bg, touches := "", false
	for i := 0; i < len(params); i++ {
		param := params[i]
		// Colon-subparameter colours (48:2:…) arrive as one parameter.
		if base, _, ok := strings.Cut(param, ":"); ok {
			switch base {
			case "48":
				bg, touches = "\x1b["+param+"m", true
			case "38":
			}
			continue
		}
		switch param {
		case "", "0", "49":
			// A reset — bare, or compound as in \x1b[0;38;2;200;200;200m — puts the
			// background back to the terminal default.
			bg, touches = "\x1b[49m", true
		case "38", "48", "58":
			value, consumed := sgrColorParam(params, i)
			i += consumed
			if param == "48" {
				bg, touches = value, true
			}
		default:
			if code, err := strconv.Atoi(param); err == nil &&
				(code >= 40 && code <= 47 || code >= 100 && code <= 107) {
				bg, touches = "\x1b["+param+"m", true
			}
		}
	}
	return bg, touches
}

// SGRBackground reports whether an SGR sequence changes the background and
// returns the minimal sequence that reproduces the resulting background.
// Callers rendering ANSI owned by another terminal application use this to
// distinguish an explicit colour from the terminal default without duplicating
// the colour-parameter parser.
func SGRBackground(seq string) (string, bool) {
	return sgrBackground(seq)
}

// RowBackgroundDefault is the sequence that restores the terminal's default
// background. Rows are terminated with it so a background opened on one row
// cannot bleed into whatever is drawn after it.
const RowBackgroundDefault = "\x1b[49m"

// BlankInkDefault clears the attributes that render on a cell with no glyph:
// underline, blink, reverse video and strikethrough. Sidecar appends its own
// padding to rows that are shorter than the box they are drawn in, and those
// cells belong to Sidecar rather than to the child, so they must not inherit
// what the child's last row left switched on.
//
// It deliberately leaves bold, faint, italic and the foreground colour alone.
// Those only show where there is a glyph, so they cost nothing on padding, and
// clearing them here would clear them for the row below too: rows are joined
// into one stream and only the background is re-established per row, so the
// child's foreground and text attributes genuinely carry. Making rows fully
// independent means carrying the whole pen, not resetting it.
const BlankInkDefault = "\x1b[24;25;27;29m"

// CarryRowBackground makes one captured row self-contained.
//
// tmux emits `capture-pane -e` as a single continuous SGR stream and writes
// only the delta, so a row whose predecessor left a background active begins
// with that background still open and never repeats it. Sidecar renders rows
// independently — it slices them apart, truncates them, pads them and joins
// them into a styled surface — which loses that carried state and lets a
// background smear across cells that should not have it.
//
// It returns the row with the inherited background re-opened at its front, the
// background left active at its end (the caller's inherited value for the next
// row), and whether the row touches the background at all. A row that reports
// touched must be terminated with [RowBackgroundDefault] once the caller has
// finished truncating and padding it.
func CarryRowBackground(line, inherited string) (out, trailing string, touched bool) {
	trailing = inherited
	touched = inherited != ""
	state := ansi.NormalState
	remaining := line
	for len(remaining) > 0 {
		seq, _, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			break
		}
		if next, touches := sgrBackground(seq); touches {
			touched = true
			if next == RowBackgroundDefault {
				trailing = ""
			} else {
				trailing = next
			}
		}
		state = newState
		remaining = remaining[n:]
	}
	if inherited == "" {
		return line, trailing, touched
	}
	return inherited + line, trailing, touched
}

// ApplyTerminalDefaultBackground renders default-background cells with bg.
// Explicit application backgrounds still win; resets to the terminal default
// are followed by bg so embedding the row inside another styled surface cannot
// expose that surface's background.
func ApplyTerminalDefaultBackground(line, bg string, width int) string {
	if bg == "" {
		return line
	}
	var out strings.Builder
	out.Grow(len(line) + len(bg)*2)
	out.WriteString(bg)

	state := ansi.NormalState
	remaining := line
	for len(remaining) > 0 {
		seq, _, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			out.WriteString(remaining)
			break
		}
		out.WriteString(seq)
		if next, touches := sgrBackground(seq); touches && next == "\x1b[49m" {
			out.WriteString(bg)
		}
		state = newState
		remaining = remaining[n:]
	}
	if gap := width - ansi.StringWidth(line); gap > 0 {
		out.WriteString(bg)
		out.WriteString(BlankInkDefault)
		out.WriteString(strings.Repeat(" ", gap))
	}
	out.WriteString("\x1b[49m")
	return out.String()
}

// sgrColorParam rebuilds an extended colour parameter starting at params[i] (38
// or 48) and reports how many extra parameters it consumed, so the caller's walk
// skips its arguments rather than reading them as codes of their own.
func sgrColorParam(params []string, i int) (string, int) {
	if i+1 >= len(params) {
		return "\x1b[49m", 0
	}
	switch params[i+1] {
	case "5":
		if i+2 < len(params) {
			return "\x1b[" + strings.Join(params[i:i+3], ";") + "m", 2
		}
		return "\x1b[49m", len(params) - i - 1
	case "2":
		if i+4 < len(params) {
			return "\x1b[" + strings.Join(params[i:i+5], ";") + "m", 4
		}
		return "\x1b[49m", len(params) - i - 1
	}
	return "\x1b[49m", 1
}

// VisualColAtRelativeX takes an already-expanded line and a relative X offset,
// walks graphemes using ansi.GraphemeWidth.DecodeSequenceInString, snaps to
// character boundaries, and clamps to last char if beyond end.
func VisualColAtRelativeX(expandedLine string, relX int) int {
	if relX < 0 {
		return 0
	}

	visualCol := relX

	// Walk expanded line grapheme-by-grapheme to find column
	state := ansi.NormalState
	cumWidth := 0
	lastCharCol := 0
	hasChars := false

	remaining := expandedLine
	for len(remaining) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			break
		}
		_ = seq
		if width > 0 {
			hasChars = true
			// If visualCol lands within this character's cells
			if visualCol >= cumWidth && visualCol < cumWidth+width {
				return cumWidth // snap to start of character
			}
			lastCharCol = cumWidth
			cumWidth += width
		}
		state = newState
		remaining = remaining[n:]
	}

	if !hasChars {
		return 0
	}

	// Beyond line end: clamp to last character column (inclusive)
	if visualCol >= cumWidth {
		return lastCharCol
	}
	return visualCol
}
