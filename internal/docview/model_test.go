package docview

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/filepreview"
	"github.com/marcus/sidecar/internal/markdown"
)

func newTestModel(t *testing.T) *Model {
	t.Helper()
	renderer, err := markdown.NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	return New(renderer)
}

func loadFixture(t *testing.T, m *Model, content string, line int) LoadedMsg {
	t.Helper()
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "fixture.md"), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	msg, ok := m.Load(7, dir, "fixture.md", line, 42)().(LoadedMsg)
	if !ok {
		t.Fatal("Load command did not return LoadedMsg")
	}
	return msg
}

func TestLoadRendersAndTogglesRawMarkdown(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(50, 4)
	msg := loadFixture(t, m, "# Heading\n\nbody", 0)
	if !m.SetResult(msg) {
		t.Fatal("current result was rejected")
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "Heading") || strings.Contains(got, "# Heading") {
		t.Fatalf("rendered view = %q", got)
	}

	m.ToggleRenderMode()
	if got := ansi.Strip(m.View()); !strings.Contains(got, "# Heading") {
		t.Fatalf("raw view = %q", got)
	}
	if got := m.Title(); got != "fixture.md" {
		t.Fatalf("Title() = %q", got)
	}
}

func TestViewHasExactHeightAndANSIClampedWidth(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(5, 3)
	m.loading = false
	m.rendered = false
	m.result.HighlightedLines = []string{"\x1b[31m界界界wide\x1b[0m"}

	lines := strings.Split(m.View(), "\n")
	if len(lines) != 3 {
		t.Fatalf("row count = %d, want 3", len(lines))
	}
	for i, line := range lines {
		if got := ansi.StringWidth(line); got != 5 {
			t.Fatalf("row %d width = %d, want 5 (%q)", i, got, line)
		}
	}
}

func TestViewExpandsRawTabsBeforeWidthClamp(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(5, 1)
	msg := loadFixture(t, m, "\t12345", 1)
	if !m.SetResult(msg) {
		t.Fatal("current result was rejected")
	}

	view := m.View()
	if strings.Contains(view, "\t") {
		t.Fatalf("view retained a terminal-expanded tab: %q", view)
	}
	if got := ansi.StringWidth(view); got != 5 {
		t.Fatalf("visible width = %d, want 5 (%q)", got, view)
	}
	if strings.Contains(ansi.Strip(view), "1") {
		t.Fatalf("content beyond the tab stop escaped the box: %q", view)
	}
}

func TestScrollAndLineTargetClamp(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(20, 2)
	msg := loadFixture(t, m, "one\ntwo\nthree\nfour", 999)
	if !m.SetResult(msg) {
		t.Fatal("current result was rejected")
	}
	rows := strings.Split(ansi.Strip(m.View()), "\n")
	if !strings.HasPrefix(strings.TrimSpace(rows[0]), "3 three") || !strings.HasPrefix(strings.TrimSpace(rows[1]), "4 four") {
		t.Fatalf("targeted view = %q", strings.Join(rows, "\n"))
	}

	m.Scroll(-999)
	if got := strings.TrimSpace(ansi.Strip(m.View())); !strings.HasPrefix(got, "1 one") {
		t.Fatalf("scroll up did not clamp: %q", got)
	}
	if !m.HandleKey(tea.KeyPressMsg{Code: 'G', Text: "G"}) {
		t.Fatal("G was not handled")
	}
	if got := strings.TrimSpace(ansi.Strip(m.View())); !strings.HasPrefix(got, "3 three") {
		t.Fatalf("end did not clamp: %q", got)
	}
	if m.HandleKey(tea.KeyPressMsg{Code: 'x', Text: "x"}) {
		t.Fatal("unowned key was handled")
	}
}

func TestNarrowWidthUsesPlainWrap(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(markdown.MinWidthForMarkdown-1, 3)
	msg := loadFixture(t, m, "# Heading with enough words to wrap onto another line", 0)
	if !m.SetResult(msg) {
		t.Fatal("current result was rejected")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "# Heading") {
		t.Fatalf("narrow fallback should retain markdown source: %q", view)
	}
	for i, line := range strings.Split(view, "\n") {
		if ansi.StringWidth(line) != markdown.MinWidthForMarkdown-1 {
			t.Fatalf("row %d has wrong width: %q", i, line)
		}
	}
}

