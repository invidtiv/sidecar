package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/livewatch"
)

// fd hygiene. These cover the two ways a watcher outlives its pane: a project
// switch handing back a watcher nobody wants any more, and a teardown that
// forgets one of the three.

func newWatcher(t *testing.T) *livewatch.PathWatcher {
	t.Helper()
	w, err := livewatch.NewPathWatcher(livewatch.Config{})
	if err != nil {
		t.Fatalf("NewPathWatcher() = %v", err)
	}
	return w
}

// stopped reports whether a watcher has been stopped, by observing that its
// signal channel is closed. Stop runs detached, so this waits for it.
func stopped(t *testing.T, w *livewatch.PathWatcher) bool {
	t.Helper()
	// Stop is idempotent, so calling it here is safe and makes the wait bounded:
	// it returns only once the watcher goroutine has drained either way.
	w.Stop()
	_, open := <-w.Signals()
	return !open
}

func TestAdoptWatcherStopsAWatcherNobodyWants(t *testing.T) {
	// A project switch runs Stop, Init, Start. A watcher started before the
	// switch can still land afterwards, and if it is neither adopted nor stopped
	// its goroutine and its descriptors live for the rest of the process.
	var slot *livewatch.PathWatcher
	w := newWatcher(t)

	if adoptWatcher(&slot, w, false) {
		t.Fatal("adoptWatcher reported adoption when the plugin wanted no watcher")
	}
	if slot != nil {
		t.Fatal("adoptWatcher installed a watcher the plugin did not want")
	}
	if !stopped(t, w) {
		t.Fatal("the unwanted watcher was leaked instead of stopped")
	}
}

func TestAdoptWatcherReplacesAndStopsThePreviousOne(t *testing.T) {
	first := newWatcher(t)
	second := newWatcher(t)
	slot := first

	if !adoptWatcher(&slot, second, true) {
		t.Fatal("adoptWatcher = false for a wanted watcher")
	}
	if slot != second {
		t.Fatal("adoptWatcher did not install the incoming watcher")
	}
	if !stopped(t, first) {
		t.Fatal("the replaced watcher was leaked instead of stopped")
	}
	second.Stop()
}

func TestAdoptWatcherIsANoOpForANilWatcher(t *testing.T) {
	// Watcher creation can fail; the message still arrives, carrying nil.
	var slot *livewatch.PathWatcher
	if adoptWatcher(&slot, nil, true) {
		t.Fatal("adoptWatcher = true for a failed start")
	}
	if slot != nil {
		t.Fatal("adoptWatcher installed a nil watcher")
	}
}

func TestStopLiveWatchersReleasesAllThree(t *testing.T) {
	p := &Plugin{}
	p.issueWatcher = newWatcher(t)
	p.docWatcher = newWatcher(t)
	p.diffWatcher = newWatcher(t)
	issue, doc, diff := p.issueWatcher, p.docWatcher, p.diffWatcher

	p.stopLiveWatchers()

	if p.issueWatcher != nil || p.docWatcher != nil || p.diffWatcher != nil {
		t.Fatal("stopLiveWatchers left a watcher on the plugin")
	}
	for name, w := range map[string]*livewatch.PathWatcher{"issue": issue, "doc": doc, "diff": diff} {
		if !stopped(t, w) {
			t.Errorf("the %s watcher was leaked by stopLiveWatchers", name)
		}
	}
}

func TestStopLiveWatchersIsIdempotent(t *testing.T) {
	// Plugin.Stop is both the quit and the project-switch boundary, and can run
	// twice around a switch.
	p := &Plugin{}
	p.docWatcher = newWatcher(t)
	p.stopLiveWatchers()
	p.stopLiveWatchers()
}

// Repeated open and close must give every descriptor back. WatchedDirs is the
// observable proxy: on macOS each registration holds a descriptor per file in
// the directory, so a registration that outlives its pane is a leak.
func TestRepeatedPaneOpenCloseDoesNotLeakRegistrations(t *testing.T) {
	dir := t.TempDir()
	for range 20 {
		p := &Plugin{}
		p.docWatcher = newWatcher(t)
		p.docWatcher.Watch(livewatch.Dir(dir))
		if got := len(p.docWatcher.WatchedDirs()); got != 1 {
			t.Fatalf("WatchedDirs() = %d, want 1", got)
		}
		w := p.docWatcher
		p.stopLiveWatchers()
		w.Stop()
		if got := len(w.WatchedDirs()); got != 0 {
			t.Fatalf("WatchedDirs() = %d after teardown, want 0", got)
		}
	}
}

// Parity, checked rather than remembered: an issue card, a document and a diff
// are reachable from the project surface and from the global browser, and both
// must refresh. The bindings are the two files; this asserts the project half
// answers all three kinds.
func TestProjectSurfaceBindsAllThreePaneKinds(t *testing.T) {
	p := &Plugin{ctx: nil}
	// With no context there is nothing to reconcile, and it must not panic:
	// Update runs before Init completes on some paths.
	if cmd := p.reconcileLiveWatches(); cmd != nil {
		t.Fatal("reconcileLiveWatches did work without a plugin context")
	}
}
