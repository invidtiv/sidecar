package notes

import (
	"bytes"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/yuin/goldmark"
	"github.com/yuin/goldmark/ast"
	"github.com/yuin/goldmark/extension"
	"github.com/yuin/goldmark/text"
)

// Notes outlines are interaction-sized numbers, not years or document IDs.
// Keeping the ceiling explicit prevents date lines such as "2026. August 19"
// from acquiring list semantics while leaving ample room for real outlines.
const maxNotesOutlineOrdinal = 999

type notesOrdinalCandidate struct {
	ordinal    int
	sourceLine int
	sentinel   string
}

type notesMarkdownProjection struct {
	content          string
	lineMap          []int
	boundarySentinel string
	candidates       []notesOrdinalCandidate
}

// renderNotesMarkdown keeps the shared Markdown renderer CommonMark-correct
// while making Notes forgiving of outline numbers that interrupt prose. The
// projection carries its AST-scoped candidates and source map through anchor
// repair; no unrelated source line is reconsidered after parsing.
func renderNotesMarkdown(renderer *markdown.Renderer, content string, width int) markdown.MappedRender {
	if width < markdown.MinWidthForMarkdown {
		return renderer.RenderMapped(content, width)
	}
	projection := projectNotesOrdinalLists(content)
	result := renderer.RenderMapped(projection.content, width)
	if projection.lineMap == nil {
		return result
	}
	// RenderMapped returns cached slices. Remapping must never rewrite the
	// shared renderer entry or a second Notes render would map an anchor twice.
	result.Lines = append([]string(nil), result.Lines...)
	for i := range result.Lines {
		result.Lines[i] = strings.ReplaceAll(result.Lines[i], projection.boundarySentinel, "")
	}
	result.Anchors = append([]markdown.Anchor(nil), result.Anchors...)
	for i := range result.Anchors {
		result.Anchors[i].SourceLine = originalNotesLine(projection.lineMap, result.Anchors[i].SourceLine)
		result.Anchors[i].BlockStart = originalNotesLine(projection.lineMap, result.Anchors[i].BlockStart)
	}
	reanchorNotesOrdinalRows(&result, projection.candidates, content, width)
	return result
}

// projectNotesOrdinalLists asks Goldmark which outline-like ordinal markers it
// swallowed into paragraphs under CommonMark's "only 1 may interrupt" rule.
// Fences, indented code, valid lists, and inline prose never become candidates
// because they are not line-start markers in Paragraph AST line segments.
func projectNotesOrdinalLists(content string) notesMarkdownProjection {
	unchanged := notesMarkdownProjection{content: content}
	if content == "" {
		return unchanged
	}
	source := []byte(content)
	sourceLines := strings.Split(content, "\n")
	candidateByLine := make(map[int]int)
	var candidates []notesOrdinalCandidate
	parser := goldmark.New(goldmark.WithExtensions(extension.GFM))
	doc := parser.Parser().Parse(text.NewReader(source))
	_ = ast.Walk(doc, func(node ast.Node, entering bool) (ast.WalkStatus, error) {
		if !entering || node.Kind() != ast.KindParagraph || node.Lines() == nil {
			return ast.WalkContinue, nil
		}
		segments := node.Lines()
		for i := 0; i < segments.Len(); i++ {
			line := bytes.Count(source[:segments.At(i).Start], []byte{'\n'})
			ordinal, ok := notesOutlineOrdinal(sourceLines, line)
			if !ok {
				continue
			}
			candidateByLine[line] = len(candidates)
			candidates = append(candidates, notesOrdinalCandidate{ordinal: ordinal, sourceLine: line})
		}
		return ast.WalkContinue, nil
	})
	if len(candidates) == 0 {
		return unchanged
	}
	usedTokens := make(map[string]bool)
	boundarySentinel := nextNotesOrdinalSentinel(content, usedTokens)
	for i := range candidates {
		candidates[i].sentinel = nextNotesOrdinalSentinel(content, usedTokens)
	}

	insertBefore := make(map[int]bool)
	for i, candidate := range candidates {
		continuesRun := false
		if i > 0 {
			previous := candidates[i-1]
			continuesRun = candidate.sourceLine == previous.sourceLine+1 && candidate.ordinal == previous.ordinal+1
		}
		if !continuesRun {
			insertBefore[candidate.sourceLine] = true
		}
	}

	projected := make([]string, 0, len(sourceLines)+3*len(insertBefore))
	lineMap := make([]int, 0, cap(projected))
	for line, sourceLine := range sourceLines {
		if insertBefore[line] {
			projected = append(projected, "", boundarySentinel, "")
			lineMap = append(lineMap, line, line, line)
		}
		if index, ok := candidateByLine[line]; ok {
			sourceLine = insertNotesOrdinalSentinel(sourceLine, candidates[index].sentinel)
		}
		projected = append(projected, sourceLine)
		lineMap = append(lineMap, line)
	}
	return notesMarkdownProjection{
		content:          strings.Join(projected, "\n"),
		lineMap:          lineMap,
		boundarySentinel: boundarySentinel,
		candidates:       candidates,
	}
}

func notesOutlineOrdinal(lines []string, line int) (int, bool) {
	if line < 0 || line >= len(lines) {
		return 0, false
	}
	ordinal, _, ok := notesOrdinalParts(lines[line])
	if !ok || ordinal <= 1 || ordinal > maxNotesOutlineOrdinal {
		return 0, false
	}
	return ordinal, true
}

