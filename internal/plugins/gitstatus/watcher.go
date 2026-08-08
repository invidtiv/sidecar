package gitstatus

import (
	"log/slog"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors the .git directory for changes.
type Watcher struct {
	fsWatcher *fsnotify.Watcher
	events    chan WatchEvent
	stop      chan struct{}
	mu        sync.Mutex
	stopped   bool
}

// WatchEvent distinguishes index-only invalidation from changes that can alter
// commit history or push status.
type WatchEvent struct {
	History bool
}

// NewWatcher creates a new git directory watcher.
func NewWatcher(workDir string) (*Watcher, error) {
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	w := &Watcher{
		fsWatcher: fsWatcher,
		events:    make(chan WatchEvent, 1),
		stop:      make(chan struct{}),
	}

	// Watch .git/index for staging changes
	gitDir := filepath.Join(workDir, ".git")
	indexPath := filepath.Join(gitDir, "index")
	headPath := filepath.Join(gitDir, "HEAD")
	refsDir := filepath.Join(gitDir, "refs")

	// Add watches
	if err := fsWatcher.Add(gitDir); err != nil {
		_ = fsWatcher.Close()
		return nil, err
	}
	// Try to watch index directly (may not exist yet)
	if err := fsWatcher.Add(indexPath); err != nil {
		slog.Debug("watcher: add index", "err", err)
	}
	if err := fsWatcher.Add(headPath); err != nil {
		slog.Debug("watcher: add HEAD", "err", err)
	}
	if err := fsWatcher.Add(refsDir); err != nil {
		slog.Debug("watcher: add refs", "err", err)
	}

	go w.run()

	return w, nil
}

// Events returns the channel that receives change notifications.
func (w *Watcher) Events() <-chan WatchEvent {
	return w.events
}

// deliver preserves the strongest pending invalidation. If an index event is
// already buffered, a later HEAD/ref event upgrades it instead of being lost.
func (w *Watcher) deliver(event WatchEvent) {
	select {
	case w.events <- event:
		return
	default:
	}

	select {
	case pending := <-w.events:
		event.History = event.History || pending.History
	default:
	}
	select {
	case w.events <- event:
	default:
	}
}

// Stop stops the watcher. The events channel is closed when run() exits.
func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()

	if w.stopped {
		return
	}
	w.stopped = true

	close(w.stop)
	_ = w.fsWatcher.Close()
	// Note: w.events is closed by run() goroutine on exit, not here
	// Closing here would race with run()'s debounce timer sending to the channel
}

// run processes file system events.
func (w *Watcher) run() {
	defer close(w.events) // Close channel when goroutine exits

	// Debounce related filesystem events while retaining whether any of them can
	// affect history.
	var debounceTimer *time.Timer
	var debounceC <-chan time.Time
	pendingHistory := false
	debounceDelay := 100 * time.Millisecond

	for {
		select {
		case <-w.stop:
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}

			// Only care about relevant files
			name := filepath.Base(event.Name)
			dir := filepath.Dir(event.Name)
			if name != "index" && name != "HEAD" && name != "COMMIT_EDITMSG" && name != "FETCH_HEAD" {
				// Check if it's a refs change
				if !strings.Contains(dir, "refs") {
					continue
				}
			}
			pendingHistory = pendingHistory || name != "index"

			if debounceTimer == nil {
				debounceTimer = time.NewTimer(debounceDelay)
				debounceC = debounceTimer.C
			} else {
				if !debounceTimer.Stop() {
					select {
					case <-debounceTimer.C:
					default:
					}
				}
				debounceTimer.Reset(debounceDelay)
			}

		case <-debounceC:
			event := WatchEvent{History: pendingHistory}
			pendingHistory = false
			w.deliver(event)

		case _, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			// Log error but continue
		}
	}
}
