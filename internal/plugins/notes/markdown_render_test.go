package notes

import (
	"fmt"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
)

func TestNotesOrdinalCandidatesStayASTAndOutlineScoped(t *testing.T) {
	tests := []struct {
		name    string
		content string
		want    []int
	}{
		{name: "date", content: "Release history:\n2026. August 19"},
		{name: "decimal", content: "Constants:\n3.14 is pi\n2.718 is e"},
		{name: "fenced code", content: "Before\n\n```\n3. code\n```"},
		{name: "indented code", content: "Before\n\n    3. code"},
		{name: "already valid list", content: "3. valid list\n4. valid next"},
		{name: "inline prose", content: "The release includes 3. outline-looking prose"},
		{name: "zero is not an outline", content: "Before\n0. zero value"},
		{name: "one belongs to CommonMark", content: "Before\n1. standard list"},
		{name: "two starts forgiving range", content: "Before\n2. candidate", want: []int{1}},
		{name: "loose outline", content: "Before\n3. candidate\n4. candidate", want: []int{1, 2}},
		{name: "formatted duplicate ordinals", content: "Before\n7. **same**\n7. **same**", want: []int{1, 2}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			projection := projectNotesOrdinalLists(tt.content)
			if got := projectionCandidateLines(projection); !slices.Equal(got, tt.want) {
				t.Fatalf("candidate lines = %v, want %v; candidates=%+v", got, tt.want, projection.candidates)
			}
			if len(tt.want) == 0 && projection.lineMap != nil {
				t.Fatal("non-outline input was projected")
			}
		})
	}
}

func TestNotesOrdinalReanchorIgnoresUnrelatedBlocksAndFormatting(t *testing.T) {
	content := strings.Join([]string{
		"Before",
		"3. **first same**",
		"4. next",
		"",
		"```",
		"7. code",
		"```",
		"",
		"    8. indented",
		"",
		"7. valid list",
		"8. valid next",
		"",
		"Bridge prose",
		"7. **same**",
		"7. **same**",
	}, "\n")
	projection := projectNotesOrdinalLists(content)
	if got, want := projectionCandidateLines(projection), []int{1, 2, 14, 15}; !slices.Equal(got, want) {
		t.Fatalf("candidate lines = %v, want %v; candidates=%+v", got, want, projection.candidates)
	}

	renderer, err := markdown.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{76, 132} {
		first := renderNotesMarkdown(renderer, content, width)
		second := renderNotesMarkdown(renderer, content, width)
		if !slices.Equal(first.Anchors, second.Anchors) {
			t.Fatalf("width %d: cached render changed anchors", width)
		}
		if !slices.Equal(first.Lines, second.Lines) {
			t.Fatalf("width %d: cached render changed lines", width)
		}
		renderedText := strings.Join(first.Lines, "\n")
		projectedTokens := []string{projection.boundarySentinel}
		for _, candidate := range projection.candidates {
			projectedTokens = append(projectedTokens, candidate.sentinel)
		}
		for _, token := range projectedTokens {
			if strings.Contains(renderedText, token) {
				t.Fatalf("width %d: projected zero-width token leaked into rendered output", width)
			}
		}

		sameRows := renderedRowsContaining(first, "same")
		if len(sameRows) != 3 {
			t.Fatalf("width %d: formatted same rows = %v, want three", width, sameRows)
		}
		wantLines := []int{1, 14, 15}
		for i, row := range sameRows {
			a := first.At(row)
			if a.SourceLine != wantLines[i] || !a.Precise {
				t.Fatalf("width %d row %d anchor = %+v, want precise line %d\n%s", width, row, a, wantLines[i], dumpNotesMapped(first))
			}
			visual := ansi.Strip(first.Lines[row])
			visualCol := strings.Index(visual, "same")
			line, col := sourceAtVisualRow(first, row, visualCol, content)
			if visualCol < 0 || line != wantLines[i] || !sourceHasPrefix(content, line, col, "same") {
				t.Fatalf("width %d row %d click mapped to %d:%d", width, row, line, col)
			}
		}

		standardRenderer, standardErr := markdown.NewRenderer()
		if standardErr != nil {
			t.Fatal(standardErr)
		}
		standard := standardRenderer.RenderMapped(content, width)
		assertUnrelatedBlockMatchesStandard(t, first, standard, "7. code")
		assertUnrelatedBlockMatchesStandard(t, first, standard, "8. indented")
		assertUnrelatedBlockMatchesStandard(t, first, standard, "valid list")
	}
}

