package ui

import (
	"sort"
	"strings"

	"github.com/marcus/sidecar/internal/mouse"
)

// Canvas composes pre-rendered blocks at absolute rectangles. A layout that
// already placed every block has one geometry; joining those blocks back
// together with per-row string arithmetic derives a second one, which is what
// makes a divider drift once the nesting is deeper than a single split.
//
// Blitted rectangles must not overlap: a block is clipped and padded to the
// rectangle it was given, but a later block landing inside an earlier one is
// dropped, because cutting a cell range out of already-composited ANSI would
// have to re-derive the styling that produced it.
type Canvas struct {
	rows [][]canvasSpan
	w, h int
}

// canvasSpan is one block's contribution to one row: exactly w cells of content
// starting at column x. open records that the block left an SGR attribute in
// effect, which the row must close before it writes anything else.
type canvasSpan struct {
	x, w int
	text string
	open bool
}

// FitBlock returns content as exactly h rows of exactly w cells, clipping and
// padding each row against ANSI cell widths. It is what a renderer that owes a
// caller a rectangle applies to its own output, so the rectangle is the block's
// answer rather than something a compositor imposes on it afterwards.
func FitBlock(content string, w, h int) string {
	if w <= 0 || h <= 0 {
		return ""
	}
	lines := strings.Split(content, "\n")
	rows := make([]string, h)
	for row := range rows {
		line := ""
		if row < len(lines) {
			line = lines[row]
		}
		rows[row] = fitLineWidth(line, w)
	}
	return strings.Join(rows, "\n")
}

// NewCanvas returns a canvas of w columns and h rows, blank until blitted onto.
func NewCanvas(w, h int) *Canvas {
	if w < 0 {
		w = 0
	}
	if h < 0 {
		h = 0
	}
	return &Canvas{rows: make([][]canvasSpan, h), w: w, h: h}
}

// Blit places content in box, in canvas-relative cells. Content lines are
// clipped and padded to the box's width and count, so a block that renders
// short or long cannot move the blocks beside it; a box that starts outside the
// canvas is dropped rather than clipped from the left, which would cut a block
// mid-escape-sequence. Tabs are the block's own business: every renderer that
// reaches a canvas has already expanded them against its own width, and
// expanding them again here would be a second authority on the same cells.
func (c *Canvas) Blit(box mouse.Rect, content string) {
	if c == nil || box.W <= 0 || box.H <= 0 || box.X < 0 || box.Y >= c.h {
		return
	}
	width := min(box.W, c.w-box.X)
	if width <= 0 {
		return
	}
	lines := strings.Split(content, "\n")
	for row := range box.H {
		y := box.Y + row
		if y < 0 {
			continue
		}
		if y >= c.h {
			break
		}
		line := ""
		if row < len(lines) {
			line = lines[row]
		}
		text := fitLineWidth(line, width)
		c.rows[y] = append(c.rows[y], canvasSpan{x: box.X, w: width, text: text, open: leavesStyleOpen(text)})
	}
}

// String renders the canvas as h rows of exactly w cells. A block that left a
// style open is closed at its own last cell: cell isolation is not isolation on
// its own, because an unclosed attribute paints the divider and every block to
// its right on that row.
func (c *Canvas) String() string {
	if c == nil || c.h == 0 {
		return ""
	}
	rows := make([]string, c.h)
	for y, spans := range c.rows {
		sort.SliceStable(spans, func(i, j int) bool { return spans[i].x < spans[j].x })
		var row strings.Builder
		col := 0
		for _, span := range spans {
			if span.x < col {
				continue
			}
			if span.x > col {
				row.WriteString(strings.Repeat(" ", span.x-col))
			}
			row.WriteString(span.text)
			if span.open {
				row.WriteString(ResetSequence)
			}
			col = span.x + span.w
		}
		if col < c.w {
			row.WriteString(strings.Repeat(" ", c.w-col))
		}
		rows[y] = row.String()
	}
	return strings.Join(rows, "\n")
}

// leavesStyleOpen reports whether text ends with an SGR attribute still in
// effect. A line that fit its box untouched keeps whatever it arrived with, and
// a line clipped to its box can lose the reset that closed it, so the state has
// to be read from the sequences themselves rather than assumed from the shape
// of the block.
//
// Only the reset closes an attribute here, so a line ending on one of SGR's
// per-attribute off codes reads as open. The bias is deliberate: a needless
// reset costs four bytes nobody sees, and a missed one paints the divider and
// every block to the right of it.
func leavesStyleOpen(text string) bool {
	open := false
	for i := 0; i < len(text); i++ {
		if text[i] != 0x1b || i+1 >= len(text) || text[i+1] != '[' {
			continue
		}
		end := i + 2
		for end < len(text) && (text[end] == ';' || text[end] == ':' || (text[end] >= '0' && text[end] <= '9')) {
			end++
		}
		if end < len(text) && text[end] == 'm' {
			open = sgrLeavesAttribute(text[i+2:end], open)
		}
		i = end
	}
	return open
}

// sgrLeavesAttribute applies one SGR sequence's parameters to the attribute
// state before it. Extended colours carry their components as further
// parameters, so those are stepped over rather than read as attributes of their
// own: a foreground colour of index 0 is not a reset.
func sgrLeavesAttribute(params string, open bool) bool {
	fields := strings.Split(params, ";")
	for i := 0; i < len(fields); i++ {
		switch fields[i] {
		case "38", "48", "58":
			open = true
			if i+1 < len(fields) {
				switch fields[i+1] {
				case "5":
					i += 2
				case "2":
					i += 4
				default:
					i++
				}
			}
		default:
			open = !isSGRReset(fields[i])
		}
	}
	return open
}

// isSGRReset reports whether one SGR parameter is the reset. An omitted
// parameter means the same thing, and leading zeros are still zero.
func isSGRReset(field string) bool {
	if field == "" {
		return true
	}
	return strings.Trim(field, "0") == ""
}
