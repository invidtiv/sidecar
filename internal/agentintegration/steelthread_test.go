package agentintegration

import (
	"fmt"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/agentlifecycle/lifecyclestore"
	"github.com/marcus/sidecar/internal/agentresolve"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/notify"
)

// The steel thread, end to end, in one place:
//
//	provider event -> handler -> lifecycle report -> JSONL store
//	              -> StoreSource -> agentresolve -> agentactivity.Tracker
//	              -> agentstatus -> notify.LaneTracker -> notification
//
// The only link not exercised here is the subprocess boundary between the
// bundled asset and `sidecar agent report`, which the CLI tests cover
// separately. Everything from the store inwards is the real code the
// application runs.

const (
	testPane    = "%7"
	testServer  = "pid=4242"
	testSession = "sidecar-sh-steel"
	testGen     = "pid=31337,start=Sat-Aug-30-12-00-00-2026"
	testHost    = "host-a"
)

// steelRig wires the real store, source, resolver, and trackers together.
type steelRig struct {
	t       *testing.T
	store   *lifecyclestore.JSONL
	source  *StoreSource
	tracker agentactivity.Tracker
	lanes   *notify.LaneTracker
	now     time.Time
	seq     uint64
	posted  []notify.Notification
}

func newSteelRig(t *testing.T) *steelRig {
	t.Helper()
	dir := t.TempDir()
	store, err := lifecyclestore.OpenPath(filepath.Join(dir, lifecyclestore.FileName))
	if err != nil {
		t.Fatal(err)
	}
	rig := &steelRig{
		t:     t,
		store: store,
		now:   time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC),
		lanes: &notify.LaneTracker{},
	}
	store.SetClock(func() time.Time { return rig.now })

	rig.source = NewStoreSource(dir)
	// The live context is stated rather than observed: this test is about the
	// evidence path, not about tmux or ps. Every one of these is a value the
	// source would otherwise read from the machine, and the resolver compares
	// stored records against them.
	rig.source.resolvePane = func(string) string { return testPane }
	rig.source.processAlive = func(string) bool { return true }
	rig.source.providerVersion = func(string) string { return "1.18.25" }
	rig.source.serverID = func() string { return testServer }
	rig.source.host = func() string { return testHost }
	return rig
}

// emit runs one provider event through the handler and stores whatever reports
// it produced, exactly as the bundled asset would.
func (r *steelRig) emit(h *OpenCodeHandler, ev OpenCodeEvent) {
	r.t.Helper()
	for _, action := range h.Handle(ev) {
		r.seq++
		rec := agentlifecycle.Report{
			SchemaVersion: agentlifecycle.SchemaVersion,
			ID:            fmt.Sprintf("rpt-%d", r.seq),
			Kind:          action.Kind,
			Identity: agentlifecycle.Identity{
				Host:              testHost,
				ServerIncarnation: testServer,
				PaneID:            testPane,
				Provider:          OpenCodeProvider,
				RunID:             "run-1",
				ProcessGeneration: testGen,
			},
			Source:        OpenCodeSource,
			SourceVersion: OpenCodeAssetVersion,
			Sequence:      r.seq,
			State:         action.State,
			Outcome:       action.Outcome,
			ObservedAt:    r.now,
			Reason:        action.Reason,
		}
		if _, err := r.store.Append(rec); err != nil {
			r.t.Fatalf("storing %s: %v", action.Kind, err)
		}
	}
}

