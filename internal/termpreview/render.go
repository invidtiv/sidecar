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
	// DefaultBackground is the host terminal background used by child-default
	// cells. It is presentation context, not a Sidecar theme color.
	DefaultBackground string

	Layout tty.Viewport
	Buffer *tty.OutputBuffer
	// AbsoluteBase lifts a window line index into the coordinates a selection is
	// recorded in.
	AbsoluteBase int
	Selection    *ui.SelectionState

	// PaneHeight, Interactive and Follow describe the live grid behind the
	// window, which is what decides letterboxing and what the pane's canvas
	// background is measured over. A watched capture has no pane behind it.
	PaneHeight  int
	Interactive bool
	Follow      bool

	// TotalItems is the scrollbar's idea of how much there is to scroll through,
	// which can exceed the loaded buffer while older history is still unfetched.
	TotalItems int

	// BarStyle is the pointer emphasis the scrollbar draws with. The zero
	// value renders byte-identically to the idle bar.
	BarStyle ui.ScrollbarStyle

	TabWidth int
	// Truncate is the consumer's ANSI-aware truncation cache; nil uses
	// TruncateANSI.
	Truncate func(string, int) string

	// Message replaces the body when there is nothing to draw: no pane, an
	// ambiguous match, a failed capture. Multi-line messages are rendered as
	// written, so a caller can put the item's metadata under the reason.
	Message string

	// Decorate is the host's per-row decoration (links). Nil draws the row
	// undecorated.
	Decorate func(line string, absoluteLine int) string

	// Backgrounds selects how far child-carried backgrounds may reach (see
	// tty.BackgroundMode). Empty means auto. Child canvas detection runs only in
	// auto; the host default still resolves default-background cells in every
	// mode.
	Backgrounds tty.BackgroundMode
	// BackgroundSpanMax is the row cap for bounded mode; <= 0 uses the default.
	BackgroundSpanMax int

	// Analyzer is the terminal surface's bounded raw-row cache. Nil keeps the
	// rendering contract but does not reuse analysis across calls.
	Analyzer *RowAnalyzer

	// CloseButton reserves the shared header × on the right of the header row,
	// drawn hovered when CloseHovered. The button is reserved out of the header
	// row alone — the viewport under it is tmux geometry and keeps every column
	// it was sized for, the same rule the project surface reserves by.
	CloseButton  bool
	CloseHovered bool
}

// RenderHeader draws the one row above an embedded terminal — identity chips
// left, hints right — filled to exactly Width columns.
//
// It is separate from RenderBody because the header belongs to the frame that
// places the box, not to whatever the box is showing. A frame that draws every
// leaf's header itself is the only way every leaf's body can start on the same
// relative row, which is what HeaderRows states.
func RenderHeader(in RenderBufferInput) string {
	if in.Width < 1 || in.Height < 1 {
		return ""
	}
	truncate := in.Truncate
	if truncate == nil {
		truncate = TruncateANSI
	}
	if in.CloseButton {
		if reserve := ui.ReserveHeaderClose(in.Width); reserve.CloseW > 0 {
			row := fill(HeaderRow(in.Chips, in.Hints, reserve.TabsWidth, 0, truncate), reserve.TabsWidth, truncate)
			return ui.ComposeHeaderClose(row, in.Width, in.CloseHovered)
		}
	}
	return fill(HeaderRow(in.Chips, in.Hints, in.Width, 0, truncate), in.Width, truncate)
}

// RenderBody draws the window of a terminal buffer that sits under the header
// row: exactly Height-HeaderRows rows of exactly Width columns, or nothing when
// the box has no row to spare below its header.
//
// It renders no cursor. A host that owns one places it natively against the box
// this returns, using the same Layout — a cursor drawn here and a cursor placed
// there would be two answers to one question.
func RenderBody(in RenderBufferInput) string {
	width, height := in.Width, in.Height
	if width < 1 || height < 1 {
		return ""
	}
	body := height - HeaderRows
	if body < 1 {
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
		return strings.Join(padRows(lines, body, width), "\n")
	}

	contentWidth := max(layout.DisplayWidth, 1)
	backgrounds := tty.NormalizeBackgroundMode(in.Backgrounds)
	draw := DrawRows(RowsInput{
		Buffer:            in.Buffer,
		Layout:            layout,
		DefaultBackground: in.DefaultBackground,
		AbsoluteBase:      in.AbsoluteBase,
		TabWidth:          tabWidth,
		Selection:         in.Selection,
		Decorate:          in.Decorate,
		Truncate:          truncate,
		PaneHeight:        in.PaneHeight,
		Interactive:       in.Interactive,
		Follow:            in.Follow,
		Pad:               true,
		Backgrounds:       backgrounds,
		BackgroundSpanMax: in.BackgroundSpanMax,
		Analyzer:          in.Analyzer,
	})
	visible := draw.Rows

	if layout.ShowScrollbar {
		// JoinHorizontal aligns the scrollbar to the widest line of the block it is
		// joined to, so the content is padded to the exact viewport width first:
		// otherwise the bar renders after the longest line and creeps right as the
		// user types (td-26bdb2).
		visible = padRows(visible, body, max(layout.PadWidth, contentWidth))
		bar, _ := ui.RenderScrollbarWithState(ui.ScrollbarParams{
			TotalItems:   max(in.TotalItems, layout.EffectiveCount),
			ScrollOffset: layout.AbsoluteStart,
			VisibleItems: layout.DisplayHeight,
			TrackHeight:  body,
		}, in.BarStyle)
		joined := lipgloss.JoinHorizontal(lipgloss.Top,
			strings.Join(visible, "\n"),
			bar,
		)
		visible = strings.Split(joined, "\n")
	}
	return PadCanvasBox(strings.Join(visible, "\n"), draw.CanvasBackground, width, body, truncate)
}

// RenderBuffer draws a whole embedded terminal box: RenderHeader over
// RenderBody. It is kept as their composition for the surfaces that want the
// box in one call, so splitting the two halves costs its callers nothing; a
// frame that owns the header row calls the halves instead.
func RenderBuffer(in RenderBufferInput) string {
	header := RenderHeader(in)
	if header == "" {
		return ""
	}
	body := RenderBody(in)
	if body == "" {
		return header
	}
	return header + "\n" + body
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
