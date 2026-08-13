package termpreview

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// RowsInput is one drawn window of a captured buffer: which lines, where they
// sit in the buffer's own coordinates, and the decoration each host adds on top.
type RowsInput struct {
	Buffer *tty.OutputBuffer
	Layout tty.Viewport
	// AbsoluteBase lifts a window line index into the coordinates a selection,
	// a search match and a link are all recorded in.
	AbsoluteBase int

	TabWidth  int
	Selection *ui.SelectionState

	// Decorate is the host's own per-row decoration — links, search matches —
	// applied after tab expansion and before the selection highlight, so a
	// highlight always wins the cells it covers. Nil draws the row undecorated.
	Decorate func(line string, absoluteLine int) string

	// Truncate is the host's ANSI-aware truncation cache; nil uses TruncateANSI.
	Truncate func(string, int) string

	// PaneHeight, Interactive and Follow decide letterboxing and name the live
	// grid the canvas background is measured over.
	PaneHeight  int
	Interactive bool
	Follow      bool

	// Pad right-pads every drawn row to the window width. A host that draws the
	// rows into a filled box wants it; one that joins them against its own
	// chrome does not.
	Pad bool
}

// DrawRows renders the window RowsInput describes into drawn rows.
//
// Every rule that decides what a row looks like lives here — carried
// backgrounds, tab stops, decoration, the selection highlight, horizontal
// offset, truncation, letterboxing and the pane's canvas background — because a
// surface that reimplements any one of them draws the same buffer differently
// from every other surface showing it.
//
// It draws no cursor and no scrollbar: those are placed against the box a host
// puts these rows in, and a second answer here would contradict the first.
func DrawRows(in RowsInput) []string {
	layout := in.Layout
	if in.Buffer == nil || layout.EffectiveCount == 0 {
		return nil
	}
	truncate := in.Truncate
	if truncate == nil {
		truncate = TruncateANSI
	}
	tabWidth := in.TabWidth
	if tabWidth <= 0 {
		tabWidth = tty.DefaultTabWidth
	}
	contentWidth := layout.DisplayWidth

	lines := in.Buffer.LinesRange(layout.Start, layout.End)
	canvasBg := CanvasBackground(in.Buffer, layout.PaneTop, in.PaneHeight)
	inheritedBg := inheritedRowBackground(in.Buffer, layout.Start)
	drawn := make([]string, 0, max(len(lines), layout.DisplayHeight))
	for i, line := range lines {
		var openBg bool
		line, inheritedBg, openBg = ui.CarryRowBackground(line, inheritedBg)
		line = ui.ExpandTabs(line, tabWidth)
		absoluteLine := in.AbsoluteBase + layout.Start + i
		if in.Decorate != nil {
			line = in.Decorate(line, absoluteLine)
		}
		if in.Selection != nil && in.Selection.HasSelection() {
			startCol, endCol := in.Selection.GetLineSelectionCols(absoluteLine)
			if startCol >= 0 {
				line = ui.InjectCharacterRangeBackground(line, startCol, endCol)
			}
		}
		if layout.Fit.ColOffset > 0 {
			line = ansi.TruncateLeft(line, layout.Fit.ColOffset, "")
		}
		line = truncate(line, contentWidth)
		// Truncation can cut inside a background span, and the padding that
		// follows appends unstyled cells, so a row that touches the background
		// closes it here rather than letting it run into the next row.
		if openBg {
			line += ui.RowBackgroundDefault
		}
		drawn = append(drawn, line)
	}

	if Letterboxed(in.PaneHeight, in.Interactive, in.Follow) {
		for len(drawn) < layout.DisplayHeight {
			drawn = append(drawn, "")
		}
	}
	if canvasBg != "" {
		for i, line := range drawn {
			drawn[i] = ui.ApplyTerminalDefaultBackground(line, canvasBg, contentWidth)
		}
	}
	if in.Pad {
		for i, line := range drawn {
			if gap := contentWidth - ansi.StringWidth(line); gap > 0 {
				drawn[i] = line + strings.Repeat(" ", gap)
			}
		}
	}
	return drawn
}

