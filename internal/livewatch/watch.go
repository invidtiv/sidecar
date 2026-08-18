package livewatch

import (
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/fsnotify/fsnotify"
)

const (
	// DefaultQuiet is how long the filesystem has to go quiet before a batch of
	// changes is reported. Agents and editors write in bursts — a tool that
	// rewrites a file three times in as many milliseconds should cost one
	// re-read, not three.
	DefaultQuiet = 150 * time.Millisecond

	// DefaultMaxLatency caps how long a change can sit unreported while writes
	// keep arriving, so a target that never goes quiet still refreshes. Without
	// it, a file being appended to continuously would never report at all.
	DefaultMaxLatency = time.Second

	// maxRegistrations caps how many directories one watcher registers.
	//
	// On macOS fsnotify uses kqueue, which needs a descriptor per file inside
	// every watched directory — a single 500-entry directory costs 501
	// descriptors. Every caller here watches a handful of specific paths, so
	// this is a guard against a pathological target set, not a working limit.
	maxRegistrations = 64
)

// Target is one thing a [PathWatcher] should report changes to.
//
// A file target reports only that exact path. A directory target reports any
// entry directly inside it. Callers name what they care about; nothing here
// walks a tree or expands a target into its children.
type Target struct {
	Path string
	// Dir marks Path as a directory whose entries are all of interest, rather
	// than a single file.
	Dir bool
}

// File returns a Target matching exactly one path.
func File(path string) Target { return Target{Path: path} }

// Dir returns a Target matching any entry directly inside a directory.
func Dir(path string) Target { return Target{Path: path, Dir: true} }

// Config tunes a [PathWatcher]. The zero value uses the defaults.
type Config struct {
	// Quiet is the settle time before a batch reports. Zero means DefaultQuiet.
	Quiet time.Duration
	// MaxLatency caps time-to-report under continuous writes. Zero means
	// DefaultMaxLatency.
	MaxLatency time.Duration
	// Ignore, when set, drops paths before they are matched against targets.
	// Callers use it to filter the scratch files editors leave beside the file
	// the user is actually looking at.
	Ignore func(path string) bool
}

func (c Config) quiet() time.Duration {
	if c.Quiet <= 0 {
		return DefaultQuiet
	}
	return c.Quiet
}

func (c Config) maxLatency() time.Duration {
	if c.MaxLatency <= 0 {
		return DefaultMaxLatency
	}
	return c.MaxLatency
}

// PathWatcher reports that something in a named set of paths changed.
//
// It says only that: there is no event payload, because every consumer's next
// move is the same regardless of which path moved — re-read and compare. That
// keeps coalescing trivially correct, since any number of pending signals
// collapse into the one buffered slot.
//
// A PathWatcher is safe to use from multiple goroutines. It is created lazily,
// when a pane opens, and must be stopped when the pane closes.
type PathWatcher struct {
	cfg     Config
	fsw     *fsnotify.Watcher
	signals chan struct{}
	stop    chan struct{}
	done    chan struct{}

	stopOnce sync.Once

	mu      sync.Mutex
	closed  bool
	targets []Target
	// watched is the set of directories this watcher believes are registered
	// with fsnotify, which is not the target set: a file target registers its
	// parent. It is a belief rather than the truth, which is why
	// reconcileLocked checks it against the kernel rather than trusting it.
	watched map[string]bool
	// lost names directories that are still wanted but whose registration went
	// away underneath us. Re-registering one is an admission that this watcher
	// was blind for a moment, so the re-add reports a change whether or not one
	// was observed.
	lost map[string]bool
}

// NewPathWatcher starts a watcher with no targets. Nothing is observed, and no
// descriptors beyond fsnotify's own are held, until [PathWatcher.Watch] is
// called.
//
// This must not run on the startup path. Call it from inside a tea.Cmd when a
// pane opens.
func NewPathWatcher(cfg Config) (*PathWatcher, error) {
	fsw, err := fsnotify.NewWatcher()
	if err != nil {
		return nil, err
	}
	w := &PathWatcher{
		cfg:     cfg,
		fsw:     fsw,
		signals: make(chan struct{}, 1),
		stop:    make(chan struct{}),
		done:    make(chan struct{}),
		watched: make(map[string]bool),
	}
	go w.run()
	return w, nil
}

