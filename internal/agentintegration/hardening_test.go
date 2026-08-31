package agentintegration

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/agentlifecycle/lifecyclestore"
	"github.com/marcus/sidecar/internal/agentresolve"
)

// Phase E step 2, the surface side: what a failing integration does to the lane
// a user is looking at.
//
// The store's own failure modes are pinned in lifecyclestore; these are about
// the producer of [agentresolve.Evidence] and the arbitration it feeds, where
// the visible consequence of getting it wrong is a pane that oscillates or one
// that never comes back.

// settled describes one poll's answer, reduced to the three facts a surface
// renders. Comparing these across polls is what "settled rather than flapping"
// means concretely.
type settled struct {
	State     agentactivity.State
	Authority agentlifecycle.Authority
	Reason    agentlifecycle.FallbackReason
}

func settledOf(exp agentlifecycle.Explanation) settled {
	return settled{State: exp.State, Authority: exp.Authority, Reason: exp.FallbackReason}
}

// pollRepeatedly runs n polls with no new events and requires every answer to
// be identical. A source that re-decided on each read, or one that alternated
// between a cached fold and a fresh one, would fail here.
func pollRepeatedly(t *testing.T, rig *steelRig, screen agentactivity.Result, n int) settled {
	t.Helper()
	first := settledOf(rig.poll(screen))
	for i := 1; i < n; i++ {
		rig.advance(2 * time.Second)
		if got := settledOf(rig.poll(screen)); got != first {
			t.Fatalf("poll %d answered %+v after %+v; the lane is flapping with no new evidence", i, got, first)
		}
	}
	return first
}

// TestAnUnreadableStoreWithdrawsAuthorityAndComesBack is the store-failure path
// the resolver has an arbitration row for and nothing was proved to produce.
//
// [StoreSource] is the only thing that can set StoreUnavailable, and getting it
// wrong has two distinct bad shapes: reporting a lane from the last fold it
// managed to read, which freezes a pane on a state the provider has moved on
// from; or latching the failure, which leaves the integration dead until
// Sidecar restarts even though the file came back seconds later.
func TestAnUnreadableStoreWithdrawsAuthorityAndComesBack(t *testing.T) {
	rig := newSteelRig(t)
	var h OpenCodeHandler

	rig.emit(&h, OpenCodeEvent{Type: "session.created", SessionID: "s1"})
	rig.emit(&h, OpenCodeEvent{Type: "session.status", Status: `{"type":"busy"}`})
	if exp := rig.poll(blankScreen); exp.Authority != agentlifecycle.AuthorityLifecycle {
		t.Fatalf("setup: authority = %q, want lifecycle", exp.Authority)
	}

	path := rig.store.Path()
	saved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}

	// A directory where the log should be is unreadable for every user,
	// including a test run as root, which chmod is not.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0o755); err != nil {
		t.Fatal(err)
	}

	// The screen still has an opinion, and it must be the one that wins —
	// unchanged across every poll while the store stays broken.
	screen := agentactivity.Result{State: agentactivity.StateIdle, Evidence: "opencode.idle", VisibleIdle: true}
	rig.advance(2 * time.Second)
	got := pollRepeatedly(t, rig, screen, 5)
	if got.Authority != agentlifecycle.AuthorityScreen {
		t.Fatalf("an unreadable store kept authority: %+v", got)
	}
	if got.Reason != agentlifecycle.ReasonStoreUnavailable {
		t.Fatalf("fallback reason = %q, want %q — the failure has to be nameable, not merely silent",
			got.Reason, agentlifecycle.ReasonStoreUnavailable)
	}
	if got.State != agentactivity.StateIdle {
		t.Fatalf("state = %q, want the screen's idle: the last fold was believed after the store broke", got.State)
	}

	// The file comes back. A latched failure would leave the integration dead.
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, saved, 0o644); err != nil {
		t.Fatal(err)
	}
	rig.advance(2 * time.Second)
	back := settledOf(rig.poll(blankScreen))
	if back.Authority != agentlifecycle.AuthorityLifecycle {
		t.Fatalf("the source never recovered from a transient store failure: %+v", back)
	}
	if back.State != agentactivity.StateWorking {
		t.Fatalf("recovered state = %q, want working", back.State)
	}
}

