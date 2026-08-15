package grok

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

// newWatcher watches the Grok sessions root for project-scoped changes.
func newWatcher(a *Adapter, projectRoot string) (<-chan adapter.Event, io.Closer, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, err
	}

	if err := watcher.Add(a.sessionsDir); err != nil {
		_ = watcher.Close()
		return nil, nil, err
	}

	projectDir := a.projectDirPath(projectRoot)
	if projectDir != "" {
		if info, err := os.Stat(projectDir); err == nil && info.IsDir() {
			_ = watcher.Add(projectDir)
			if entries, err := os.ReadDir(projectDir); err == nil {
				for _, e := range entries {
					if e.IsDir() {
						_ = watcher.Add(filepath.Join(projectDir, e.Name()))
					}
				}
			}
		}
	}

	// Canonical prefix for path-segment isolation (not substring Contains).
	// Sibling dirs like .../sidecar and .../sidecar-grok-conversations must not match.
	projectPrefix := ""
	if projectDir != "" {
		projectPrefix = filepath.Clean(projectDir) + string(filepath.Separator)
	}

	events := make(chan adapter.Event, 32)
	closer := &watchCloser{watcher: watcher}

	go func() {
		var debounceTimer *time.Timer
		var lastEvent fsnotify.Event
		debounceDelay := 150 * time.Millisecond

		var closed bool
		var mu sync.Mutex

		defer func() {
			mu.Lock()
			closed = true
			if debounceTimer != nil {
				debounceTimer.Stop()
			}
			mu.Unlock()
			close(events)
		}()

		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}

				// Dynamically watch new project / session directories under our project only.
				if event.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						if pathInProject(event.Name, projectDir, projectPrefix) {
							_ = watcher.Add(event.Name)
							if entries, err := os.ReadDir(event.Name); err == nil {
								for _, e := range entries {
									if e.IsDir() {
										_ = watcher.Add(filepath.Join(event.Name, e.Name()))
									}
								}
							}
						}
					}
				}

				base := filepath.Base(event.Name)
				interestingFile := base == "summary.json" || base == "chat_history.jsonl"
				interestingCreate := event.Op&fsnotify.Create != 0
				if !interestingFile && !interestingCreate {
					continue
				}
				if !pathInProject(event.Name, projectDir, projectPrefix) {
					continue
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

					if !pathInProject(lastEvent.Name, projectDir, projectPrefix) {
						return
					}

					sessionID, eventType := classifyGrokWatchEvent(lastEvent, projectDir)
					if sessionID == "" {
						// Project-level discovery (new dir under project) without a known session file yet.
						if lastEvent.Op&fsnotify.Create != 0 {
							select {
							case events <- adapter.Event{
								Type:      adapter.EventSessionCreated,
								AdapterID: adapterID,
							}:
							default:
							}
						}
						return
					}

					select {
					case events <- adapter.Event{
						Type:      eventType,
						AdapterID: adapterID,
						SessionID: sessionID,
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

// pathInProject reports whether path is the projectDir itself or a descendant.
// Uses Clean + separator prefix so "…/sidecar" does not match "…/sidecar-foo".
func pathInProject(path, projectDir, projectPrefix string) bool {
	if projectDir == "" || projectPrefix == "" {
		return false
	}
	clean := filepath.Clean(path)
	if clean == filepath.Clean(projectDir) {
		return true
	}
	return strings.HasPrefix(clean, projectPrefix)
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

// classifyGrokWatchEvent extracts session ID and event type from an fsnotify event.
func classifyGrokWatchEvent(event fsnotify.Event, projectDir string) (sessionID string, eventType adapter.EventType) {
	base := filepath.Base(event.Name)
	dir := filepath.Dir(event.Name)

	switch base {
	case "summary.json", "chat_history.jsonl":
		sessionID = filepath.Base(dir)
	default:
		// Session directory created directly under the project dir.
		if event.Op&fsnotify.Create != 0 {
			if filepath.Clean(filepath.Dir(event.Name)) == filepath.Clean(projectDir) {
				sessionID = filepath.Base(event.Name)
				return sessionID, adapter.EventSessionCreated
			}
		}
		return "", ""
	}

	switch {
	case event.Op&fsnotify.Create != 0:
		if base == "summary.json" {
			eventType = adapter.EventSessionCreated
		} else {
			eventType = adapter.EventMessageAdded
		}
	case event.Op&fsnotify.Write != 0:
		if base == "summary.json" {
			eventType = adapter.EventSessionUpdated
		} else {
			eventType = adapter.EventMessageAdded
		}
	case event.Op&fsnotify.Remove != 0:
		return sessionID, adapter.EventSessionUpdated
	default:
		eventType = adapter.EventSessionUpdated
	}
	return sessionID, eventType
}
