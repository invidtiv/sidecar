package codex

import (
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/fsnotify/fsnotify"
	"github.com/marcus/sidecar/internal/adapter"
)

type sessionIDResolver func(string) (string, error)

// NewWatcher watches Codex's dated directory tree and resolves rollout paths
// to stable thread IDs. fsnotify is not recursive, so every existing directory
// under the current and previous month is registered explicitly and newly
// created directories are added as they appear.
func NewWatcher(root string, resolve sessionIDResolver) (<-chan adapter.Event, io.Closer, error) {
	watcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, nil, err
	}

	watchRoot := root
	if _, err := os.Stat(watchRoot); os.IsNotExist(err) {
		watchRoot = filepath.Dir(root)
	}
	if err := watcher.Add(watchRoot); err != nil {
		_ = watcher.Close()
		return nil, nil, err
	}
	if watchRoot == root {
		for _, year := range recentSessionYears(root) {
			if info, err := os.Stat(year); err == nil && info.IsDir() {
				_ = watcher.Add(year)
			}
		}
		for _, month := range recentSessionDirs(root) {
			addDirectoryTree(watcher, month)
		}
	}

	events := make(chan adapter.Event, 32)
	go func() {
		defer close(events)
		pending := make(map[string]fsnotify.Op)
		ticker := time.NewTicker(200 * time.Millisecond)
		defer ticker.Stop()
		emit := func(path string, op fsnotify.Op) {
			id, err := resolve(path)
			if err != nil || id == "" {
				return
			}
			typ := adapter.EventSessionUpdated
			switch {
			case op&fsnotify.Create != 0:
				typ = adapter.EventSessionCreated
			case op&fsnotify.Write != 0:
				typ = adapter.EventMessageAdded
			case op&fsnotify.Remove != 0:
				return
			}
			select {
			case events <- adapter.Event{Type: typ, SessionID: id}:
			default:
			}
		}
		for {
			select {
			case event, ok := <-watcher.Events:
				if !ok {
					return
				}
				if event.Op&fsnotify.Create != 0 {
					if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
						addDirectoryTree(watcher, event.Name)
						scanNewDirForSessions(event.Name, func(path string) { emit(path, fsnotify.Create) })
						continue
					}
				}
				if strings.HasSuffix(event.Name, ".jsonl") {
					pending[event.Name] |= event.Op
				}
			case <-ticker.C:
				for path, op := range pending {
					emit(path, op)
					delete(pending, path)
				}
			case _, ok := <-watcher.Errors:
				if !ok {
					return
				}
			}
		}
	}()
	return events, watcher, nil
}

func recentSessionYears(root string) []string {
	now := time.Now()
	prev := now.AddDate(0, -1, 0)
	years := []string{filepath.Join(root, now.Format("2006"))}
	if prev.Year() != now.Year() {
		years = append(years, filepath.Join(root, prev.Format("2006")))
	}
	return years
}

func addDirectoryTree(watcher *fsnotify.Watcher, root string) {
	_ = filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			_ = watcher.Add(path)
		}
		return nil
	})
}

func recentSessionDirs(root string) []string {
	now := time.Now()
	prev := now.AddDate(0, -1, 0)
	return []string{
		filepath.Join(root, now.Format("2006"), now.Format("01")),
		filepath.Join(root, prev.Format("2006"), prev.Format("01")),
	}
}

func scanNewDirForSessions(dir string, emit func(string)) {
	_ = filepath.WalkDir(dir, func(path string, d os.DirEntry, err error) error {
		if err == nil && !d.IsDir() && strings.HasSuffix(path, ".jsonl") {
			emit(path)
		}
		return nil
	})
}
