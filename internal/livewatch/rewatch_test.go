package livewatch

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// A watched directory that is removed and recreated — a checkout across a
// branch without it, a worktree rebuild, a tool that replaces a folder rather
// than its contents — loses its kqueue/inotify registration. The failure this
// guards is silent and permanent: the pane underneath simply stops updating for
// the life of the process, with nothing on screen to say so.
func TestWatchSurvivesTheWatchedDirectoryBeingReplaced(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs")
	file := filepath.Join(dir, "notes.md")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewPathWatcher(Config{Quiet: 30 * time.Millisecond, MaxLatency: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	w.Watch(File(file))

	// Baseline: an ordinary write signals.
	if err := os.WriteFile(file, []byte("first\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(w, 2*time.Second) {
		t.Fatal("no signal for an ordinary write")
	}

	// The directory goes away and comes back with the file in it.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("after\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	drainSignals(w)

	// Reconcile runs on every host update; it must notice the registration died.
	w.Watch(File(file))

	if err := os.WriteFile(file, []byte("after the replace\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if !waitSignal(w, 2*time.Second) {
		t.Fatal("the watch did not survive the watched directory being replaced")
	}
}

// The window between a watched directory dying and its re-add is a window in
// which nothing is observed. A file written inside it produces no event, so the
// re-add itself has to report, or the pane holds content that was already stale
// when the watch came back.
func TestARewatchReportsWhatItMissedWhileUnregistered(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "docs")
	file := filepath.Join(dir, "notes.md")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w, err := NewPathWatcher(Config{Quiet: 30 * time.Millisecond, MaxLatency: 100 * time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Stop()
	w.Watch(File(file))

	// The directory goes away, and stays away long enough that the registration
	// is definitely gone rather than merely in doubt.
	if err := os.RemoveAll(dir); err != nil {
		t.Fatal(err)
	}
	if !waitFor(2*time.Second, func() bool { return len(w.WatchedDirs()) == 0 }) {
		t.Fatal("the registration outlived the directory")
	}
	drainSignals(w)

	// It comes back, already holding content this watcher never saw arrive.
	// Nothing here produces an event: the directory is not registered.
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte("written while nobody was watching\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	w.Watch(File(file))

	if !waitSignal(w, 2*time.Second) {
		t.Fatal("re-registering a lost watch reported nothing, so the write inside the gap is invisible")
	}
}

func waitFor(d time.Duration, cond func() bool) bool {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if cond() {
			return true
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cond()
}

func waitSignal(w *PathWatcher, d time.Duration) bool {
	select {
	case _, ok := <-w.Signals():
		return ok
	case <-time.After(d):
		return false
	}
}

func drainSignals(w *PathWatcher) {
	for {
		select {
		case <-w.Signals():
		case <-time.After(300 * time.Millisecond):
			return
		}
	}
}
