package tty

import (
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
)

// DefaultTabWidth is the tab stop every terminal surface expands against.
const DefaultTabWidth = 8

// Cell is a position in a captured terminal buffer: a line index in whichever
// coordinate space that buffer keeps, and a visual column after tab expansion.
type Cell struct {
	Line int
	Col  int
}

// Geometry places a terminal surface's drawn content on the screen, which is
// everything hit testing needs to know about it. Content is the absolute rect of
// the content area, so the first text cell sits at its origin — a host with
// chrome (a border, a header) subtracts that before building one. Start and End
// are the buffer lines drawn in it, and ColOffset is the pane column drawn at
// Content.X when a pane wider than the viewport is clipped.
type Geometry struct {
	Content   mouse.Rect
	Start     int
	End       int
	ColOffset int
	TabWidth  int
}

// GeometryFor places a drawn window on screen. The origin is the host's — it
// alone knows where its chrome ends — and everything else is the layout's, so a
// surface cannot hit-test against a window different from the one it drew.
func GeometryFor(x, y int, layout Viewport, tabWidth int) Geometry {
	if tabWidth <= 0 {
		tabWidth = DefaultTabWidth
	}
	return Geometry{
		Content:   mouse.Rect{X: x, Y: y, W: layout.DisplayWidth, H: layout.DisplayHeight},
		Start:     layout.Start,
		End:       layout.End,
		ColOffset: layout.Fit.ColOffset,
		TabWidth:  tabWidth,
	}
}

// Rows is the number of buffer lines the surface currently draws.
func (g Geometry) Rows() int { return g.End - g.Start }

// Valid reports whether the surface has a buffer window to map onto. Whether
// the surface is on screen at all is the host's gate: it owns the rect.
func (g Geometry) Valid() bool { return g.End > g.Start }

func (g Geometry) tabWidth() int {
	if g.TabWidth <= 0 {
		return DefaultTabWidth
	}
	return g.TabWidth
}

// OutputRowAt converts a screen row to a 0-indexed output row of the surface.
// The result is deliberately unbounded: a click needs to reject rows outside the
// output area, while a drag clamps them.
func OutputRowAt(g Geometry, y int) int {
	return y - g.Content.Y
}

// AbsoluteLine lifts a window-relative line index into the buffer's absolute
// coordinates when it keeps any.
func AbsoluteLine(buf *OutputBuffer, lineIdx int) int {
	if buf == nil {
		return lineIdx
	}
	if base, _, absolute := buf.AbsoluteRange(); absolute {
		return lineIdx + base
	}
	return lineIdx
}

// BufferBase is the coordinate space a buffer keeps: the absolute line its
// first loaded row sits at, and how many lines the space runs to.
//
// It is the one answer a host needs to draw what a gesture recorded. CellAt
// records selection points through AbsoluteLine, so a viewport told nothing
// about the base draws every highlight short by exactly it — off screen
// entirely once a pane has any scrollback. A relative buffer's own indices are
// already its coordinate space, so its base is zero.
func BufferBase(buf *OutputBuffer) (base, total int) {
	if buf == nil {
		return 0, 0
	}
	base, end, absolute := buf.AbsoluteRange()
	if !absolute {
		return 0, buf.LineCount()
	}
	return base, end
}

// BufferAbsolute reports that a buffer numbers its lines in absolute pane
// coordinates, which stay put as the pane produces more output.
func BufferAbsolute(buf *OutputBuffer) bool {
	if buf == nil {
		return false
	}
	_, _, absolute := buf.AbsoluteRange()
	return absolute
}

// ScrollKeepsSelection reports whether a selection survives a scroll made
// outside a pointer gesture — a wheel notch, a shift-scrollback key.
//
// A selection in absolute coordinates names the same rows wherever the window
// moves, so scrolling away from it and back must leave it where the user made
// it. A buffer without them renumbers its lines as it grows, so the same anchor
// would come to cover rows the user never picked.
func ScrollKeepsSelection(buf *OutputBuffer) bool {
	return BufferAbsolute(buf)
}

