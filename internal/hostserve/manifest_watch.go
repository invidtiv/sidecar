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
// The two degrade differently, and conflating them is what made a first-use host
// permanently slow. A watcher that could not be created at all stays on the clock
// for the life of the connection. A project with no state directory yet is
// temporary: the watcher exists with nothing registered on it, reconcile picks
// the project up on the first full inventory after one appears, and the note is
// withdrawn on the stream rather than left standing.
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
	watcher *livewatch.PathWatcher

	// roots is the deduplicated set of non-empty configured project paths, in
	// configured order. It is what "the watch is whole" is measured against, and
	// it is not len(projects): two config entries naming one Path, or an entry
	// with an empty Path, would leave the set permanently short of the project
	// count, and reconcile would then pay a projectdir.LookupAll — a ReadDir of
	// <state>/projects plus a meta.json read per entry — on every full inventory
	// for the life of the connection.
	roots []string

	// paths is project root -> shells.json. It can be incomplete, which is a
	// normal state rather than an error: a project configured on the host but
	// never opened there has no state directory yet.
	paths map[string]string

	// degraded is what to tell the viewer about a watch that could not be
	// established in full, or empty when there is nothing to say. It is
	// re-derived on every reconcile, because a note that is never withdrawn
	// leaves a host claiming to be degraded after it has stopped being so.
	degraded string
}

// startManifestWatch registers a watch over each project's manifest. It never
// returns nil and never returns an error: every failure mode degrades to the
// clock, and Degraded says which one happened.
//
// The watcher is created whenever there is a project to watch, even when not
// one of them has a state directory yet — which is the first-use case, a
// machine where Sidecar has never been opened. Returning early there left the
// watch unstartable for the life of the connection: reconcile has nothing to
// register onto without a watcher, so the first `sidecar create shell` on that
// host went back to costing a full inventory tick, and the degraded note said
// so forever. A watcher with no targets holds no descriptors (livewatch.Watch
// releases every registration when passed none), so creating it up front costs
// one fsnotify handle and buys the recovery.
//
// This runs after the hello has been written, not before. A hello that waited
// on a directory read would make every viewer's first impression of a host
// slower for a freshness gain none of them can observe yet.
func startManifestWatch(projects []Project) *manifestWatch {
	w := &manifestWatch{roots: manifestRoots(projects)}
	if len(w.roots) == 0 {
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
	w.paths = resolveManifests(w.roots)
	w.watcher.Watch(w.targets()...)
	w.degraded = w.note()
	return w
}

// note is what a watch of this shape has to tell the viewer, or "" when it is
// whole. Derived rather than assigned once, so the same function that raises
// the note is the one that withdraws it.
func (w *manifestWatch) note() string {
	switch {
	case len(w.paths) == 0:
		return "no project state directory exists on this host yet, so shells.json cannot be watched; new shells appear on the inventory cadence until one does"
	case len(w.paths) < len(w.roots):
		return fmt.Sprintf("watching %d of %d project manifests; the rest have no state directory on this host yet and appear on the inventory cadence",
			len(w.paths), len(w.roots))
	default:
		return ""
	}
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
// before. On a host where NO project had one, this is the whole of how the
// watch ever starts.
func (w *manifestWatch) reconcile() {
	if w == nil || w.watcher == nil || len(w.paths) >= len(w.roots) {
		return
	}
	paths := resolveManifests(w.roots)
	if len(paths) <= len(w.paths) {
		return
	}
	w.paths = paths
	w.watcher.Watch(w.targets()...)
	w.degraded = w.note()
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
	for _, root := range w.roots {
		if path := w.paths[root]; path != "" {
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

// manifestRoots is the deduplicated, non-empty project paths in configured
// order. Duplicates and blanks are dropped here rather than tolerated
// downstream, because every count taken of this set — "is the watch whole?" —
// would otherwise be measured against entries that can never resolve.
func manifestRoots(projects []Project) []string {
	roots := make([]string, 0, len(projects))
	seen := make(map[string]bool, len(projects))
	for _, project := range projects {
		if project.Path == "" || seen[project.Path] {
			continue
		}
		seen[project.Path] = true
		roots = append(roots, project.Path)
	}
	return roots
}

func resolveManifests(roots []string) map[string]string {
	dirs := projectdir.LookupAll(roots)
	paths := make(map[string]string, len(dirs))
	for root, dir := range dirs {
		paths[root] = filepath.Join(dir, "shells.json")
	}
	return paths
}
