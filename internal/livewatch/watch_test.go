package livewatch

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fastConfig keeps the debounce short enough for tests to stay quick while
// still exercising the coalescing path.
func fastConfig() Config {
	return Config{Quiet: 20 * time.Millisecond, MaxLatency: 200 * time.Millisecond}
}

func newTestWatcher(t *testing.T, cfg Config) *PathWatcher {
	t.Helper()
	w, err := NewPathWatcher(cfg)
	if err != nil {
		t.Fatalf("NewPathWatcher() = %v", err)
	}
	t.Cleanup(w.Stop)
	return w
}

func awaitSignal(t *testing.T, w *PathWatcher) {
	t.Helper()
	select {
	case _, ok := <-w.Signals():
		if !ok {
			t.Fatal("signal channel closed while waiting for a change")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("timed out waiting for a change signal")
	}
}

func expectNoSignal(t *testing.T, w *PathWatcher, within time.Duration) {
	t.Helper()
	select {
	case _, ok := <-w.Signals():
		if ok {
			t.Fatal("got a change signal for a path that is not a target")
		}
	case <-time.After(within):
	}
}

func write(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestPathWatcherReportsFileTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "doc.md")
	write(t, target, "one")

	w := newTestWatcher(t, fastConfig())
	w.Watch(File(target))

	write(t, target, "two")
	awaitSignal(t, w)
}

func TestPathWatcherIgnoresOtherFilesInTheSameDirectory(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "doc.md")
	other := filepath.Join(dir, "unrelated.md")
	write(t, target, "one")

	w := newTestWatcher(t, fastConfig())
	w.Watch(File(target))

	// A file target registers the parent directory with the kernel, so the
	// watcher sees this event. It must not report it: a preview pane open on one
	// document should not re-read because a sibling changed.
	write(t, other, "noise")
	expectNoSignal(t, w, 250*time.Millisecond)

	write(t, target, "two")
	awaitSignal(t, w)
}

func TestPathWatcherReportsDirTarget(t *testing.T) {
	dir := t.TempDir()
	store := filepath.Join(dir, "store")
	if err := os.Mkdir(store, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}

	w := newTestWatcher(t, fastConfig())
	w.Watch(Dir(store))

	// A directory target is how the td store is watched: SQLite's WAL file may
	// move without the database file being touched at all.
	write(t, filepath.Join(store, "issues.db-wal"), "wal")
	awaitSignal(t, w)
}

func TestPathWatcherCoalescesABurst(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "doc.md")
	write(t, target, "one")

	w := newTestWatcher(t, fastConfig())
	w.Watch(File(target))

	for i := range 12 {
		write(t, target, strings.Repeat("x", i+1))
	}
	awaitSignal(t, w)

	// One burst, one signal. A second would mean an agent's rewrite loop costs a
	// re-read per write.
	expectNoSignal(t, w, 250*time.Millisecond)
}

func TestPathWatcherReportsCreateOfAMissingTarget(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "not-yet.md")

	w := newTestWatcher(t, fastConfig())
	// Watching a path that does not exist is normal: an agent has not written
	// the file yet. The registration on the parent still reports the create.
	w.Watch(File(target))

	write(t, target, "hello")
	awaitSignal(t, w)
}

func TestPathWatcherIgnoreFilter(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "doc.md")
	write(t, target, "one")

	w := newTestWatcher(t, Config{
		Quiet:      20 * time.Millisecond,
		MaxLatency: 200 * time.Millisecond,
		Ignore:     func(p string) bool { return strings.HasSuffix(p, ".md") },
	})
	w.Watch(File(target))

	write(t, target, "two")
	expectNoSignal(t, w, 250*time.Millisecond)
}

func TestPathWatcherWatchReplacesTheTargetSet(t *testing.T) {
	dir := t.TempDir()
	first := filepath.Join(dir, "first.md")
	second := filepath.Join(dir, "second.md")
	write(t, first, "one")
	write(t, second, "one")

	w := newTestWatcher(t, fastConfig())
	w.Watch(File(first))
	w.Watch(File(second))

	// Navigating from one document to another must stop reporting the old one,
	// or a closed tab keeps waking the pane up.
	write(t, first, "changed")
	expectNoSignal(t, w, 250*time.Millisecond)

	write(t, second, "changed")
	awaitSignal(t, w)
}

