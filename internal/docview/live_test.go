package docview

import (
	"errors"

	tea "charm.land/bubbletea/v2"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/filepreview"
)

// td-03c21c: a previewed document must follow writes to its file, keeping the
// reader's position and never repainting for a write that changed nothing.

func body(lines int) string {
	var b strings.Builder
	for i := range lines {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("x", 1+i%7))
		b.WriteString("\n")
	}
	return b.String()
}

func result(content string) filepreview.PreviewResult {
	lines := strings.Split(content, "\n")
	return filepreview.PreviewResult{
		Content:   content,
		Lines:     lines,
		TotalSize: int64(len(content)),
		ModTime:   time.Unix(1700000000, 0),
	}
}

// loadedDoc returns a document that has completed one load, as if it were open
// on screen.
func loadedDoc(t *testing.T, root, relPath, content string) *Model {
	t.Helper()
	m := New(nil)
	m.SetSize(80, 20)
	_ = m.Load(3, root, relPath, 0, 11)
	if !m.SetResult(LoadedMsg{
		ModelID: 3, RequestGeneration: m.requestGeneration, Epoch: 11,
		Path: relPath, Result: result(content),
	}) {
		t.Fatal("SetResult() = false for the initial load")
	}
	return m
}

func refreshMsg(m *Model, r filepreview.PreviewResult) LoadedMsg {
	return LoadedMsg{
		ModelID: m.modelID, RequestGeneration: m.requestGeneration, Epoch: m.epoch,
		Path: m.path, Result: r, Refresh: true,
	}
}

func TestRefreshIsNotOfferedUntilTheFileMoves(t *testing.T) {
	m := loadedDoc(t, t.TempDir(), "doc.md", body(50))
	if cmd := m.Refresh(false); cmd != nil {
		t.Fatal("Refresh() returned a command with no change observed")
	}
}

func TestRefreshIsNotOfferedWithoutARoot(t *testing.T) {
	// The overview loads documents by file descriptor, so the model never learns
	// where the file lives unless the host says. Without a root there is no path
	// to re-read, and Refresh must decline rather than guess.
	m := New(nil)
	m.SetSize(80, 20)
	_ = m.load(3, "doc.md", 0, 11, func() tea.Msg { return nil })
	m.Observe()
	if cmd := m.Refresh(false); cmd != nil {
		t.Fatal("Refresh() returned a command for a model with no root")
	}
}

func TestRefreshIsOfferedOnceTheFileMoves(t *testing.T) {
	m := loadedDoc(t, t.TempDir(), "doc.md", body(50))
	m.Observe()
	if cmd := m.Refresh(false); cmd == nil {
		t.Fatal("Refresh() = nil after the file changed")
	}
	m.Observe()
	if cmd := m.Refresh(false); cmd != nil {
		t.Fatal("Refresh() stacked a second read while one was in flight")
	}
}

func TestRefreshSuppressedStaysOwed(t *testing.T) {
	m := loadedDoc(t, t.TempDir(), "doc.md", body(50))
	m.Observe()
	if cmd := m.Refresh(true); cmd != nil {
		t.Fatal("Refresh(suppressed=true) issued a command")
	}
	if !m.RefreshPending() {
		t.Fatal("a suppressed refresh was dropped instead of deferred")
	}
	if cmd := m.Refresh(false); cmd == nil {
		t.Fatal("the deferred refresh did not land once the veto lifted")
	}
}

// The motivating case: an agent appends to a markdown file the user is reading
// a third of the way down. The reader must not be thrown back to line one.
func TestRefreshPreservesScroll(t *testing.T) {
	m := loadedDoc(t, t.TempDir(), "doc.md", body(200))
	m.Scroll(40)
	before := m.ScrollOffset()
	if before == 0 {
		t.Fatal("test setup did not scroll the document")
	}

	m.Observe()
	m.Refresh(false)
	if !m.SetResult(refreshMsg(m, result(body(220)))) {
		t.Fatal("SetResult() = false for a changed refresh")
	}
	if got := m.ScrollOffset(); got != before {
		t.Fatalf("scroll = %d after refresh, want %d preserved", got, before)
	}
}

// "Preserve where the content still supports it": a document that lost most of
// its length pulls the viewport back to the new end instead of showing blank.
func TestRefreshClampsScrollWhenTheDocumentShrinks(t *testing.T) {
	m := loadedDoc(t, t.TempDir(), "doc.md", body(200))
	m.Scroll(150)
	if m.ScrollOffset() == 0 {
		t.Fatal("test setup did not scroll the document")
	}

	m.Observe()
	m.Refresh(false)
	if !m.SetResult(refreshMsg(m, result(body(10)))) {
		t.Fatal("SetResult() = false for a changed refresh")
	}
	if got := m.ScrollOffset(); got > 10 {
		t.Fatalf("scroll = %d after the document shrank to 10 lines; it was not clamped", got)
	}
}

func TestUnchangedRefreshDoesNotRepaint(t *testing.T) {
	content := body(50)
	m := loadedDoc(t, t.TempDir(), "doc.md", content)
	m.Observe()
	m.Refresh(false)

	if m.SetResult(refreshMsg(m, result(content))) {
		t.Fatal("SetResult() = true for a write that changed nothing; the pane would flash")
	}
}

