package workspace

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// terminalViewportInput contains every value needed to lay out and render a
// captured terminal buffer. It is deliberately value-only: rendering must not
// mutate plugin or interactive state.
type terminalViewportInput struct {
	Buffer *tty.OutputBuffer
	Width  int
	Height int

	// Offset is absolute from the top unless OffsetFromBottom is set.
	Offset           int
	OffsetFromBottom bool
	Follow           bool
	TrimTrailing     bool

	Interactive   bool
	Selection     *ui.SelectionState
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	PaneHeight    int
	PaneWidth     int
	NativeCursor  bool
	AbsoluteBase  int
	TotalItems    int
	LoadingOlder  bool
	SearchMatches *terminalSearchMatches
	LinkResolver  *terminalLineLinkResolver
}

type terminalViewportLayout struct {
	Start          int
	End            int
	EffectiveCount int
	DisplayWidth   int
	DisplayHeight  int
	MaxOffset      int
	AbsoluteStart  int
	ShowScrollbar  bool

	// PadWidth is the column the content block is padded to before a scrollbar
	// is joined to it. It tracks the viewport rather than the pane so the
	// scrollbar stays at the viewport's edge.
	PadWidth int

	// Fit records how the pane's observed geometry was projected onto the
	// viewport: letterboxed when the pane is smaller, clipped (with ColOffset
	// as the first visible column) when it is larger.
	Fit tty.PaneFit

	// PaneClipped reports that the pane itself does not fit the viewport, as
	// opposed to Fit.ClippedWidth, which also trips when the scrollbar takes a
	// column off an otherwise perfectly sized pane.
	PaneClipped bool

	// PaneTop is the buffer index of pane row 0, so a rendered row maps back to
	// the pane row tmux would report for it, and so the native cursor lands on
	// the row it belongs to.
	//
	// It comes from the buffer, which was told the split by whoever published
	// the content — a screen-model frame knows its loaded history rows and its
	// grid height at the instant it is built, and a capture knows its own row
	// count and the pane height read with it. Both alternatives were tried and
	// both drift. The absolute-coordinate form (history_size + cursor_row -
	// buffer base) mixes two independently observed quantities: display-message
	// and capture-pane are separate writes, so N lines can scroll into history
	// between them and the cursor is drawn N rows too high until the pane is
	// re-seeded. Re-deriving it as "the buffer's last PaneHeight lines" instead
	// assumes a serialization detail that does not hold: a grid whose final row
	// is blank ends in a newline that reads as a terminator, the buffer is a row
	// short, and the cursor is drawn one row too high (td-d29821). Absolute
	// coordinates are still what scrollback, search, and selection use, where a
	// transient off-by-N is harmless.
	PaneTop int
}

// paneRowAt maps a 0-indexed rendered row to a 0-indexed pane row.
func (l terminalViewportLayout) paneRowAt(relY int) int {
	return l.Start + relY - l.PaneTop
}

type terminalViewportResult struct {
	Content string
	Layout  terminalViewportLayout
}

