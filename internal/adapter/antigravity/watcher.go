package antigravity

import (
	"io"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/marcus/sidecar/internal/adapter"
)

// Watch implements the adapter.Adapter interface for Antigravity.
func (a *Adapter) Watch(projectRoot string) (<-chan adapter.Event, io.Closer, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, err
	}

	if err := watcher.Add(a.brainDir); err != nil {
		_ = watcher.Close()
		return nil, nil, err
	}

	events := make(chan adapter.Event, 32)

	go func() {
		var debounceTimer *time.Timer
		var lastEvent fsnotify.Event
		debounceDelay := 100 * time.Millisecond

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

				if !strings.HasSuffix(event.Name, ".jsonl") {
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

					// Path: brain/<sessionID>/.system_generated/logs/transcript.jsonl
					// Extract sessionID from grandparent of .system_generated
					sessionID := filepath.Base(filepath.Dir(filepath.Dir(filepath.Dir(lastEvent.Name))))

					var eventType adapter.EventType
					switch {
					case lastEvent.Op&fsnotify.Create != 0:
						eventType = adapter.EventSessionCreated
					case lastEvent.Op&fsnotify.Write != 0:
						eventType = adapter.EventMessageAdded
					default:
						eventType = adapter.EventSessionUpdated
					}

					select {
					case events <- adapter.Event{
						Type:      eventType,
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

	return events, watcher, nil
}