// ColAt maps a screen X coordinate to a visual column in the given line. The
// returned column is in visual space (post-tab-expansion, accounting for
// multi-width chars). It reports false only for positions left of the content;
// a line that cannot be read still yields column zero, because a pointer there
// is over the surface.
func ColAt(g Geometry, buf *OutputBuffer, x, lineIdx int) (int, bool) {
	relX := x - g.Content.X
	if relX < 0 {
		return 0, false
	}
	// A pane wider than the viewport is drawn scrolled, so screen column 0 is
	// the pane's ColOffset (td-73fa86).
	relX += g.ColOffset

	line, ok := LineTextAt(buf, lineIdx)
	if !ok {
		return 0, true
	}
	return ui.VisualColAtRelativeX(ui.ExpandTabs(line, g.tabWidth()), relX), true
}

// LineIndexAt maps a screen row to the absolute buffer line drawn there, or
// false when the row is outside the output area.
func LineIndexAt(g Geometry, buf *OutputBuffer, y int) (int, bool) {
	if !g.Valid() {
		return 0, false
	}
	outputRow := OutputRowAt(g, y)
	if outputRow < 0 {
		return 0, false
	}
	lineIdx := g.Start + outputRow
	if lineIdx >= g.End {
		return 0, false
	}
	return AbsoluteLine(buf, lineIdx), true
}

// CellAt maps screen coordinates to a buffer cell, refusing positions outside
// the output area: a click on the header or on padding is not a click on text.
func CellAt(g Geometry, buf *OutputBuffer, x, y int) (Cell, bool) {
	lineIdx, ok := LineIndexAt(g, buf, y)
	if !ok {
		return Cell{}, false
	}
	col, ok := ColAt(g, buf, x, lineIdx)
	if !ok {
		return Cell{}, false
	}
	return Cell{Line: lineIdx, Col: col}, true
}

// ClampedCellAt maps a pointer position onto the nearest visible cell instead of
// refusing positions outside the output area. A held pointer routinely leaves
// the pane mid-gesture — below the last row, left of the content, out over the
// sidebar — and a selection that stops tracking there reads as broken.
func ClampedCellAt(g Geometry, buf *OutputBuffer, x, y int) (Cell, bool) {
	if !g.Valid() {
		return Cell{}, false
	}
	outputRow := min(max(OutputRowAt(g, y), 0), g.Rows()-1)
	lineIdx := AbsoluteLine(buf, g.Start+outputRow)
	// Past the right edge, VisualColAtRelativeX already lands on the end of the
	// line, which is what dragging off the right of a row should select.
	col, ok := ColAt(g, buf, max(x, g.Content.X), lineIdx)
	if !ok {
		return Cell{}, false
	}
	return Cell{Line: lineIdx, Col: col}, true
}

// EdgeScrollRows turns a pointer row that has run past the content into the rows
// to scroll, negative above the top. Speed scales with the overshoot so a
// pointer parked just past the edge crawls while one thrown to the window edge
// moves quickly, bounded so no single step skips a screenful.
func EdgeScrollRows(outputRow, rows, limit int) int {
	switch {
	case outputRow < 0:
		return -min(-outputRow, limit)
	case outputRow >= rows:
		return min(outputRow-rows+1, limit)
	}
	return 0
}

// LineTextAt reads one line of a buffer, in whichever coordinate space that
// buffer keeps.
func LineTextAt(buf *OutputBuffer, lineIdx int) (string, bool) {
	if buf == nil {
		return "", false
	}
	var lines []string
	if _, _, absolute := buf.AbsoluteRange(); absolute {
		lines = buf.LinesAbsoluteRange(lineIdx, lineIdx+1)
	} else {
		lines = buf.LinesRange(lineIdx, lineIdx+1)
	}
	if len(lines) == 0 {
		return "", false
	}
	return lines[0], true
}

