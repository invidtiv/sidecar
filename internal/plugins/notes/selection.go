package notes

import (
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/rivo/uniseg"
)

// srcPos is a source caret: logical line plus rune offset. The caret sits
// before the rune at col, or at EOL when col == line length.
type srcPos struct {
	line int
	col  int
}

func srcFromPoint(p ui.SelectionPoint) srcPos {
	return srcPos{line: p.Line, col: p.Col}
}

func (p srcPos) point() ui.SelectionPoint {
	return ui.SelectionPoint{Line: p.line, Col: p.col}
}

func compareSrc(a, b srcPos) int {
	if a.line != b.line {
		if a.line < b.line {
			return -1
		}
		return 1
	}
	if a.col != b.col {
		if a.col < b.col {
			return -1
		}
		return 1
	}
	return 0
}

func orderSrc(a, b srcPos) (srcPos, srcPos) {
	if compareSrc(a, b) <= 0 {
		return a, b
	}
	return b, a
}

func sourceLines(content string) []string {
	if content == "" {
		return []string{""}
	}
	return strings.Split(content, "\n")
}

func clampSrc(p srcPos, lines []string) srcPos {
	if len(lines) == 0 {
		return srcPos{}
	}
	if p.line < 0 {
		p.line = 0
	}
	if p.line >= len(lines) {
		p.line = len(lines) - 1
	}
	n := utf8.RuneCountInString(lines[p.line])
	if p.col < 0 {
		p.col = 0
	}
	if p.col > n {
		p.col = n
	}
	return p
}

// caretPairToExclusive stores the two carets as an exclusive [start, end)
// range. An unmoved caret is empty.
func caretPairToExclusive(anchor, caret srcPos) (start, end srcPos, empty bool) {
	if compareSrc(anchor, caret) == 0 {
		return anchor, caret, true
	}
	start, end = orderSrc(anchor, caret)
	return start, end, false
}

// extractExclusive returns the source text in [start, end).
func extractExclusive(lines []string, start, end srcPos) string {
	if len(lines) == 0 {
		return ""
	}
	start = clampSrc(start, lines)
	end = clampSrc(end, lines)
	if compareSrc(start, end) >= 0 {
		return ""
	}
	if start.line == end.line {
		runes := []rune(lines[start.line])
		return string(runes[start.col:end.col])
	}
	var b strings.Builder
	first := []rune(lines[start.line])
	if start.col < len(first) {
		b.WriteString(string(first[start.col:]))
	}
	b.WriteByte('\n')
	for i := start.line + 1; i < end.line; i++ {
		b.WriteString(lines[i])
		b.WriteByte('\n')
	}
	last := []rune(lines[end.line])
	if end.col > 0 {
		if end.col > len(last) {
			end.col = len(last)
		}
		b.WriteString(string(last[:end.col]))
	}
	return b.String()
}

// spliceExclusive replaces [start, end) with insert and returns the new
// content plus the caret after the insert.
func spliceExclusive(content, insert string, start, end srcPos) (string, srcPos) {
	lines := sourceLines(content)
	start = clampSrc(start, lines)
	end = clampSrc(end, lines)
	if compareSrc(start, end) > 0 {
		start, end = end, start
	}
	before := extractExclusive(lines, srcPos{}, start)
	after := extractExclusive(lines, end, srcPos{line: len(lines) - 1, col: utf8.RuneCountInString(lines[len(lines)-1])})
	out := before + insert + after
	parts := strings.Split(insert, "\n")
	caret := srcPos{line: start.line, col: start.col}
	if len(parts) == 1 {
		caret.col = start.col + utf8.RuneCountInString(insert)
	} else {
		caret.line = start.line + len(parts) - 1
		caret.col = utf8.RuneCountInString(parts[len(parts)-1])
	}
	return out, caret
}

