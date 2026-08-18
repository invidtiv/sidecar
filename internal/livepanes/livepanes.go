// Package livepanes owns the lifecycle every live-refreshed pane kind shares.
//
// [livewatch] answers two questions — has anything changed, and is the result
// different from what is on screen. Everything between those two answers was,
// until this package, written out once per pane kind per surface: start a
// watcher lazily inside a tea.Cmd, adopt it across a project switch or stop the
// one that lost the race, point it at the panes that are actually open, re-arm
// a one-shot listener on every signal, and give every descriptor back on
// teardown. Six copies of that, differing only in which map they walked and
// which message type they carried, is six chances for a new pane kind to
// silently not refresh — which is exactly what happened to the Resource leaf,
// which has none of it.
//
// So a surface registers its kinds once and inherits the rest:
//
//	set := livepanes.NewSet("workspace",
//	    livepanes.Binding{Kind: "doc", Targets: p.docTargets, Refresh: p.refreshDocs},
//	    ...)
//
// and calls [Set.Reconcile] once per update, [Set.Handle] on every message, and
// [Set.Stop] on teardown. What a kind still answers for itself is only what is
// genuinely its own: what to watch, and how to re-read it. Suppression vetoes,
// scroll preservation and the no-change gate stay with the pane, because each
// kind preserves different UI state across a re-read.
//
// # What to watch
//
// A Binding reports the panes that are on screen, not every pane that exists.
// On macOS each registration costs a descriptor per file in the watched
// directory, so watching a background tab in a directory nobody is looking at
// is a real cost paid for a frame nobody sees. The pane that comes back into
// view re-reads on focus instead; the no-change gate means an unchanged file
// costs one read and no repaint.
//
// # Startup
//
// Nothing here runs on the startup path. Watchers are created from inside a
// tea.Cmd the first time a kind has something to watch, so a session that never
// opens a pane never opens a descriptor for one.
package livepanes

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/livewatch"
)

// Binding is what one pane kind on one surface answers for itself.
type Binding struct {
	// Kind identifies this binding within its Set and travels in the messages
	// it produces. It must be stable and unique per Set.
	Kind string

	// Config tunes the watcher. The zero value takes livewatch's defaults,
	// which suit a pane reading one file. A kind whose store moves in bursts —
	// a td store several writes deep, a repository mid-rebase — should settle
	// longer, and say what the settle buys.
	Config livewatch.Config

	// Targets is what to watch right now: the visible panes of this kind, and
	// nothing else. It runs on the update goroutine on every reconcile, so it
	// must be cheap — read cached resolutions and open tabs, never walk a tree
	// or shell out. Returning nothing releases the registrations while leaving
	// the watcher usable.
	Targets func() []livewatch.Target

	// Prepare, when set, returns commands that resolve whatever Targets needs
	// before it can answer — where td keeps its store, where git keeps its
	// administrative files. It runs on every reconcile, so the host must cache
	// the answer and guard against resolving the same thing twice.
	Prepare func() tea.Cmd

	// Refresh re-reads the visible panes of this kind and returns the commands
	// carrying the results. The host owns the suppression veto: a refresh it
	// declines must stay owed, not be dropped.
	Refresh func() []tea.Cmd

	// Owed, when set, reports whether any pane of this kind has a re-read it
	// has not performed — a change that arrived while the host was vetoing
	// refreshes. It is what makes "the change lands as soon as the veto lifts"
	// true rather than aspirational: without it, a signal that arrives while a
	// modal, an overlay or a pane search owns the screen is remembered by the
	// pane and then never driven again, and the pane stays wrong until some
	// later write happens to arrive.
	//
	// Reconcile retries an owed refresh on every pass. That is cheap because a
	// still-vetoed refresh declines without reading anything.
	Owed func() bool
}

// WatchStartedMsg carries a watcher created off the update goroutine back to
// the Set that asked for it.
//
// It must always reach [Set.Handle], including for a project the user has since
// switched away from. It deliberately does not implement the hosts' stale-epoch
// interface: a host that dropped it would leak the watcher it carries — nothing
// else holds a reference to stop it — and wedge the kind, since the flag that
// says a start is in flight is cleared only by handling this. Handle already
// stops a watcher nobody wants, which is the right answer to a stale one; Epoch
// is here so a host can tell which project it belongs to, not so it can be
// discarded.
type WatchStartedMsg struct {
	Owner   string
	Kind    string
	Epoch   uint64
	Watcher *livewatch.PathWatcher
}

