package workspace

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/livepanes"
)

// place records a frame that put these content leaves on screen, which is what
// the watch set is built from. A test that opens a pane and never draws is a
// test with nothing visible.
func place(p *Plugin, leafIDs ...int) {
	layout := PaneLayout{}
	for _, id := range leafIDs {
		leaf := FindPane(p.paneRoot, id)
		if leaf == nil {
			continue
		}
		layout.Leaves = append(layout.Leaves, Placement{Node: leaf})
	}
	p.paneFrame, p.paneFrameDrawn = layout, true
}

// runLive drains a command tree, feeding results back into the plugin. Commands
// run detached because livewatch.Listen blocks until the next signal.
func runLive(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	runLiveDepth(t, p, cmd, 0)
}

func runLiveDepth(t *testing.T, p *Plugin, cmd tea.Cmd, depth int) {
	t.Helper()
	if cmd == nil || depth > 8 {
		return
	}
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	var msg tea.Msg
	select {
	case msg = <-done:
	case <-time.After(2 * time.Second):
		return
	}
	switch m := msg.(type) {
	case nil:
	case tea.BatchMsg:
		for _, child := range m {
			runLiveDepth(t, p, child, depth+1)
		}
	case docview.LoadedMsg:
		p.applyDocLoaded(m)
	case livepanes.WatchStartedMsg:
		// Adopt the watcher but drop the livewatch.Listen it returns: a listener
		// goroutine here would consume the signal the test waits for.
		p.handleLiveWatchMsg(m)
	default:
		if cmd, handled := p.handleLiveWatchMsg(msg); handled {
			runLiveDepth(t, p, cmd, depth+1)
		}
	}
}

// docPaneForLiveTest opens one document pane on a fresh plugin and lands its
// initial load.
func docPaneForLiveTest(t *testing.T, root, rel string) *Plugin {
	t.Helper()
	p := docPaneTestPlugin(t, root, true)
	runLive(t, p, p.openTerminalPath(rel, 0))
	doc, leaf := p.activeDocPane()
	if doc == nil || leaf == nil {
		t.Fatalf("opening %q produced no document pane", rel)
	}
	place(p, leaf.ID)
	return p
}

func docPaneText(t *testing.T, p *Plugin) string {
	t.Helper()
	doc, _ := p.activeDocPane()
	if doc == nil || doc.view() == nil {
		t.Fatal("no document pane")
	}
	doc.view().SetSize(80, 20)
	return doc.view().View()
}

// The motivating case: an agent writes a file the user is reading beside it.
func TestVisibleDocPaneRereadsWhenTheFileChanges(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "notes.md", "# notes\n\nBEFORE\n")
	p := docPaneForLiveTest(t, root, "notes.md")
	t.Cleanup(p.stopLiveWatchers)

	runLive(t, p, p.reconcileLiveWatches())
	watcher := p.live.Watcher(liveDocs)
	if watcher == nil {
		t.Fatal("a visible document pane started no watcher")
	}

	writeDocPaneFixture(t, root, "notes.md", "# notes\n\nAFTER the agent wrote\n")
	select {
	case <-watcher.Signals():
	case <-time.After(5 * time.Second):
		t.Fatal("the changed file produced no signal")
	}
	changed, _ := p.live.Handle(livepanes.ChangedMsg{Owner: liveOwner, Kind: liveDocs})
	runLive(t, p, changed)

	if got := docPaneText(t, p); !strings.Contains(got, "AFTER") {
		t.Fatalf("the document pane did not live-update; it still shows:\n%s", got)
	}
}

// Watching only what is on screen is the whole cost argument: on macOS a
// registration costs a descriptor per file in the watched directory.
func TestOnlyVisibleContentPanesAreWatched(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "notes.md", "# notes\n")
	p := docPaneForLiveTest(t, root, "notes.md")
	t.Cleanup(p.stopLiveWatchers)

	if got := p.docWatchTargets(); len(got) != 1 {
		t.Fatalf("docWatchTargets() = %v for one visible pane, want one", got)
	}

	// The frame stops placing it — a modal, the kanban board, a window too small
	// to lay the tree out.
	p.paneFrame, p.paneFrameDrawn = PaneLayout{}, false
	if got := p.docWatchTargets(); len(got) != 0 {
		t.Fatalf("docWatchTargets() = %v with nothing on screen, want none", got)
	}
	if got := p.issueWatchTargets(); len(got) != 0 {
		t.Fatalf("issueWatchTargets() = %v with nothing on screen, want none", got)
	}
	if got := p.diffWatchRoot(); got != "" && p.diff.WorkDir == "" {
		t.Fatalf("diffWatchRoot() = %q with nothing on screen, want empty", got)
	}
}

// A pane that leaves the screen gives its descriptors back, and the watcher is
// kept for its return rather than torn down and rebuilt.
func TestLeavingTheScreenReleasesRegistrationsAndKeepsTheWatcher(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "notes.md", "# notes\n")
	p := docPaneForLiveTest(t, root, "notes.md")
	t.Cleanup(p.stopLiveWatchers)

	runLive(t, p, p.reconcileLiveWatches())
	watcher := p.live.Watcher(liveDocs)
	if watcher == nil || len(watcher.WatchedDirs()) != 1 {
		t.Fatalf("WatchedDirs() = %v for one visible pane, want one", watcher)
	}

	p.paneFrame, p.paneFrameDrawn = PaneLayout{}, false
	runLive(t, p, p.reconcileLiveWatches())

	if got := watcher.WatchedDirs(); len(got) != 0 {
		t.Fatalf("WatchedDirs() = %v after the pane left the screen, want none", got)
	}
	if p.live.Watcher(liveDocs) == nil {
		t.Fatal("the watcher was torn down rather than released")
	}
}

func TestStopLiveWatchersIsIdempotent(t *testing.T) {
	// Plugin.Stop is both the quit and the project-switch boundary, and can run
	// twice around a switch.
	root := t.TempDir()
	writeDocPaneFixture(t, root, "notes.md", "# notes\n")
	p := docPaneForLiveTest(t, root, "notes.md")
	runLive(t, p, p.reconcileLiveWatches())

	p.stopLiveWatchers()
	p.stopLiveWatchers()

	if p.live.Watcher(liveDocs) != nil {
		t.Fatal("stopLiveWatchers left a watcher on the plugin")
	}
}

func TestReconcileDoesNothingWithoutAPluginContext(t *testing.T) {
	// Update runs before Init completes on some paths, and must not panic.
	p := &Plugin{ctx: nil}
	if cmd := p.reconcileLiveWatches(); cmd != nil {
		t.Fatal("reconcileLiveWatches did work without a plugin context")
	}
}

// Parity, checked rather than remembered: an issue card, a document and a diff
// are reachable from this surface and from the global browser, and both must
// refresh. This asserts the project half registers all three kinds; the global
// half is asserted in internal/overview.
func TestProjectSurfaceRegistersEveryLiveKind(t *testing.T) {
	p := docPaneTestPlugin(t, t.TempDir(), true)
	p.live = p.newLiveSet()
	t.Cleanup(p.stopLiveWatchers)
	for _, kind := range []string{liveIssues, liveDocs, liveDiffs} {
		if _, handled := p.live.Handle(livepanes.ChangedMsg{Owner: liveOwner, Kind: kind}); !handled {
			t.Errorf("the %q kind is not registered on the project surface", kind)
		}
	}
}