// WordSpanAt returns the inclusive visual column span of the word covering col
// in a tab-expanded line. A run of word runes selects whole; anything else
// selects the single cell under the pointer, matching xterm.
func WordSpanAt(line string, col int) (int, int, bool) {
	plain := ansi.Strip(line)
	isWord := func(r rune) bool {
		return r == '_' || r == '-' || r == '.' || r == '/' || r == ':' ||
			r >= '0' && r <= '9' || r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z'
	}
	type visualToken struct {
		text       string
		start, end int
	}
	var tokens []visualToken
	state := ansi.NormalState
	at := 0
	remaining := plain
	for len(remaining) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			break
		}
		if width > 0 {
			tokens = append(tokens, visualToken{text: seq, start: at, end: at + width})
			at += width
		}
		state = newState
		remaining = remaining[n:]
	}
	if len(tokens) == 0 {
		return 0, 0, false
	}
	index := len(tokens) - 1
	for i, token := range tokens {
		if col < token.end {
			index = i
			break
		}
	}
	left, right := index, index
	tokenWord := func(token visualToken) bool {
		runes := []rune(token.text)
		return len(runes) > 0 && isWord(runes[0])
	}
	if tokenWord(tokens[index]) {
		for left > 0 && tokenWord(tokens[left-1]) {
			left--
		}
		for right+1 < len(tokens) && tokenWord(tokens[right+1]) {
			right++
		}
	}
	return tokens[left].start, tokens[right].end - 1, true
}

// UnitSpanAt returns the inclusive visual span of the unit covering col in line.
// Character gestures have no span to snap to.
func UnitSpanAt(unit SelectionUnit, line string, lineIdx, col, tabWidth int) (ui.SelectionPoint, ui.SelectionPoint, bool) {
	if tabWidth <= 0 {
		tabWidth = DefaultTabWidth
	}
	expanded := ui.ExpandTabs(line, tabWidth)
	switch unit {
	case SelectUnitWord:
		startCol, endCol, ok := WordSpanAt(expanded, col)
		if !ok {
			return ui.SelectionPoint{}, ui.SelectionPoint{}, false
		}
		return ui.SelectionPoint{Line: lineIdx, Col: startCol},
			ui.SelectionPoint{Line: lineIdx, Col: endCol}, true
	case SelectUnitLine:
		width := ansi.StringWidth(expanded)
		return ui.SelectionPoint{Line: lineIdx, Col: 0},
			ui.SelectionPoint{Line: lineIdx, Col: max(width-1, 0)}, true
	}
	return ui.SelectionPoint{}, ui.SelectionPoint{}, false
}

// SelectAllSpan returns the span covering every line a buffer holds, in
// whichever coordinate space it keeps.
func SelectAllSpan(buf *OutputBuffer, tabWidth int) (ui.SelectionPoint, ui.SelectionPoint, bool) {
	if buf == nil || buf.LineCount() == 0 {
		return ui.SelectionPoint{}, ui.SelectionPoint{}, false
	}
	if tabWidth <= 0 {
		tabWidth = DefaultTabWidth
	}
	start, end := 0, buf.LineCount()
	if absoluteStart, absoluteEnd, absolute := buf.AbsoluteRange(); absolute {
		start, end = absoluteStart, absoluteEnd
	}
	last := buf.LinesRange(buf.LineCount()-1, buf.LineCount())
	lastWidth := 0
	if len(last) > 0 {
		lastWidth = ansi.StringWidth(ui.ExpandTabs(last[0], tabWidth))
	}
	return ui.SelectionPoint{Line: start, Col: 0},
		ui.SelectionPoint{Line: end - 1, Col: max(lastWidth-1, 0)}, true
}

// SelectedLines is the text a selection covers, clipped to the lines the buffer
// still holds.
func SelectedLines(buf *OutputBuffer, sel *ui.SelectionState, tabWidth int) []string {
	if sel == nil || !sel.HasSelection() || buf == nil || buf.LineCount() == 0 {
		return nil
	}
	if tabWidth <= 0 {
		tabWidth = DefaultTabWidth
	}

	startLine := sel.Start.Line
	endLine := sel.End.Line
	if startLine > endLine {
		startLine, endLine = endLine, startLine
	}
	var lines []string
	if base, end, absolute := buf.AbsoluteRange(); absolute {
		startLine = max(startLine, base)
		endLine = min(endLine, end-1)
		lines = buf.LinesAbsoluteRange(startLine, endLine+1)
	} else {
		startLine = max(startLine, 0)
		endLine = min(endLine, buf.LineCount()-1)
		lines = buf.LinesRange(startLine, endLine+1)
	}
	if len(lines) == 0 {
		return nil
	}
	return sel.SelectedText(lines, startLine, tabWidth)
}