// poll runs one surface refresh: resolve, advance the tracker, and feed the
// notification lane tracker, exactly as a polling surface does.
func (r *steelRig) poll(screen agentactivity.Result) agentlifecycle.Explanation {
	r.t.Helper()
	// Everything except the screen comes from the real StoreSource: the latest
	// record, the capability looked up from the shipped registry, the derived
	// integration status, the version-range check, and — the part that matters
	// most — the live identity the resolver checks the record against. Only the
	// screen half is injected, so a test can state a disagreement.
	//
	// This mirrors agentresolve.Resolve's body exactly. Supplying capability or
	// status from the test instead would hide precisely the class of defect
	// that shipped last time, where the source handed the resolver an identity
	// copied from the record and every check compared a value with itself.
	in := agentlifecycle.Input{Now: r.now, Screen: screen}
	if ev, ok := r.source.Evidence(agentresolve.PaneRef{Session: testSession}); ok {
		in.Live = ev.Live
		in.ProcessAlive = ev.ProcessAlive
		in.Capability = ev.Capability
		in.Status = ev.Status
		in.ProviderInTestedRange = ev.ProviderInTestedRange
		in.Latest = ev.Latest
		in.StoreUnavailable = ev.StoreUnavailable
		in.InvalidReports = ev.InvalidReports
	}
	dec := agentlifecycle.Resolve(in)

	r.tracker.Apply(dec.Result, r.now)
	presentation := agentstatus.Resolve(agentstatus.Input{
		Activity:          r.tracker,
		ProviderSupported: true,
		Now:               r.now,
		CapturedAt:        r.now,
	})
	events := r.lanes.Observe([]notify.LaneObservation{{
		Key:          testSession,
		Label:        "Steel",
		Context:      "sidecar",
		Provider:     OpenCodeProvider,
		Presentation: presentation,
	}}, r.now)
	r.posted = append(r.posted, events.Post...)
	return dec.Explanation
}

func (r *steelRig) advance(d time.Duration) { r.now = r.now.Add(d) }

// blankScreen is what screen detection produces when it has no opinion, which
// is the honest baseline for a lane walk driven by native provider events.
var blankScreen = agentactivity.Result{State: agentactivity.StateUnknown}

// TestNativeEventsWalkTheLanesAndNotifyOnce is the Phase B lane walk: an agent
// reaches working, blocked, working, then idle purely from provider events, and
// the existing notification path produces the needs-input and finished records
// exactly once each.
func TestNativeEventsWalkTheLanesAndNotifyOnce(t *testing.T) {
	rig := newSteelRig(t)
	var h OpenCodeHandler

	// Baseline. The lane tracker never announces a first sighting, so this is
	// what stops an agent already mid-turn from posting the moment Sidecar
	// starts watching.
	rig.emit(&h, OpenCodeEvent{Type: "session.created", SessionID: "s1"})
	rig.poll(blankScreen)

	rig.advance(5 * time.Second)
	rig.emit(&h, OpenCodeEvent{Type: "session.status", Status: `{"type":"busy"}`})
	if exp := rig.poll(blankScreen); exp.State != agentactivity.StateWorking {
		t.Fatalf("working: state = %q, authority %q", exp.State, exp.Authority)
	}

	rig.advance(5 * time.Second)
	rig.emit(&h, OpenCodeEvent{Type: "permission.asked"})
	exp := rig.poll(blankScreen)
	if exp.State != agentactivity.StateBlocked {
		t.Fatalf("blocked: state = %q", exp.State)
	}
	if exp.Authority != agentlifecycle.AuthorityLifecycle {
		t.Fatalf("blocked was not authored by the provider: %q", exp.Authority)
	}
	// Settle so the lane tracker commits and posts.
	rig.advance(5 * time.Second)
	rig.poll(blankScreen)

	rig.advance(5 * time.Second)
	rig.emit(&h, OpenCodeEvent{Type: "permission.replied"})
	if exp := rig.poll(blankScreen); exp.State != agentactivity.StateWorking {
		t.Fatalf("resumed working: state = %q", exp.State)
	}
	rig.advance(5 * time.Second)
	rig.poll(blankScreen)

	rig.advance(5 * time.Second)
	rig.emit(&h, OpenCodeEvent{Type: "session.status", Status: `{"type":"idle"}`})
	if exp := rig.poll(blankScreen); exp.State != agentactivity.StateIdle {
		t.Fatalf("idle: state = %q", exp.State)
	}
	rig.advance(5 * time.Second)
	rig.poll(blankScreen)

	// Extra polls with nothing new must not produce anything further. This is
	// the "exactly once" half of the claim.
	for i := 0; i < 3; i++ {
		rig.advance(5 * time.Second)
		rig.poll(blankScreen)
	}

	var needsInput, finished int
	for _, n := range rig.posted {
		if n.Transition == nil {
			continue
		}
		switch n.Transition.Class {
		case notify.TransitionWaiting:
			needsInput++
		case notify.TransitionDone:
			finished++
		}
	}
	if needsInput != 1 {
		t.Fatalf("needs-input posted %d times, want 1 (%+v)", needsInput, kinds(rig.posted))
	}
	if finished != 1 {
		t.Fatalf("finished posted %d times, want 1 (%+v)", finished, kinds(rig.posted))
	}
}