// ChangedMsg says the targets of one kind moved and its panes should re-read.
type ChangedMsg struct {
	Owner string
	Kind  string
}

// Owns reports whether msg belongs to a Set with this owner. Surfaces share a
// message bus, so a host uses this to classify a background result it must
// deliver whether or not it is the visible surface.
func Owns(owner string, msg tea.Msg) bool {
	switch m := msg.(type) {
	case WatchStartedMsg:
		return m.Owner == owner
	case ChangedMsg:
		return m.Owner == owner
	}
	return false
}

type binding struct {
	Binding
	watcher  *livewatch.PathWatcher
	starting bool
	// watching is the target set as of the last reconcile, which is what makes
	// "a pane that comes back into view re-reads" answerable: a path present now
	// and absent then is a pane that just became visible.
	watching map[string]bool
}

// appeared reports the targets that were not being watched a reconcile ago, and
// records the new set.
func (b *binding) appeared(targets []livewatch.Target) bool {
	next := make(map[string]bool, len(targets))
	fresh := false
	for _, t := range targets {
		next[t.Path] = true
		if !b.watching[t.Path] {
			fresh = true
		}
	}
	b.watching = next
	return fresh
}

// Set owns one surface's watchers, one per registered kind.
//
// A Set is used only from the update goroutine. The watchers it holds are
// themselves concurrency-safe; nothing here needs to be.
type Set struct {
	owner    string
	epoch    func() uint64
	bindings []*binding
}

// NewSet registers the kinds of one surface. owner distinguishes this surface's
// messages from another's on the shared bus. epoch may be nil for a surface
// with no project epoch, in which case every message carries zero.
func NewSet(owner string, epoch func() uint64, bindings ...Binding) *Set {
	s := &Set{owner: owner, epoch: epoch}
	for _, b := range bindings {
		s.bindings = append(s.bindings, &binding{Binding: b})
	}
	return s
}

func (s *Set) currentEpoch() uint64 {
	if s == nil || s.epoch == nil {
		return 0
	}
	return s.epoch()
}

func (s *Set) find(kind string) *binding {
	if s == nil {
		return nil
	}
	for _, b := range s.bindings {
		if b.Kind == kind {
			return b
		}
	}
	return nil
}