func TestNotModifiedRefreshDoesNotReplaceContent(t *testing.T) {
	content := body(50)
	m := loadedDoc(t, t.TempDir(), "doc.md", content)
	m.Observe()
	cmd := m.RefreshFrom(func() tea.Msg {
		return NotModified{Path: "doc.md", Epoch: m.epoch, Revision: "r2"}
	})
	if cmd == nil {
		t.Fatal("RefreshFrom() = nil after Observe")
	}
	msg, ok := cmd().(LoadedMsg)
	if !ok {
		t.Fatalf("RefreshFrom returned %T", cmd())
	}
	if !msg.NotModified || !msg.Refresh {
		t.Fatalf("msg = %#v", msg)
	}
	if m.SetResult(msg) {
		t.Fatal("NotModified refresh replaced content")
	}
	if m.result.Content != content {
		t.Fatal("NotModified refresh dropped the document on screen")
	}
	if m.revision != "r2" {
		t.Fatalf("revision = %q, want r2", m.revision)
	}
}

// A file caught mid-write reads as empty or truncated. Flickering through
// nothing is worse than holding the last good content for a moment.
func TestFailedRefreshKeepsTheRenderedDocument(t *testing.T) {
	m := loadedDoc(t, t.TempDir(), "doc.md", body(50))
	m.Observe()
	m.Refresh(false)

	if m.SetResult(refreshMsg(m, filepreview.PreviewResult{Error: errors.New("no such file")})) {
		t.Fatal("SetResult() = true for a failed refresh")
	}
	if m.result.Error != nil {
		t.Fatalf("a failed refresh surfaced an error: %v", m.result.Error)
	}
	if m.result.Content == "" {
		t.Fatal("a failed refresh discarded the document on screen")
	}
}

func TestRetargetingClearsTheRefreshGate(t *testing.T) {
	root := t.TempDir()
	m := loadedDoc(t, root, "first.md", body(50))
	m.Observe()

	_ = m.Load(3, root, "second.md", 0, 11)
	if m.RefreshPending() {
		t.Fatal("a refresh owed for the previous document survived the retarget")
	}
}

func TestWatchTargetIsTheSingleFile(t *testing.T) {
	root := t.TempDir()
	m := loadedDoc(t, root, filepath.Join("docs", "guide.md"), body(10))

	target := m.WatchTarget()
	if target.Dir {
		t.Fatal("WatchTarget() returned a directory; only the previewed path is watched")
	}
	if want := filepath.Join(root, "docs", "guide.md"); target.Path != want {
		t.Fatalf("WatchTarget().Path = %q, want %q", target.Path, want)
	}
}

func TestWatchTargetIsEmptyWithoutARoot(t *testing.T) {
	m := New(nil)
	if got := m.WatchTarget(); got.Path != "" {
		t.Fatalf("WatchTarget() = %+v for an unbound model, want empty", got)
	}
}

func TestSetRootEnablesRefreshForADescriptorLoad(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(root, "doc.md")
	if err := os.WriteFile(path, []byte(body(10)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	m := New(nil)
	m.SetSize(80, 20)
	cmd := m.LoadFile(4, f, "doc.md", 0, 11)
	msg, ok := cmd().(LoadedMsg)
	if !ok {
		t.Fatal("LoadFile() did not produce a LoadedMsg")
	}
	if !m.SetResult(msg) {
		t.Fatal("SetResult() = false for the descriptor load")
	}

	if got := m.WatchTarget(); got.Path != "" {
		t.Fatal("a descriptor load knew its path without SetRoot")
	}
	m.SetRoot(root)
	if got := m.WatchTarget().Path; got != path {
		t.Fatalf("WatchTarget().Path = %q after SetRoot, want %q", got, path)
	}
	m.Observe()
	if cmd := m.Refresh(false); cmd == nil {
		t.Fatal("Refresh() = nil after SetRoot gave the model a path to re-read")
	}
}

func TestAbsoluteDocumentKeepsItsPathAcrossHostRootAndRefresh(t *testing.T) {
	root := t.TempDir()
	path := filepath.Join(t.TempDir(), "outside.md")
	if err := os.WriteFile(path, []byte("one\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m := New(nil)
	loaded := m.Load(4, "", path, 0, 11)().(LoadedMsg)
	if !m.SetResult(loaded) {
		t.Fatal("absolute load result was rejected")
	}

	m.SetRoot(root)
	if got := m.Root(); got != "" {
		t.Fatalf("host SetRoot re-rooted absolute document to %q", got)
	}
	if got := m.WatchTarget().Path; got != path {
		t.Fatalf("absolute watch target = %q, want %q", got, path)
	}
	if err := os.WriteFile(path, []byte("two\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	m.Observe()
	cmd := m.Refresh(false)
	if cmd == nil {
		t.Fatal("absolute document did not refresh")
	}
	refreshed := cmd().(LoadedMsg)
	if refreshed.Result.Error != nil || refreshed.Result.Content != "two\n" {
		t.Fatalf("absolute refresh = error %v body %q", refreshed.Result.Error, refreshed.Result.Content)
	}
	if !m.SetResult(refreshed) {
		t.Fatal("absolute refresh result was rejected")
	}
}
