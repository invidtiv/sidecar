package filebrowser

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
)

// td-03c21c on the Files surface: the preview already reloaded on a filesystem
// change, but every reload was applied — wiping the selection and re-rendering
// for writes that changed nothing — and nothing held it back while the inline
// editor owned the pane.

func livePreviewPlugin(t *testing.T, content string) (*Plugin, string) {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "doc.md")
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}

	p := &Plugin{
		ctx: &plugin.Context{
			WorkDir:     dir,
			ProjectRoot: dir,
			Logger:      slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError})),
		},
		width:         120,
		height:        40,
		stateRestored: true,
	}
	p.tree = NewFileTree(dir)
	if err := p.tree.Build(); err != nil {
		t.Fatalf("build tree: %v", err)
	}
	p.previewFile = "doc.md"

	// An explicit load is what puts content on screen and arms the refresh gate.
	msg, ok := LoadPreview(dir, "doc.md", 0)().(PreviewLoadedMsg)
	if !ok {
		t.Fatal("LoadPreview did not produce a PreviewLoadedMsg")
	}
	p.adoptPreviewFingerprint(msg.Result)
	p.applyPreviewResult(msg.Result)
	return p, path
}

// runRefreshOnce drives one full watcher-signal cycle and returns whether the
// pane was repainted.
func runRefreshOnce(t *testing.T, p *Plugin) bool {
	t.Helper()
	cmd := p.refreshPreview()
	if cmd == nil {
		return false
	}
	msg, ok := cmd().(previewRefreshedMsg)
	if !ok {
		t.Fatalf("refreshPreview produced %T, want previewRefreshedMsg", cmd())
	}
	before := strings.Join(p.previewLines, "\n")
	p.applyPreviewRefresh(msg)
	return strings.Join(p.previewLines, "\n") != before
}

func body(lines int) string {
	var b strings.Builder
	for i := range lines {
		b.WriteString("line ")
		b.WriteString(strings.Repeat("y", 1+i%5))
		b.WriteString("\n")
	}
	return b.String()
}

func TestPreviewRefreshPicksUpAnExternalWrite(t *testing.T) {
	p, path := livePreviewPlugin(t, body(30))
	if err := os.WriteFile(path, []byte(body(30)+"appended by an agent\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !runRefreshOnce(t, p) {
		t.Fatal("an external write did not reach the preview")
	}
	joined := strings.Join(p.previewLines, "\n")
	if !strings.Contains(joined, "appended by an agent") {
		t.Fatal("the appended line is not in the preview")
	}
}

func TestUnchangedRewriteDoesNotRepaint(t *testing.T) {
	content := body(30)
	p, path := livePreviewPlugin(t, content)

	// A formatter with nothing to do, or a save with no edits: the file is
	// rewritten byte-identically. Applying it would clear the selection and drop
	// the rendered markdown for no reason the user can see.
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if runRefreshOnce(t, p) {
		t.Fatal("an identical rewrite repainted the preview")
	}
}

func TestPreviewRefreshPreservesScroll(t *testing.T) {
	p, path := livePreviewPlugin(t, body(400))
	p.previewScroll = 120

	if err := os.WriteFile(path, []byte(body(420)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !runRefreshOnce(t, p) {
		t.Fatal("the write did not reach the preview")
	}
	if p.previewScroll != 120 {
		t.Fatalf("previewScroll = %d after refresh, want 120 preserved", p.previewScroll)
	}
}

func TestPreviewRefreshClampsScrollWhenTheFileShrinks(t *testing.T) {
	p, path := livePreviewPlugin(t, body(400))
	p.previewScroll = 350

	if err := os.WriteFile(path, []byte(body(10)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if !runRefreshOnce(t, p) {
		t.Fatal("the write did not reach the preview")
	}
	if p.previewScroll > 10 {
		t.Fatalf("previewScroll = %d after the file shrank to 10 lines; it was not clamped", p.previewScroll)
	}
}

// The inline editor runs a real editor in tmux and is itself what is writing
// the file. Refreshing underneath it churns a pane nobody can see, and vim
// writes a probe, a swap file and a backup on every save.
func TestInlineEditSuppressesTheRefresh(t *testing.T) {
	p, path := livePreviewPlugin(t, body(30))
	p.edit.Active = true

	if err := os.WriteFile(path, []byte(body(30)+"saved from vim\n"), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	if cmd := p.refreshPreview(); cmd != nil {
		t.Fatal("refreshPreview issued a read while the inline editor owned the pane")
	}
	if !p.previewLive.Pending() {
		t.Fatal("the suppressed change was dropped instead of deferred")
	}

	// Leaving the editor lets the deferred change land, with the editor's own
	// saved content — never a buffer the editor still owns.
	p.edit.Active = false
	if !runRefreshOnce(t, p) {
		t.Fatal("the deferred refresh did not land after the editor exited")
	}
	if !strings.Contains(strings.Join(p.previewLines, "\n"), "saved from vim") {
		t.Fatal("the editor's saved content did not reach the preview")
	}
}

func TestOverlaysSuppressTheRefresh(t *testing.T) {
	for name, set := range map[string]func(*Plugin){
		"info":  func(p *Plugin) { p.infoMode = true },
		"blame": func(p *Plugin) { p.blameMode = true },
	} {
		t.Run(name, func(t *testing.T) {
			p, path := livePreviewPlugin(t, body(30))
			set(p)
			if err := os.WriteFile(path, []byte(body(31)), 0o644); err != nil {
				t.Fatalf("write: %v", err)
			}
			if cmd := p.refreshPreview(); cmd != nil {
				t.Fatalf("refreshPreview issued a read while the %s overlay owned the pane", name)
			}
			if !p.previewLive.Pending() {
				t.Fatal("the suppressed change was dropped instead of deferred")
			}
		})
	}
}

func TestRefreshIsNotOfferedWithNoPreview(t *testing.T) {
	p, _ := livePreviewPlugin(t, body(10))
	p.previewFile = ""
	if cmd := p.refreshPreview(); cmd != nil {
		t.Fatal("refreshPreview issued a read with no file previewed")
	}
}

// A result for a file the user has since navigated away from must not be
// painted over the file they are now looking at.
func TestRefreshResultForAnotherFileIsDropped(t *testing.T) {
	p, path := livePreviewPlugin(t, body(30))
	if err := os.WriteFile(path, []byte(body(40)), 0o644); err != nil {
		t.Fatalf("write: %v", err)
	}
	cmd := p.refreshPreview()
	if cmd == nil {
		t.Fatal("refreshPreview = nil after a write")
	}
	msg := cmd().(previewRefreshedMsg)

	before := strings.Join(p.previewLines, "\n")
	p.previewFile = "somewhere/else.md"
	p.applyPreviewRefresh(msg)
	if strings.Join(p.previewLines, "\n") != before {
		t.Fatal("a result for the previous file was painted after navigating away")
	}
}

// Navigating to a different file must clear the gate, or the new file's first
// load could be mistaken for "unchanged" against the old file's fingerprint.
func TestNavigationResetsTheRefreshGate(t *testing.T) {
	p, _ := livePreviewPlugin(t, body(30))
	p.previewLive.Observe()

	other, _ := LoadPreview(p.ctx.WorkDir, "doc.md", 0)().(PreviewLoadedMsg)
	p.adoptPreviewFingerprint(other.Result)
	if p.previewLive.Pending() {
		t.Fatal("a refresh owed for the previous file survived the navigation")
	}
}
