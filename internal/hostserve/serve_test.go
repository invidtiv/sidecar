package hostserve

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/tty"
)

// fakeRunner answers the two commands the collector shells out to — tmux and
// git — so the whole serve loop runs with no tmux server, no repository, and
// no subprocesses at all.
type fakeRunner struct {
	mu    sync.Mutex
	panes string
	err   error
	calls []string

	// hook, when set, runs just before each answer, with the call it is about
	// to serve. Tests that need one cycle to see something different from the
	// next — a pane that leaves the listing, an inventory that starts failing —
	// use it to change the fixture in step with the loop rather than racing it.
	// It runs under the runner's lock and must not re-enter it.
	hook func(f *fakeRunner, name string, args []string)
}

func (f *fakeRunner) Output(_ context.Context, name string, args ...string) ([]byte, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, name+" "+strings.Join(args, " "))
	if f.hook != nil {
		f.hook(f, name, args)
	}
	if name == "tmux" {
		if f.err != nil {
			return nil, f.err
		}
		return []byte(f.panes), nil
	}
	// A project with no worktrees. The shell rows are what these tests are
	// about; a git failure would also suppress them, which is exactly the
	// behaviour CollectProjectInventory is careful to avoid.
	return []byte("worktree /project\nbranch refs/heads/main\n"), nil
}

func decode(t *testing.T, raw string) []hostproto.Message {
	t.Helper()
	var messages []hostproto.Message
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var msg hostproto.Message
		if err := json.Unmarshal([]byte(line), &msg); err != nil {
			t.Fatalf("undecodable serve output %q: %v", line, err)
		}
		messages = append(messages, msg)
	}
	return messages
}

func baseOptions(out *strings.Builder, runner *fakeRunner, now func() time.Time) Options {
	return Options{
		Out:      out,
		HostID:   "testhost",
		Projects: []Project{{Name: "proj", Path: t0ProjectPath}},
		Now:      now,
		Runner:   runner,
		Capture: func(string, int) (string, tty.PaneState, error) {
			return "screen text", tty.PaneState{}, nil
		},
		ServerIncarnation: func() tmuxserver.Incarnation { return tmuxserver.Absent() },
		Hostname:          func() string { return "testhost" },
		TmuxPath:          func() (string, bool) { return "/usr/bin/tmux", true },
		TmuxVersion:       func() string { return "tmux 3.6" },
	}
}

// t0ProjectPath is a path that does not exist, which is deliberate: the loop
// must still emit a well-formed hello and snapshot for a project it cannot
// read, because a viewer has to be able to tell "host is fine, project is
// broken" from "host is unreachable".
const t0ProjectPath = "/nonexistent-spike-project"

func TestServeEmitsHelloFirst(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	opts := baseOptions(&out, runner, time.Now)
	opts.Cycles = 1

	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	messages := decode(t, out.String())
	if len(messages) == 0 {
		t.Fatal("no messages")
	}
	if messages[0].Kind != hostproto.KindHello {
		t.Fatalf("first message is %q, want hello", messages[0].Kind)
	}
	hello := messages[0].Hello
	if hello.Proto != hostproto.Version {
		t.Errorf("hello proto = %d, want %d", hello.Proto, hostproto.Version)
	}
	if !hello.TmuxPresent || hello.TmuxVersion != "tmux 3.6" {
		t.Errorf("hello tmux = %v/%q", hello.TmuxPresent, hello.TmuxVersion)
	}
	if hello.ServerRunning {
		t.Error("hello reports a running server when the incarnation is absent")
	}
	if hello.Projects != 1 {
		t.Errorf("hello projects = %d, want 1", hello.Projects)
	}
	for _, msg := range messages {
		if msg.Seq == 0 {
			t.Error("message emitted without a sequence number")
		}
	}
}