// TestAnIntegrationThatKeepsFailingSettlesInsteadOfFlapping is the repeated
// provider failure case. An agent whose every turn ends in a provider error is
// the situation where a wrong arbitration is most visible: each failed run must
// end once, hand the pane back to screen detection, and then stay put.
//
// The specific hazard is a terminal report that is re-announced on every poll,
// or a run rotation that reanimates the previous run's lane — either would show
// a user a pane oscillating between working and its fallback for as long as the
// agent kept failing.
func TestAnIntegrationThatKeepsFailingSettlesInsteadOfFlapping(t *testing.T) {
	rig := newSteelRig(t)

	const runs = 3
	for run := 1; run <= runs; run++ {
		// Each failed turn is a fresh provider generation in the same pane,
		// which is what relaunching an agent after a failure actually produces.
		runID := fmt.Sprintf("run-%d", run)
		gen := fmt.Sprintf("pid=%d,start=Sat-Aug-30-12-00-00-2026", 1000+run)

		var h OpenCodeHandler
		emitAs(t, rig, &h, runID, gen, OpenCodeEvent{Type: "session.created", SessionID: fmt.Sprintf("s%d", run)})
		emitAs(t, rig, &h, runID, gen, OpenCodeEvent{Type: "session.status", Status: `{"type":"busy"}`})

		rig.advance(2 * time.Second)
		working := pollRepeatedly(t, rig, blankScreen, 3)
		if working.State != agentactivity.StateWorking || working.Authority != agentlifecycle.AuthorityLifecycle {
			t.Fatalf("run %d never reached lifecycle-authored working: %+v", run, working)
		}

		emitAs(t, rig, &h, runID, gen, OpenCodeEvent{
			Type:      "session.error",
			ErrorName: "ProviderAuthError",
		})
		// OpenCode's trailing status idle after every error, which the handler's
		// latch is there to absorb.
		emitAs(t, rig, &h, runID, gen, OpenCodeEvent{Type: "session.status", Status: `{"type":"idle"}`})

		rig.advance(2 * time.Second)
		after := pollRepeatedly(t, rig, blankScreen, 5)
		if after.Authority != agentlifecycle.AuthorityScreen {
			t.Fatalf("run %d kept authority after its terminal outcome: %+v", run, after)
		}
		if after.Reason != agentlifecycle.ReasonRunEnded {
			t.Fatalf("run %d fell back for %q, want %q", run, after.Reason, agentlifecycle.ReasonRunEnded)
		}
	}

	// Exactly one terminal record per run, and each one failed. More than one
	// would mean a run was ended twice; fewer would mean a failure was lost.
	records := rig.store.List(lifecyclestore.PaneKey{ServerIncarnation: testServer, PaneID: testPane})
	ends := map[string]int{}
	for _, r := range records {
		if r.Kind == agentlifecycle.KindEnd {
			if r.Outcome != agentlifecycle.OutcomeFailed {
				t.Fatalf("run %s ended as %q, want failed", r.Identity.RunID, r.Outcome)
			}
			ends[r.Identity.RunID]++
		}
	}
	if len(ends) != runs {
		t.Fatalf("%d runs ended, want %d (%v)", len(ends), runs, ends)
	}
	for run, n := range ends {
		if n != 1 {
			t.Fatalf("run %s produced %d terminal records, want 1", run, n)
		}
	}
}