func notesOrdinalParts(line string) (ordinal, insertAt int, ok bool) {
	i := 0
	for i < len(line) && line[i] == ' ' && i < 4 {
		i++
	}
	if i > 3 {
		return 0, 0, false
	}
	digitStart := i
	for i < len(line) && line[i] >= '0' && line[i] <= '9' && i-digitStart < 10 {
		i++
	}
	if i == digitStart || i-digitStart > 9 || i >= len(line) || (line[i] != '.' && line[i] != ')') {
		return 0, 0, false
	}
	parsed, err := strconv.Atoi(line[digitStart:i])
	if err != nil {
		return 0, 0, false
	}
	ordinal = parsed
	i++
	if i >= len(line) || (line[i] != ' ' && line[i] != '\t') {
		return 0, 0, false
	}
	insertAt = i + 1
	if strings.TrimSpace(line[insertAt:]) == "" {
		return 0, 0, false
	}
	return ordinal, insertAt, true
}

// reanchorNotesOrdinalRows uses only AST-owned candidate identity and order.
// Each zero-width identity occurs only on its projected candidate line, so
// formatted/duplicate bodies remain exact and code cannot claim an anchor.
func reanchorNotesOrdinalRows(result *markdown.MappedRender, candidates []notesOrdinalCandidate, content string, width int) {
	if result == nil || len(result.Lines) == 0 {
		return
	}
	nextRendered := 0
	for _, candidate := range candidates {
		end := -1
		for row := nextRendered; row < len(result.Lines); row++ {
			if strings.Contains(result.Lines[row], candidate.sentinel) {
				result.Lines[row] = strings.ReplaceAll(result.Lines[row], candidate.sentinel, "")
				end = row
				break
			}
		}
		if end < 0 {
			continue
		}
		start := end
		for row := end; row >= nextRendered; row-- {
			ordinal, ok := renderedNotesOrdinal(result.Lines[row])
			if ok && ordinal == candidate.ordinal {
				start = row
				break
			}
		}
		columns := notesCandidateRowColumns(content, candidate.sourceLine, width, end-start+1)
		for row := start; row <= end; row++ {
			result.Anchors[row] = markdown.Anchor{
				SourceLine: candidate.sourceLine,
				SourceCol:  columns[row-start],
				BlockStart: candidate.sourceLine,
				Precise:    true,
			}
		}
		nextRendered = end + 1
	}
}

// notesCandidateRowColumns reuses the raw/editor wrap map for monotonically
// increasing source-column estimates. Glamour may allocate a different number
// of rows for list chrome, so the raw segments are distributed across the
// exact marker-to-sentinel rendered extent rather than assuming equal counts.
func notesCandidateRowColumns(content string, sourceLine, width, renderedRows int) []int {
	columns := make([]int, renderedRows)
	if renderedRows == 0 {
		return columns
	}
	lines := strings.Split(content, "\n")
	if sourceLine < 0 || sourceLine >= len(lines) {
		return columns
	}
	raw := markdown.MapWrappedSource(lines[sourceLine], width)
	if len(raw.Anchors) == 0 {
		return columns
	}
	for row := range columns {
		rawRow := row * len(raw.Anchors) / renderedRows
		if rawRow >= len(raw.Anchors) {
			rawRow = len(raw.Anchors) - 1
		}
		columns[row] = raw.At(rawRow).SourceCol
	}
	return columns
}

// nextNotesOrdinalSentinel returns a deterministic, zero-width token proven
// absent from user source and distinct from every token in this projection.
func nextNotesOrdinalSentinel(source string, used map[string]bool) string {
	for length := 1; ; length++ {
		token := "\u2063" + strings.Repeat("\u200b", length) + "\u2064"
		if !used[token] && !strings.Contains(source, token) {
			used[token] = true
			return token
		}
	}
}

func insertNotesOrdinalSentinel(line, sentinel string) string {
	_, _, ok := notesOrdinalParts(line)
	if !ok {
		return line
	}
	insertAt := len(line)
	for insertAt > 0 && (line[insertAt-1] == ' ' || line[insertAt-1] == '\t') {
		insertAt--
	}
	// Preserve a Markdown hard-break backslash as the final non-space byte.
	if insertAt > 0 && line[insertAt-1] == '\\' {
		insertAt--
	}
	return line[:insertAt] + sentinel + line[insertAt:]
}

func renderedNotesOrdinal(line string) (int, bool) {
	line = strings.TrimLeft(ansi.Strip(line), " \t")
	i := 0
	for i < len(line) && line[i] >= '0' && line[i] <= '9' {
		i++
	}
	if i == 0 || i >= len(line) || (line[i] != '.' && line[i] != ')') {
		return 0, false
	}
	ordinal, err := strconv.Atoi(line[:i])
	if err != nil {
		return 0, false
	}
	i++
	if i < len(line) && line[i] != ' ' && line[i] != '\t' {
		return 0, false
	}
	return ordinal, true
}

func originalNotesLine(lineMap []int, projected int) int {
	if len(lineMap) == 0 {
		return projected
	}
	if projected < 0 {
		return lineMap[0]
	}
	if projected >= len(lineMap) {
		return lineMap[len(lineMap)-1]
	}
	return lineMap[projected]
}
