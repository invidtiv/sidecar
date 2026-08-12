package termpreview

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// RenderBufferInput describes one embedded terminal box: the header row's chips
// and hints, the box size, and the window of a captured buffer to draw in it.
//
// Layout is passed in rather than computed here because it is the same value the
// host hit-tests against. A second derivation is how a click ends up landing on a
// different cell than the one the user aimed at.
type RenderBufferInput struct {
	Width, Height int
	Chips         []string
	Hints         string

	Layout tty.Viewport
	Buffer *tty.OutputBuffer
	// AbsoluteBase lifts a window line index into the coordinates a selection is
	// recorded in.
	AbsoluteBase int
	Selection    *ui.SelectionState

	// Letterbox pads the drawn rows out to the viewport height. A live grid wants
	// it — tmux strips trailing blank rows, and a short capture would otherwise
	// shift the chrome below — while a scrollback window does not.
	Letterbox bool

	// TotalItems is the scrollbar's idea of how much there is to scroll through,
	// which can exceed the loaded buffer while older history is still unfetched.
	TotalItems int

	TabWidth int
	// Truncate is the consumer's ANSI-aware truncation cache; nil uses
	// TruncateANSI.
	Truncate func(string, int) string

	// Message replaces the body when there is nothing to draw: no pane, an
	// ambiguous match, a failed capture. Multi-line messages are rendered as
	// written, so a caller can put the item's metadata under the reason.
	Message string
}

// RenderBuffer draws a window of a terminal buffer into a fixed box: one header
// row, then exactly Height-HeaderRows body rows of exactly Width columns.
//
// It renders no cursor. A host that owns one places it natively against the box
// this returns, using the same Layout — a cursor drawn here and a cursor placed
// there would be two answers to one question.
func RenderBuffer(in RenderBufferInput) string {
	width, height := in.Width, in.Height
	if width < 1 || height < 1 {
		return ""
	}
	truncate := in.Truncate
	if truncate == nil {
		truncate = TruncateANSI
	}
	tabWidth := in.TabWidth
	if tabWidth <= 0 {
		tabWidth = tty.DefaultTabWidth
	}
	header := fill(HeaderRow(in.Chips, in.Hints, width, 0, truncate), width, truncate)

	body := height - HeaderRows
	if body < 1 {
		return header
	}

	layout := in.Layout
	if in.Message != "" || in.Buffer == nil || layout.Rows() < 1 {
		message := in.Message
		if message == "" {
			message = "No output captured"
		}
		lines := make([]string, 0, body)
		for _, line := range strings.Split(message, "\n") {
			lines = append(lines, fill(line, width, truncate))
		}
		return strings.Join(append([]string{header}, padRows(lines, body, width)...), "\n")
	}

	contentWidth := max(layout.DisplayWidth, 1)
	rows := in.Buffer.LinesRange(layout.Start, layout.End)
	visible := make([]string, 0, max(len(rows), body))
	for i, line := range rows {
		line = ui.ExpandTabs(line, tabWidth)
		if in.Selection != nil && in.Selection.HasSelection() {
			startCol, endCol := in.Selection.GetLineSelectionCols(in.AbsoluteBase + layout.Start + i)
			if startCol >= 0 {
				line = ui.InjectCharacterRangeBackground(line, startCol, endCol)
			}
		}
		if layout.Fit.ColOffset > 0 {
			line = ansi.TruncateLeft(line, layout.Fit.ColOffset, "")
		}
		visible = append(visible, fill(line, contentWidth, truncate))
	}
	if in.Letterbox {
		visible = padRows(visible, min(layout.DisplayHeight, body), contentWidth)
	}

	if layout.ShowScrollbar {
		// JoinHorizontal aligns the scrollbar to the widest line of the block it is
		// joined to, so the content is padded to the exact viewport width first:
		// otherwise the bar renders after the longest line and creeps right as the
		// user types (td-26bdb2).
		visible = padRows(visible, body, max(layout.PadWidth, contentWidth))
		joined := lipgloss.JoinHorizontal(lipgloss.Top,
			strings.Join(visible, "\n"),
			ui.RenderScrollbar(ui.ScrollbarParams{
				TotalItems:   max(in.TotalItems, layout.EffectiveCount),
				ScrollOffset: layout.AbsoluteStart,
				VisibleItems: layout.DisplayHeight,
				TrackHeight:  body,
			}),
		)
		visible = strings.Split(joined, "\n")
	}
	for i, line := range visible {
		visible[i] = fill(line, width, truncate)
	}

	return strings.Join(append([]string{header}, padRows(visible, body, width)...), "\n")
}

// fill truncates a line to width and right-pads it, so every rendered row is
// exactly width columns of background.
func fill(line string, width int, truncate func(string, int) string) string {
	if width < 1 {
		return ""
	}
	line = ui.ExpandTabs(line, tty.DefaultTabWidth)
	if ansi.StringWidth(line) > width {
		line = truncate(line, width)
	}
	if gap := width - ansi.StringWidth(line); gap > 0 {
		line += strings.Repeat(" ", gap)
	}
	return line
}

func padRows(lines []string, target, width int) []string {
	for len(lines) < target {
		lines = append(lines, strings.Repeat(" ", max(width, 0)))
	}
	return lines[:target]
}
