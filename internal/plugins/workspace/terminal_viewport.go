package workspace

import (
	"strings"

	"charm.land/lipgloss/v2"
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
	if in.Buffer == nil || layout.DisplayWidth == 0 || layout.DisplayHeight == 0 {
		return layout
	}

	if in.Interactive {
		if in.PaneWidth > 0 && in.PaneWidth < layout.DisplayWidth {
			layout.DisplayWidth = in.PaneWidth
		}
		if in.PaneHeight > 0 && in.PaneHeight < layout.DisplayHeight {
			layout.DisplayHeight = in.PaneHeight
		}
	}
	if in.TotalItems > layout.DisplayHeight && layout.DisplayWidth > 1 {
		layout.DisplayWidth--
		layout.ShowScrollbar = true
	}

	layout.EffectiveCount = in.Buffer.LineCount()
	if in.TrimTrailing {
		layout.EffectiveCount = in.Buffer.LastNonEmptyLine() + 1
	}
	layout.MaxOffset = max(layout.EffectiveCount-layout.DisplayHeight, 0)

	switch {
	case in.Follow:
		layout.Start = layout.MaxOffset
	case in.OffsetFromBottom:
		layout.Start = layout.MaxOffset - min(max(in.Offset, 0), layout.MaxOffset)
	default:
		layout.Start = min(max(in.Offset, 0), layout.MaxOffset)
	}
	layout.End = min(layout.Start+layout.DisplayHeight, layout.EffectiveCount)
	layout.AbsoluteStart = in.AbsoluteBase + layout.Start
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
		displayLines = append(displayLines, cache.Truncate(line, layout.DisplayWidth, ""))
	}

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
	y = in.CursorRow
	if in.PaneHeight > visibleRows {
		y -= in.PaneHeight - visibleRows
	} else if in.PaneHeight > 0 && in.PaneHeight < visibleRows {
		y += visibleRows - in.PaneHeight
	}
	y = min(max(y, 0), visibleRows-1)
	x = min(max(in.CursorCol, 0), layout.DisplayWidth-1)
	return x, y, true
}
