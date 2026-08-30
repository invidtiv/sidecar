package hostserve

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The screens the collector resolves into lanes. They are the same fixtures
// agentactivity is tested against, so a rule change there fails here too
// rather than silently making this test assert about nothing.
const (
	workingScreen = "esc to interrupt\n✻ Thinking…"
	blockedScreen = "Which option would you like to go with?\n❯ 1. Alpha\n  2. Beta\nEnter to select · ↑/↓ to navigate · Esc to cancel"
)

// stepClock advances a fixed amount on every read, so a whole serve loop runs
// against a deterministic timeline with no sleeping and no wall clock.
func stepClock(start time.Time, step time.Duration) func() time.Time {
	current := start.Add(-step)
	return func() time.Time {
		current = current.Add(step)
		return current
	}
}

func notifyEvents(messages []hostproto.Message) []hostproto.NotifyEvent {
	var out []hostproto.NotifyEvent
	for _, msg := range messages {
		if msg.Kind == hostproto.KindNotify && msg.Notify != nil {
			out = append(out, *msg.Notify)
		}
	}
	return out
}

// runBlockedTransition drives a full serve loop over a live project whose
// agent works for two cycles and then blocks. It returns everything the host
// wrote.
func runBlockedTransition(t *testing.T, project Project, runner *fakeRunner, start time.Time) []hostproto.Message {
	t.Helper()
	var out strings.Builder

	opts := baseOptions(&out, runner, stepClock(start, 200*time.Millisecond))
	opts.Projects = []Project{project}
	opts.Cycles = 4
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Millisecond, time.Millisecond, time.Millisecond
	// One observation is enough to settle a lane here; the debounce itself is
	// notify.LaneTracker's and is tested there.
	opts.NotifyDebounce = time.Nanosecond

	cycle := 0
	opts.Capture = func(string, int) (string, tty.PaneState, error) {
		cycle++
		if cycle <= 2 {
			return workingScreen, tty.PaneState{}, nil
		}
		return blockedScreen, tty.PaneState{}, nil
	}
	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	return decode(t, out.String())
}

// A live transition — not a snapshot, not a generation — is what produces an
// event, and it carries the semantic fields the viewer files a notification
// from.
func TestServeForwardsALiveTransition(t *testing.T) {
	runner := &fakeRunner{}
	project := liveProject(t, runner)
	events := notifyEvents(runBlockedTransition(t, project, runner, time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)))
	if len(events) != 1 {
		t.Fatalf("notify events = %d, want 1: %+v", len(events), events)
	}
	event := events[0]
	if event.Class != hostproto.NotifyWaiting {
		t.Errorf("class = %q, want waiting", event.Class)
	}
	if event.Source != string(notify.SourceWaiting) || event.Severity != string(notify.SeverityWarning) {
		t.Errorf("source/severity = %q/%q", event.Source, event.Severity)
	}
	if !strings.Contains(event.Title, "needs input") {
		t.Errorf("title = %q", event.Title)
	}
	if !event.Sticky {
		t.Error("a waiting event is not sticky; the viewer would expire the wait before it was answered")
	}
	if event.Origin.Session != "spike-claude" || event.Origin.ItemID == "" {
		t.Errorf("origin = %+v", event.Origin)
	}
	if event.OccurredAt.IsZero() {
		t.Error("no occurrence time; the viewer cannot apply its live-event window")
	}
	if want := hostproto.NotifyKey(event.Origin, event.Class, event.OccurredAt); event.Key != want {
		t.Errorf("key = %q, want %q", event.Key, want)
	}
}

// Two viewers of one host mean two `sidecar host serve` processes, started at
// different moments and polling on their own clocks. If the key moved with the
// observer, each viewer would store its own copy of one transition — and on a
// machine running two Sidecars, so would each of those.
//
// The agreement is quantized, not exact (see hostproto.NotifyKeyResolution): a
// transition observed either side of a bucket boundary still produces two
// keys, and the viewer's store collapses that case by logical dedupe key
// instead.
func TestTwoServeStreamsAgreeOnTheEventKey(t *testing.T) {
	runner := &fakeRunner{}
	// One project observed by two processes, which is the situation: the same
	// machine, the same workspace, two independently started serve loops.
	project := liveProject(t, runner)
	base := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	first := notifyEvents(runBlockedTransition(t, project, runner, base))
	second := notifyEvents(runBlockedTransition(t, project, runner, base.Add(2*time.Second)))
	if len(first) != 1 || len(second) != 1 {
		t.Fatalf("events = %d and %d, want 1 each", len(first), len(second))
	}
	if first[0].Key != second[0].Key {
		t.Errorf("two serve processes produced %q and %q for one transition", first[0].Key, second[0].Key)
	}
	if first[0].OccurredAt.Equal(second[0].OccurredAt) {
		t.Error("the two runs observed the same instant; the test is not proving clock independence")
	}
}