func TestNotesDateStaysProseWhenAnotherOutlineIsProjected(t *testing.T) {
	content := "Release history:\n2026. August 19\nOutline follows:\n7. **real item**"
	projection := projectNotesOrdinalLists(content)
	if got := projectionCandidateLines(projection); !slices.Equal(got, []int{3}) {
		t.Fatalf("candidate lines = %v, want [3]", got)
	}
	renderer, err := markdown.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	rendered := renderNotesMarkdown(renderer, content, 100)
	if _, ok := ordinalBoundaryRow(rendered, "2026.", "August 19"); ok {
		t.Fatal("date line rendered as a Notes outline")
	}
	row, ok := ordinalBoundaryRow(rendered, "7.", "real item")
	if !ok || rendered.At(row).SourceLine != 3 {
		t.Fatalf("real outline row=%d ok=%v anchor=%+v", row, ok, rendered.At(row))
	}
}

func TestNotesOrdinalContinuationRowsKeepCandidateSource(t *testing.T) {
	words := make([]string, 80)
	for i := range words {
		words[i] = fmt.Sprintf("tok%02d", i)
	}
	content := "Before\n7. short\n8. " + strings.Join(words, " ")
	renderer, err := markdown.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{76, 132} {
		rendered := renderNotesMarkdown(renderer, content, width)
		start, ok := ordinalBoundaryRow(rendered, "8.", "tok00")
		if !ok {
			t.Fatalf("width %d: missing 8 marker row\n%s", width, dumpNotesMapped(rendered))
		}
		rows := nonemptyItemRows(rendered, start)
		if len(rows) < 3 {
			t.Fatalf("width %d: long 8 item used only %d rows\n%s", width, len(rows), dumpNotesMapped(rendered))
		}
		selected := []int{rows[0], rows[len(rows)/2], rows[len(rows)-1]}
		for _, row := range selected {
			a := rendered.At(row)
			if a.SourceLine != 2 || !a.Precise {
				t.Fatalf("width %d continuation row %d anchor=%+v, want precise line 2\n%s", width, row, a, dumpNotesMapped(rendered))
			}
			visual := ansi.Strip(rendered.Lines[row])
			token := firstTokWord(visual)
			visualCol := strings.Index(visual, token)
			line, col := sourceAtVisualRow(rendered, row, visualCol, content)
			if token == "" || visualCol < 0 || line != 2 || !sourceHasPrefix(content, line, col, token) {
				t.Fatalf("width %d continuation click row %d token=%q mapped to %d:%d", width, row, token, line, col)
			}
		}
	}
}

func TestNotesProjectionTokensCannotCollideWithAuthoredContent(t *testing.T) {
	authoredOne := "\u2063\u200b\u2064"
	authoredTwo := "\u2063\u200b\u200b\u2064"
	content := authoredOne + authoredTwo + " Before\n7. body " + authoredOne + " middle " + authoredTwo + " tail"
	projection := projectNotesOrdinalLists(content)
	if len(projection.candidates) != 1 {
		t.Fatalf("candidates=%+v, want one", projection.candidates)
	}
	generated := []string{projection.boundarySentinel, projection.candidates[0].sentinel}
	for _, token := range generated {
		if strings.Contains(content, token) {
			t.Fatalf("generated token %q collides with authored source", []rune(token))
		}
	}

	renderer, err := markdown.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	first := renderNotesMarkdown(renderer, content, 76)
	second := renderNotesMarkdown(renderer, content, 76)
	if !slices.Equal(first.Lines, second.Lines) || !slices.Equal(first.Anchors, second.Anchors) {
		t.Fatal("collision-safe projection changed across cached renders")
	}
	renderedText := strings.Join(first.Lines, "\n")
	if strings.Count(renderedText, authoredOne) != 2 || strings.Count(renderedText, authoredTwo) != 2 {
		t.Fatalf("authored zero-width content vanished or changed: one=%d two=%d", strings.Count(renderedText, authoredOne), strings.Count(renderedText, authoredTwo))
	}
	for _, token := range generated {
		if strings.Contains(renderedText, token) {
			t.Fatalf("generated token leaked into output: %q", []rune(token))
		}
	}
	row, ok := ordinalBoundaryRow(first, "7.", "body")
	if !ok || first.At(row).SourceLine != 1 {
		t.Fatalf("candidate identity collision changed anchor: row=%d ok=%v anchor=%+v", row, ok, first.At(row))
	}
}

