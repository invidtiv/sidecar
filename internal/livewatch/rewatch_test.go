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