// Watch replaces the target set, adding and removing only the fsnotify
// registrations that actually changed.
//
// Passing no targets releases every descriptor while leaving the watcher usable
// — that is how a pane that navigated away stops observing without tearing down
// and rebuilding. Watch never returns an error for a target that does not
// exist: a file an agent has not written yet is a normal state, and the
// registration on its parent directory will report the create.
func (w *PathWatcher) Watch(targets ...Target) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}

	normalized := make([]Target, 0, len(targets))
	for _, t := range targets {
		if t.Path == "" {
			continue
		}
		abs, err := filepath.Abs(t.Path)
		if err != nil {
			continue
		}
		normalized = append(normalized, Target{Path: filepath.Clean(abs), Dir: t.Dir})
	}
	w.targets = normalized
	w.reconcileLocked()
}

// reconcileLocked brings the fsnotify registrations in line with the targets.
//
// A directory target registers itself; a file target registers its parent,
// because a watch on a file alone misses the create-and-rename dance most
// editors and every atomic writer use.
//
// The registration a watcher holds is not the registration it asked for. A
// watched directory that is removed and recreated — a checkout across a branch
// without it, a worktree rebuild, a tool that replaces a folder rather than its
// contents — is dropped by the backend, and a `watched` map consulted as if it
// were the truth would then skip re-adding it forever, leaving every pane
// underneath permanently stale with nothing to show for it. So the live set is
// read back from fsnotify on each pass and the belief is corrected from it.
func (w *PathWatcher) reconcileLocked() {
	desired := make(map[string]bool, len(w.targets))
	for _, t := range w.targets {
		if len(desired) >= maxRegistrations {
			break
		}
		if t.Dir {
			desired[t.Path] = true
			continue
		}
		desired[filepath.Dir(t.Path)] = true
	}

	// What the backend actually holds, which is the only thing worth diffing
	// against. WatchList returns the paths as they were added, and Watch has
	// already cleaned and absolutized every target, so the two agree.
	live := make(map[string]bool)
	for _, dir := range w.fsw.WatchList() {
		live[dir] = true
	}
	for dir := range w.watched {
		if !live[dir] {
			// The registration died under us. Forget it so the add below runs,
			// and remember that it was lost: whatever was written between the
			// death and the re-add produced no event anyone saw.
			delete(w.watched, dir)
			if desired[dir] {
				if w.lost == nil {
					w.lost = make(map[string]bool)
				}
				w.lost[dir] = true
			}
		}
	}

	for dir := range live {
		if !desired[dir] {
			_ = w.fsw.Remove(dir)
			delete(w.watched, dir)
			delete(w.lost, dir)
		}
	}
	recovered := false
	for dir := range desired {
		if w.watched[dir] {
			continue
		}
		if err := w.fsw.Add(dir); err != nil {
			// The directory is gone or unreadable. Not an error worth surfacing:
			// the pane simply will not live-update until it comes back, and the
			// next Watch call retries.
			continue
		}
		w.watched[dir] = true
		if w.lost[dir] {
			delete(w.lost, dir)
			recovered = true
		}
	}
	if recovered {
		// Report unconditionally. A directory that came back almost always came
		// back with different contents, and the consumer's answer to a signal is
		// to re-read and compare — so a signal that turns out to be nothing costs
		// one read and no repaint, while a missed one leaves the pane wrong with
		// no second chance.
		w.emit()
	}
}

// isWatchedDir reports whether path is one of the directories this watcher is
// registered on.
func (w *PathWatcher) isWatchedDir(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.watched[filepath.Clean(abs)]
}

// rereconcile re-checks the registrations against the kernel. It is what the
// event loop calls when a remove or rename has taken a watched directory with
// it.
//
// A directory removed and immediately recreated is usually still gone at this
// point, so the re-add here fails and the recovery is recorded rather than
// completed: the host's next reconcile finishes it, and the re-add reports the
// change nobody was watching for.
func (w *PathWatcher) rereconcile() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.closed {
		return
	}
	w.reconcileLocked()
}