func TestWrapSplitsLongLineAndSurvivesLoad(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(8, 3)
	m.SetWrap(true)
	msg := loadFixture(t, m, "abcdefghijklmnop", 1)
	if !m.SetResult(msg) {
		t.Fatal("current result was rejected")
	}
	if !m.Wrap() {
		t.Fatal("Load cleared wrap")
	}
	view := ansi.Strip(m.View())
	rows := strings.Split(view, "\n")
	if len(rows) != 3 {
		t.Fatalf("row count = %d, want 3 (%q)", len(rows), view)
	}
	// Width 8 reserves 1 column for scrollbar, leaving 7 for wrapped content.
	if strings.TrimSpace(rows[0]) != "abcdefg" || strings.TrimSpace(rows[1]) != "hijklmn" {
		t.Fatalf("wrapped view = %q", view)
	}

	m.SetWrap(false)
	truncated := strings.TrimSpace(strings.Split(ansi.Strip(m.View()), "\n")[0])
	if truncated != "abcdefg" {
		t.Fatalf("truncated view = %q", truncated)
	}
}

func TestMissingFileShowsError(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(60, 2)
	msg, ok := m.Load(1, t.TempDir(), "missing.md", 0, 9)().(LoadedMsg)
	if !ok || !m.SetResult(msg) {
		t.Fatal("missing-file result was not accepted")
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "Document unavailable") || !strings.Contains(got, "missing.md") {
		t.Fatalf("error view = %q", got)
	}
}

func TestLoadFileReadsPinnedInodeAfterPathReplacement(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.md")
	if err := os.WriteFile(path, []byte("inside"), 0o644); err != nil {
		t.Fatal(err)
	}
	file, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	outside := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(outside, []byte("outside"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(outside, path); err != nil {
		t.Fatal(err)
	}
	m := newTestModel(t)
	m.SetSize(30, 1)
	msg, ok := m.LoadFile(1, file, "fixture.md", 1, 9)().(LoadedMsg)
	if !ok || !m.SetResult(msg) {
		t.Fatal("pinned-file result was not accepted")
	}
	view := ansi.Strip(m.View())
	if !strings.Contains(view, "inside") || strings.Contains(view, "outside") {
		t.Fatalf("loader re-followed replaced path: %q", view)
	}
}

func TestLoadingState(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(30, 4)
	_ = m.Load(1, t.TempDir(), "wait.md", 0, 3)
	rows := strings.Split(ansi.Strip(m.View()), "\n")
	if len(rows) < 3 || strings.TrimSpace(rows[0]) != "" {
		t.Fatalf("loading view missing spacer: %q", rows)
	}
	if !strings.HasPrefix(strings.TrimRight(rows[1], " "), "  Loading document") {
		t.Fatalf("loading label = %q", rows[1])
	}
	if !strings.Contains(rows[2], "wait.md") || !strings.HasPrefix(strings.TrimRight(rows[2], " "), "  wait.md") {
		t.Fatalf("loading path = %q", rows[2])
	}
}

func TestEmptyAndTruncatedStates(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(30, 3)
	msg := loadFixture(t, m, "", 0)
	if !m.SetResult(msg) || !strings.Contains(ansi.Strip(m.View()), "Empty document") {
		t.Fatalf("empty view = %q", m.View())
	}

	msg = loadFixture(t, m, "# visible", 0)
	msg.Result.IsTruncated = true
	if !m.SetResult(msg) || !strings.Contains(ansi.Strip(m.View()), "Preview truncated") {
		t.Fatalf("truncated view = %q", m.View())
	}
}

func TestSetResultRejectsStaleIdentityWithoutMutation(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(30, 4)
	current := loadFixture(t, m, "current", 0)

	tests := []struct {
		name string
		edit func(*LoadedMsg)
	}{
		{"model", func(msg *LoadedMsg) { msg.ModelID++ }},
		{"request", func(msg *LoadedMsg) { msg.RequestGeneration++ }},
		{"epoch", func(msg *LoadedMsg) { msg.Epoch++ }},
		{"path", func(msg *LoadedMsg) { msg.Path = "other.md" }},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			stale := current
			stale.Result = filepreview.PreviewResult{Error: errors.New("stale")}
			tt.edit(&stale)
			if m.SetResult(stale) {
				t.Fatal("stale result was accepted")
			}
			if got := ansi.Strip(m.View()); !strings.Contains(got, "Loading document") {
				t.Fatalf("stale result mutated model: %q", got)
			}
		})
	}

	if !m.SetResult(current) {
		t.Fatal("current result was rejected")
	}
}

