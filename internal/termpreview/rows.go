package termpreview

import (
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/terminalperf"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// RowsInput is one drawn window of a captured buffer: which lines, where they
// sit in the buffer's own coordinates, and the decoration each host adds on top.
type RowsInput struct {
	Buffer *tty.OutputBuffer
	Layout tty.Viewport
	// DefaultBackground is the host terminal's background SGR. Cells for which
	// the child selected the terminal default must resolve to this color, just
	// as they do when the child runs directly in the host terminal.
	DefaultBackground string
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

	// Backgrounds selects how far carried backgrounds may reach (see
	// tty.BackgroundMode). Empty means auto.
	Backgrounds tty.BackgroundMode
	// BackgroundSpanMax is the row cap enforced when Backgrounds is bounded;
	// <= 0 uses tty.DefaultBackgroundSpanMax.
	BackgroundSpanMax int

	// Pad right-pads every drawn row to the window width. A host that draws the
	// rows into a filled box wants it; one that joins them against its own
	// chrome does not.
	Pad bool

	// Analyzer owns collision-safe raw ANSI facts for this terminal surface.
	// Nil is correct but ephemeral; live hosts keep one so unchanged rows survive
	// accepted buffer revisions without another ANSI walk.
	Analyzer *RowAnalyzer
}

// DrawResult is the shared answer every terminal host composes. CanvasBackground
// belongs to the same bounded analysis that produced Rows, so an outer renderer
// never needs to walk the live grid again.
type DrawResult struct {
	Rows             []string
	CanvasBackground string
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
func DrawRows(in RowsInput) DrawResult {
	layout := in.Layout
	if in.Buffer == nil || layout.EffectiveCount == 0 {
		return DrawResult{}
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

	terminalperf.Record(terminalperf.TerminalViewRendered)
	backgrounds := tty.NormalizeBackgroundMode(in.Backgrounds)
	spanMax := in.BackgroundSpanMax
	if spanMax <= 0 {
		spanMax = tty.DefaultBackgroundSpanMax
	}
	analyzer := in.Analyzer
	if analyzer == nil {
		analyzer = &RowAnalyzer{}
	}
	analysis := analyzer.analyze(in, backgrounds, spanMax)
	canvasBg := in.DefaultBackground
	if backgrounds == tty.BackgroundAuto {
		if inferred := inferCanvas(analysis.live); inferred != "" {
			// A child-owned full-pane canvas is an explicit override of the host
			// default. Inset bubbles and highlights never pass inferCanvas, so
			// they retain the host fallback around their own colored cells.
			canvasBg = inferred
		}
	}
	// tailWidth is the last column that still belongs to a captured pane row.
	// tmux trims each row's trailing blank cells but keeps the SGR change that
	// applied to them, so the row's own trailing background is what those cells
	// were; rebuilding them here is reconstruction, not inference. Columns past
	// this belong to the viewport around a letterboxed pane, not to the row.
	tailWidth := contentWidth + layout.Fit.ColOffset
	if layout.Fit.LetterboxedWidth && layout.Fit.Width > 0 {
		tailWidth = layout.Fit.Width + layout.Fit.ColOffset
	}
	bandLen := analysis.visiblePredecessorBand
	drawn := make([]string, 0, max(len(analysis.visible), layout.DisplayHeight))
	for i, row := range analysis.visible {
		line, touchedBg := row.wire, row.touched
		// Band accounting reads the source stream, not the stripped output: a
		// run keeps counting through suppressed rows so one long wall cannot
		// masquerade as several short spans. A row belongs to the band when it
		// paints anything — sets its own background even if it closes the row
		// with 0m, or carries one in from the row above.
		if touchedBg || row.trailing != "" {
			bandLen++
		} else {
			bandLen = 0
		}
		switch {
		case backgrounds == tty.BackgroundNever:
			line = row.backgroundFree
			touchedBg = false
		case backgrounds == tty.BackgroundBounded && bandLen > spanMax:
			line = row.backgroundFree
			touchedBg = false
		}
		line = ui.ExpandTabs(line, tabWidth)
		// Restore the cells tmux trimmed off the end of this row before anything
		// reads the row's shape. Without them the row's trailing background is
		// only a carry into the next row, so the colour lands one row late and
		// the cells it belonged to fall through to whatever pads them.
		//
		// Only a row tmux described at all can be rebuilt this way. A row it
		// emitted no bytes for is either a wholly blank row of the carried
		// colour or a wholly blank default row, and the trimmed capture spells
		// both `""`; filling it would paint every blank separator between two
		// coloured blocks. Those rows keep going to the canvas, which is the
		// answer built for exactly that ambiguity. A row that carries only an
		// SGR change is described: tmux trimmed its blanks but told us what
		// colour they were.
		if touchedBg && row.trailing != "" && row.described {
			width := row.visibleWidth
			if row.hasTab {
				width = ansi.StringWidth(line)
			}
			if gap := tailWidth - width; gap > 0 {
				line += row.trailing + strings.Repeat(" ", gap)
			}
		}
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
		if touchedBg {
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
	return DrawResult{Rows: drawn, CanvasBackground: canvasBg}
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
//
// Covering the rows is not enough on its own: a candidate must also open a
// majority of them. A canvas owns column 0 of the rows it paints; an inset
// block — a chat bubble, a callout — never does, however many of a sparsely
// painted pane's rows it happens to cover. See CanvasRowShare for the coverage
// bar and inferCanvas for how the two rules divide the work.
func CanvasBackground(buffer *tty.OutputBuffer, paneTop, paneHeight int) string {
	if buffer == nil || paneTop < 0 || paneHeight <= 0 {
		return ""
	}
	layout := tty.Viewport{Start: paneTop, End: paneTop + paneHeight, EffectiveCount: paneHeight, PaneTop: paneTop}
	analyzer := &RowAnalyzer{}
	analysis := analyzer.analyze(RowsInput{Buffer: buffer, Layout: layout, PaneHeight: paneHeight}, tty.BackgroundAuto, tty.DefaultBackgroundSpanMax)
	return inferCanvas(analysis.live)
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
