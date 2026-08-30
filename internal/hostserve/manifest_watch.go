package hostserve

import (
	"fmt"
	"path/filepath"
	"time"

	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/projectdir"
)

// Cross-instance freshness for a remote host, which is the same problem the
// global browser already solved and for the same reason.
//
// Serve re-reads durable state only every Options.InventoryEvery — 60s by
// default — because that phase costs a `git worktree list` and a state-tree
// read per project. Everything in between refreshes tmux evidence only. That
// split is right, and it has one consequence that is not: a shell created on
// the host by `sidecar create shell` writes its shells.json record instantly
// and is then invisible to the viewer for up to a minute. The record and the
// row that proves it are one causal chain; a clock is a poor way to close it.
//
// internal/overview/live_shells.go states the reasoning in full for the local
// case. The remote case is the same one with the minute added, and the fix is
// the same mechanism: fsnotify on each configured project's shells.json,
// through livewatch.PathWatcher, which already owns the quiet period, the
// latency cap, and returning its descriptors.
//
// Two deliberate differences from the local binding:
//
//   - A signal only sets fullInventory for the next cycle. It never starts a
//     collection of its own. Burst coalescing and single-flight discipline
//     already live in the serve loop, and a second entry point into collection
//     is how they stop being true.
//   - No per-project digest. The browser fingerprints each manifest so a write
//     in one project costs one project's work; serve re-collects every project
//     on a full inventory anyway, and adding a second freshness mechanism to
//     save a `git worktree list` on a host with a handful of projects is not a
//     trade worth its complexity. What bounds the cost is that shells.json is
//     written by user gestures — create, rename, delete, forget — not by an
//     agent's write burst, and the quiet period collapses the atomic
//     write-and-rename pair into one signal.
//
// Worktree directories are deliberately NOT watched, for the reason
// live_shells.go gives under "What is deliberately not watched": fsnotify's
// kqueue backend costs a descriptor per file in every watched directory, so a
// worktrees/ watch grows the descriptor set with worktree count on every
// project at once. The existing inventory cadence is the worktree answer here
// as it is there.
//
// # Failure is not fatal
//
// A host can be out of inotify watches, or a configured project can name a
// directory that has never been opened on that machine and so has no state
// directory to watch. Neither may stop serve: the whole point of a host
// protocol is that a viewer can tell "this machine is fine and this part of it
// is degraded" from "this machine is unreachable". So the watch degrades to the
// pre-existing clock behaviour and says so on the stream, non-fatally.
//
// Everything below runs on the serve loop's own goroutine — start, reconcile,
// and stop are all called from Serve — so there is no lock here. The only value
// crossing goroutines is the watcher's signal channel, which livewatch owns.

const (
	// manifestQuiet and manifestMaxLatency are the browser's numbers, for the
	// browser's reason: manifest writes are atomic (temp file plus rename) and
	// arrive one per user gesture, so this only has to absorb the create/rename
	// pair rather than an agent's write burst.
	manifestQuiet      = 200 * time.Millisecond
	manifestMaxLatency = time.Second
)

// newManifestWatcher is indirected so a test can drive the degraded path
// without exhausting the machine's real watch budget.
var newManifestWatcher = livewatch.NewPathWatcher

// manifestWatch is serve's change signal over the state it reports. The zero
// value is inert and safe: signals() returns a nil channel, which a select
// simply never takes.
type manifestWatch struct {
	watcher  *livewatch.PathWatcher
	projects []Project

	// paths is project root -> shells.json. It can be incomplete, which is a
	// normal state rather than an error: a project configured on the host but
	// never opened there has no state directory yet.
	paths map[string]string

	// degraded is what to tell the viewer about a watch that could not be
	// established in full, or empty when there is nothing to say.
	degraded string
}

// startManifestWatch resolves each project's manifest and registers a watch
// over them. It never returns nil and never returns an error: every failure
// mode degrades to the clock, and Degraded says which one happened.
//
// This runs after the hello has been written, not before. A hello that waited
// on a directory read would make every viewer's first impression of a host
// slower for a freshness gain none of them can observe yet.
func startManifestWatch(projects []Project) *manifestWatch {
	w := &manifestWatch{projects: projects}
	if len(projects) == 0 {
		return w
	}
	w.paths = resolveManifests(projects)
	if len(w.paths) == 0 {
		w.degraded = "no project state directory exists on this host yet, so shells.json cannot be watched; new shells appear on the inventory cadence"
		return w
	}
	watcher, err := newManifestWatcher(livewatch.Config{
		Quiet:      manifestQuiet,
		MaxLatency: manifestMaxLatency,
	})
	if err != nil {
		w.degraded = fmt.Sprintf("shells.json watch unavailable on this host (%v); new shells appear on the inventory cadence", err)
		return w
	}
	w.watcher = watcher
	w.watcher.Watch(w.targets()...)
	if len(w.paths) < len(projects) {
		w.degraded = fmt.Sprintf("watching %d of %d project manifests; the rest have no state directory on this host yet and appear on the inventory cadence",
			len(w.paths), len(projects))
	}
	return w
}

// signals is the channel a cycle's tail select waits on beside the poll timer.
// A nil channel from an inert watch blocks forever, which is exactly the
// degraded behaviour: the poll timer alone decides the cadence.
func (w *manifestWatch) signals() <-chan struct{} {
	if w == nil || w.watcher == nil {
		return nil
	}
	return w.watcher.Signals()
}

// Degraded is what to tell the viewer, once, about a watch that is not whole.
func (w *manifestWatch) Degraded() string {
	if w == nil {
		return ""
	}
	return w.degraded
}

// reconcile re-resolves manifests whose project had no state directory when the
// watch started, and re-registers the target set.
//
// It is called from the full-inventory phase rather than every cycle, and only
// while the set is incomplete, because resolution reads every registered
// project's meta.json. A project that gains a state directory — the first
// `sidecar create shell` in a remote project that had never been opened on that
// host, which is exactly the case Phase C found the hard way — therefore starts
// being watched within one inventory tick, which is the freshness it had
// before.
func (w *manifestWatch) reconcile() {
	if w == nil || w.watcher == nil || len(w.paths) >= len(w.projects) {
		return
	}
	paths := resolveManifests(w.projects)
	if len(paths) <= len(w.paths) {
		return
	}
	w.paths = paths
	w.watcher.Watch(w.targets()...)
}

// targets is the watch set: one shells.json per configured project, in
// configured order so livewatch's registration cap — 64 directories — drops the
// same projects on every reconcile rather than an arbitrary subset.
//
// A file target registers its parent directory, and a project's state directory
// holds only a handful of entries, so the descriptor cost is proportional to
// project count rather than to worktree count.
func (w *manifestWatch) targets() []livewatch.Target {
	targets := make([]livewatch.Target, 0, len(w.paths))
	for _, project := range w.projects {
		if path := w.paths[project.Path]; path != "" {
			targets = append(targets, livewatch.File(path))
		}
	}
	return targets
}

// stop gives every descriptor back.
func (w *manifestWatch) stop() {
	if w == nil || w.watcher == nil {
		return
	}
	w.watcher.Stop()
	w.watcher = nil
}

func resolveManifests(projects []Project) map[string]string {
	roots := make([]string, 0, len(projects))
	for _, project := range projects {
		if project.Path != "" {
			roots = append(roots, project.Path)
		}
	}
	dirs := projectdir.LookupAll(roots)
	paths := make(map[string]string, len(dirs))
	for root, dir := range dirs {
		paths[root] = filepath.Join(dir, "shells.json")
	}
	return paths
}