func TestNotesRepeatedWordContinuationClicksUseRowColumns(t *testing.T) {
	content := "Before\n7. short\n8. " + strings.TrimSpace(strings.Repeat("repeat ", 100))
	sourceLine := strings.Split(content, "\n")[2]
	renderer, err := markdown.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	for _, width := range []int{76, 132} {
		rendered := renderNotesMarkdown(renderer, content, width)
		start, ok := ordinalBoundaryRow(rendered, "8.", "repeat")
		if !ok {
			t.Fatalf("width %d: missing repeated 8 item", width)
		}
		rows := nonemptyItemRows(rendered, start)
		if len(rows) < 3 {
			t.Fatalf("width %d: repeated item rows=%v, want at least three", width, rows)
		}
		selected := []int{rows[0], rows[len(rows)/2], rows[len(rows)-1]}
		for _, row := range selected {
			visual := ansi.Strip(rendered.Lines[row])
			visualCol := strings.Index(visual, "repeat")
			occurrence := repeatedWordCountBeforeRow(rendered, rows, row, "repeat")
			wantCol := nthWordColumn(sourceLine, "repeat", occurrence)
			line, col := sourceAtVisualRow(rendered, row, visualCol, content)
			if visualCol < 0 || wantCol < 0 || line != 2 || col != wantCol {
				t.Fatalf("width %d row %d occurrence %d click=%d:%d want 2:%d anchor=%+v", width, row, occurrence, line, col, wantCol, rendered.At(row))
			}

			// Drive the production edit-entry helper with the click result; the
			// textarea caret must land on this row's repeated-word occurrence.
			p := layoutTestPlugin(t, content)
			_ = p.enterEditAt(line, col)
			if p.editorTextarea.Line() != 2 || p.editorTextarea.Column() != wantCol {
				t.Fatalf("width %d row %d edit entry=%d:%d want 2:%d", width, row, p.editorTextarea.Line(), p.editorTextarea.Column(), wantCol)
			}
		}
	}
}

func repeatedWordCountBeforeRow(surface markdown.MappedRender, itemRows []int, target int, word string) int {
	count := 0
	for _, row := range itemRows {
		if row == target {
			return count
		}
		count += strings.Count(ansi.Strip(surface.Lines[row]), word)
	}
	return count
}

func nthWordColumn(line, word string, occurrence int) int {
	start := 0
	for i := 0; i <= occurrence; i++ {
		rel := strings.Index(line[start:], word)
		if rel < 0 {
			return -1
		}
		if i == occurrence {
			return start + rel
		}
		start += rel + len(word)
	}
	return -1
}

func nonemptyItemRows(surface markdown.MappedRender, start int) []int {
	var rows []int
	for row := start; row < len(surface.Lines); row++ {
		if strings.TrimSpace(ansi.Strip(surface.Lines[row])) == "" {
			if len(rows) > 0 {
				break
			}
			continue
		}
		rows = append(rows, row)
	}
	return rows
}

func firstTokWord(line string) string {
	for _, field := range strings.Fields(line) {
		field = strings.Trim(field, ".,:;()[]{}")
		if strings.HasPrefix(field, "tok") {
			return field
		}
	}
	return ""
}

func renderedRowsContaining(surface markdown.MappedRender, text string) []int {
	var rows []int
	for row, line := range surface.Lines {
		if strings.Contains(ansi.Strip(line), text) {
			rows = append(rows, row)
		}
	}
	return rows
}

func assertUnrelatedBlockMatchesStandard(t *testing.T, surface, standard markdown.MappedRender, text string) {
	t.Helper()
	rows := renderedRowsContaining(surface, text)
	standardRows := renderedRowsContaining(standard, text)
	if len(rows) != 1 || len(standardRows) != 1 {
		t.Fatalf("%q rows = %v standard=%v, want one each", text, rows, standardRows)
	}
	a := surface.At(rows[0])
	want := standard.At(standardRows[0])
	if a != want {
		t.Fatalf("%q unrelated block anchor changed: got %+v want standard %+v", text, a, want)
	}
}

func dumpNotesMapped(surface markdown.MappedRender) string {
	var out strings.Builder
	for row, line := range surface.Lines {
		out.WriteString(strconv.Itoa(row))
		out.WriteString(": ")
		out.WriteString(strings.TrimSpace(ansi.Strip(line)))
		out.WriteString(" ")
		out.WriteString(fmt.Sprintf("%+v", surface.At(row)))
		out.WriteByte('\n')
	}
	return out.String()
}