func calculateTerminalViewportLayout(in terminalViewportInput) terminalViewportLayout {
	layout := terminalViewportLayout{
		DisplayWidth:  max(in.Width, 0),
		DisplayHeight: max(in.Height, 0),
	}
	if layout.DisplayWidth == 0 || layout.DisplayHeight == 0 {
		return layout
	}

	// Project the pane's observed geometry onto the viewport (td-73fa86). The
	// pane can be any size — another sidecar instance may own the session — so
	// the requested size is only a request.
	fit := tty.FitPane(tty.PaneFitInput{
		ViewWidth:  layout.DisplayWidth,
		ViewHeight: layout.DisplayHeight,
		PaneWidth:  in.PaneWidth,
		PaneHeight: in.PaneHeight,
		CursorCol:  in.CursorCol,
		HasCursor:  in.Interactive && in.CursorVisible,
	})
	layout.DisplayWidth = fit.Width
	// A shorter pane only letterboxes while the viewport mirrors the live pane.
	// Outside interactive mode the viewport is a scrollback window, so the extra
	// rows show more history rather than stretching the pane.
	if !in.Interactive && fit.LetterboxedHeight {
		fit.Height = layout.DisplayHeight
		fit.LetterboxedHeight = false
	}
	layout.DisplayHeight = fit.Height
	layout.PadWidth = layout.DisplayWidth
	// Record the pane-vs-viewport verdict before the scrollbar steals a column:
	// losing a column to chrome is not a geometry mismatch and must not read as
	// one (td-73fa86).
	layout.PaneClipped = fit.Clipped()
	if layout.DisplayWidth > 1 {
		// The scrollbar owns the viewport's final column even when all content
		// fits; RenderScrollbar draws a spacer in that state. Keeping the chrome
		// stable prevents a newly published frame from clipping the application's
		// last column while the corresponding tmux resize is still in flight
		// (td-0818ef). A pane already sized to the remaining content area fits
		// as-is; subtracting again would clip its own final column (td-e8bdcf).
		contentWidth := max(in.Width, 0) - 1
		layout.DisplayWidth = min(layout.DisplayWidth, contentWidth)
		fit = fit.WithWidth(layout.DisplayWidth, in.PaneWidth, in.CursorCol, in.Interactive && in.CursorVisible)
		// Keep the scrollbar pinned to the viewport edge even when a narrower
		// pane letterboxes the content.
		layout.PadWidth = max(layout.DisplayWidth, contentWidth)
		layout.ShowScrollbar = true
	}
	layout.Fit = fit
	// Geometry is settled above so hit testing can ask for it without a buffer;
	// only the scroll window needs one.
	if in.Buffer == nil {
		return layout
	}

	lineCount, paneTop, paneKnown := in.Buffer.PaneWindow()
	// Pane row 0, in buffer coordinates. Settled against the full line count
	// before any trailing-blank trim: the producer split describes the live
	// grid including blank final rows (td-d29821).
	//
	// Between a resize and the capture that follows it, paneTop still describes
	// the old pane height while in.PaneHeight is already the new one, so the
	// cursor sits off by the delta for one poll. That is the accepted cost of
	// taking the split from the producer: the alternative — inferring it from
	// the buffer's tail — was self-consistent across a resize but wrong whenever
	// the grid's last row was blank, which is every prompt at the bottom of a
	// screen rather than the instant after a drag (td-d29821).
	switch {
	case paneKnown:
		layout.PaneTop = min(paneTop, lineCount)
	case in.PaneHeight > 0:
		layout.PaneTop = max(lineCount-in.PaneHeight, 0)
	}

	// Live-grid follow: geometry is known and we are pinned to the live edge.
	// Full-screen programs (Grok, Claude, …) keep intentional blank rows in the
	// pane. Trimming them shrinks EffectiveCount so MaxOffset walks Start up
	// into history — painting the previous bottom chrome under the header until
	// interactive mode (which never trims) re-aligns it.
	liveGridFollow := in.Follow && (paneKnown || in.PaneHeight > 0)

	layout.EffectiveCount = lineCount
	if in.TrimTrailing && !liveGridFollow {
		layout.EffectiveCount = max(in.Buffer.LastNonEmptyLine()+1, 0)
		if layout.PaneTop > layout.EffectiveCount {
			layout.PaneTop = layout.EffectiveCount
		}
	}
	layout.MaxOffset = max(layout.EffectiveCount-layout.DisplayHeight, 0)

	switch {
	case liveGridFollow:
		// Pin to the live grid rather than MaxOffset of a (possibly trimmed)
		// effective count. When the pane is taller than the viewport, show its
		// bottom; when shorter, show from PaneTop and let render pad.
		paneRows := lineCount - layout.PaneTop
		if paneRows <= layout.DisplayHeight {
			layout.Start = layout.PaneTop
		} else {
			layout.Start = max(lineCount-layout.DisplayHeight, layout.PaneTop)
		}
	case in.Follow:
		layout.Start = layout.MaxOffset
	case in.OffsetFromBottom:
		layout.Start = layout.MaxOffset - min(max(in.Offset, 0), layout.MaxOffset)
	default:
		layout.Start = min(max(in.Offset, 0), layout.MaxOffset)
	}
	// A pane taller than the viewport is clipped, so pin the window to the
	// cursor when following: the live row matters more than the pane's last
	// row, which is usually blank padding below it (td-73fa86).
	// Gated on interactive mode, which is the only state that carries a cursor
	// row at all; ClippedHeight implies observed geometry, so PaneTop is already
	// set. Deliberately not gated on the cursor being *visible*: a full-screen
	// app that hides it (less, some TUIs) still has a live row, and anchoring on
	// the pane's tail instead would clip exactly what the user is looking at.
	if fit.ClippedHeight && in.Follow && in.Interactive {
		cursorLine := layout.PaneTop + in.CursorRow
		layout.Start = min(layout.Start, max(cursorLine-layout.DisplayHeight+1, 0))
	}
	// Live-grid follow reads from the full buffer so blank final pane rows stay
	// addressable; scrollback browsing still ends at EffectiveCount after trim.
	endBound := layout.EffectiveCount
	if liveGridFollow {
		endBound = lineCount
	}
	layout.End = min(layout.Start+layout.DisplayHeight, endBound)
	layout.AbsoluteStart = in.AbsoluteBase + layout.Start
	if !paneKnown && in.PaneHeight <= 0 {
		layout.PaneTop = layout.Start
	}
	return layout
}

