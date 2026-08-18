package markdown

import (
	"bytes"
	"strings"
	"unicode"

	"github.com/charmbracelet/x/ansi"
	rw "github.com/mattn/go-runewidth"
	"github.com/rivo/uniseg"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Anchor maps one rendered visual row back to a source location.
// Precise is true when SourceLine is line-accurate (ordinary paragraphs and
// headings via wrap math). Tables, fences, and other heavily reshaped
// constructs degrade to the top of their block with Precise=false.
type Anchor struct {
	SourceLine int  // 0-based source line
	SourceCol  int  // 0-based rune offset on that line
	BlockStart int  // 0-based first source line of the containing block
	Precise    bool // line-accurate when true
}

// MappedRender is a rendered markdown document plus a parallel source map.
// Lines[i] is what the user sees; Anchors[i] says which source line produced it.
type MappedRender struct {
	Lines   []string
	Anchors []Anchor
}

// At returns the source anchor for a visual row. Out-of-range rows clamp
// to the nearest mapped line.
func (m MappedRender) At(visualRow int) Anchor {
	if len(m.Anchors) == 0 {
		return Anchor{}
	}
	if visualRow < 0 {
		return m.Anchors[0]
	}
	if visualRow >= len(m.Anchors) {
		return m.Anchors[len(m.Anchors)-1]
	}
	return m.Anchors[visualRow]
}

// Click maps a viewport-relative row plus the view's scroll offset to source.
func (m MappedRender) Click(scrollOff, viewRow int) Anchor {
	return m.At(scrollOff + viewRow)
}

// VisualRowForSource returns the first visual row whose source position is
// at or before (sourceLine, sourceCol). Used when leaving edit so the
// rendered view can recover the line that was being edited.
func (m MappedRender) VisualRowForSource(sourceLine, sourceCol int) int {
	if len(m.Anchors) == 0 {
		return 0
	}
	best := 0
	found := false
	for i, a := range m.Anchors {
		if a.SourceLine < sourceLine || (a.SourceLine == sourceLine && a.SourceCol <= sourceCol) {
			best = i
			found = true
			continue
		}
		if found {
			return best
		}
		return i
	}
	return best
}

// RenderMapped renders markdown and a source map for the same width.
// Existing RenderContent consumers are unchanged; this is an adjacent result.
func (r *Renderer) RenderMapped(content string, width int) MappedRender {
	if content == "" {
		return MappedRender{Lines: []string{}, Anchors: []Anchor{}}
	}

	key := r.cacheKey(content, width)

	r.mu.RLock()
	if cached, ok := r.mappedCache[key]; ok {
		r.mu.RUnlock()
		return cached
	}
	r.mu.RUnlock()

	var result MappedRender
	if width < MinWidthForMarkdown {
		result = MapWrappedSource(content, width)
	} else {
		lines := r.RenderContent(content, width)
		result = r.assignAnchors(content, width, lines)
	}

	r.mu.Lock()
	if r.mappedCache == nil {
		r.mappedCache = make(map[uint64]MappedRender)
	}
	if len(r.mappedCache) >= MaxCacheEntries {
		r.mappedCache = make(map[uint64]MappedRender)
	}
	r.mappedCache[key] = result
	r.mu.Unlock()
	return result
}

// MapWrappedSource wraps each source line at width and maps every visual
// row back to that source line plus the starting column of the wrap segment.
// This is the raw-view / narrow-fallback visual-row policy: always wrap,
// never truncate.
func MapWrappedSource(content string, width int) MappedRender {
	srcLines := splitSourceLines(content)
	if width < 1 {
		width = 1
	}
	var lines []string
	var anchors []Anchor
	for i, line := range srcLines {
		segs := wrapSegments(line, width)
		if len(segs) == 0 {
			segs = []wrapSeg{{text: "", col: 0}}
		}
		for _, seg := range segs {
			lines = append(lines, seg.text)
			anchors = append(anchors, Anchor{
				SourceLine: i,
				SourceCol:  seg.col,
				BlockStart: i,
				Precise:    true,
			})
		}
	}
	if len(lines) == 0 {
		return MappedRender{Lines: []string{""}, Anchors: []Anchor{{Precise: true}}}
	}
	return MappedRender{Lines: lines, Anchors: anchors}
}

type wrapSeg struct {
	text string
	col  int
}

func wrapSegments(line string, width int) []wrapSeg {
	if width < 1 {
		return []wrapSeg{{text: line, col: 0}}
	}
	plain := line
	if ansi.StringWidth(plain) < width {
		return []wrapSeg{{text: plain, col: 0}}
	}
	parts := textareaWrap([]rune(plain), width)
	// Bubbles adds one synthetic trailing space so cursor navigation has an
	// edge after the final rune. It participates in row calculation but is not
	// source content and must not advance our source-column anchors.
	last := len(parts) - 1
	if last >= 0 && len(parts[last]) > 0 {
		parts[last] = parts[last][:len(parts[last])-1]
	}
	out := make([]wrapSeg, 0, len(parts))
	col := 0
	for _, part := range parts {
		out = append(out, wrapSeg{text: string(part), col: col})
		col += len(part)
	}
	if len(out) == 0 {
		return []wrapSeg{{text: "", col: 0}}
	}
	return out
}

// textareaWrap mirrors charm.land/bubbles/v2/textarea's soft-wrap policy.
// Notes raw view and its source anchors must use the editor's exact visual-row
// boundaries; a generic terminal wrapper differs at word boundaries. The
// parity regression in the Notes package protects this dependency when
// Bubbles is upgraded.
func textareaWrap(runes []rune, width int) [][]rune {
	lines := [][]rune{{}}
	word := []rune{}
	row := 0
	spaces := 0

	for _, r := range runes {
		if unicode.IsSpace(r) {
			spaces++
		} else {
			word = append(word, r)
		}

		if spaces > 0 {
			if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces > width {
				row++
				lines = append(lines, []rune{})
			}
			lines[row] = append(lines[row], word...)
			lines[row] = append(lines[row], []rune(strings.Repeat(" ", spaces))...)
			spaces = 0
			word = nil
		} else {
			lastCharWidth := rw.RuneWidth(word[len(word)-1])
			if uniseg.StringWidth(string(word))+lastCharWidth > width {
				if len(lines[row]) > 0 {
					row++
					lines = append(lines, []rune{})
				}
				lines[row] = append(lines[row], word...)
				word = nil
			}
		}
	}

	if uniseg.StringWidth(string(lines[row]))+uniseg.StringWidth(string(word))+spaces >= width {
		lines = append(lines, []rune{})
		lines[row+1] = append(lines[row+1], word...)
		spaces++
		lines[row+1] = append(lines[row+1], []rune(strings.Repeat(" ", spaces))...)
	} else {
		lines[row] = append(lines[row], word...)
		spaces++
		lines[row] = append(lines[row], []rune(strings.Repeat(" ", spaces))...)
	}

	return lines
}

type srcBlock struct {
	startLine int
	endLine   int
	kind      ast.NodeKind
	precise   bool // paragraph or heading: wrap math is meaningful
}

func (r *Renderer) assignAnchors(content string, width int, rendered []string) MappedRender {
	if len(rendered) == 0 {
		return MappedRender{Lines: []string{}, Anchors: []Anchor{}}
	}
	blocks := parseBlocks([]byte(content))
	if len(blocks) == 0 {
		return stampAnchors(rendered, Anchor{Precise: true})
	}

	srcLines := splitSourceLines(content)
	anchors := make([]Anchor, len(rendered))

	if len(blocks) == 1 {
		assignBlockRows(anchors, rendered, 0, len(rendered), blocks[0], srcLines, width)
		return MappedRender{Lines: rendered, Anchors: anchors}
	}

	// Walk the full render and consume independently-rendered blocks in
	// order. Extra blank lines between blocks stay with the previous block.
	// This does not parse ANSI: the assignment is block order plus each
	// block's own glamour line count.
	cursor := 0
	for i, b := range blocks {
		chunk := r.RenderContent(blockSource(srcLines, b), width)
		need := countNonEmpty(chunk)
		if need < 1 {
			need = 1
		}
		start := cursor
		taken := 0
		j := start
		for j < len(rendered) && taken < need {
			if strings.TrimSpace(ansi.Strip(rendered[j])) != "" {
				taken++
			}
			j++
		}
		// Last block consumes the tail so trailing chrome is not left unmapped.
		if i == len(blocks)-1 {
			j = len(rendered)
		}
		if j <= start {
			j = start + 1
			if j > len(rendered) {
				j = len(rendered)
			}
		}
		assignBlockRows(anchors, rendered, start, j, b, srcLines, width)
		cursor = j
	}
	// Any prefix the walk skipped (leading chrome) inherits the first block.
	for i := 0; i < len(anchors); i++ {
		if anchors[i] == (Anchor{}) {
			anchors[i] = Anchor{
				SourceLine: blocks[0].startLine,
				BlockStart: blocks[0].startLine,
			}
		}
	}
	return MappedRender{Lines: rendered, Anchors: anchors}
}

func assignBlockRows(anchors []Anchor, rendered []string, start, end int, b srcBlock, srcLines []string, width int) {
	if start < 0 {
		start = 0
	}
	if end > len(rendered) {
		end = len(rendered)
	}
	if start >= end {
		return
	}
	fallback := Anchor{
		SourceLine: b.startLine,
		SourceCol:  0,
		BlockStart: b.startLine,
		Precise:    false,
	}
	if !b.precise {
		for i := start; i < end; i++ {
			anchors[i] = fallback
		}
		return
	}

	// Ordinary paragraph/heading: wrap each source line and match the
	// non-empty rendered rows. If the wrap count disagrees with glamour,
	// keep the source line (the block is still that paragraph) and
	// approximate the column from wrap width.
	type segRef struct {
		line int
		col  int
	}
	var segs []segRef
	for line := b.startLine; line <= b.endLine && line < len(srcLines); line++ {
		for _, seg := range wrapSegments(srcLines[line], width) {
			segs = append(segs, segRef{line: line, col: seg.col})
		}
	}
	if len(segs) == 0 {
		segs = []segRef{{line: b.startLine, col: 0}}
	}

	nonEmpty := make([]int, 0, end-start)
	for i := start; i < end; i++ {
		if strings.TrimSpace(ansi.Strip(rendered[i])) != "" {
			nonEmpty = append(nonEmpty, i)
		}
	}
	if len(nonEmpty) == 0 {
		for i := start; i < end; i++ {
			anchors[i] = Anchor{
				SourceLine: b.startLine,
				BlockStart: b.startLine,
				Precise:    true,
			}
		}
		return
	}

	precise := len(nonEmpty) == len(segs)
	segFor := func(k int) segRef {
		if precise {
			return segs[k]
		}
		// Proportional: still the right source line for a single-line
		// paragraph; multi-line paragraphs get the nearest wrap segment.
		idx := k * len(segs) / len(nonEmpty)
		if idx >= len(segs) {
			idx = len(segs) - 1
		}
		return segs[idx]
	}

	si := 0
	last := Anchor{SourceLine: b.startLine, BlockStart: b.startLine, Precise: true}
	for i := start; i < end; i++ {
		if si < len(nonEmpty) && i == nonEmpty[si] {
			s := segFor(si)
			last = Anchor{
				SourceLine: s.line,
				SourceCol:  s.col,
				BlockStart: b.startLine,
				// Paragraphs and headings stay word-landable even when
				// glamour's wrap count disagrees with wrapSegments.
				Precise: true,
			}
			si++
		}
		anchors[i] = last
	}
}

func stampAnchors(lines []string, a Anchor) MappedRender {
	anchors := make([]Anchor, len(lines))
	for i := range anchors {
		anchors[i] = a
	}
	return MappedRender{Lines: lines, Anchors: anchors}
}

func parseBlocks(src []byte) []srcBlock {
	md := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := md.Parser().Parse(text.NewReader(src))
	var blocks []srcBlock
	for n := doc.FirstChild(); n != nil; n = n.NextSibling() {
		start, end, ok := nodeLineRange(n, src)
		if !ok {
			start, end, ok = descendantLineRange(n, src)
		}
		if !ok {
			continue
		}
		kind := n.Kind()
		precise := kind == ast.KindParagraph || kind == ast.KindHeading
		blocks = append(blocks, srcBlock{
			startLine: start,
			endLine:   end,
			kind:      kind,
			precise:   precise,
		})
	}
	return blocks
}

func nodeLineRange(n ast.Node, src []byte) (start, end int, ok bool) {
	if n.Type() != ast.TypeBlock {
		return 0, 0, false
	}
	lines := n.Lines()
	if lines == nil || lines.Len() == 0 {
		return 0, 0, false
	}
	seg0 := lines.At(0)
	segN := lines.At(lines.Len() - 1)
	start = lineAt(src, seg0.Start)
	endOff := segN.Stop
	if endOff > 0 && endOff <= len(src) && src[endOff-1] == '\n' {
		endOff--
	}
	end = lineAt(src, endOff)
	if end < start {
		end = start
	}
	return start, end, true
}

func descendantLineRange(n ast.Node, src []byte) (start, end int, ok bool) {
	start, end = -1, -1
	_ = ast.Walk(n, func(child ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || child == n {
			return ast.WalkContinue, nil
		}
		s, e, found := nodeLineRange(child, src)
		if !found {
			return ast.WalkContinue, nil
		}
		if start < 0 || s < start {
			start = s
		}
		if e > end {
			end = e
		}
		return ast.WalkContinue, nil
	})
	if start < 0 {
		return 0, 0, false
	}
	return start, end, true
}

func lineAt(src []byte, off int) int {
	if off < 0 {
		off = 0
	}
	if off > len(src) {
		off = len(src)
	}
	return bytes.Count(src[:off], []byte{'\n'})
}

func splitSourceLines(content string) []string {
	if content == "" {
		return []string{""}
	}
	return strings.Split(content, "\n")
}

func blockSource(srcLines []string, b srcBlock) string {
	start := b.startLine
	end := b.endLine
	if start < 0 {
		start = 0
	}
	if start >= len(srcLines) {
		return ""
	}
	if end >= len(srcLines) {
		end = len(srcLines) - 1
	}
	if end < start {
		end = start
	}
	return strings.Join(srcLines[start:end+1], "\n")
}

func countNonEmpty(lines []string) int {
	n := 0
	for _, line := range lines {
		if strings.TrimSpace(ansi.Strip(line)) != "" {
			n++
		}
	}
	return n
}
