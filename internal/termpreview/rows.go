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
	fillWidth := contentWidth
	if layout.PadWidth > fillWidth {
		fillWidth = layout.PadWidth
	}

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
			drawn[i] = ui.ApplyTerminalDefaultBackground(line, canvasBg, fillWidth)
		}
	}
	if in.Pad {
		padTo := contentWidth
		if canvasBg != "" {
			padTo = fillWidth
		}
		for i, line := range drawn {
			if gap := padTo - ansi.StringWidth(line); gap > 0 {
				drawn[i] = line + strings.Repeat(" ", gap)
			}
		}
	}
	return drawn
}

// PadCanvasBox makes content exactly height rows of width columns. When bg is
// set, default-background cells and unused rows/columns take that canvas so a
// capture shorter or narrower than the allotted box cannot expose the
// surrounding surface. When bg is empty the box is padded with unstyled spaces.
func PadCanvasBox(content, bg string, width, height int, truncate ...func(string, int) string) string {
	if width < 1 || height < 1 {
		return ""
	}
	cut := TruncateANSI
	if len(truncate) > 0 && truncate[0] != nil {
		cut = truncate[0]
	}
	var lines []string
	if content != "" {
		lines = strings.Split(content, "\n")
	}
	out := make([]string, height)
	for i := range out {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		if bg == "" {
			out[i] = fill(line, width, cut)
			continue
		}
		// Default-bg cells are painted in DrawRows. This only grows the
		// allotted box: unused columns on an already-drawn row, and unused
		// rows below the capture. Re-walking SGR here doubled canvas/49m
		// sequences and broke the panel/default contract (td-5d79ba).
		if line == "" {
			out[i] = ui.ApplyTerminalDefaultBackground("", bg, width)
			continue
		}
		if gap := width - ansi.StringWidth(line); gap > 0 {
			line += bg + strings.Repeat(" ", gap) + ui.RowBackgroundDefault
		}
		out[i] = line
	}
	return strings.Join(out, "\n")
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
	type painted struct {
		bgs   map[string]struct{}
		first string
		blank bool
	}
	resolved := make([]painted, 0, len(rows))
	inherited := inheritedRowBackground(buffer, paneTop)
	for _, row := range rows {
		// Counting the row as tmux would render it, not as it was captured: a
		// canvas is emitted once and then carried, so without re-opening the
		// inherited background only the first row of the canvas would vote.
		text, next, _ := ui.CarryRowBackground(row, inherited)
		inherited = next
		resolved = append(resolved, painted{
			bgs:   rowBackgrounds(text),
			first: firstCellBackground(text),
			blank: strings.TrimSpace(ansi.Strip(text)) == "",
		})
	}
	// Trailing default-background blanks are unused cells after a resize, not
	// a vote against the canvas that is still on the rows the TUI painted.
	last := len(resolved) - 1
	for last >= 0 && resolved[last].blank && len(resolved[last].bgs) == 0 {
		last--
	}
	if last < 0 {
		return ""
	}
	measured := resolved[:last+1]

	// Interior rows without any background abstain rather than vote against:
	// tmux stores cells an application never touched as default-attribute, and
	// a real terminal draws those in its own default — which is the colour the
	// application matched to its canvas through OSC 11. Counting them against
	// the canvas made detection flip with every partial repaint (an opencode
	// pane that had filled only some of its rows), and the flip itself was the
	// visible inconsistency: the pane alternated between the canvas and the
	// surrounding surface as the TUI redrew.
	counts := make(map[string]int)
	blankRows := make(map[string]int)
	firstCell := make(map[string]int)
	overlap := make(map[string]int)
	paintedRowCount := 0
	for _, row := range measured {
		if len(row.bgs) == 0 {
			continue
		}
		paintedRowCount++
		if row.first != "" {
			firstCell[row.first]++
		}
		for bg := range row.bgs {
			counts[bg]++
			if row.blank {
				blankRows[bg]++
			}
			if len(row.bgs) > 1 {
				overlap[bg]++
			}
		}
	}
	canvas, best, tied := "", 0, false
	for bg, count := range counts {
		if count > best {
			canvas, best, tied = bg, count, false
		} else if count == best {
			tied = true
		}
	}
	// A full-height panel rides every row the canvas does — opencode's side
	// panel spans the grid, so both backgrounds count every row and the vote
	// ties. The canvas is the background the rows are drawn *in*: it owns each
	// row's first cell, either painted there or carried in from the row above,
	// while a panel opens mid-row. Ties go to the candidate that owns the most
	// row starts; a tie on that too means there is no single canvas to find.
	if tied {
		canvas = ""
		bestFirst := 0
		for bg, count := range counts {
			if count != best {
				continue
			}
			if firstCell[bg] > bestFirst {
				canvas, bestFirst = bg, firstCell[bg]
			} else if firstCell[bg] == bestFirst {
				canvas = ""
			}
		}
	}
	// The share is measured against the rows that carry a background. A
	// highlight drawn on top of a canvas (a Claude Code diff's added-line
	// green) covers content rows only, so it never reaches the blank-row
	// requirement below; the near-total bar keeps a bare majority of painted
	// rows from promoting a panel colour.
	if canvas == "" || paintedRowCount == 0 || best < CanvasRowShare(paintedRowCount) {
		return ""
	}
	// A blank row in the canvas is the usual proof it is a canvas and not
	// highlighting — but the screen model's serialization closes every row and
	// trims BCE tails, so the same opencode pane that proves itself in a raw
	// capture comes back with no blank canvas rows at all, and the pane
	// flickered between the two answers as the model took over from the first
	// raw frame (td-fb5a9d). The fallback evidence is structural: a canvas owns
	// the first cell of nearly every painted row (the TUI's own margin) and has
	// other backgrounds drawn on top of it — on the same rows, which is what a
	// box riding on a canvas looks like. A highlight has neither: a diff's
	// green owns no margins, and its red deletions sit beside the green rows,
	// never on them, so a second background elsewhere in the pane is no
	// evidence at all. The co-occurrence bar is a quarter of the canvas's own
	// rows, not a majority: a message-heavy screen is mostly bare margin rows
	// with a box only around the input (measured live at 5 of 11), while
	// line-level highlighting co-occurs with nothing, ever.
	if blankRows[canvas] == 0 &&
		(firstCell[canvas] < CanvasRowShare(paintedRowCount) ||
			overlap[canvas] < max(2, counts[canvas]/4)) {
		return ""
	}
	return canvas
}

// CanvasRowShare is how many of the rows that carry a background a candidate
// must cover to be the pane's canvas rather than highlighting drawn on top of
// one. Rows without any background are abstentions (see CanvasBackground), so
// the share is measured over the painted ones.
//
// The bar is deliberately near-total rather than a simple majority. Measured
// against live panes: Grok's canvas covers 43 of 43 and 56 of 56 rows, while a
// Claude Code diff's added-line green covered 19 of 55. An earlier one-third
// rule sat directly between those two, so scrolling a long diff by a single row
// flipped it and repainted the whole pane green.
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

// firstCellBackground is the background the row's first cell is drawn in: the
// last background set before any printable content. A row with no printable
// content answers with whatever background it leaves active.
func firstCellBackground(row string) string {
	bg := ""
	state := ansi.NormalState
	remaining := row
	for len(remaining) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			break
		}
		if next, touches := ui.SGRBackground(seq); touches {
			if next == ui.RowBackgroundDefault {
				bg = ""
			} else {
				bg = next
			}
		} else if width > 0 {
			return bg
		}
		state = newState
		remaining = remaining[n:]
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