func renderTerminalViewport(in terminalViewportInput, cache *ui.TruncateCache) terminalViewportResult {
	layout := calculateTerminalViewportLayout(in)
	if in.Buffer == nil || layout.EffectiveCount == 0 {
		return terminalViewportResult{Layout: layout}
	}

	lines := in.Buffer.LinesRange(layout.Start, layout.End)
	canvasBg := terminalCanvasBackground(in.Buffer, layout.PaneTop, in.PaneHeight)
	inheritedBg := inheritedRowBackground(in.Buffer, layout.Start)
	displayLines := make([]string, 0, max(len(lines), layout.DisplayHeight))
	for i, line := range lines {
		var openBg bool
		line, inheritedBg, openBg = ui.CarryRowBackground(line, inheritedBg)
		line = ui.ExpandTabs(line, tabStopWidth)
		line = decorateTerminalLinks(line, in.LinkResolver)
		absoluteLine := in.AbsoluteBase + layout.Start + i
		if in.SearchMatches != nil {
			for _, match := range in.SearchMatches.Items {
				if match.Line == absoluteLine {
					line = ui.InjectCharacterRangeBackground(line, match.StartCol, match.EndCol)
				}
			}
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
		line = cache.Truncate(line, layout.DisplayWidth, "")
		// Truncation can cut inside a background span, and the padding that
		// follows appends unstyled cells, so a row that touches the background
		// closes it here rather than letting it run into the next row.
		if openBg {
			line += ui.RowBackgroundDefault
		}
		displayLines = append(displayLines, line)
	}

	// Letterboxing pads the live grid out to the viewport rather than leaving
	// a short capture (tmux strips trailing blank rows) to shift chrome. Same
	// rule for passive follow and interactive: both show the live pane.
	if in.PaneHeight > 0 && (in.Interactive || in.Follow) {
		displayLines = padLinesToHeight(displayLines, layout.DisplayHeight)
	}
	if canvasBg != "" {
		for i, line := range displayLines {
			displayLines[i] = ui.ApplyTerminalDefaultBackground(line, canvasBg, layout.DisplayWidth)
		}
	}

	if !in.NativeCursor {
		if x, y, ok := terminalViewportCursorPosition(in); ok && y < len(displayLines) {
			displayLines[y] = tty.RenderCursorLine(displayLines[y], x, true)
		}
	}

	content := strings.Join(displayLines, "\n")
	if layout.ShowScrollbar {
		displayLines = padLinesToHeight(displayLines, layout.DisplayHeight)
		// JoinHorizontal aligns the scrollbar to the widest line of the block it
		// is joined to. Terminal lines are truncated but never padded, so without
		// this the scrollbar renders immediately after the longest line — right
		// after the prompt on a fresh shell — and creeps right as the user types
		// (td-26bdb2). Padding to the exact content width pins it to the edge and
		// keeps the joined block from exceeding the pane and wrapping.
		displayLines = padLinesToWidth(displayLines, layout.PadWidth)
		content = lipgloss.JoinHorizontal(lipgloss.Top,
			strings.Join(displayLines, "\n"),
			ui.RenderScrollbar(ui.ScrollbarParams{
				TotalItems:   max(in.TotalItems, layout.EffectiveCount),
				ScrollOffset: layout.AbsoluteStart,
				VisibleItems: layout.DisplayHeight,
				TrackHeight:  layout.DisplayHeight,
			}),
		)
	}
	return terminalViewportResult{
		Content: content,
		Layout:  layout,
	}
}

// terminalCanvasBackground recognizes the background carried across a
// substantial share of a fullscreen TUI's live rows. tmux renders later
// default-background cells correctly in a real terminal because that terminal's
// default matches the canvas. Inside Sidecar those cells otherwise fall through
// to the surrounding plugin surface and form rectangular seams.
func terminalCanvasBackground(buffer *tty.OutputBuffer, paneTop, paneHeight int) string {
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
	if canvas == "" || best < canvasRowShare(len(rows)) || blankRows[canvas] == 0 {
		return ""
	}
	return canvas
}

// canvasRowShare is how many of the observed rows a background must cover to be
// the pane's canvas rather than highlighting drawn on top of one.
//
// A canvas is on every row by definition — it is the surface the application
// paints onto — so this is deliberately near-total rather than a simple
// majority. Measured against live panes: Grok's canvas covers 43 of 43 and 56
// of 56 rows, while a Claude Code diff's added-line green covers 19 of 55. An
// earlier one-third rule sat directly between those two, so scrolling a long
// diff by a single row flipped it and repainted the whole pane green.
func canvasRowShare(rows int) int {
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
		if next, touches := ui.SGRBackground(seq); touches && next != "\x1b[49m" {
			backgrounds[next] = struct{}{}
		}
		state = newState
		remaining = remaining[n:]
	}
	return backgrounds
}

func terminalViewportCursorPosition(in terminalViewportInput) (x, y int, ok bool) {
	layout := calculateTerminalViewportLayout(in)
	if in.Buffer == nil || layout.EffectiveCount == 0 ||
		!shouldOverlayCursor(in.Interactive, in.CursorVisible, in.Follow) ||
		layout.DisplayWidth <= 0 || layout.DisplayHeight <= 0 {
		return 0, 0, false
	}
	visibleRows := layout.End - layout.Start
	if in.PaneHeight > 0 && (in.Interactive || in.Follow) {
		visibleRows = layout.DisplayHeight
	}
	if visibleRows <= 0 {
		return 0, 0, false
	}
	// Pane row 0 is a buffer index; the cursor is CursorRow rows below it, and
	// the scroll offset turns that into a rendered row. This is the same PaneTop
	// hit testing maps clicks through, so the drawn cursor and the pane row tmux
	// reports for it can never disagree (td-d29821).
	y = layout.PaneTop + in.CursorRow - layout.Start
	if y < 0 || y >= visibleRows {
		return 0, 0, false
	}
	x = min(max(in.CursorCol-layout.Fit.ColOffset, 0), layout.DisplayWidth-1)
	return x, y, true
}
