package markdown

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRenderMapped_ParagraphClickIsExactLine(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}

	// One ordinary paragraph, one source line, long enough to wrap several times.
	const word = "word "
	content := strings.Repeat(word, 40) // 240 cells of source
	content = strings.TrimSpace(content)
	const width = 20

	m := r.RenderMapped(content, width)
	if len(m.Lines) == 0 {
		t.Fatal("RenderMapped returned no lines")
	}
	if len(m.Anchors) != len(m.Lines) {
		t.Fatalf("anchors %d != lines %d", len(m.Anchors), len(m.Lines))
	}

	// Steel thread: a click on a mid-paragraph visual row after scrolling
	// lands on source line 0 (the only line) with an approximate column.
	nonEmpty := nonemptyRows(m)
	if len(nonEmpty) < 3 {
		t.Fatalf("expected wrapping paragraph, got %d non-empty rows:\n%s", len(nonEmpty), dumpMapped(m))
	}
	scrollOff := nonEmpty[1]
	viewRow := nonEmpty[2] - scrollOff
	got := m.Click(scrollOff, viewRow)
	if got.SourceLine != 0 {
		t.Fatalf("click visual row %d (scroll %d) mapped to source line %d, want 0\n%s",
			scrollOff+viewRow, scrollOff, got.SourceLine, dumpMapped(m))
	}
	if !got.Precise {
		t.Fatalf("single-line paragraph click should be Precise: %+v", got)
	}
	// Approximate column: wrap math puts later visual rows further into the line.
	if got.SourceCol < width {
		t.Fatalf("mid-paragraph click col %d, want at least one wrap width (%d)\n%s",
			got.SourceCol, width, dumpMapped(m))
	}

	// Inverse: that source column should recover the same visual row (or the
	// start of the same wrap segment).
	back := m.VisualRowForSource(0, got.SourceCol)
	if back != scrollOff+viewRow {
		t.Fatalf("VisualRowForSource(0, %d) = %d, want %d\n%s",
			got.SourceCol, back, scrollOff+viewRow, dumpMapped(m))
	}
}

func TestRenderMapped_MultiLineParagraphKeepsSourceLines(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	line0 := strings.Repeat("alpha ", 12)
	line1 := strings.Repeat("bravo ", 12)
	content := strings.TrimSpace(line0) + "\n" + strings.TrimSpace(line1)
	const width = 24

	m := r.RenderMapped(content, width)
	var saw0, saw1 bool
	for i, line := range m.Lines {
		plain := strings.TrimSpace(ansi.Strip(line))
		if plain == "" {
			continue
		}
		a := m.At(i)
		if strings.HasPrefix(plain, "alpha") {
			saw0 = true
			if a.SourceLine != 0 {
				t.Fatalf("alpha row %d mapped to source %d, want 0\n%s", i, a.SourceLine, dumpMapped(m))
			}
		}
		if strings.HasPrefix(plain, "bravo") {
			saw1 = true
			if a.SourceLine != 1 {
				t.Fatalf("bravo row %d mapped to source %d, want 1\n%s", i, a.SourceLine, dumpMapped(m))
			}
		}
		if a.BlockStart > a.SourceLine {
			t.Fatalf("row %d block start %d > source %d", i, a.BlockStart, a.SourceLine)
		}
	}
	if !saw0 || !saw1 {
		t.Fatalf("missing source lines: saw0=%v saw1=%v\n%s", saw0, saw1, dumpMapped(m))
	}
}

func TestRenderMapped_HeadingIsPreciseLine(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	m := r.RenderMapped("# Title\n\nbody paragraph here", 60)
	if len(m.Lines) == 0 {
		t.Fatal("empty render")
	}
	// First non-empty row belongs to the heading (source line 0).
	for i, line := range m.Lines {
		if strings.TrimSpace(ansi.Strip(line)) == "" {
			continue
		}
		a := m.At(i)
		if a.SourceLine != 0 {
			t.Fatalf("first content row %d mapped to source %d, want heading line 0\n%s",
				i, a.SourceLine, dumpMapped(m))
		}
		if !a.Precise {
			t.Fatalf("heading should be Precise: %+v", a)
		}
		break
	}
}

