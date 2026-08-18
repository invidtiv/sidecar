package overview

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/marcus/sidecar/internal/livepanes"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The motivating case on this surface: an agent writes a file the user is
// reading in the preview beside its shell.
func TestPreviewDocPaneRereadsWhenTheFileChanges(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindShell)
	t.Cleanup(m.stopLiveWatchers)
	runLive(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	if m.preview.doc == nil {
		t.Fatal("the file link opened no preview document")
	}

	runLive(t, m, m.reconcileLiveWatches())
	watcher := m.preview.live.Watcher(livePreviewDocs)
	if watcher == nil {
		t.Fatal("a visible preview document started no watcher")
	}

	path := filepath.Join(m.preview.doc.root, "README.md")
	if err := os.WriteFile(path, []byte("# Hello from preview\nAGENT WROTE THIS\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	select {
	case <-watcher.Signals():
	case <-time.After(5 * time.Second):
		t.Fatal("the changed file produced no signal")
	}
	changed, _ := m.preview.live.Handle(livepanes.ChangedMsg{Owner: livePreviewOwner, Kind: livePreviewDocs})
	runLive(t, m, changed)

	if got := ansi.Strip(m.WorkspacesView(previewWide, previewTall)); !strings.Contains(got, "AGENT WROTE THIS") {
		t.Fatal("the preview document pane did not live-update")
	}
}

// A preview the layout could not place is not on screen, and is not worth a
// registration.
func TestUnplaceablePreviewPaneIsNotWatched(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindShell)
	t.Cleanup(m.stopLiveWatchers)
	runLive(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))

	if got := m.previewDocTargets(); len(got) != 1 {
		t.Fatalf("previewDocTargets() = %v for a placed preview, want one", got)
	}

	// A window too narrow to lay the tree out zooms to the focused leaf alone.
	m.WorkspacesView(10, 6)
	if m.previewPaneVisible(panelayout.Document) {
		return // the document is the focused leaf; nothing to assert
	}
	if got := m.previewDocTargets(); len(got) != 0 {
		t.Fatalf("previewDocTargets() = %v for a pane the layout did not place, want none", got)
	}
}

// Parity, checked rather than remembered: the global browser must register the
// same live kinds the project surface does. Its half is asserted in
// internal/plugins/workspace.
func TestGlobalSurfaceRegistersEveryLiveKind(t *testing.T) {
	m, _ := previewModel(t)
	m.preview.live = m.newLiveSet()
	t.Cleanup(m.stopLiveWatchers)
	for _, kind := range []string{livePreviewIssues, livePreviewDocs, livePreviewDiffs} {
		if _, handled := m.preview.live.Handle(livepanes.ChangedMsg{Owner: livePreviewOwner, Kind: kind}); !handled {
			t.Errorf("the %q kind is not registered on the global surface", kind)
		}
	}
}

// The two surfaces share a message bus, so each must ignore the other's
// signals: a workspace refresh driving the global browser's panes would re-arm
// a listener on a watcher it does not own.
func TestGlobalSurfaceIgnoresTheProjectSurfacesMessages(t *testing.T) {
	m, _ := previewModel(t)
	m.preview.live = m.newLiveSet()
	t.Cleanup(m.stopLiveWatchers)
	if _, handled := m.handleLiveWatchMsg(livepanes.ChangedMsg{Owner: "workspace", Kind: "docs"}); handled {
		t.Fatal("the global browser claimed the project surface's ChangedMsg")
	}
	if isLiveWatchMessage(livepanes.ChangedMsg{Owner: "workspace", Kind: "docs"}) {
		t.Fatal("the project surface's message was classified as this surface's background work")
	}
	if !isLiveWatchMessage(livepanes.ChangedMsg{Owner: livePreviewOwner, Kind: "docs"}) {
		t.Fatal("this surface's own message was not classified as background work")
	}
}

// runLive is run() with a per-command timeout and a depth cap, since
// livewatch.Listen blocks and tea.Tick chains never end.
func runLive(t *testing.T, m *Model, cmd tea.Cmd) { runLiveDepth(t, m, cmd, 0) }

func runLiveDepth(t *testing.T, m *Model, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 6 {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(300 * time.Millisecond):
		return
	}
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			runLiveDepth(t, m, sub, depth+1)
		}
		return
	}
	if started, ok := msg.(livepanes.WatchStartedMsg); ok {
		// Adopt the watcher but drop the livewatch.Listen it returns: a listener
		// goroutine here would consume the signal the test waits for.
		m.handleLiveWatchMsg(started)
		return
	}
	runLiveDepth(t, m, m.Update(msg), depth+1)
}