// Letterboxed reports whether the drawn rows are padded out to the viewport.
//
// A live grid wants it: tmux strips trailing blank rows, so a short capture
// would otherwise shift the chrome below it every time the pane emptied. A
// scrollback window does not — there is nothing below its last line. Passive
// follow and interactive answer the same because both show the live pane.
func Letterboxed(paneHeight int, interactive, follow bool) bool {
	return paneHeight > 0 && (interactive || follow)
}

// CanvasBackground recognizes the background carried across a substantial share
// of a fullscreen TUI's live rows. tmux renders later default-background cells
// correctly in a real terminal because that terminal's default matches the
// canvas. Inside Sidecar those cells otherwise fall through to the surrounding
// surface and form rectangular seams.
//
// It is measured over the live grid, so a window with no pane behind it — a
// watched capture, a scrollback-only view — has no canvas to find.
func CanvasBackground(buffer *tty.OutputBuffer, paneTop, paneHeight int) string {
	if buffer == nil || paneTop < 0 || paneHeight <= 0 {
		return ""
	}
	rows := buffer.LinesRange(paneTop, paneTop+paneHeight)
	if len(rows) == 0 {
		return ""
	}
	counts := make(map[string]int)
	blankRows := make(map[string]int)
	inherited := inheritedRowBackground(buffer, paneTop)
	for _, row := range rows {
		// Counting the row as tmux would render it, not as it was captured: a
		// canvas is emitted once and then carried, so without re-opening the
		// inherited background only the first row of the canvas would vote.
		resolved, next, _ := ui.CarryRowBackground(row, inherited)
		inherited = next
		blank := strings.TrimSpace(ansi.Strip(resolved)) == ""
		for bg := range rowBackgrounds(resolved) {
			counts[bg]++
			if blank {
				blankRows[bg]++
			}
		}
	}
	canvas, best := "", 0
	for bg, count := range counts {
		if count > best {
			canvas, best = bg, count
		} else if count == best {
			canvas = ""
		}
	}
	if canvas == "" || best < CanvasRowShare(len(rows)) || blankRows[canvas] == 0 {
		return ""
	}
	return canvas
}

// CanvasRowShare is how many of the observed rows a background must cover to be
// the pane's canvas rather than highlighting drawn on top of one.
//
// A canvas is on every row by definition — it is the surface the application
// paints onto — so this is deliberately near-total rather than a simple
// majority. Measured against live panes: Grok's canvas covers 43 of 43 and 56
// of 56 rows, while a Claude Code diff's added-line green covers 19 of 55. An
// earlier one-third rule sat directly between those two, so scrolling a long
// diff by a single row flipped it and repainted the whole pane green.
func CanvasRowShare(rows int) int {
	return max(2, rows*9/10)
}

// rowBackgroundLookback bounds how far back the inherited background is
// resolved. tmux only ever re-emits a background when it changes, so in
// principle the search runs to the top of the scrollback; bounding it keeps
// render cost independent of history size, and a background that has survived
// this many rows unchanged is a canvas whose first row is off-screen anyway.
const rowBackgroundLookback = 300

// inheritedRowBackground resolves the background left active by the rows above
// start, which is what start's first cell is actually drawn in.
func inheritedRowBackground(buffer *tty.OutputBuffer, start int) string {
	if buffer == nil || start <= 0 {
		return ""
	}
	from := max(start-rowBackgroundLookback, 0)
	bg := ""
	for _, row := range buffer.LinesRange(from, start) {
		_, bg, _ = ui.CarryRowBackground(row, bg)
	}
	return bg
}

func rowBackgrounds(row string) map[string]struct{} {
	backgrounds := make(map[string]struct{})
	state := ansi.NormalState
	remaining := row
	for len(remaining) > 0 {
		seq, _, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			break
		}
		if next, touches := ui.SGRBackground(seq); touches && next != ui.RowBackgroundDefault {
			backgrounds[next] = struct{}{}
		}
		state = newState
		remaining = remaining[n:]
	}
	return backgrounds
}
