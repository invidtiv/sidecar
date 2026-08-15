package uirequest

import (
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/fsnotify/fsnotify"
	"github.com/marcus/sidecar/internal/config"
)

// RequestMsg is emitted to BubbleTea when a UI request is detected in the state tree.
type RequestMsg struct {
	Request Request
}

// Watcher monitors the $XDG_STATE_HOME/sidecar/requests directory for incoming requests.
type Watcher struct {
	fsWatcher *fsnotify.Watcher
	stateDir  string
	dir       string
	msgChan   chan tea.Msg
	stopChan  chan struct{}
	doneChan  chan struct{}
	mu        sync.Mutex
	started   bool
	stopped   bool
	seen      map[string]time.Time
}

const watcherDebounce = 50 * time.Millisecond

// NewWatcher creates a watcher for UI requests under stateDir.
func NewWatcher(stateDir string) (*Watcher, error) {
	if err := config.AssertIsolatedPath(stateDir); err != nil {
		return nil, err
	}
	dir := filepath.Join(stateDir, "requests")
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}

	if err := fsWatcher.Add(dir); err != nil {
		slog.Debug("uirequest watcher: add dir", "err", err)
	}

	w := &Watcher{
		fsWatcher: fsWatcher,
		stateDir:  stateDir,
		dir:       dir,
		msgChan:   make(chan tea.Msg, 16),
		stopChan:  make(chan struct{}),
		doneChan:  make(chan struct{}),
		seen:      make(map[string]time.Time),
	}

	// Sweep on init
	_ = Sweep(stateDir, time.Now().UTC())

	return w, nil
}

// Start begins watching and returns the channel emitting RequestMsg.
func (w *Watcher) Start() <-chan tea.Msg {
	w.mu.Lock()
	if w.started || w.stopped {
		w.mu.Unlock()
		return w.msgChan
	}
	w.started = true
	w.mu.Unlock()
	go w.run()
	return w.msgChan
}

// Stop terminates the watcher and cleans up resources.
func (w *Watcher) Stop() {
	w.mu.Lock()
	if w.stopped {
		w.mu.Unlock()
		return
	}
	w.stopped = true
	close(w.stopChan)
	_ = w.fsWatcher.Close()
	started := w.started
	if !started {
		close(w.doneChan)
		close(w.msgChan)
	}
	w.mu.Unlock()

	if !started {
		return
	}
	<-w.doneChan
}

func (w *Watcher) run() {
	defer close(w.doneChan)
	defer close(w.msgChan)

	var debounceTimer *time.Timer

	// Scan initial directory contents
	w.scanAndEmit()

	sweepTicker := time.NewTicker(30 * time.Second)
	defer sweepTicker.Stop()

	for {
		select {
		case <-w.stopChan:
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			return

		case <-sweepTicker.C:
			_ = Sweep(w.stateDir, time.Now().UTC())

		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			if event.Op&(fsnotify.Write|fsnotify.Create|fsnotify.Rename) == 0 {
				continue
			}
			if !strings.HasSuffix(event.Name, ".json") || strings.Contains(event.Name, ".tmp.") {
				continue
			}

			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			debounceTimer = time.AfterFunc(watcherDebounce, func() {
				select {
				case <-w.stopChan:
					return
				default:
					w.scanAndEmit()
				}
			})

		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			slog.Debug("uirequest watcher: error", "err", err)
		}
	}
}

func (w *Watcher) scanAndEmit() {
	entries, err := os.ReadDir(w.dir)
	if err != nil {
		return
	}
	now := time.Now().UTC()
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") || strings.Contains(entry.Name(), ".tmp.") {
			continue
		}
		path := filepath.Join(w.dir, entry.Name())
		req, err := ReadRequest(path)
		if err != nil {
			continue
		}

		ttl := time.Duration(req.TTLMs) * time.Millisecond
		if ttl <= 0 {
			ttl = DefaultTTL
		}
		if now.Sub(req.CreatedAt) > ttl {
			continue
		}

		w.mu.Lock()
		if _, exists := w.seen[req.ID]; exists {
			w.mu.Unlock()
			continue
		}
		w.seen[req.ID] = now
		w.mu.Unlock()

		select {
		case w.msgChan <- RequestMsg{Request: req}:
		case <-w.stopChan:
			return
		default:
			// Buffer full, skip
		}
	}
}