func TestArmNeedsLoadAndPendingScroll(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(20, 4)
	if !m.NeedsLoad() {
		t.Fatal("new model should need a load")
	}
	m.Arm(3, "notes.md", 9)
	if !m.NeedsLoad() || m.Title() != "notes.md" {
		t.Fatalf("armed model = path %q needsLoad=%v", m.Title(), m.NeedsLoad())
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "Loading document") || !strings.Contains(got, "notes.md") {
		t.Fatalf("armed view = %q", got)
	}

	msg := loadFixture(t, m, "one\ntwo\nthree\nfour\nfive\n", 0)
	m.SetRendered(false)
	m.SetSize(20, 2)
	m.SetPendingScroll(3)
	if m.ScrollOffset() != 3 {
		t.Fatalf("pending scroll = %d", m.ScrollOffset())
	}
	if !m.SetResult(msg) {
		t.Fatal("load result was rejected")
	}
	if m.NeedsLoad() {
		t.Fatal("loaded model still reports NeedsLoad")
	}
	if m.ScrollOffset() != 3 {
		t.Fatalf("restored scroll = %d", m.ScrollOffset())
	}

	m.ApplyLine(1)
	if m.Rendered() || m.ScrollOffset() != 0 {
		t.Fatalf("ApplyLine = rendered=%v scroll=%d", m.Rendered(), m.ScrollOffset())
	}
}

func TestReloadPreservesScrollAndLeavesLoadingUntilResult(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(20, 4)
	msg := loadFixture(t, m, "one\ntwo\nthree\nfour\nfive\n", 0)
	m.SetRendered(false)
	m.SetSize(20, 2)
	if !m.SetResult(msg) {
		t.Fatal("load result was rejected")
	}
	m.Scroll(3)
	if m.ScrollOffset() != 3 {
		t.Fatalf("scroll = %d", m.ScrollOffset())
	}

	cmd := m.Reload()
	if cmd == nil {
		t.Fatal("reload returned no command")
	}
	if got := ansi.Strip(m.View()); !strings.Contains(got, "Loading document") {
		t.Fatalf("reload did not show the loading placeholder: %q", got)
	}
	reloaded, ok := cmd().(LoadedMsg)
	if !ok || !m.SetResult(reloaded) {
		t.Fatal("reload result was rejected")
	}
	if m.NeedsLoad() || m.ScrollOffset() != 3 {
		t.Fatalf("after reload needsLoad=%v scroll=%d", m.NeedsLoad(), m.ScrollOffset())
	}
}

func TestRawViewNumbersLinesAndRenderedViewDoesNot(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(60, 3)
	msg := loadFixture(t, m, "# Heading\n\nbody", 0)
	if !m.SetResult(msg) {
		t.Fatal("current result was rejected")
	}
	if got := ansi.Strip(m.View()); strings.Contains(got, "   1 ") {
		t.Fatalf("rendered markdown must not be numbered: %q", got)
	}

	m.ToggleRenderMode()
	rows := strings.Split(ansi.Strip(m.View()), "\n")
	if !strings.HasPrefix(rows[0], "   1 # Heading") {
		t.Fatalf("raw row 0 = %q", rows[0])
	}
	if !strings.HasPrefix(rows[2], "   3 body") {
		t.Fatalf("raw row 2 = %q", rows[2])
	}
	for i, row := range rows {
		if got := ansi.StringWidth(row); got != 60 {
			t.Fatalf("row %d width = %d, want 60", i, got)
		}
	}
}

func TestPlaceholderLinesAreNotNumbered(t *testing.T) {
	cases := []struct {
		name   string
		setup  func(*Model)
		expect string
	}{
		{"loading", func(m *Model) { _ = m.Load(1, t.TempDir(), "wait.md", 0, 3) }, "Loading document"},
		{"error", func(m *Model) {
			msg, _ := m.Load(1, t.TempDir(), "missing.md", 0, 9)().(LoadedMsg)
			m.SetResult(msg)
		}, "Document unavailable"},
		{"binary", func(m *Model) { m.loading = false; m.result.IsBinary = true }, "Binary preview"},
		{"image", func(m *Model) { m.loading = false; m.result.IsImage = true }, "Image preview"},
		{"empty", func(m *Model) { m.loading = false }, "Empty document"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			m := newTestModel(t)
			m.SetSize(60, 3)
			tc.setup(m)
			view := ansi.Strip(m.View())
			if !strings.Contains(view, tc.expect) {
				t.Fatalf("view = %q, want %q", view, tc.expect)
			}
			if !strings.HasPrefix(strings.TrimLeft(view, " \n"), tc.expect) {
				t.Fatalf("placeholder was given a gutter: %q", view)
			}
		})
	}
}

