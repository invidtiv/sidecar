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
	if in.TotalItems > layout.DisplayHeight && layout.DisplayWidth > 1 {
		// The scrollbar owns the viewport's final column. A pane already sized
		// to the remaining content area fits as-is; subtracting again would clip
		// its own final column (td-e8bdcf).
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
	layout.EffectiveCount = lineCount
	if in.TrimTrailing {
		layout.EffectiveCount = in.Buffer.LastNonEmptyLine() + 1
	}
	layout.MaxOffset = max(layout.EffectiveCount-layout.DisplayHeight, 0)
	// Pane row 0, in buffer coordinates. Settled before the scroll window so the
	// clipped-follow anchor below and the cursor placement agree with it by
	// construction. A buffer that has never been told its split falls back to
	// its tail; PaneHeight of 0 means no geometry has been observed either, and
	// the visible window is treated as the pane once Start is known.
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
		layout.PaneTop = min(paneTop, layout.EffectiveCount)
	case in.PaneHeight > 0:
		layout.PaneTop = max(layout.EffectiveCount-in.PaneHeight, 0)
	}

	switch {
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
	layout.End = min(layout.Start+layout.DisplayHeight, layout.EffectiveCount)
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
	displayLines := make([]string, 0, max(len(lines), layout.DisplayHeight))
	for i, line := range lines {
		line = ui.ExpandTabs(line, tabStopWidth)
		line = decorateTerminalLinks(line)
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
		displayLines = append(displayLines, cache.Truncate(line, layout.DisplayWidth, ""))
	}

	// Letterboxing pads the pane out to its own height rather than stretching
	// it; a clipped pane already fills the viewport.
	if in.Interactive && in.PaneHeight > 0 {
		displayLines = padLinesToHeight(displayLines, layout.DisplayHeight)
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

func terminalViewportCursorPosition(in terminalViewportInput) (x, y int, ok bool) {
	layout := calculateTerminalViewportLayout(in)
	if in.Buffer == nil || layout.EffectiveCount == 0 ||
		!shouldOverlayCursor(in.Interactive, in.CursorVisible, in.Follow) ||
		layout.DisplayWidth <= 0 || layout.DisplayHeight <= 0 {
		return 0, 0, false
	}
	visibleRows := layout.End - layout.Start
	if in.Interactive && in.PaneHeight > 0 {
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
