package livepanes

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/livewatch"
)

// fixture is a surface with one kind whose visible targets and re-reads the
// test drives directly.
type fixture struct {
	set      *Set
	targets  []livewatch.Target
	refreshN int
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	f := &fixture{}
	f.set = NewSet("test", func() uint64 { return 7 }, Binding{
		Kind:    "doc",
		Config:  livewatch.Config{Quiet: 20 * time.Millisecond, MaxLatency: 80 * time.Millisecond},
		Targets: func() []livewatch.Target { return f.targets },
		Refresh: func() []tea.Cmd { f.refreshN++; return nil },
	})
	t.Cleanup(f.set.Stop)
	return f
}

// start runs the reconcile command and adopts whatever it produced, which is
// what the update loop does with it.
func (f *fixture) start(t *testing.T) {
	t.Helper()
	cmd := f.set.Reconcile()
	if cmd == nil {
		t.Fatal("Reconcile produced no command for a kind with targets")
	}
	msg := cmd()
	started, ok := msg.(WatchStartedMsg)
	if !ok {
		t.Fatalf("Reconcile produced %T, want WatchStartedMsg", msg)
	}
	// Handle returns livewatch.Listen, which blocks; the tests read Signals
	// directly instead of parking a goroutine on it.
	if _, handled := f.set.Handle(started); !handled {
		t.Fatal("Handle did not claim its own WatchStartedMsg")
	}
}

func TestSetWatchesOnlyWhatABindingReportsAsVisible(t *testing.T) {
	dir := t.TempDir()
	visible := filepath.Join(dir, "visible.md")

	f := newFixture(t)
	f.targets = []livewatch.Target{livewatch.File(visible)}
	f.start(t)

	w := f.set.Watcher("doc")
	if w == nil {
		t.Fatal("no watcher was adopted")
	}
	if got := w.WatchedDirs(); len(got) != 1 {
		t.Fatalf("WatchedDirs() = %v, want exactly the one visible target's directory", got)
	}

	// The pane scrolls out of view: the registrations go back, the watcher stays.
	f.targets = nil
	f.set.Reconcile()
	if got := w.WatchedDirs(); len(got) != 0 {
		t.Fatalf("WatchedDirs() = %v after the pane left the screen, want none", got)
	}
	if f.set.Watcher("doc") == nil {
		t.Fatal("the watcher was torn down rather than released; it is reused when the pane returns")
	}

	// And it comes back without a second watcher being started.
	f.targets = []livewatch.Target{livewatch.File(visible)}
	if cmd := f.set.Reconcile(); cmd != nil {
		t.Fatal("a returning pane started a second watcher instead of retargeting the first")
	}
	if got := w.WatchedDirs(); len(got) != 1 {
		t.Fatalf("WatchedDirs() = %v after the pane returned, want one", got)
	}
}