func TestTruncationBannerIsNotNumberedButContentIs(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(40, 3)
	msg := loadFixture(t, m, "alpha\nbeta", 1)
	msg.Result.IsTruncated = true
	if !m.SetResult(msg) {
		t.Fatal("current result was rejected")
	}
	rows := strings.Split(ansi.Strip(m.View()), "\n")
	if !strings.HasPrefix(rows[0], "     Preview truncated") {
		t.Fatalf("banner row = %q", rows[0])
	}
	if !strings.HasPrefix(rows[2], "   1 alpha") {
		t.Fatalf("first content row = %q", rows[2])
	}
}

func TestGutterConsumesWidthWhenTruncatingAndWrapping(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(15, 3)
	msg := loadFixture(t, m, "abcdefghijklmnopqrstuvwxyz", 1)
	if !m.SetResult(msg) {
		t.Fatal("current result was rejected")
	}
	rows := strings.Split(ansi.Strip(m.View()), "\n")
	// 15 cells - 1 scrollbar cell - 5 gutter cells leaves exactly 9 for the text.
	if rows[0] != "   1 abcdefghi " {
		t.Fatalf("truncated row = %q", rows[0])
	}

	m.SetWrap(true)
	rows = strings.Split(ansi.Strip(m.View()), "\n")
	if rows[0] != "   1 abcdefghi " || rows[1] != "     jklmnopqr " || rows[2] != "     stuvwxyz  " {
		t.Fatalf("wrapped rows = %q", rows)
	}
	for i, row := range rows {
		if got := ansi.StringWidth(row); got != 15 {
			t.Fatalf("row %d width = %d, want 15", i, got)
		}
	}
}

func TestApplyLineLandsOnSourceLineWithWrap(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(15, 2)
	// Each source line wraps into three rows of ten cells.
	long := strings.Repeat("x", 25)
	msg := loadFixture(t, m, strings.Join([]string{long, long, long + "END", long}, "\n"), 1)
	if !m.SetResult(msg) {
		t.Fatal("current result was rejected")
	}
	m.SetWrap(true)

	m.ApplyLine(3)
	rows := strings.Split(ansi.Strip(m.View()), "\n")
	if !strings.HasPrefix(rows[0], "   3 ") {
		t.Fatalf("ApplyLine(3) landed on %q", rows[0])
	}
	if m.ScrollOffset() != 6 {
		t.Fatalf("scroll offset = %d, want 6", m.ScrollOffset())
	}

	m.ApplyLine(1)
	if m.ScrollOffset() != 0 {
		t.Fatalf("ApplyLine(1) scroll = %d", m.ScrollOffset())
	}
}

func TestLoadTargetLineLandsOnSourceLineWithWrap(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(15, 2)
	m.SetWrap(true)
	long := strings.Repeat("y", 25)
	msg := loadFixture(t, m, strings.Join([]string{long, long, long}, "\n"), 2)
	if !m.SetResult(msg) {
		t.Fatal("current result was rejected")
	}
	if got := m.ScrollOffset(); got != 3 {
		t.Fatalf("targeted scroll = %d, want 3", got)
	}
	if row := strings.Split(ansi.Strip(m.View()), "\n")[0]; !strings.HasPrefix(row, "   2 ") {
		t.Fatalf("target row = %q", row)
	}
}

func TestGutterGrowsWithLineCount(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(40, 2)
	lines := make([]string, 12000)
	for i := range lines {
		lines[i] = "line"
	}
	m.loading = false
	m.rendered = false
	m.result.Content = "line"
	m.result.HighlightedLines = lines
	m.ApplyLine(12000)
	rows := strings.Split(ansi.Strip(m.View()), "\n")
	row := rows[len(rows)-1]
	if !strings.HasPrefix(row, "12000 line") {
		t.Fatalf("wide gutter row = %q", row)
	}
}

func largeDoc(lines int) []string {
	out := make([]string, lines)
	for i := range out {
		out[i] = strings.Repeat("source text ", 12)
	}
	return out
}