// emitAs is [steelRig.emit] with the run and process generation named, so a
// test can walk a pane through successive agent runs rather than one.
func emitAs(t *testing.T, r *steelRig, h *OpenCodeHandler, runID, gen string, ev OpenCodeEvent) {
	t.Helper()
	for _, action := range h.Handle(ev) {
		r.seq++
		rec := agentlifecycle.Report{
			SchemaVersion: agentlifecycle.SchemaVersion,
			ID:            fmt.Sprintf("rpt-%s-%d", runID, r.seq),
			Kind:          action.Kind,
			Identity: agentlifecycle.Identity{
				Host:              testHost,
				ServerIncarnation: testServer,
				PaneID:            testPane,
				Provider:          OpenCodeProvider,
				RunID:             runID,
				ProcessGeneration: gen,
			},
			Source:        OpenCodeSource,
			SourceVersion: OpenCodeAssetVersion,
			ObservedAt:    r.now,
			State:         action.State,
			Outcome:       action.Outcome,
			Reason:        action.Reason,
		}
		if _, _, err := r.store.AppendNext(rec); err != nil {
			t.Fatalf("storing %s for %s: %v", action.Kind, runID, err)
		}
	}
}

// TestTheSourceRereadsOnlyWhenTheLogChanges backs the claim the polling budget
// rests on: every surface calls this for every pane on its own cadence, and
// re-reading and re-parsing the whole log each time would put a file read per
// pane per poll on a path that runs continuously.
//
// It is falsifiable rather than decorative: the file's bytes are replaced with
// garbage while its size and modification time are held constant, so a source
// that honours the no-change gate keeps answering and one that re-reads finds
// nothing and reports no evidence at all.
func TestTheSourceRereadsOnlyWhenTheLogChanges(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, lifecyclestore.FileName)
	store, err := lifecyclestore.OpenPath(path)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)
	store.SetClock(func() time.Time { return now })
	if _, err := store.Append(agentlifecycle.Report{
		SchemaVersion: agentlifecycle.SchemaVersion,
		ID:            "seed",
		Kind:          agentlifecycle.KindState,
		Identity: agentlifecycle.Identity{
			Host: testHost, ServerIncarnation: testServer, PaneID: testPane,
			Provider: OpenCodeProvider, RunID: "run-1", ProcessGeneration: testGen,
		},
		Source: OpenCodeSource, SourceVersion: OpenCodeAssetVersion,
		Sequence: 1, State: agentactivity.StateWorking, ObservedAt: now,
		Reason: agentlifecycle.ReasonTurnStart,
	}); err != nil {
		t.Fatal(err)
	}

	src := NewStoreSource(dir)
	src.resolvePane = func(string) string { return testPane }
	src.processAlive = func(string) bool { return true }
	src.providerVersion = func(string) string { return "1.18.25" }
	src.serverID = func() string { return testServer }
	src.host = func() string { return testHost }

	if _, ok := src.Evidence(agentresolve.PaneRef{Session: testSession}); !ok {
		t.Fatal("the first poll found no evidence")
	}

	// Same length, same modification time, different bytes. Nothing a
	// legitimate writer does looks like this; it exists purely to make the
	// difference between reading and not reading observable.
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	garbage := make([]byte, len(data))
	for i := range garbage {
		garbage[i] = 'x'
	}
	garbage[len(garbage)-1] = '\n'
	if err := os.WriteFile(path, garbage, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime(), info.ModTime()); err != nil {
		t.Fatal(err)
	}

	for i := 0; i < 20; i++ {
		ev, ok := src.Evidence(agentresolve.PaneRef{Session: testSession})
		if !ok {
			t.Fatalf("poll %d re-read an unchanged log; the no-change gate is gone", i)
		}
		if ev.Latest == nil || ev.Latest.Sequence != 1 {
			t.Fatalf("poll %d answered from a re-read fold: %+v", i, ev.Latest)
		}
	}

	// And a real change is still noticed, so the gate is a cache rather than a
	// latch.
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chtimes(path, info.ModTime().Add(time.Second), info.ModTime().Add(time.Second)); err != nil {
		t.Fatal(err)
	}
	if _, ok := src.Evidence(agentresolve.PaneRef{Session: testSession}); !ok {
		t.Fatal("the source stopped answering after the log legitimately changed")
	}
}
