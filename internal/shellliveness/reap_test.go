package shellliveness

import (
	"fmt"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/tmuxserver"
)

// The guards live here now, so this is where they are pinned. Each surface's
// own tests cover its binding — how it builds an observation and what it does
// with a plan — but the rules themselves must not need two suites to stay true.

const (
	reapNamespace = "/tmp/tmux-501/default"
	reapSession   = "sidecar-sh-project-1"
)

func reapShellRecord() Shell {
	return Shell{
		ProjectKey:  "/tmp/project",
		ProjectRoot: "/tmp/project",
		TmuxName:    reapSession,
		Namespace:   reapNamespace,
	}
}

func observation(panes ...string) ReapObservation {
	return ReapObservation{
		Server:    tmuxserver.Present(1, 2, 3),
		Namespace: reapNamespace,
		Panes:     panes,
		Shells:    []Shell{reapShellRecord()},
		Now:       time.Now(),
	}
}

// seenAliveTracker is a tracker that has watched the shell run for one cycle,
// which is the positive liveness every later absence is judged against.
func seenAliveTracker(t *testing.T) *Tracker {
	t.Helper()
	tracker := NewTracker()
	plan := PlanReap(tracker, observation(reapSession))
	if len(plan.Probes) != 0 {
		t.Fatalf("a listed shell was probed: %+v", plan)
	}
	if !tracker.SeenAlive(reapSession) {
		t.Fatal("precondition: a listed shell was not recorded alive")
	}
	return tracker
}

func TestPlanReapProbesAShellThatLeftTheListing(t *testing.T) {
	tracker := seenAliveTracker(t)
	plan := PlanReap(tracker, observation("unrelated-session"))
	if len(plan.Probes) != 1 || plan.Probes[0].TmuxName != reapSession {
		t.Fatalf("probes = %+v, want exactly the missing shell", plan.Probes)
	}
	if plan.Skipped != "" {
		t.Errorf("a usable listing was skipped: %q", plan.Skipped)
	}
	if plan.Probes[0].Server.IsUnknown() {
		t.Error("the probe carries no server incarnation, so nothing can fence it")
	}
}

// The td-8d18de guard. `tmux kill-server` does not unlink its socket, so a dead
// server and a live one with no sessions are indistinguishable by identity; the
// collector reports both as zero panes and no error.
func TestPlanReapRefusesAnEmptyListing(t *testing.T) {
	tracker := seenAliveTracker(t)
	plan := PlanReap(tracker, observation())
	if len(plan.Probes) != 0 {
		t.Fatalf("an empty listing produced probes: %+v", plan.Probes)
	}
	if plan.Skipped == "" {
		t.Error("an empty listing was skipped without saying so")
	}
}

// A listing that failed is not evidence about anything, and it fails for every
// project at once.
func TestPlanReapRefusesAFailedListing(t *testing.T) {
	tracker := seenAliveTracker(t)
	obs := observation("unrelated-session")
	obs.ListingFailed = true
	if plan := PlanReap(tracker, obs); len(plan.Probes) != 0 {
		t.Fatalf("a failed listing produced probes: %+v", plan.Probes)
	}
}

// A shell on another tmux server is invisible to this listing, so its absence
// from it says nothing at all.
func TestPlanReapIgnoresAForeignNamespace(t *testing.T) {
	tracker := seenAliveTracker(t)
	obs := observation("unrelated-session")
	obs.Shells[0].Namespace = "/tmp/some-other-socket/default"
	if plan := PlanReap(tracker, obs); len(plan.Probes) != 0 {
		t.Fatalf("a foreign-namespace shell was probed: %+v", plan.Probes)
	}
}

// A manifest entry nothing ever saw running is what survives a reboot. The
// offline-shell recreate path owns it, not auto-close.
func TestPlanReapLeavesAShellItNeverSawAlive(t *testing.T) {
	if plan := PlanReap(NewTracker(), observation("unrelated-session")); len(plan.Probes) != 0 {
		t.Fatalf("a cold record was probed: %+v", plan.Probes)
	}
}

// The server-transition fence. A surface running outside tmux survives a
// restart and must observe it live: every prior sighting is cleared, so a
// listing that simply does not contain the old shells is not a mass reap.
func TestPlanReapRefusesAcrossAServerRestart(t *testing.T) {
	tracker := seenAliveTracker(t)
	obs := observation("unrelated-session")
	obs.Server = tmuxserver.Present(9, 10, 11)
	if plan := PlanReap(tracker, obs); len(plan.Probes) != 0 {
		t.Fatalf("a server restart produced probes: %+v", plan.Probes)
	}
	if tracker.SeenAlive(reapSession) {
		t.Error("the transition did not clear the prior sighting")
	}
}