// Reconcile brings every watch set in line with the panes that are actually on
// screen, starting and releasing watchers as panes come and go.
//
// Call it once per update rather than from each of the places that create,
// close, retarget or restore a pane. Those call sites are spread across pane
// creation, tab selection, layout decode, leaf close and project switch, and
// missing one shows up as either a pane that never updates or a watcher that
// outlives its pane — both silent. Reconciling from one place makes "the watch
// set matches the visible pane set" hold by construction.
func (s *Set) Reconcile() tea.Cmd {
	if s == nil {
		return nil
	}
	var cmds []tea.Cmd
	for _, b := range s.bindings {
		if b.Prepare != nil {
			if cmd := b.Prepare(); cmd != nil {
				cmds = append(cmds, cmd)
			}
		}
		if cmd := s.sync(b); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

func (s *Set) sync(b *binding) tea.Cmd {
	var targets []livewatch.Target
	if b.Targets != nil {
		targets = b.Targets()
	}
	if len(targets) == 0 {
		// Release the descriptors but keep the watcher: a pane that scrolled out
		// of view or a tab the user switched away from usually comes back, and
		// tearing the watcher down and rebuilding it costs a goroutine and an
		// fsnotify instance for nothing.
		b.appeared(nil)
		if b.watcher != nil {
			b.watcher.Watch()
		}
		return nil
	}
	if b.watcher != nil {
		fresh := b.appeared(targets)
		b.watcher.Watch(targets...)
		if b.Refresh == nil {
			return nil
		}
		if !fresh {
			if b.Owed != nil && b.Owed() {
				// A change that arrived under a veto. Retried here rather than
				// from wherever the veto lifts, because a modal, an overlay and a
				// pane search each close from several places and one of them
				// would eventually forget.
				return tea.Batch(b.Refresh()...)
			}
			return nil
		}
		// Something is on screen that was not being watched a moment ago: a tab
		// the user just selected, a pane that came back from behind a modal, a
		// leaf the layout could not place until the window grew. Whatever it
		// shows was last read before it stopped being watched, so read it again.
		// A pane still loading declines, and the no-change gate means an
		// unchanged file costs one read and no repaint.
		return tea.Batch(b.Refresh()...)
	}
	if b.starting {
		return nil
	}
	b.starting = true
	owner, kind, epoch := s.owner, b.Kind, s.currentEpoch()
	return livewatch.Start(b.Config, targets, func(w *livewatch.PathWatcher, err error) tea.Msg {
		if err != nil {
			// A watcher that could not start is reported as one that started
			// with nothing, so the starting flag clears and a later reconcile
			// tries again rather than wedging the kind off forever.
			return WatchStartedMsg{Owner: owner, Kind: kind, Epoch: epoch}
		}
		return WatchStartedMsg{Owner: owner, Kind: kind, Epoch: epoch, Watcher: w}
	})
}

// Handle processes a live-refresh message, reporting whether msg was one of
// this Set's.
func (s *Set) Handle(msg tea.Msg) (tea.Cmd, bool) {
	if s == nil {
		return nil, false
	}
	switch m := msg.(type) {
	case WatchStartedMsg:
		if m.Owner != s.owner {
			return nil, false
		}
		return s.adopt(m), true
	case ChangedMsg:
		if m.Owner != s.owner || s.find(m.Kind) == nil {
			return nil, false
		}
		return s.changed(m.Kind), true
	}
	return nil, false
}

// Kinds lists the registered kinds in registration order. It is what a parity
// test asserts against: claiming a message is not the same as having a binding
// to answer it with, and a test that checks only the former passes against a
// surface that quietly stopped refreshing one of its panes.
func (s *Set) Kinds() []string {
	if s == nil {
		return nil
	}
	kinds := make([]string, 0, len(s.bindings))
	for _, b := range s.bindings {
		kinds = append(kinds, b.Kind)
	}
	return kinds
}

// adopt installs a watcher created off the update goroutine, stopping it
// instead if this Set no longer wants one.
//
// A project switch runs Stop then Init then Start, so a watcher started before
// the switch can still land afterwards. Whichever watcher is not adopted has to
// be stopped, or its goroutine and its descriptors live for the rest of the
// process.
func (s *Set) adopt(msg WatchStartedMsg) tea.Cmd {
	b := s.find(msg.Kind)
	if b == nil {
		if msg.Watcher != nil {
			go msg.Watcher.Stop()
		}
		return nil
	}
	b.starting = false
	if msg.Watcher == nil {
		return nil
	}

	var targets []livewatch.Target
	if b.Targets != nil {
		targets = b.Targets()
	}
	if len(targets) == 0 {
		// Every pane of this kind closed while the watcher was being created.
		go msg.Watcher.Stop()
		return nil
	}
	if b.watcher != nil && b.watcher != msg.Watcher {
		old := b.watcher
		go old.Stop()
	}
	b.watcher = msg.Watcher
	// Seed the watched set from the first arming. These panes were loaded on
	// the way here; counting them as newly visible would re-read every one of
	// them the moment the watcher starts.
	b.appeared(targets)
	b.watcher.Watch(targets...)
	return livewatch.Listen(b.watcher, ChangedMsg{Owner: s.owner, Kind: b.Kind})
}

// changed re-reads the visible panes of one kind and re-arms its listener.
//
// The listener is re-armed first: a signal that arrives while the re-read is in
// flight must not be lost, or a file written twice in quick succession leaves
// the pane showing the first write.
func (s *Set) changed(kind string) tea.Cmd {
	b := s.find(kind)
	if b == nil || b.watcher == nil {
		return nil
	}
	cmds := []tea.Cmd{livewatch.Listen(b.watcher, ChangedMsg{Owner: s.owner, Kind: kind})}
	if b.Refresh != nil {
		cmds = append(cmds, b.Refresh()...)
	}
	return tea.Batch(cmds...)
}

// Stop releases every descriptor this Set holds.
//
// livewatch.Stop blocks until the watcher goroutine has drained, so this runs
// detached: a host calls it from the quit and project-switch boundary, and
// stalling there would stall the switch. It is idempotent, because that
// boundary can run twice around a switch.
func (s *Set) Stop() {
	if s == nil {
		return
	}
	for _, b := range s.bindings {
		// Cleared for every binding, watcher or not. A start still in flight has
		// no watcher to release, and leaving its flag set would tell every later
		// reconcile that one is already coming — turning a teardown into a kind
		// that never watches anything again.
		b.starting = false
		b.watching = nil
		if b.watcher == nil {
			continue
		}
		w := b.watcher
		b.watcher = nil
		go w.Stop()
	}
}

// Watcher returns the watcher for a kind, or nil when none is running. It
// exists for tests that assert descriptors are given back.
func (s *Set) Watcher(kind string) *livewatch.PathWatcher {
	b := s.find(kind)
	if b == nil {
		return nil
	}
	return b.watcher
}