// visualColsForSourceRange maps an exclusive source range onto one wrapped
// visual row. Returns inclusive visual columns [vStart, vEnd], or ok=false
// when this row does not overlap the selection.
func visualColsForSourceRange(surface markdown.MappedRender, visual int, start, end srcPos, source string) (vStart, vEnd int, ok bool) {
	if visual < 0 || visual >= len(surface.Anchors) {
		return 0, 0, false
	}
	a := surface.At(visual)
	segStart := srcPos{line: a.SourceLine, col: a.SourceCol}
	segEnd := visualSegEnd(surface, visual, source)
	ovStart, ovEnd, overlap := exclusiveOverlap(segStart, segEnd, start, end)
	if !overlap {
		return 0, 0, false
	}
	plain := ansi.Strip(surface.Lines[visual])
	runes := []rune(plain)
	localStart := ovStart.col - a.SourceCol
	localEnd := ovEnd.col - a.SourceCol
	if localStart < 0 {
		localStart = 0
	}
	if localEnd > len(runes) {
		localEnd = len(runes)
	}
	if localStart >= localEnd {
		// Newline-only overlap at a wrap/line boundary: paint the last
		// cell of a non-empty row so the selection stays visible.
		if len(runes) == 0 {
			return 0, 0, false
		}
		w := uniseg.StringWidth(plain)
		if w < 1 {
			return 0, 0, false
		}
		return w - 1, w - 1, true
	}
	vStart = uniseg.StringWidth(string(runes[:localStart]))
	vEnd = uniseg.StringWidth(string(runes[:localEnd])) - 1
	if vEnd < vStart {
		vEnd = vStart
	}
	return vStart, vEnd, true
}

func visualSegEnd(surface markdown.MappedRender, visual int, source string) srcPos {
	a := surface.At(visual)
	if visual+1 < len(surface.Anchors) {
		next := surface.At(visual + 1)
		if next.SourceLine == a.SourceLine {
			return srcPos{line: a.SourceLine, col: next.SourceCol}
		}
	}
	// Last visual row of this source line: exclusive end is EOL on this
	// line, not (line+1, 0). Mapping that later-line caret back into this
	// row's local columns would collapse the highlight to a single cell.
	lines := sourceLines(source)
	if a.SourceLine >= 0 && a.SourceLine < len(lines) {
		return srcPos{line: a.SourceLine, col: utf8.RuneCountInString(lines[a.SourceLine])}
	}
	return srcPos{line: a.SourceLine, col: a.SourceCol}
}

func exclusiveOverlap(segStart, segEnd, selStart, selEnd srcPos) (srcPos, srcPos, bool) {
	start := segStart
	if compareSrc(selStart, start) > 0 {
		start = selStart
	}
	end := segEnd
	if compareSrc(selEnd, end) < 0 {
		end = selEnd
	}
	if compareSrc(start, end) >= 0 {
		return srcPos{}, srcPos{}, false
	}
	return start, end, true
}

func overlayExclusiveOnView(view string, surface markdown.MappedRender, start, end srcPos, source string, scrollOff int) string {
	if compareSrc(start, end) == 0 {
		return view
	}
	if compareSrc(start, end) > 0 {
		start, end = end, start
	}
	lines := strings.Split(view, "\n")
	for i, line := range lines {
		vStart, vEnd, ok := visualColsForSourceRange(surface, scrollOff+i, start, end, source)
		if !ok {
			continue
		}
		lines[i] = ui.InjectCharacterRangeBackground(line, vStart, vEnd)
	}
	return strings.Join(lines, "\n")
}

func allSourceRange(content string) (start, end srcPos) {
	lines := sourceLines(content)
	end = srcPos{line: len(lines) - 1, col: utf8.RuneCountInString(lines[len(lines)-1])}
	return srcPos{}, end
}

func incrementIfOnChar(p srcPos, content string) srcPos {
	lines := sourceLines(content)
	p = clampSrc(p, lines)
	n := utf8.RuneCountInString(lines[p.line])
	if p.col < n {
		p.col++
	}
	return p
}

// mouseExclusiveRange converts two inclusive click carets (character under
// the pointer, or EOL) into an exclusive [start, end). The later endpoint
// is advanced so both directions include the characters under the click
// and the pointer.
func mouseExclusiveRange(click, pointer srcPos, content string) (srcPos, srcPos) {
	start, end := orderSrc(click, pointer)
	return start, incrementIfOnChar(end, content)
}