// liveProject builds a project the collector will fully traverse: a real
// directory, a registered state directory with a shell manifest, and a pane in
// the listing that the manifest's namespace matches.
//
// Without all four, the status pass short-circuits and never reaches the
// capture path — which is how an earlier version of TestServeIsReadOnly came
// to assert against three list-panes calls and nothing else.
func liveProject(t *testing.T, runner *fakeRunner) Project {
	t.Helper()
	root := t.TempDir()
	state := t.TempDir()
	t.Setenv("XDG_STATE_HOME", state)

	projectDir := filepath.Join(state, "sidecar", "projects", "spike")
	if err := os.MkdirAll(projectDir, 0o755); err != nil {
		t.Fatal(err)
	}
	// projectdir resolves a project by its meta.json path, not by directory
	// name, and matches on the canonical path.
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	write := func(name, content string) {
		if err := os.WriteFile(filepath.Join(projectDir, name), []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("meta.json", fmt.Sprintf(`{"path": %q}`, canonical))
	// The namespace must be the tmux socket path: RefreshProjectStatus refuses
	// to correlate a shell row to a pane unless it matches exactly.
	write("shells.json", fmt.Sprintf(
		`{"version":2,"shells":[{"tmuxName":"spike-claude","displayName":"Claude pane","agentType":"claude","namespace":%q}]}`,
		tmuxenv.Namespace()))

	runner.panes = strings.Join([]string{
		"%1", "spike-claude", canonical, "node", "spinner title", "0", "4242",
	}, "\t") + "\n"
	return Project{Name: "spike", Path: canonical}
}

// TestServeIsReadOnly is the guarantee the whole design rests on.
//
// It asserts against the commands actually issued, because that — not "the
// package links in no writer" — is the level at which the guarantee really
// holds: hostserve depends on internal/tty, which contains resize-window,
// send-keys and the geometry lease.
//
// The project is a real one with a live pane, so the run reaches the status
// pass, the capture, and the tracker. A version of this test that stops at a
// missing directory proves only that a loop which does nothing mutates
// nothing.
func TestServeIsReadOnly(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	project := liveProject(t, runner)

	opts := baseOptions(&out, runner, time.Now)
	opts.Projects = []Project{project}
	opts.Cycles = 3
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Millisecond, time.Millisecond, time.Millisecond
	opts.InventoryEvery = time.Nanosecond

	var captured int
	opts.Capture = func(target string, lines int) (string, tty.PaneState, error) {
		captured++
		return "esc to interrupt\n> working", tty.PaneState{}, nil
	}

	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	forbidden := []string{
		"resize-window", "resize-pane", "send-keys", "kill-session", "kill-pane",
		"kill-server", "set-option", "new-session", "respawn-pane", "rename-session",
		"set-buffer", "paste-buffer", "split-window", "kill-window",
	}
	runner.mu.Lock()
	calls := append([]string(nil), runner.calls...)
	runner.mu.Unlock()
	for _, call := range calls {
		for _, verb := range forbidden {
			if strings.Contains(call, verb) {
				t.Errorf("serve issued a mutating command: %q", call)
			}
		}
	}

	// The run must actually have reached the interesting code, or the loop
	// above is vacuous. Both a git read and a capture are required.
	if captured == 0 {
		t.Error("no pane was ever captured; the assertions above are vacuous")
	}
	var sawGit, sawListPanes bool
	for _, call := range calls {
		sawGit = sawGit || strings.HasPrefix(call, "git ")
		sawListPanes = sawListPanes || strings.Contains(call, "list-panes")
	}
	if !sawGit || !sawListPanes {
		t.Errorf("serve never read git (%v) or tmux (%v); calls=%v", sawGit, sawListPanes, calls)
	}
}

// TestServeShipsThePreviewItAlreadyCaptured covers the mechanism the design
// leans on for zero-extra-capture previews: the capture decorator retains the
// text the status pass took, and it reaches the wire.
func TestServeShipsThePreviewItAlreadyCaptured(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	project := liveProject(t, runner)

	opts := baseOptions(&out, runner, time.Now)
	opts.Projects = []Project{project}
	opts.Cycles = 1
	opts.Capture = func(string, int) (string, tty.PaneState, error) {
		return "PREVIEW-MARKER", tty.PaneState{}, nil
	}
	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	var found bool
	for _, msg := range decode(t, out.String()) {
		if msg.Kind != hostproto.KindSnapshot {
			continue
		}
		for _, p := range msg.Snapshot.Projects {
			for _, item := range p.Items {
				if item.Preview == "PREVIEW-MARKER" {
					found = true
				}
			}
		}
	}
	if !found {
		t.Errorf("the capture text never reached the wire: %s", out.String())
	}
}

// TestServeDropsAStalePreviewWhenCaptureFails. A preview retained from an
// earlier cycle would be shipped with a CapturedAt of now — a silently stale
// screen, which is the failure this feature exists to avoid.
func TestServeDropsAStalePreviewWhenCaptureFails(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	project := liveProject(t, runner)

	opts := baseOptions(&out, runner, time.Now)
	opts.Projects = []Project{project}
	opts.Cycles = 2
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Millisecond, time.Millisecond, time.Millisecond
	opts.InventoryEvery = time.Nanosecond

	var calls int
	opts.Capture = func(string, int) (string, tty.PaneState, error) {
		calls++
		if calls == 1 {
			return "FIRST-CYCLE-TEXT", tty.PaneState{}, nil
		}
		return "", tty.PaneState{}, fmt.Errorf("capture failed")
	}
	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}

	snapshots := 0
	for _, msg := range decode(t, out.String()) {
		if msg.Kind != hostproto.KindSnapshot {
			continue
		}
		snapshots++
		if snapshots == 1 {
			continue
		}
		for _, p := range msg.Snapshot.Projects {
			for _, item := range p.Items {
				if strings.Contains(item.Preview, "FIRST-CYCLE-TEXT") {
					t.Errorf("cycle %d shipped the previous cycle's capture as current", snapshots)
				}
			}
		}
	}
	if snapshots < 2 {
		t.Fatalf("only %d snapshots; the assertion never ran", snapshots)
	}
}

// TestServeSnapshotThenEvents pins the stream shape: a snapshot whenever the
// expensive inventory runs, deltas in between. A viewer relies on the periodic
// snapshot to reconverge without asking.
func TestServeSnapshotThenEvents(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	opts := baseOptions(&out, runner, time.Now)
	opts.Cycles = 3
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Millisecond, time.Millisecond, time.Millisecond
	// Long enough that only the first cycle re-runs inventory.
	opts.InventoryEvery = time.Hour

	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var snapshots int
	for _, msg := range decode(t, out.String()) {
		if msg.Kind == hostproto.KindSnapshot {
			snapshots++
		}
	}
	if snapshots != 1 {
		t.Errorf("snapshots = %d over 3 cycles with a 1h inventory interval, want 1", snapshots)
	}
}

// TestServeReportsServerRestart covers the axis the spike exercised against a
// real host: the remote tmux server going away and coming back must produce an
// explicit event, because a viewer cannot infer it from row liveness alone.
func TestServeReportsServerRestart(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	opts := baseOptions(&out, runner, time.Now)
	opts.Cycles = 3
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Millisecond, time.Millisecond, time.Millisecond
	opts.InventoryEvery = time.Hour

	var cycle int
	var mu sync.Mutex
	opts.ServerIncarnation = func() tmuxserver.Incarnation {
		mu.Lock()
		defer mu.Unlock()
		cycle++
		if cycle > 2 {
			// A different socket identity: this is what a restart looks like.
			return tmuxserver.Present(99, 1234, 4321)
		}
		return tmuxserver.Present(11, 1111, 2222)
	}

	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	var serverEvents int
	for _, msg := range decode(t, out.String()) {
		if msg.Kind == hostproto.KindEvent && msg.Event.Kind == hostproto.EventServer {
			serverEvents++
			if msg.Event.ServerIncarnation == 0 {
				t.Error("server event carries no incarnation")
			}
		}
	}
	if serverEvents != 1 {
		t.Errorf("server events = %d, want exactly 1", serverEvents)
	}
}

func TestIncarnationIDDistinguishesStates(t *testing.T) {
	unknown := incarnationID(tmuxserver.Unknown())
	absent := incarnationID(tmuxserver.Absent())
	present := incarnationID(tmuxserver.Present(11, 22, 33))
	other := incarnationID(tmuxserver.Present(11, 22, 34))

	if unknown != 0 || absent != 1 {
		t.Fatalf("reserved ids changed: unknown=%d absent=%d", unknown, absent)
	}
	if present == unknown || present == absent {
		t.Errorf("a present server collided with a reserved id: %d", present)
	}
	if present == other {
		t.Error("two distinct incarnations hashed to the same id")
	}
	if again := incarnationID(tmuxserver.Present(11, 22, 33)); again != present {
		t.Errorf("incarnation id is not stable: %d then %d", present, again)
	}
}

// TestPreviewStoreKeepsTail pins the truncation direction. The bottom of a
// pane holds the prompt and the question an agent is blocked on, which is the
// entire reason a preview cell is worth showing; truncating from the end would
// throw away the only part that matters.
func TestPreviewStoreKeepsTail(t *testing.T) {
	store := newPreviewStore(10)
	store.put("%1", "0123456789ABCDEF")
	if got := store.get("%1"); got != "6789ABCDEF" {
		t.Errorf("preview = %q, want the last 10 bytes", got)
	}
}

func TestPreviewStoreNeverSplitsARune(t *testing.T) {
	// "✳" is three bytes; a cut landing inside it must not leave a partial
	// sequence, or the JSON encoder emits a replacement character and the
	// preview shows mojibake exactly where an agent's status icon should be.
	store := newPreviewStore(4)
	store.put("%1", "ab✳cd")
	got := store.get("%1")
	if !utf8Valid(got) {
		t.Errorf("preview %q is not valid UTF-8", got)
	}
}

func utf8Valid(s string) bool {
	for _, r := range s {
		if r == '�' {
			return false
		}
	}
	return true
}

// TestDiffItemsClassifiesTransitions covers the delta encoder directly,
// including the done-to-idle decay: the lane change that agentstatus.Resolve
// produces when a completed turn outlives its TTL has to reach a viewer as an
// ordinary status event, not be swallowed as "nothing changed".
func TestDiffItemsClassifiesTransitions(t *testing.T) {
	item := func(id, lane string, live bool) hostproto.Item {
		out := hostproto.Item{ID: id, Live: live}
		if lane != "" {
			out.Agent = &hostproto.Presentation{Lane: lane}
		}
		return out
	}
	previous := map[string]hostproto.Item{
		"a": item("a", "working", true),
		"b": item("b", "done", true),
		"c": item("c", "idle", true),
	}
	current := map[string]hostproto.Item{
		"a": item("a", "blocked", true),
		"b": item("b", "idle", true), // done decayed past its TTL
		"d": item("d", "working", true),
	}

	events := diffItems(previous, current, 7)
	byID := make(map[string]hostproto.Event, len(events))
	for _, event := range events {
		byID[event.ItemID] = event
		if event.Generation != 7 {
			t.Errorf("event %s carries generation %d, want 7", event.ItemID, event.Generation)
		}
	}
	if got := byID["a"]; got.Kind != hostproto.EventStatus || got.From != "working" || got.To != "blocked" {
		t.Errorf("a: %+v", got)
	}
	if got := byID["b"]; got.Kind != hostproto.EventStatus || got.From != "done" || got.To != "idle" {
		t.Errorf("b (done decay): %+v", got)
	}
	if got := byID["c"]; got.Kind != hostproto.EventDisappear {
		t.Errorf("c: %+v", got)
	}
	if got := byID["d"]; got.Kind != hostproto.EventAppear || got.Item == nil {
		t.Errorf("d: %+v", got)
	}
	if len(events) != 4 {
		t.Errorf("events = %d, want 4", len(events))
	}
}

// TestDiffItemsIsQuietWhenNothingChanges keeps the idle stream genuinely idle.
// An unchanged host that still emits an event per row per cycle would defeat
// the whole point of a delta channel.
func TestDiffItemsIsQuietWhenNothingChanges(t *testing.T) {
	items := map[string]hostproto.Item{
		"a": {ID: "a", Live: true, Agent: &hostproto.Presentation{Lane: "idle"}},
	}
	same := map[string]hostproto.Item{
		"a": {ID: "a", Live: true, Agent: &hostproto.Presentation{Lane: "idle"}, ObservedAt: time.Now()},
	}
	if events := diffItems(items, same, 2); len(events) != 0 {
		t.Errorf("events = %v, want none for an unchanged row", events)
	}
}

func TestDiffItemsIsDeterministic(t *testing.T) {
	previous := map[string]hostproto.Item{}
	current := map[string]hostproto.Item{}
	for i := 0; i < 20; i++ {
		id := fmt.Sprintf("item-%02d", i)
		current[id] = hostproto.Item{ID: id, Agent: &hostproto.Presentation{Lane: "idle"}}
	}
	first := diffItems(previous, current, 1)
	for attempt := 0; attempt < 20; attempt++ {
		again := diffItems(previous, current, 1)
		for i := range first {
			if first[i].ItemID != again[i].ItemID {
				t.Fatalf("event order is not deterministic: %s vs %s", first[i].ItemID, again[i].ItemID)
			}
		}
	}
}

// TestPollIntervalMatchesOverview pins the cadence rule to the Overview's,
// including the subtlety that idle and done earn the ready cadence rather than
// the quiet one.
func TestPollIntervalMatchesOverview(t *testing.T) {
	opts := Options{LivePoll: 5 * time.Second, ReadyPoll: 10 * time.Second, IdlePoll: 30 * time.Second}
	snapshotWith := func(lanes ...string) hostproto.Snapshot {
		project := hostproto.Project{}
		for _, lane := range lanes {
			item := hostproto.Item{}
			if lane != "" {
				item.Agent = &hostproto.Presentation{Lane: lane}
			}
			project.Items = append(project.Items, item)
		}
		return hostproto.Snapshot{Projects: []hostproto.Project{project}}
	}

	for _, tc := range []struct {
		name  string
		lanes []string
		want  time.Duration
	}{
		{"working wins", []string{"idle", "working"}, opts.LivePoll},
		{"blocked wins", []string{"idle", "blocked"}, opts.LivePoll},
		{"idle is not quiet", []string{"idle"}, opts.ReadyPoll},
		{"done is not quiet", []string{"done"}, opts.ReadyPoll},
		{"no agents at all", []string{""}, opts.IdlePoll},
	} {
		if got := pollInterval(snapshotWith(tc.lanes...), opts); got != tc.want {
			t.Errorf("%s: interval = %v, want %v", tc.name, got, tc.want)
		}
	}
}

// TestServeStopsWhenTheWriterDies is how an ephemeral serve is supposed to
// end: the viewer disconnects, the pipe breaks, the process exits. It must not
// spin forever writing into a dead pipe.
func TestServeStopsWhenTheWriterDies(t *testing.T) {
	runner := &fakeRunner{}
	opts := baseOptions(nil, runner, time.Now)
	opts.Out = brokenWriter{}
	opts.Cycles = 100

	done := make(chan error, 1)
	go func() { done <- Serve(context.Background(), opts) }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("Serve returned nil after its output pipe died")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return after its output pipe died")
	}
}

