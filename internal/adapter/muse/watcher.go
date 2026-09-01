package muse

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/marcus/sidecar/internal/adapter"
)

func newWatcher(a *Adapter, projectRoot string) (<-chan adapter.Event, io.Closer, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, err
	}
	// Watch sessions root and index DB parent.
	if err := watcher.Add(a.sessionsDir); err != nil {
		_ = watcher.Close()
		return nil, nil, err
	}
	if dir := filepath.Dir(a.indexDBPath); dir != a.sessionsDir {
		_ = watcher.Add(dir) // best effort
	}
	// Also watch existing date directories up to 2 levels (YYYY/MM/DD)
	if entries, err := os.ReadDir(a.sessionsDir); err == nil {
		for _, e := range entries {
			if !e.IsDir() {
				continue
			}
			yearDir := filepath.Join(a.sessionsDir, e.Name())
			_ = watcher.Add(yearDir)
			if months, err := os.ReadDir(yearDir); err == nil {
				for _, m := range months {
					if !m.IsDir() {
						continue
					}
					monthDir := filepath.Join(yearDir, m.Name())
					_ = watcher.Add(monthDir)
					if days, err := os.ReadDir(monthDir); err == nil {
						for _, d := range days {
							if d.IsDir() {
								_ = watcher.Add(filepath.Join(monthDir, d.Name()))
							}
						}
					}
				}
			}
		}
	}

	events := make(chan adapter.Event, 32)
	closer := &watchCloser{watcher: watcher}
	go func() {
		defer close(events)
		var debounceTimer *time.Timer
		var lastEvent fsnotify.Event
		debounceDelay := 200 * time.Millisecond
		var closed bool
		var mu sync.Mutex
		defer func() {
			mu.Lock()
			closed = true
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			mu.Unlock()
		}()
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				base := filepath.Base(event.Name)
				interesting := base == "session.jsonl" || base == "session-index.db" || event.Op&fsnotify.Create != 0
				if !interesting {
					continue
				}
				// Dynamically watch new YYYY/MM/DD directories
				if event.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						// Heuristic: add any new directory under sessions root
						if strings.HasPrefix(event.Name, a.sessionsDir) {
							_ = watcher.Add(event.Name)
						}
					}
				}
				mu.Lock()
				lastEvent = event
				if debounceTimer != nil {
					debounceTimer.Stop()
				}
				debounceTimer = time.AfterFunc(debounceDelay, func() {
					mu.Lock()
					defer mu.Unlock()
					if closed {
						return
					}
					ev := lastEvent
					base := filepath.Base(ev.Name)
					var sessID string
					var eventType adapter.EventType
					if base == "session.jsonl" {
						sessID = filepath.Base(filepath.Dir(ev.Name))
						if ev.Op&fsnotify.Create != 0 {
							eventType = adapter.EventSessionCreated
						} else if ev.Op&fsnotify.Write != 0 {
							eventType = adapter.EventMessageAdded
						} else {
							eventType = adapter.EventSessionUpdated
						}
					} else if base == "session-index.db" {
						eventType = adapter.EventSessionUpdated
					} else if base != "" && ev.Op&fsnotify.Create != 0 {
						// New session directory
						sessID = base
						eventType = adapter.EventSessionCreated
					}
					select {
					case events <- adapter.Event{
						Type:      eventType,
						AdapterID: adapterID,
						SessionID: sessID,
					}:
					default:
					}
				})
				mu.Unlock()
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return events, closer, nil
}

type watchCloser struct {
	watcher *fsnotify.Watcher
	once    sync.Once
}

func (c *watchCloser) Close() error {
	var err error
	c.once.Do(func() {
		err = c.watcher.Close()
	})
	return err
}
