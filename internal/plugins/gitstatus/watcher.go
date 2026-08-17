package gitstatus

import (
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

// Watcher monitors Git's administrative files. These are not necessarily below
// <worktree>/.git: linked worktrees keep HEAD and index in a per-worktree admin
// directory while refs live in the common repository directory.
type Watcher struct {
	fsWatcher    *fsnotify.Watcher
	events       chan WatchEvent
	stop         chan struct{}
	mu           sync.Mutex
	stopped      bool
	indexPath    string
	historyPaths map[string]struct{}
	refsDirs     []string
}

type WatchEvent struct{ History bool }

func resolveGitPath(workDir, name string) (string, error) {
	cmd := gitReadOnly("rev-parse", "--path-format=absolute", "--git-path", name)
	cmd.Dir = workDir
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("resolve git path %s: %w", name, err)
	}
	path := strings.TrimSpace(string(out))
	if path == "" {
		return "", fmt.Errorf("resolve git path %s: empty result", name)
	}
	if !filepath.IsAbs(path) {
		path = filepath.Join(workDir, path)
	}
	return filepath.Clean(path), nil
}

func addWatchDir(fsWatcher *fsnotify.Watcher, seen map[string]struct{}, dir string) error {
	dir = filepath.Clean(dir)
	if _, ok := seen[dir]; ok {
		return nil
	}
	if err := fsWatcher.Add(dir); err != nil {
		return err
	}
	seen[dir] = struct{}{}
	return nil
}

func NewWatcher(workDir string) (*Watcher, error) {
	paths := make(map[string]string)
	for _, name := range []string{"index", "HEAD", "COMMIT_EDITMSG", "FETCH_HEAD", "packed-refs", "refs"} {
		path, err := resolveGitPath(workDir, name)
		if err != nil {
			return nil, err
		}
		paths[name] = path
	}
	fsWatcher, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &Watcher{fsWatcher: fsWatcher, events: make(chan WatchEvent, 1), stop: make(chan struct{}), indexPath: paths["index"], historyPaths: make(map[string]struct{})}
	for _, name := range []string{"HEAD", "COMMIT_EDITMSG", "FETCH_HEAD", "packed-refs"} {
		w.historyPaths[paths[name]] = struct{}{}
	}

	seen := make(map[string]struct{})
	for _, name := range []string{"index", "HEAD", "COMMIT_EDITMSG", "FETCH_HEAD", "packed-refs"} {
		if err := addWatchDir(fsWatcher, seen, filepath.Dir(paths[name])); err != nil {
			_ = fsWatcher.Close()
			return nil, fmt.Errorf("watch git path %s: %w", paths[name], err)
		}
	}
	refsRoot := paths["refs"]
	err = filepath.WalkDir(refsRoot, func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil {
			return walkErr
		}
		if !entry.IsDir() {
			return nil
		}
		if err := addWatchDir(fsWatcher, seen, path); err != nil {
			return err
		}
		w.refsDirs = append(w.refsDirs, filepath.Clean(path))
		return nil
	})
	if err != nil && !os.IsNotExist(err) {
		_ = fsWatcher.Close()
		return nil, fmt.Errorf("watch git refs %s: %w", refsRoot, err)
	}
	go w.run()
	return w, nil
}

func (w *Watcher) Events() <-chan WatchEvent { return w.events }

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

func (w *Watcher) Stop() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.stopped {
		return
	}
	w.stopped = true
	close(w.stop)
	_ = w.fsWatcher.Close()
}

// isAttributeOnly reports whether an event carries nothing but an attribute
// change. Dropping those is what keeps this watcher from driving itself:
// reading .git/index — exactly what the `git status` this watcher triggers
// does — touches the index's attributes, and the kqueue backend reports that as
// CHMOD on the index. Acting on it meant every refresh scheduled the next one,
// so an idle Sidecar forked three `git` processes about six times a second
// forever, all day.
//
// Nothing observable is lost. Git writes the index by creating index.lock and
// renaming it into place, which arrives as REMOVE+CREATE on .git/index (and as
// WRITE on backends that report in-place writes) — none of which is
// attribute-only.
func isAttributeOnly(op fsnotify.Op) bool {
	return op == fsnotify.Chmod
}

func (w *Watcher) classify(path string) (WatchEvent, bool) {
	path = filepath.Clean(path)
	if path == w.indexPath {
		return WatchEvent{}, true
	}
	if _, ok := w.historyPaths[path]; ok {
		return WatchEvent{History: true}, true
	}
	for _, dir := range w.refsDirs {
		if path != dir && strings.HasPrefix(path, dir+string(filepath.Separator)) {
			return WatchEvent{History: true}, true
		}
	}
	return WatchEvent{}, false
}

func (w *Watcher) run() {
	defer close(w.events)
	var timer *time.Timer
	var timerC <-chan time.Time
	pendingHistory := false
	for {
		select {
		case <-w.stop:
			if timer != nil {
				timer.Stop()
			}
			return
		case event, ok := <-w.fsWatcher.Events:
			if !ok {
				return
			}
			if isAttributeOnly(event.Op) {
				continue
			}
			classified, relevant := w.classify(event.Name)
			if !relevant {
				continue
			}
			if event.Op&fsnotify.Create != 0 {
				if info, err := os.Stat(event.Name); err == nil && info.IsDir() {
					if err := w.fsWatcher.Add(event.Name); err != nil {
						slog.Warn("git watcher could not add new refs directory", "path", event.Name, "err", err)
					} else {
						w.refsDirs = append(w.refsDirs, filepath.Clean(event.Name))
					}
				}
			}
			pendingHistory = pendingHistory || classified.History
			if timer == nil {
				timer = time.NewTimer(100 * time.Millisecond)
				timerC = timer.C
			} else {
				if !timer.Stop() {
					select {
					case <-timer.C:
					default:
					}
				}
				timer.Reset(100 * time.Millisecond)
			}
		case <-timerC:
			w.deliver(WatchEvent{History: pendingHistory})
			pendingHistory = false
		case err, ok := <-w.fsWatcher.Errors:
			if !ok {
				return
			}
			slog.Warn("git watcher error", "err", err)
		}
	}
}