// TestFreshFullAuthorityBeatsAContradictingScreen is the arbitration property
// the whole initiative exists for: the provider's own account of itself wins
// over what the terminal looks like.
func TestFreshFullAuthorityBeatsAContradictingScreen(t *testing.T) {
	rig := newSteelRig(t)
	var h OpenCodeHandler

	rig.emit(&h, OpenCodeEvent{Type: "session.status", Status: `{"type":"busy"}`})
	rig.emit(&h, OpenCodeEvent{Type: "permission.asked"})

	// The screen is confidently and positively wrong.
	screen := agentactivity.Result{
		State:          agentactivity.StateWorking,
		Evidence:       "opencode.screen.progress-working",
		VisibleWorking: true,
	}
	exp := rig.poll(screen)

	if exp.State != agentactivity.StateBlocked {
		t.Fatalf("state = %q, want blocked", exp.State)
	}
	if exp.Authority != agentlifecycle.AuthorityLifecycle {
		t.Fatalf("authority = %q", exp.Authority)
	}
	if exp.Tier != agentlifecycle.TierFull {
		t.Fatalf("tier = %q, want full", exp.Tier)
	}
	// The screen's disagreement is recorded rather than discarded, which is
	// what makes this diagnosable when the provider is the one that is wrong.
	if exp.ScreenState != agentactivity.StateWorking {
		t.Fatalf("the screen's opinion was lost: %q", exp.ScreenState)
	}
}

// TestSimultaneousAgreementProducesOneTransition covers the duplicate case the
// plan calls out: a hook-to-idle and a screen-to-idle arriving together must be
// one transition, not two.
func TestSimultaneousAgreementProducesOneTransition(t *testing.T) {
	rig := newSteelRig(t)
	var h OpenCodeHandler

	rig.emit(&h, OpenCodeEvent{Type: "session.status", Status: `{"type":"busy"}`})
	rig.poll(agentactivity.Result{State: agentactivity.StateWorking, VisibleWorking: true})
	rig.advance(5 * time.Second)
	rig.poll(agentactivity.Result{State: agentactivity.StateWorking, VisibleWorking: true})

	rig.advance(5 * time.Second)
	rig.emit(&h, OpenCodeEvent{Type: "session.status", Status: `{"type":"idle"}`})

	// Both kinds of evidence say idle at the same moment.
	agreeing := agentactivity.Result{State: agentactivity.StateIdle, VisibleIdle: true, Evidence: "opencode.screen.idle"}
	rig.poll(agreeing)
	rig.advance(5 * time.Second)
	rig.poll(agreeing)
	rig.advance(5 * time.Second)
	rig.poll(agreeing)

	var finished int
	for _, n := range rig.posted {
		if n.Transition != nil && n.Transition.Class == notify.TransitionDone {
			finished++
		}
	}
	if finished != 1 {
		t.Fatalf("agreement produced %d finished notifications, want 1 (%v)", finished, kinds(rig.posted))
	}
}