func TestPathWatcherEmptyWatchReleasesRegistrations(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "doc.md")
	write(t, target, "one")

	w := newTestWatcher(t, fastConfig())
	w.Watch(File(target))
	if got := len(w.WatchedDirs()); got != 1 {
		t.Fatalf("WatchedDirs() = %d directories, want 1", got)
	}

	// Navigating away releases the descriptors without tearing the watcher down.
	w.Watch()
	if got := w.WatchedDirs(); len(got) != 0 {
		t.Fatalf("WatchedDirs() = %v after Watch() with no targets, want none", got)
	}
}

func TestPathWatcherStopReleasesEveryRegistration(t *testing.T) {
	dir := t.TempDir()
	a := filepath.Join(dir, "a")
	b := filepath.Join(dir, "b")
	for _, d := range []string{a, b} {
		if err := os.Mkdir(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
	}

	w, err := NewPathWatcher(fastConfig())
	if err != nil {
		t.Fatalf("NewPathWatcher() = %v", err)
	}
	w.Watch(Dir(a), Dir(b))
	if got := len(w.WatchedDirs()); got != 2 {
		t.Fatalf("WatchedDirs() = %d, want 2", got)
	}

	w.Stop()
	if got := w.WatchedDirs(); len(got) != 0 {
		t.Fatalf("WatchedDirs() = %v after Stop(), want none", got)
	}
	if _, ok := <-w.Signals(); ok {
		t.Fatal("signal channel still open after Stop(); a consumer could block forever")
	}
}

func TestPathWatcherStopIsIdempotent(t *testing.T) {
	w, err := NewPathWatcher(fastConfig())
	if err != nil {
		t.Fatalf("NewPathWatcher() = %v", err)
	}
	w.Stop()
	w.Stop() // Must not panic on a second close.
}

func TestPathWatcherWatchAfterStopIsInert(t *testing.T) {
	dir := t.TempDir()
	w, err := NewPathWatcher(fastConfig())
	if err != nil {
		t.Fatalf("NewPathWatcher() = %v", err)
	}
	w.Stop()

	// A pane that closed while its watcher was mid-flight can still call Watch.
	w.Watch(Dir(dir))
	if got := w.WatchedDirs(); len(got) != 0 {
		t.Fatalf("WatchedDirs() = %v after Stop(); a stopped watcher must not re-register", got)
	}
}

// TestPathWatcherRepeatedOpenCloseDoesNotLeak stands in for the fd-hygiene
// requirement: repeatedly opening and closing a pane must give every descriptor
// back. WatchedDirs is the observable proxy — on macOS, fsnotify's kqueue
// backend holds a descriptor per file in each registered directory, so a
// registration that outlives its pane is a leak.
func TestPathWatcherRepeatedOpenCloseDoesNotLeak(t *testing.T) {
	dir := t.TempDir()
	target := filepath.Join(dir, "doc.md")
	write(t, target, "one")

	for range 25 {
		w, err := NewPathWatcher(fastConfig())
		if err != nil {
			t.Fatalf("NewPathWatcher() = %v", err)
		}
		w.Watch(File(target))
		if got := len(w.WatchedDirs()); got != 1 {
			t.Fatalf("WatchedDirs() = %d, want 1", got)
		}
		w.Stop()
		if got := len(w.WatchedDirs()); got != 0 {
			t.Fatalf("WatchedDirs() = %d after Stop(), want 0", got)
		}
	}
}

func TestPathWatcherCapsRegistrations(t *testing.T) {
	root := t.TempDir()
	targets := make([]Target, 0, maxRegistrations+20)
	for i := range maxRegistrations + 20 {
		d := filepath.Join(root, string(rune('a'+i%26))+string(rune('a'+i/26)))
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		targets = append(targets, Dir(d))
	}

	w := newTestWatcher(t, fastConfig())
	w.Watch(targets...)
	if got := len(w.WatchedDirs()); got > maxRegistrations {
		t.Fatalf("WatchedDirs() = %d, want at most %d", got, maxRegistrations)
	}
}
