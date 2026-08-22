package workspace

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/termpreview"
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

	// Backgrounds selects how far carried backgrounds may reach (see
	// tty.BackgroundMode). Empty means auto.
	Backgrounds       tty.BackgroundMode
	BackgroundSpanMax int
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

// terminalWindowInput is this surface's one construction of a terminal's
// window: which buffer it draws, how big the surface is, and where the window
// sits inside it. The render path, hit testing and the native cursor must
// resolve a screen row to the same buffer row, so none of them may build a
// second one — every field here moves EffectiveCount, MaxOffset or Start.
//
// Decoration is the caller's: a selection, links and search matches change what
// a row looks like, never which row it is.
func (p *Plugin) terminalWindowInput(termPanel bool, buffer *tty.OutputBuffer, width, height int) terminalViewportInput {
	interactive := p.interactiveDescribes(termPanel)
	in := terminalViewportInput{
		Buffer:            buffer,
		Width:             width,
		Height:            height,
		Interactive:       interactive,
		TrimTrailing:      tty.TrimsTrailingRows(interactive),
		Backgrounds:       p.backgrounds,
		BackgroundSpanMax: p.backgroundSpanMax,
	}
	in.PaneWidth, in.PaneHeight = p.resolvedPaneGeometry(termPanel, interactive)
	if interactive {
		row, col, _, _, visible, _ := p.getCursorPosition()
		in.CursorRow, in.CursorCol, in.CursorVisible = row, col, visible
	}
	// The scrollbar takes a column from the content, and tracked history moves
	// where the window's rows sit in the buffer's coordinates (td-73fa86).
	in.AbsoluteBase, in.TotalItems, in.LoadingOlder = p.terminalHistorySummary(termPanel, buffer)
	in.Follow, in.Offset, in.OffsetFromBottom = p.terminalScrollState(termPanel)
	return in
}

func calculateTerminalViewportLayout(in terminalViewportInput) terminalViewportLayout {
	return tty.FitViewport(in.viewport())
}

// terminalWindowBound is how far back a named surface's window can be placed,
// taken from the window the render path actually draws. Both of this plugin's
// terminal surfaces route their bound through it, and it is the shared rule
// that answers — a second derivation beside the drawn layout is how the primary
// surface and the panel came to disagree with their own renderers (td-bbbbfe).
func (p *Plugin) terminalWindowBound(termPanel bool) int {
	return tty.WindowBound(p.terminalWindowInputFor(termPanel).viewport())
}

func renderTerminalViewport(in terminalViewportInput, cache *ui.TruncateCache) terminalViewportResult {
	layout := calculateTerminalViewportLayout(in)
	if in.Buffer == nil || layout.EffectiveCount == 0 {
		return terminalViewportResult{Layout: layout}
	}

	displayLines := termpreview.DrawRows(termpreview.RowsInput{
		Buffer:            in.Buffer,
		Layout:            layout,
		AbsoluteBase:      in.AbsoluteBase,
		TabWidth:          tabStopWidth,
		Selection:         in.Selection,
		Decorate:          in.decorate,
		Truncate:          func(line string, width int) string { return cache.Truncate(line, width, "") },
		PaneHeight:        in.PaneHeight,
		Interactive:       in.Interactive,
		Follow:            in.Follow,
		Backgrounds:       in.Backgrounds,
		BackgroundSpanMax: in.BackgroundSpanMax,
	})

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
	var canvasBg string
	if tty.NormalizeBackgroundMode(in.Backgrounds) == tty.BackgroundAuto {
		canvasBg = termpreview.CanvasBackground(in.Buffer, layout.PaneTop, in.PaneHeight)
	}
	if canvasBg != "" {
		content = termpreview.PadCanvasBox(content, canvasBg, in.Width, in.Height,
			func(line string, width int) string { return cache.Truncate(line, width, "") })
	}
	return terminalViewportResult{
		Content: content,
		Layout:  layout,
	}
}

// decorate is this surface's own per-row decoration: activatable links and
// search matches, neither of which the browser surface has.
func (in terminalViewportInput) decorate(line string, absoluteLine int) string {
	line = decorateTerminalLinks(line, in.LinkResolver)
	if in.SearchMatches != nil {
		for _, match := range in.SearchMatches.Items {
			if match.Line == absoluteLine {
				line = ui.InjectCharacterRangeBackground(line, match.StartCol, match.EndCol)
			}
		}
	}
	return line
}

func terminalViewportCursorPosition(in terminalViewportInput) (x, y int, ok bool) {
	if !tty.ShouldOverlayCursor(in.Interactive, in.CursorVisible, in.Follow) {
		return 0, 0, false
	}
	shared := in.viewport()
	return tty.ViewportCursor(tty.FitViewport(shared), shared)
}