type brokenWriter struct{}

func (brokenWriter) Write([]byte) (int, error) { return 0, fmt.Errorf("broken pipe") }

func TestServeStopsOnContextCancel(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	opts := baseOptions(&out, runner, time.Now)
	opts.Cycles = 0 // run forever
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = 10*time.Millisecond, 10*time.Millisecond, 10*time.Millisecond

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() { done <- Serve(ctx, opts) }()
	time.Sleep(50 * time.Millisecond)
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Serve returned %v on cancel, want nil", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Serve did not return on context cancel")
	}
}

// TestHelloAdvertisesVerbCapabilities. A viewer chooses the argument list for a
// remote mutation from what the host said it understands, so a host that
// silently stopped announcing a flag it accepts would make every viewer fall
// back forever. This is the one assertion holding the announcement to the build.
func TestHelloAdvertisesVerbCapabilities(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	opts := baseOptions(&out, runner, time.Now)
	opts.Cycles = 1

	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	hello := decode(t, out.String())[0].Hello
	if hello == nil {
		t.Fatal("no hello")
	}
	// This build's CLI takes `create shell --agent`, so it must say so: a viewer
	// that reads false falls back to the two-step create-then-send.
	if !hello.Capabilities.Verbs.CreateShellAgent {
		t.Error("the host does not advertise `create shell --agent`, so no viewer will ever send it")
	}
}