// PlanReap must record the server even on a pass it refuses, or the reset fires
// only on the next listing that happens to be usable.
func TestPlanReapObservesTheServerBeforeItsGuards(t *testing.T) {
	tracker := seenAliveTracker(t)
	obs := observation() // empty listing: guarded, but still evidence of a server
	obs.Server = tmuxserver.Present(9, 10, 11)
	PlanReap(tracker, obs)
	if !tracker.Server().Equal(tmuxserver.Present(9, 10, 11)) {
		t.Fatalf("server = %v; a skipped pass did not record the incarnation", tracker.Server())
	}
}

// Nothing but Gone closes a shell, and the fences are checked at Confirm as
// well as at plan time.
func TestConfirmReap(t *testing.T) {
	for _, tc := range []struct {
		name    string
		verdict Verdict
		mutate  func(*ReapProbe)
		server  tmuxserver.Incarnation
		want    bool
	}{
		{"gone closes", Gone, nil, tmuxserver.Present(1, 2, 3), true},
		{"unknown never closes", Unknown, nil, tmuxserver.Present(1, 2, 3), false},
		{"alive never closes", Alive, nil, tmuxserver.Present(1, 2, 3), false},
		{"a previous name-life is refused", Gone, func(p *ReapProbe) { p.Incarnation += 7 }, tmuxserver.Present(1, 2, 3), false},
		{"a previous server is refused", Gone, nil, tmuxserver.Present(9, 10, 11), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tracker := seenAliveTracker(t)
			plan := PlanReap(tracker, observation("unrelated-session"))
			if len(plan.Probes) != 1 {
				t.Fatalf("precondition: probes = %+v", plan.Probes)
			}
			probe := plan.Probes[0]
			if tc.mutate != nil {
				tc.mutate(&probe)
			}
			if got := ConfirmReap(tracker, tc.server, probe, tc.verdict); got != tc.want {
				t.Errorf("ConfirmReap = %v, want %v", got, tc.want)
			}
		})
	}
}

// The verdict was true when taken and false by the time it was applied, because
// the user brought the session back in between. The re-probe at the point of
// the write is what notices; without it the identity of a running shell would
// be deleted.
func TestReapShellRefusesAResurrectedSession(t *testing.T) {
	var forgotten []string
	resurrected, err := ReapShell(
		func(string) Verdict { return Alive },
		func(_, session, _ string, _ time.Time) error {
			forgotten = append(forgotten, session)
			return nil
		},
		ReapProbe{Shell: reapShellRecord()},
	)
	if err != nil {
		t.Fatalf("ReapShell: %v", err)
	}
	if !resurrected {
		t.Error("a session that came back was not reported resurrected")
	}
	if len(forgotten) != 0 {
		t.Errorf("the manifest entry of a live shell was deleted: %v", forgotten)
	}
}

func TestReapShellWritesTheTombstoneWithTheObservedIdentity(t *testing.T) {
	record := reapShellRecord()
	record.CreatedAt = time.Unix(1700000000, 0)

	var gotRoot, gotSession, gotNamespace string
	var gotObserved time.Time
	resurrected, err := ReapShell(
		func(string) Verdict { return Gone },
		func(root, session, namespace string, observedAt time.Time) error {
			gotRoot, gotSession, gotNamespace, gotObserved = root, session, namespace, observedAt
			return nil
		},
		ReapProbe{Shell: record},
	)
	if err != nil || resurrected {
		t.Fatalf("ReapShell: resurrected=%v err=%v", resurrected, err)
	}
	// The conditional writer needs all four: without CreatedAt it would delete
	// a replacement record written under a reused name.
	if gotRoot != record.ProjectRoot || gotSession != record.TmuxName ||
		gotNamespace != record.Namespace || !gotObserved.Equal(record.CreatedAt) {
		t.Errorf("forget(%q, %q, %q, %v) does not describe the observed record",
			gotRoot, gotSession, gotNamespace, gotObserved)
	}
}

func TestReapShellReportsAFailedWrite(t *testing.T) {
	_, err := ReapShell(
		func(string) Verdict { return Gone },
		func(string, string, string, time.Time) error { return fmt.Errorf("manifest locked") },
		ReapProbe{Shell: reapShellRecord()},
	)
	if err == nil {
		t.Fatal("a failed tombstone write was reported as success")
	}
}