func TestSetRefreshesOnASignalAndStaysArmed(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "notes.md")
	if err := os.WriteFile(file, []byte("before\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	f := newFixture(t)
	f.targets = []livewatch.Target{livewatch.File(file)}
	f.start(t)

	for i, content := range []string{"first\n", "second\n"} {
		if err := os.WriteFile(file, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		select {
		case <-f.set.Watcher("doc").Signals():
		case <-time.After(3 * time.Second):
			t.Fatalf("write %d produced no signal", i+1)
		}
		if cmd, handled := f.set.Handle(ChangedMsg{Owner: "test", Kind: "doc"}); !handled || cmd == nil {
			t.Fatalf("write %d: Handle(ChangedMsg) = %v, %v, want a re-arm plus refresh", i+1, cmd, handled)
		}
		if f.refreshN != i+1 {
			t.Fatalf("write %d: refreshed %d times, want %d", i+1, f.refreshN, i+1)
		}
	}
}

func TestSetRereadsAPaneThatComesBackIntoView(t *testing.T) {
	// Watching only what is on screen is what makes a background tab affordable,
	// and it is also what makes it stale. The tab the user selects must re-read
	// rather than show whatever it last read before it left the screen.
	dir := t.TempDir()
	front := livewatch.File(filepath.Join(dir, "front.md"))
	back := livewatch.File(filepath.Join(dir, "back.md"))

	f := newFixture(t)
	f.targets = []livewatch.Target{front}
	f.start(t)
	if f.refreshN != 0 {
		t.Fatalf("arming the watcher re-read %d panes; the pane was just loaded", f.refreshN)
	}

	// Reconciling with the same visible pane must not re-read it.
	f.set.Reconcile()
	if f.refreshN != 0 {
		t.Fatalf("an unchanged visible set re-read %d times, want 0", f.refreshN)
	}

	// The user selects the other tab.
	f.targets = []livewatch.Target{back}
	f.set.Reconcile()
	if f.refreshN != 1 {
		t.Fatalf("selecting a tab that was not being watched re-read %d times, want 1", f.refreshN)
	}

	// And back again.
	f.targets = []livewatch.Target{front}
	f.set.Reconcile()
	if f.refreshN != 2 {
		t.Fatalf("returning to the first tab re-read %d times, want 2", f.refreshN)
	}
}

func TestSetIgnoresAnotherSurfacesMessages(t *testing.T) {
	// Both surfaces read the same bus. A workspace signal must not drive the
	// global browser's panes, or a refresh lands on a surface nobody is looking
	// at and, worse, re-arms a listener on a watcher it does not own.
	f := newFixture(t)
	if _, handled := f.set.Handle(ChangedMsg{Owner: "other", Kind: "doc"}); handled {
		t.Fatal("Handle claimed another surface's ChangedMsg")
	}
	if _, handled := f.set.Handle(WatchStartedMsg{Owner: "other", Kind: "doc"}); handled {
		t.Fatal("Handle claimed another surface's WatchStartedMsg")
	}
	if f.refreshN != 0 {
		t.Fatal("another surface's message caused a refresh")
	}
	if !Owns("test", ChangedMsg{Owner: "test"}) || Owns("test", ChangedMsg{Owner: "other"}) {
		t.Fatal("Owns did not classify by owner")
	}
}

func TestSetStopsAWatcherNobodyWants(t *testing.T) {
	// A project switch runs Stop, Init, Start. A watcher started before the
	// switch can still land afterwards, and if it is neither adopted nor stopped
	// its goroutine and its descriptors live for the rest of the process.
	dir := t.TempDir()
	f := newFixture(t)
	f.targets = []livewatch.Target{livewatch.File(filepath.Join(dir, "notes.md"))}

	cmd := f.set.Reconcile()
	started := cmd().(WatchStartedMsg)

	f.targets = nil // every pane closed while the watcher was being created
	f.set.Handle(started)

	if f.set.Watcher("doc") != nil {
		t.Fatal("a watcher nobody wanted was installed")
	}
	if !stopped(started.Watcher) {
		t.Fatal("the unwanted watcher was leaked instead of stopped")
	}
}

func TestSetStopReleasesEveryKindAndIsIdempotent(t *testing.T) {
	dir := t.TempDir()
	f := newFixture(t)
	f.targets = []livewatch.Target{livewatch.File(filepath.Join(dir, "notes.md"))}
	f.start(t)
	w := f.set.Watcher("doc")

	f.set.Stop()
	f.set.Stop()

	if f.set.Watcher("doc") != nil {
		t.Fatal("Stop left a watcher on the set")
	}
	if !stopped(w) {
		t.Fatal("Stop leaked the watcher")
	}
	if got := w.WatchedDirs(); len(got) != 0 {
		t.Fatalf("WatchedDirs() = %v after Stop, want none", got)
	}
}

func TestSetRetriesAKindWhoseWatcherFailedToStart(t *testing.T) {
	// A failed start must clear the starting flag, or the kind is wedged off for
	// the life of the process.
	dir := t.TempDir()
	f := newFixture(t)
	f.targets = []livewatch.Target{livewatch.File(filepath.Join(dir, "notes.md"))}

	f.set.Reconcile()
	f.set.Handle(WatchStartedMsg{Owner: "test", Kind: "doc", Epoch: 7})

	if cmd := f.set.Reconcile(); cmd == nil {
		t.Fatal("a kind whose watcher failed to start never tried again")
	}
}

func TestReconcileRunsPrepareForEveryKind(t *testing.T) {
	prepared := 0
	set := NewSet("test", nil, Binding{
		Kind:    "issue",
		Prepare: func() tea.Cmd { prepared++; return nil },
		Targets: func() []livewatch.Target { return nil },
	})
	defer set.Stop()

	set.Reconcile()
	set.Reconcile()
	if prepared != 2 {
		t.Fatalf("Prepare ran %d times, want once per reconcile", prepared)
	}
}

// stopped reports whether a watcher has been stopped, by observing that its
// signal channel is closed. Stop runs detached, so this waits for it.
func stopped(w *livewatch.PathWatcher) bool {
	if w == nil {
		return false
	}
	// Stop is idempotent, so calling it here is safe and makes the wait bounded.
	w.Stop()
	_, open := <-w.Signals()
	return !open
}

// The double-start path a project switch produces: Stop, Init, Start can leave
// two watchers in flight for one kind. The second must replace the first AND
// stop it, or the loser's goroutine and descriptors live for the rest of the
// process.
func TestAdoptingASecondWatcherStopsTheFirst(t *testing.T) {
	dir := t.TempDir()
	f := newFixture(t)
	f.targets = []livewatch.Target{livewatch.File(filepath.Join(dir, "notes.md"))}

	first := f.set.Reconcile()().(WatchStartedMsg)
	f.set.Handle(first)
	if f.set.Watcher("doc") != first.Watcher {
		t.Fatal("the first watcher was not adopted")
	}

	// A second start lands — the one begun before the switch.
	second, err := livewatch.NewPathWatcher(livewatch.Config{})
	if err != nil {
		t.Fatal(err)
	}
	f.set.Handle(WatchStartedMsg{Owner: "test", Kind: "doc", Epoch: 7, Watcher: second})

	if f.set.Watcher("doc") != second {
		t.Fatal("the incoming watcher was not installed")
	}
	if !stopped(first.Watcher) {
		t.Fatal("the replaced watcher was leaked instead of stopped")
	}
	if got := second.WatchedDirs(); len(got) != 1 {
		t.Fatalf("WatchedDirs() = %v on the adopted watcher, want the current targets", got)
	}
}

// Repeated open and close must give every descriptor back. WatchedDirs is the
// observable proxy: on macOS each registration holds a descriptor per file in
// the directory, so a registration that outlives its pane is a leak.
func TestRepeatedOpenAndCloseDoesNotLeakRegistrations(t *testing.T) {
	dir := t.TempDir()
	for range 20 {
		f := newFixture(t)
		f.targets = []livewatch.Target{livewatch.File(filepath.Join(dir, "notes.md"))}
		f.start(t)
		w := f.set.Watcher("doc")
		if len(w.WatchedDirs()) != 1 {
			t.Fatalf("WatchedDirs() = %v, want one", w.WatchedDirs())
		}
		f.set.Stop()
		w.Stop()
		if got := w.WatchedDirs(); len(got) != 0 {
			t.Fatalf("WatchedDirs() = %v after teardown, want none", got)
		}
	}
}

// Stop must clear the in-flight flag as well as the watcher. A start that was
// still in flight has no watcher to release, and a flag left set tells every
// later reconcile that one is already coming — turning a teardown into a kind
// that never watches anything again.
func TestStopUnwedgesAKindWhoseStartWasInFlight(t *testing.T) {
	dir := t.TempDir()
	f := newFixture(t)
	f.targets = []livewatch.Target{livewatch.File(filepath.Join(dir, "notes.md"))}

	started := f.set.Reconcile()().(WatchStartedMsg)
	// The teardown happens before the start lands.
	f.set.Stop()
	go started.Watcher.Stop()

	if cmd := f.set.Reconcile(); cmd == nil {
		t.Fatal("the kind was wedged off: no watcher, and no new start")
	}
}

// A change that arrives while the host is vetoing refreshes must be retried,
// not remembered and forgotten.
func TestReconcileRetriesAnOwedRefresh(t *testing.T) {
	owed := false
	refreshed := 0
	dir := t.TempDir()
	targets := []livewatch.Target{livewatch.File(filepath.Join(dir, "notes.md"))}
	set := NewSet("test", nil, Binding{
		Kind:    "doc",
		Targets: func() []livewatch.Target { return targets },
		Refresh: func() []tea.Cmd { refreshed++; return nil },
		Owed:    func() bool { return owed },
	})
	defer set.Stop()
	set.Handle(set.Reconcile()().(WatchStartedMsg))

	set.Reconcile()
	if refreshed != 0 {
		t.Fatalf("an unchanged, unowed reconcile refreshed %d times, want 0", refreshed)
	}

	owed = true
	set.Reconcile()
	if refreshed != 1 {
		t.Fatalf("an owed refresh was retried %d times, want 1", refreshed)
	}
}
