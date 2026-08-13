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
// starting at column x.
type canvasSpan struct {
	x, w int
	text string
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
		c.rows[y] = append(c.rows[y], canvasSpan{x: box.X, w: width, text: fitLineWidth(line, width)})
	}
}

// String renders the canvas as h rows of exactly w cells.
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
			col = span.x + span.w
		}
		if col < c.w {
			row.WriteString(strings.Repeat(" ", c.w-col))
		}
		rows[y] = row.String()
	}
	return strings.Join(rows, "\n")
}
