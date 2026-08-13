package docview

import (
	"errors"
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
	if got := strings.TrimSpace(rows[0]) + "\n" + strings.TrimSpace(rows[1]); got != "three\nfour" {
		t.Fatalf("targeted view = %q", got)
	}

	m.Scroll(-999)
	if got := strings.TrimSpace(ansi.Strip(m.View())); !strings.HasPrefix(got, "one") {
		t.Fatalf("scroll up did not clamp: %q", got)
	}
	if !m.HandleKey(tea.KeyPressMsg{Code: 'G', Text: "G"}) {
		t.Fatal("G was not handled")
	}
	if got := strings.TrimSpace(ansi.Strip(m.View())); !strings.HasPrefix(got, "three") {
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
	m.SetSize(30, 2)
	_ = m.Load(1, t.TempDir(), "wait.md", 0, 3)
	if got := ansi.Strip(m.View()); !strings.Contains(got, "Loading document") || !strings.Contains(got, "wait.md") {
		t.Fatalf("loading view = %q", got)
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
	m.SetSize(30, 1)
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
