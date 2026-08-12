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

// terminalViewportLayout is the shared layout value. Links, search and the
// overlaid cursor are drawn on top of it here; where the drawn window *is* is
// one rule, shared with every other surface that embeds a terminal.
type terminalViewportLayout = tty.Viewport

type terminalViewportResult struct {
	Content string
	Layout  terminalViewportLayout
}

// viewport is the plugin's render inputs narrowed to the shared layout inputs.
// This surface always reserves the scrollbar column.
func (in terminalViewportInput) viewport() tty.ViewportInput {
	return tty.ViewportInput{
		Buffer:           in.Buffer,
		Width:            in.Width,
		Height:           in.Height,
		Offset:           in.Offset,
		OffsetFromBottom: in.OffsetFromBottom,
		Follow:           in.Follow,
		TrimTrailing:     in.TrimTrailing,
		Interactive:      in.Interactive,
		CursorRow:        in.CursorRow,
		CursorCol:        in.CursorCol,
		CursorVisible:    in.CursorVisible,
		PaneHeight:       in.PaneHeight,
		PaneWidth:        in.PaneWidth,
		AbsoluteBase:     in.AbsoluteBase,
		Scrollbar:        true,
	}
}

func calculateTerminalViewportLayout(in terminalViewportInput) terminalViewportLayout {
	return tty.FitViewport(in.viewport())
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
	if !shouldOverlayCursor(in.Interactive, in.CursorVisible, in.Follow) {
		return 0, 0, false
	}
	shared := in.viewport()
	return tty.ViewportCursor(tty.FitViewport(shared), shared)
}