func loadedRawModel(t *testing.T, lines []string, width, height int) *Model {
	t.Helper()
	m := newTestModel(t)
	m.SetSize(width, height)
	m.result = filepreview.PreviewResult{Content: strings.Join(lines, "\n"), Lines: lines}
	m.loading = false
	m.rendered = false
	m.invalidateRender()
	return m
}

// layoutPasses counts how many times fn rebuilds the laid-out document.
func (m *Model) layoutPasses(fn func()) int {
	before := m.layoutBuilds
	fn()
	return m.layoutBuilds - before
}

// A document is laid out lazily and the result is reused, because View,
// maxScroll and displayRowForLine all want it and clampScroll runs on every
// scroll key. Without reuse a large file re-measures every line several times
// per keystroke.
func TestLayoutIsReusedUntilSomethingItDependsOnMoves(t *testing.T) {
	m := loadedRawModel(t, largeDoc(5000), 80, 40)
	m.View() // first pass builds it

	if got := m.layoutPasses(func() { m.View(); m.Scroll(1); m.View() }); got != 0 {
		t.Fatalf("scrolling and re-rendering rebuilt the layout %d times, want 0", got)
	}
	if got := m.layoutPasses(func() { m.SetSize(100, 40); m.View() }); got == 0 {
		t.Fatal("a width change must rebuild the layout")
	}
	if got := m.layoutPasses(func() { m.ToggleWrap(); m.View() }); got == 0 {
		t.Fatal("toggling wrap must rebuild the layout")
	}
	if got := m.layoutPasses(func() { m.ToggleRenderMode(); m.View() }); got == 0 {
		t.Fatal("toggling render mode must rebuild the layout")
	}
}

// Scrolling a large document must not walk it. This guards the regression
// where every keystroke re-measured all of its lines.
func BenchmarkScrollLargeDocument(b *testing.B) {
	lines := largeDoc(20000)
	m := New(nil)
	m.SetSize(120, 40)
	m.result = filepreview.PreviewResult{Content: "x", Lines: lines}
	m.loading = false
	m.rendered = false
	m.invalidateRender()
	m.View()

	b.ResetTimer()
	for range b.N {
		m.Scroll(1)
		m.View()
	}
}

func TestScrollbarRendersOnOverflowAndSpacerWhenFitting(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(30, 4)
	m.loading = false
	m.rendered = false

	// 1. Short content: fits on screen -> spacer column (spaces at right edge).
	m.result = filepreview.PreviewResult{Content: "one\ntwo", Lines: []string{"one", "two"}}
	m.invalidateRender()
	rows := strings.Split(ansi.Strip(m.View()), "\n")
	if len(rows) != 4 {
		t.Fatalf("row count = %d, want 4", len(rows))
	}
	for i, row := range rows {
		if ansi.StringWidth(row) != 30 {
			t.Errorf("row %d width = %d, want 30", i, ansi.StringWidth(row))
		}
		if !strings.HasSuffix(row, " ") {
			t.Errorf("row %d does not end in spacer space: %q", i, row)
		}
	}

	// 2. Long content: overflows -> scrollbar thumb and track rendered.
	longLines := make([]string, 20)
	for i := range longLines {
		longLines[i] = fmt.Sprintf("line %d", i+1)
	}
	m.result = filepreview.PreviewResult{Content: strings.Join(longLines, "\n"), Lines: longLines}
	m.invalidateRender()

	// At top (scroll = 0): thumb is at top.
	rows = strings.Split(ansi.Strip(m.View()), "\n")
	if !strings.HasSuffix(rows[0], "┃") {
		t.Fatalf("row 0 at scroll 0 should end with thumb ┃: %q", rows[0])
	}
	if !strings.HasSuffix(rows[3], "│") {
		t.Fatalf("row 3 at scroll 0 should end with track │: %q", rows[3])
	}

	// Scroll to bottom: thumb is at bottom.
	m.Scroll(999)
	rows = strings.Split(ansi.Strip(m.View()), "\n")
	if !strings.HasSuffix(rows[3], "┃") {
		t.Fatalf("row 3 at bottom scroll should end with thumb ┃: %q", rows[3])
	}
	if !strings.HasSuffix(rows[0], "│") {
		t.Fatalf("row 0 at bottom scroll should end with track │: %q", rows[0])
	}
}