// TestAnOldRunCannotReplayAfterRestart is the restart-safety property. The
// store refuses a prior run's report outright, so a Sidecar that restarts and
// refolds the log cannot hand a dead run authority over a live pane.
func TestAnOldRunCannotReplayAfterRestart(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lifecyclestore.FileName)
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	store, err := lifecyclestore.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	store.SetClock(func() time.Time { return now })

	record := func(s *lifecyclestore.JSONL, run string, seq uint64, state agentactivity.State) error {
		_, err := s.Append(agentlifecycle.Report{
			SchemaVersion: agentlifecycle.SchemaVersion,
			ID:            fmt.Sprintf("%s-%d", run, seq),
			Kind:          agentlifecycle.KindState,
			Identity: agentlifecycle.Identity{
				Host: testHost, ServerIncarnation: testServer, PaneID: testPane,
				Provider: OpenCodeProvider, RunID: run, ProcessGeneration: "pid=1,start=x",
			},
			Source: OpenCodeSource, SourceVersion: OpenCodeAssetVersion,
			Sequence: seq, State: state, ObservedAt: now, Reason: agentlifecycle.ReasonTurnStart,
		})
		return err
	}

	if err := record(store, "run-old", 1, agentactivity.StateBlocked); err != nil {
		t.Fatal(err)
	}
	if err := record(store, "run-new", 1, agentactivity.StateWorking); err != nil {
		t.Fatal(err)
	}

	// Sidecar restarts: a fresh store over the same file.
	reopened, err := lifecyclestore.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	reopened.SetClock(func() time.Time { return now })

	// The dead run speaks again.
	if err := record(reopened, "run-old", 2, agentactivity.StateIdle); err == nil {
		t.Fatal("a prior run was allowed to report after a restart")
	}

	latest, ok := reopened.Latest(lifecyclestore.PaneKey{ServerIncarnation: testServer, PaneID: testPane})
	if !ok {
		t.Fatal("no record survived the restart")
	}
	if latest.Identity.RunID != "run-new" || latest.State != agentactivity.StateWorking {
		t.Fatalf("an old run took authority after restart: %+v", latest)
	}
}

// TestIntegrationRemovedReturnsToScreenImmediately is the last acceptance
// property: when the integration is gone or unhealthy, the pane goes straight
// back to ordinary detection with an actionable reason and no grace period.
func TestIntegrationRemovedReturnsToScreenImmediately(t *testing.T) {
	rig := newSteelRig(t)
	var h OpenCodeHandler

	rig.emit(&h, OpenCodeEvent{Type: "session.status", Status: `{"type":"busy"}`})
	rig.emit(&h, OpenCodeEvent{Type: "permission.asked"})
	if exp := rig.poll(blankScreen); exp.State != agentactivity.StateBlocked {
		t.Fatalf("setup: state = %q", exp.State)
	}

	// The provider process goes away, which is what dispose reports.
	rig.advance(time.Second)
	rig.emit(&h, OpenCodeEvent{Type: "dispose"})

	screen := agentactivity.Result{State: agentactivity.StateIdle, Evidence: "opencode.screen.idle", VisibleIdle: true}
	exp := rig.poll(screen)
	if exp.Authority != agentlifecycle.AuthorityScreen {
		t.Fatalf("authority = %q, want screen after release", exp.Authority)
	}
	if exp.FallbackReason != agentlifecycle.ReasonAuthorityRelease {
		t.Fatalf("fallback reason = %q", exp.FallbackReason)
	}
	if exp.State != agentactivity.StateIdle {
		t.Fatalf("state = %q; the pane kept its last reported lane instead of falling back", exp.State)
	}
}

func kinds(ns []notify.Notification) []string {
	var out []string
	for _, n := range ns {
		if n.Transition != nil {
			out = append(out, string(n.Transition.Class))
			continue
		}
		out = append(out, "(no transition)")
	}
	return out
}