func TestRenderMapped_CodeFenceIsTopOfBlock(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	content := "intro\n\n```\nline-a\nline-b\nline-c\n```\n"
	m := r.RenderMapped(content, 60)
	// The fence starts at source line 2. Every visual row inside the fence
	// may degrade to that block start; none should claim the intro line.
	var fenceRows int
	for _, a := range m.Anchors {
		if a.BlockStart >= 2 {
			fenceRows++
			if a.SourceLine < 2 {
				t.Fatalf("fence visual row claimed intro source line %d", a.SourceLine)
			}
			if a.Precise {
				t.Fatalf("fence should be top-of-block (Precise=false): %+v", a)
			}
		}
	}
	if fenceRows == 0 {
		t.Fatalf("no visual rows mapped to the fence block\n%s", dumpMapped(m))
	}
}

func TestRenderMapped_DoesNotBreakRenderContent(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	content := "# Title\n\nA paragraph with **bold**.\n\n- list\n"
	plain := r.RenderContent(content, 72)
	mapped := r.RenderMapped(content, 72)
	if len(plain) != len(mapped.Lines) {
		t.Fatalf("RenderMapped lines %d, RenderContent %d", len(mapped.Lines), len(plain))
	}
	for i := range plain {
		if plain[i] != mapped.Lines[i] {
			t.Fatalf("line %d differs between RenderContent and RenderMapped", i)
		}
	}
}

func TestRenderMapped_EmptyAndNarrow(t *testing.T) {
	r, err := NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	empty := r.RenderMapped("", 80)
	if len(empty.Lines) != 0 || empty.Lines == nil {
		t.Fatalf("empty: %+v", empty)
	}

	narrow := r.RenderMapped("hello world this wraps", 16)
	if len(narrow.Lines) == 0 {
		t.Fatal("narrow fallback returned no lines")
	}
	if len(narrow.Anchors) != len(narrow.Lines) {
		t.Fatalf("narrow anchors %d != lines %d", len(narrow.Anchors), len(narrow.Lines))
	}
	if !narrow.At(0).Precise {
		t.Fatal("narrow fallback should be precise wrap math")
	}
}

func TestMapWrappedSource_WrapsAndMapsColumns(t *testing.T) {
	content := strings.Repeat("abcd", 10) // 40 cells, one line
	m := MapWrappedSource(content, 10)
	if len(m.Lines) < 4 {
		t.Fatalf("expected wrap, got %d lines: %q", len(m.Lines), m.Lines)
	}
	if m.At(0).SourceLine != 0 || m.At(0).SourceCol != 0 {
		t.Fatalf("row 0: %+v", m.At(0))
	}
	if m.At(1).SourceLine != 0 || m.At(1).SourceCol < 10 {
		t.Fatalf("row 1 should start near col 10: %+v", m.At(1))
	}
	if m.VisualRowForSource(0, 25) < 2 {
		t.Fatalf("col 25 should be at least visual row 2, got %d", m.VisualRowForSource(0, 25))
	}
}

func TestMapWrappedSource_PreservesSourceLines(t *testing.T) {
	m := MapWrappedSource("short\n"+strings.Repeat("x", 30)+"\nend", 10)
	if m.At(0).SourceLine != 0 {
		t.Fatalf("first line: %+v", m.At(0))
	}
	var saw1, saw2 bool
	for _, a := range m.Anchors {
		if a.SourceLine == 1 {
			saw1 = true
		}
		if a.SourceLine == 2 {
			saw2 = true
		}
	}
	if !saw1 || !saw2 {
		t.Fatalf("missing source lines in map: saw1=%v saw2=%v\n%s", saw1, saw2, dumpMapped(m))
	}
}

func nonemptyRows(m MappedRender) []int {
	var rows []int
	for i, line := range m.Lines {
		if strings.TrimSpace(ansi.Strip(line)) != "" {
			rows = append(rows, i)
		}
	}
	return rows
}

func dumpMapped(m MappedRender) string {
	var b strings.Builder
	for i, line := range m.Lines {
		a := m.At(i)
		b.WriteString(strings.TrimRight(ansi.Strip(line), " "))
		b.WriteString("  // ")
		b.WriteString(strings.ReplaceAll(strings.TrimSpace(ansi.Strip(line)), "\n", " "))
		_ = a
		b.WriteByte('\n')
	}
	for i, a := range m.Anchors {
		b.WriteString(strings.TrimSpace(ansi.Strip(m.Lines[i])))
		b.WriteString(" -> ")
		b.WriteString(strings.TrimSpace(ansi.Strip(m.Lines[i])))
		b.WriteByte(' ')
		b.WriteString("(")
		b.WriteString(itoa(a.SourceLine))
		b.WriteString(",")
		b.WriteString(itoa(a.SourceCol))
		b.WriteString(" p=")
		if a.Precise {
			b.WriteString("1")
		} else {
			b.WriteString("0")
		}
		b.WriteString(")\n")
	}
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [16]byte
	i := len(buf)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