// An agent that is already waiting when a viewer attaches is state, not news.
// This is the whole of the reconnect rule: a reconnect is a new serve process,
// and a new process's tracker starts empty.
func TestServeIsSilentAboutStateItFoundOnArrival(t *testing.T) {
	var out strings.Builder
	runner := &fakeRunner{}
	project := liveProject(t, runner)

	opts := baseOptions(&out, runner, stepClock(time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC), time.Second))
	opts.Projects = []Project{project}
	opts.Cycles = 4
	opts.LivePoll, opts.ReadyPoll, opts.IdlePoll = time.Millisecond, time.Millisecond, time.Millisecond
	opts.NotifyDebounce = time.Nanosecond
	// Blocked from the first capture and never anything else: the agent was
	// already waiting before this process existed.
	opts.Capture = func(string, int) (string, tty.PaneState, error) {
		return blockedScreen, tty.PaneState{}, nil
	}
	if err := Serve(context.Background(), opts); err != nil {
		t.Fatalf("Serve: %v", err)
	}
	messages := decode(t, out.String())
	if events := notifyEvents(messages); len(events) != 0 {
		t.Errorf("arrival state produced %d notification(s): %+v", len(events), events)
	}
	// The rows themselves must still be reported, or the test would also pass
	// for a loop that observed nothing at all.
	var snapshots int
	for _, msg := range messages {
		if msg.Kind == hostproto.KindSnapshot {
			snapshots++
		}
	}
	if snapshots == 0 {
		t.Fatal("no snapshot; the silence above proves nothing")
	}
}

func blockedObservation(key, session string, at time.Time) notify.LaneObservation {
	return notify.LaneObservation{
		Key: key, Label: "Claude pane", Context: "spike/main", Provider: "claude",
		Presentation: agentstatus.Presentation{Lane: agentstatus.LaneBlocked, ChangedAt: at},
		Origin:       notify.Origin{TmuxSession: session, ProjectKey: "spike", WorkDir: "/project"},
		ProjectRoot:  "/project",
	}
}

func workingObservation(key, session string, at time.Time) notify.LaneObservation {
	o := blockedObservation(key, session, at)
	o.Presentation.Lane = agentstatus.LaneWorking
	return o
}

// Leaving the blocked lane withdraws the event that announced it. Without
// this the viewer's sticky "needs input" record outlives the wait, which is
// the failure notify.LaneTracker exists to prevent locally.
func TestNotifierWithdrawsAnAnsweredWait(t *testing.T) {
	at := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	n := newNotifier(time.Nanosecond)
	n.observe([]notify.LaneObservation{workingObservation("w1", "s1", at)}, at)
	posted := n.observe([]notify.LaneObservation{blockedObservation("w1", "s1", at)}, at.Add(time.Second))
	posted = append(posted, n.observe([]notify.LaneObservation{blockedObservation("w1", "s1", at)}, at.Add(2*time.Second))...)
	if len(posted) != 1 || posted[0].Class != hostproto.NotifyWaiting {
		t.Fatalf("posts = %+v", posted)
	}
	answered := n.observe([]notify.LaneObservation{workingObservation("w1", "s1", at)}, at.Add(3*time.Second))
	answered = append(answered, n.observe([]notify.LaneObservation{workingObservation("w1", "s1", at)}, at.Add(4*time.Second))...)
	if len(answered) != 1 {
		t.Fatalf("withdrawals = %+v", answered)
	}
	if !answered[0].IsWithdrawal() || answered[0].Withdraws != posted[0].Key {
		t.Errorf("withdrawal = %+v, want a withdrawal of %q", answered[0], posted[0].Key)
	}
}

// A workspace that vanished takes its wait with it, and the withdrawal must
// name the key the viewer actually has.
func TestNotifierWithdrawsWhenAWorkspaceDisappears(t *testing.T) {
	at := time.Date(2026, 8, 30, 9, 0, 0, 0, time.UTC)
	n := newNotifier(time.Nanosecond)
	n.observe([]notify.LaneObservation{workingObservation("w1", "s1", at)}, at)
	n.observe([]notify.LaneObservation{blockedObservation("w1", "s1", at)}, at.Add(time.Second))
	posted := n.observe([]notify.LaneObservation{blockedObservation("w1", "s1", at)}, at.Add(2*time.Second))
	if len(posted) != 1 {
		t.Fatalf("posts = %+v", posted)
	}
	gone := n.observe(nil, at.Add(3*time.Second))
	if len(gone) != 1 || gone[0].Withdraws != posted[0].Key {
		t.Errorf("disappearance produced %+v", gone)
	}
}

// A withdrawal for a wait this process never announced is a message about
// nothing, and a viewer that received one would have no record to retire.
func TestNotifierDropsAnUnknownWithdrawal(t *testing.T) {
	n := newNotifier(time.Nanosecond)
	n.keys = map[string]string{}
	events := n.observe(nil, time.Now())
	if len(events) != 0 {
		t.Errorf("events = %+v", events)
	}
}

func TestLaneObservationsSkipRowsWithoutAnAgent(t *testing.T) {
	result := workspaceinventory.ProjectResult{
		ProjectKey: "spike", ProjectName: "spike", ProjectRoot: "/project",
		Workspaces: []workspaceinventory.Workspace{
			{ID: "plain", Name: "main", Path: "/project", Kind: workspaceinventory.KindWorktree, Plain: true},
			{
				ID: "agent", Name: "Claude pane", Path: "/project", TmuxName: "spike-claude",
				Provider: "claude", Presentation: agentstatus.Presentation{Lane: agentstatus.LaneBlocked},
			},
		},
	}
	obs := laneObservations(result)
	if len(obs) != 1 || obs[0].Key != "agent" {
		t.Fatalf("observations = %+v", obs)
	}
	if obs[0].Origin.TmuxSession != "spike-claude" || obs[0].Origin.WorkDir != "/project" {
		t.Errorf("origin = %+v", obs[0].Origin)
	}
	if obs[0].Context != "spike" {
		t.Errorf("context = %q", obs[0].Context)
	}
}
