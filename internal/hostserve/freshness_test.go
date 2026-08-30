package hostserve

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/livewatch"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/tty"
)

// syncBuilder is a strings.Builder the serve loop writes from its own goroutine
// while the test reads. These tests run the loop concurrently — the whole point
// is what happens between cycles — so the transcript needs a lock the
// single-shot tests do not.
type syncBuilder struct {
	mu  sync.Mutex
	buf strings.Builder
}

func (b *syncBuilder) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuilder) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

// watchedProject is liveProject's shape with the manifest path handed back, so
// a test can write the file the watch is registered on.
func watchedProject(t *testing.T, runner *fakeRunner) (Project, string) {
	t.Helper()
	root := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	projectDir := filepath.Join(state, "sidecar", "projects", "spike")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectDir, "meta.json"),
		fmt.Appendf(nil, `{"path": %q}`, canonical), 0o644); err != nil {
		t.Fatal(err)
	}
	manifest := filepath.Join(projectDir, "shells.json")
	if err := os.WriteFile(manifest, []byte(`{"version":2,"shells":[]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return Project{Name: "spike", Path: canonical}, manifest
}

func writeManifest(t *testing.T, path string, sessions ...string) {
	t.Helper()
	records := make([]string, 0, len(sessions))
	for i, session := range sessions {
		records = append(records, fmt.Sprintf(
			`{"tmuxName":%q,"displayName":"Shell %d","namespace":%q}`, session, i+1, tmuxenv.Namespace()))
	}
	body := fmt.Sprintf(`{"version":2,"shells":[%s]}`, strings.Join(records, ","))
	// Atomic write plus rename, exactly as shellstate writes it: the watch has
	// to survive the same create/rename pair the real writer produces.
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Rename(tmp, path); err != nil {
		t.Fatal(err)
	}
}

// snapshotCount counts full snapshots in a transcript. A snapshot is emitted
// exactly when the expensive inventory ran, so it is the observable proof that
// durable state was re-read.
func snapshotCount(t *testing.T, raw string) int {
	t.Helper()
	count := 0
	for _, msg := range decode(t, raw) {
		if msg.Kind == hostproto.KindSnapshot {
			count++
		}
	}
	return count
}

func waitFor(t *testing.T, why string, check func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if check() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", why)
}

// TestServeReInventoriesWhenAManifestChanges is item 1 of the workstream: a
// shell created on the host appears in the coalesce window, not on the next
// minute boundary. The inventory interval here is an hour, so any second
// snapshot can only have come from the watch.
func TestServeReInventoriesWhenAManifestChanges(t *testing.T) {
	out := &syncBuilder{}
	runner := &fakeRunner{}
	project, manifest := watchedProject(t, runner)

	opts := baseOptions(nil, runner, time.Now)
	opts.Out = out
	opts.Projects = []Project{project}
	opts.Cycles = 0
	opts.InventoryEvery = time.Hour
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Hour, time.Hour, time.Hour

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, opts) }()

	waitFor(t, "the first snapshot", func() bool { return snapshotCount(t, out.String()) == 1 })

	// Nothing but the manifest write can produce another snapshot: every timer
	// in this run is an hour out.
	writeManifest(t, manifest, "spike-created-remotely")
	waitFor(t, "a snapshot triggered by the manifest write", func() bool {
		return snapshotCount(t, out.String()) >= 2
	})

	var found bool
	for _, msg := range decode(t, out.String()) {
		if msg.Kind != hostproto.KindSnapshot {
			continue
		}
		for _, p := range msg.Snapshot.Projects {
			for _, item := range p.Items {
				if item.Session == "spike-created-remotely" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("the new shell never reached the wire: %s", out.String())
	}

	cancel()
	if err := <-done; err != nil {
		t.Fatalf("Serve: %v", err)
	}
}

// TestServeStillServesWhenTheWatchCannotStart is the degradation contract. A
// host out of inotify watches must keep serving on the clock and must say so,
// because a viewer that cannot tell "degraded" from "unreachable" will read the
// wrong one.
func TestServeStillServesWhenTheWatchCannotStart(t *testing.T) {
	previous := newManifestWatcher
	newManifestWatcher = func(livewatch.Config) (*livewatch.PathWatcher, error) {
		return nil, fmt.Errorf("too many open files")
	}
	t.Cleanup(func() { newManifestWatcher = previous })

	var out strings.Builder
	runner := &fakeRunner{}
	project, _ := watchedProject(t, runner)

	opts := baseOptions(&out, runner, time.Now)
	opts.Projects = []Project{project}
	opts.Cycles = 2
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Millisecond, time.Millisecond, time.Millisecond
	opts.InventoryEvery = time.Nanosecond

	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve refused to run without a watch: %v", err)
	}
	messages := decode(t, out.String())

	var snapshots int
	var degraded *hostproto.Error
	for _, msg := range messages {
		switch msg.Kind {
		case hostproto.KindSnapshot:
			snapshots++
		case hostproto.KindError:
			if msg.Error != nil && strings.Contains(msg.Error.Message, "watch unavailable") {
				degraded = msg.Error
			}
		}
	}
	if snapshots < 2 {
		t.Errorf("snapshots = %d; the clock cadence did not survive a failed watch", snapshots)
	}
	if degraded == nil {
		t.Fatalf("a failed watch was never reported: %s", out.String())
	}
	if degraded.Fatal {
		t.Error("a failed watch was reported as fatal; it must not take the host down")
	}
}

// TestServeSaysNothingWhenTheWatchIsWhole keeps the degraded note meaningful. A
// note on every healthy connection is a note nobody reads.
func TestServeSaysNothingWhenTheWatchIsWhole(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	project, _ := watchedProject(t, runner)

	opts := baseOptions(&out, runner, time.Now)
	opts.Projects = []Project{project}
	opts.Cycles = 1

	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	for _, msg := range decode(t, out.String()) {
		if msg.Kind == hostproto.KindError {
			t.Errorf("a healthy host reported %q", msg.Error.Message)
		}
	}
}

// reapOptions builds a serve run whose probe and manifest writer are recorded
// rather than real.
type reapRecorder struct {
	mu        sync.Mutex
	verdict   shellliveness.Verdict
	probes    []string
	forgotten []string
}

func (r *reapRecorder) probe(session string) shellliveness.Verdict {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.probes = append(r.probes, session)
	return r.verdict
}

func (r *reapRecorder) forget(_, session, _ string, _ time.Time) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.forgotten = append(r.forgotten, session)
	return nil
}

func (r *reapRecorder) forgottenNames() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.forgotten...)
}

// unrelatedPane is a tmux listing that answered and simply does not contain the
// shell under test — a live server, one dead session.
const unrelatedPane = "%9\tunrelated-session\t/tmp\tbash\t\t0\t4243\n"

// onSecondListing runs change when the pane listing is asked for the second
// time or later, so the first cycle can establish the positive liveness the
// reap requires before any later absence means anything.
func onSecondListing(runner *fakeRunner, change func(*fakeRunner)) {
	listings := 0
	runner.hook = func(f *fakeRunner, name string, args []string) {
		if name != "tmux" || !strings.Contains(strings.Join(args, " "), "list-panes") {
			return
		}
		listings++
		if listings >= 2 {
			change(f)
		}
	}
}

// reapRun drives two cycles: one in which the shell's pane is listed, and one
// in which the listing answers without it. What the second listing looks like
// is the variable each guard test changes.
func reapRun(t *testing.T, verdict shellliveness.Verdict, second func(*fakeRunner), mutate func(*Options)) *reapRecorder {
	t.Helper()
	var out strings.Builder
	runner := &fakeRunner{}
	project, manifest := watchedProject(t, runner)
	writeManifest(t, manifest, "spike-dying-shell")

	recorder := &reapRecorder{verdict: verdict}
	opts := baseOptions(&out, runner, time.Now)
	opts.Projects = []Project{project}
	opts.Cycles = 2
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Millisecond, time.Millisecond, time.Millisecond
	opts.InventoryEvery = time.Nanosecond
	opts.ProbeShell = recorder.probe
	opts.ForgetShell = recorder.forget
	opts.ServerIncarnation = func() tmuxserver.Incarnation { return tmuxserver.Present(11, 22, 33) }
	opts.Capture = func(string, int) (string, tty.PaneState, error) { return "", tty.PaneState{}, nil }

	runner.panes = "%1\tspike-dying-shell\t" + project.Path + "\tbash\t\t0\t4242\n"
	if second == nil {
		second = func(f *fakeRunner) { f.panes = unrelatedPane }
	}
	onSecondListing(runner, second)

	if mutate != nil {
		mutate(&opts)
	}
	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return recorder
}

// TestServeReapsAShellWhoseSessionIsGone is item 2 of the workstream: the
// record itself goes away on the host, not only the liveness flag on the row.
func TestServeReapsAShellWhoseSessionIsGone(t *testing.T) {
	recorder := reapRun(t, shellliveness.Gone, nil, nil)
	if got := recorder.forgottenNames(); len(got) != 1 || got[0] != "spike-dying-shell" {
		t.Fatalf("forgotten = %v, want exactly the dead shell", got)
	}
}

// TestServeKeepsAShellWhenTmuxCannotAnswer. Unknown is what an unreachable tmux
// produces, and it must never close anything.
func TestServeKeepsAShellWhenTmuxCannotAnswer(t *testing.T) {
	recorder := reapRun(t, shellliveness.Unknown, nil, nil)
	if got := recorder.forgottenNames(); len(got) != 0 {
		t.Fatalf("an unconfirmed death wrote the manifest: %v", got)
	}
}

// TestServeReapsNothingOnAnEmptyPaneListing is the td-8d18de guard, executed
// against a machine the user is not sitting at. `tmux kill-server` does not
// unlink its socket, so a dead server and a server with no sessions are
// indistinguishable here; acting on the empty listing is how six live shells
// were destroyed.
func TestServeReapsNothingOnAnEmptyPaneListing(t *testing.T) {
	recorder := reapRun(t, shellliveness.Gone, func(f *fakeRunner) { f.panes = "" }, nil)
	if got := recorder.forgottenNames(); len(got) != 0 {
		t.Fatalf("a vanished tmux server closed shells: %v", got)
	}
	if probes := len(recorder.probes); probes != 0 {
		t.Fatalf("an empty listing produced %d probes; the pass must not start", probes)
	}
}

// TestServeReapsNothingWhenTheListingFails. A failed inventory is not evidence
// about anything, and it arrives for every project at once.
func TestServeReapsNothingWhenTheListingFails(t *testing.T) {
	recorder := reapRun(t, shellliveness.Gone, func(f *fakeRunner) {
		f.err = fmt.Errorf("tmux inventory failed")
	}, nil)
	if got := recorder.forgottenNames(); len(got) != 0 {
		t.Fatalf("a failed inventory closed shells: %v", got)
	}
	if probes := len(recorder.probes); probes != 0 {
		t.Fatalf("a failed inventory produced %d probes; the pass must not start", probes)
	}
}

// TestServeReapsNothingAcrossAServerRestart is the incarnation fence. A listing
// taken under a replacement server says nothing about records judged under the
// old one, and the transition alone clears every prior sighting.
func TestServeReapsNothingAcrossAServerRestart(t *testing.T) {
	recorder := reapRun(t, shellliveness.Gone, nil, func(opts *Options) {
		var mu sync.Mutex
		calls := 0
		opts.ServerIncarnation = func() tmuxserver.Incarnation {
			mu.Lock()
			defer mu.Unlock()
			calls++
			// Call 1 is the hello's, call 2 is the first cycle's. The restart
			// lands between the two cycles, which is the case that matters: the
			// shell was seen alive under one server and is missing from a
			// listing taken under another.
			if calls > 2 {
				return tmuxserver.Present(99, 1234, 4321)
			}
			return tmuxserver.Present(11, 22, 33)
		}
	})
	if got := recorder.forgottenNames(); len(got) != 0 {
		t.Fatalf("a server restart closed shells: %v", got)
	}
	if probes := len(recorder.probes); probes != 0 {
		t.Fatalf("a server restart produced %d probes; the transition must clear every prior sighting", probes)
	}
}

// TestServeNeverReapsAShellItNeverSawAlive. A manifest entry that was already
// cold when this serve connected is what survives a reboot on the host, and the
// recreate path owns it — not a viewer that has just arrived.
func TestServeNeverReapsAShellItNeverSawAlive(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	project, manifest := watchedProject(t, runner)
	writeManifest(t, manifest, "spike-cold-shell")

	recorder := &reapRecorder{verdict: shellliveness.Gone}
	opts := baseOptions(&out, runner, time.Now)
	opts.Projects = []Project{project}
	opts.Cycles = 3
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Millisecond, time.Millisecond, time.Millisecond
	opts.InventoryEvery = time.Nanosecond
	opts.ProbeShell = recorder.probe
	opts.ForgetShell = recorder.forget
	// A live server that has simply never listed this shell's pane.
	runner.panes = strings.Join([]string{"%9", "unrelated-session", "/tmp", "bash", "", "0", "4243"}, "\t") + "\n"

	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	if got := recorder.forgottenNames(); len(got) != 0 {
		t.Fatalf("a cold manifest entry was auto-closed: %v", got)
	}
	if probes := len(recorder.probes); probes != 0 {
		t.Fatalf("a shell that was never seen alive was probed %d times", probes)
	}
}

// TestServeNeverReapsAForeignNamespaceShell. A shell on another tmux server is
// invisible to this listing, so its absence from it says nothing at all.
func TestServeNeverReapsAForeignNamespaceShell(t *testing.T) {
	recorder := reapRun(t, shellliveness.Gone, nil, func(opts *Options) {
		opts.Namespace = func() string { return "/tmp/some-other-socket/default" }
	})
	if got := recorder.forgottenNames(); len(got) != 0 {
		t.Fatalf("a foreign-namespace shell was closed: %v", got)
	}
}