// matches reports whether a raw event path is one of the targets.
func (w *PathWatcher) matches(path string) bool {
	if w.cfg.Ignore != nil && w.cfg.Ignore(path) {
		return false
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	abs = filepath.Clean(abs)

	w.mu.Lock()
	defer w.mu.Unlock()
	for _, t := range w.targets {
		if t.Dir {
			if abs == t.Path || strings.HasPrefix(abs, t.Path+string(filepath.Separator)) {
				return true
			}
			continue
		}
		if abs == t.Path {
			return true
		}
	}
	return false
}

// Signals returns the channel on which coalesced change notifications arrive.
// It is closed once the watcher stops, so a consumer blocked on a receive is
// always released.
func (w *PathWatcher) Signals() <-chan struct{} { return w.signals }

// Stop releases every descriptor and blocks until the signal channel is closed,
// so a caller that has stopped a watcher can never see another signal from it.
//
// It is idempotent. Every pane that creates a watcher must call this when it
// closes, navigates away, or loses a race to adopt the watcher it started.
func (w *PathWatcher) Stop() {
	w.stopOnce.Do(func() {
		w.mu.Lock()
		w.closed = true
		// fsnotify's kqueue backend marks itself closed before unregistering, so
		// Close alone leaks a descriptor per watched file on macOS. Remove the
		// directories first.
		for dir := range w.watched {
			_ = w.fsw.Remove(dir)
		}
		w.watched = make(map[string]bool)
		w.targets = nil
		w.mu.Unlock()

		close(w.stop)
		_ = w.fsw.Close()
	})
	<-w.done
}

// WatchedDirs returns the directories currently registered with fsnotify. It
// exists so tests can assert that closing a pane gives its descriptors back.
func (w *PathWatcher) WatchedDirs() []string {
	w.mu.Lock()
	defer w.mu.Unlock()
	dirs := make([]string, 0, len(w.watched))
	for dir := range w.watched {
		dirs = append(dirs, dir)
	}
	return dirs
}

// run coalesces raw filesystem events into at most one pending signal.
func (w *PathWatcher) run() {
	defer func() {
		close(w.signals)
		close(w.done)
	}()

	quiet := stoppedTimer()
	maxLat := stoppedTimer()
	var quietC, maxLatC <-chan time.Time
	pending := false

	flush := func() {
		drainTimer(quiet)
		drainTimer(maxLat)
		quietC, maxLatC = nil, nil
		if !pending {
			return
		}
		pending = false
		w.emit()
	}

	for {
		select {
		case <-w.stop:
			return

		case ev, ok := <-w.fsw.Events:
			if !ok {
				return
			}
			// Chmod is permission and timestamp churn; it never changes what a
			// pane renders.
			if ev.Op == fsnotify.Chmod {
				continue
			}
			// A removed or renamed directory this watcher is registered on has
			// just had its registration dropped by the backend. Notice it here
			// rather than only on the host's next Watch call.
			//
			// Narrowed to the registered directory itself on purpose: a checkout
			// or rebase deletes hundreds of files inside a watched directory, and
			// reconciling on each of them would put a WatchList and two map
			// builds — and, where a re-add succeeds, a directory read — inline on
			// the goroutine that has to keep draining fsnotify's events.
			if ev.Op&(fsnotify.Remove|fsnotify.Rename) != 0 && w.isWatchedDir(ev.Name) {
				w.rereconcile()
			}
			if !w.matches(ev.Name) {
				continue
			}
			drainTimer(quiet)
			quiet.Reset(w.cfg.quiet())
			quietC = quiet.C
			// Restart the quiet period on every change, but arm the latency cap
			// only once per batch, so a target under continuous write still
			// reports on schedule.
			if !pending {
				drainTimer(maxLat)
				maxLat.Reset(w.cfg.maxLatency())
				maxLatC = maxLat.C
			}
			pending = true

		case <-quietC:
			flush()

		case <-maxLatC:
			flush()

		case _, ok := <-w.fsw.Errors:
			if !ok {
				return
			}
			// Keep watching. A dropped event costs one stale frame; tearing the
			// watcher down costs every frame after it.
		}
	}
}

// emit delivers a signal, or leaves the already-pending one in place. The
// channel holds one slot because a second signal would tell the consumer
// nothing the first did not.
func (w *PathWatcher) emit() {
	select {
	case w.signals <- struct{}{}:
	default:
	}
}

func stoppedTimer() *time.Timer {
	t := time.NewTimer(time.Hour)
	drainTimer(t)
	return t
}

func drainTimer(t *time.Timer) {
	if !t.Stop() {
		select {
		case <-t.C:
		default:
		}
	}
}
