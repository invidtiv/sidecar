package agentintegration

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/agentlifecycle/lifecyclestore"
	"github.com/marcus/sidecar/internal/agentresolve"
)

// These cover the identity half of the deviation record: namespacing lifecycle
// records by the tmux server PID rather than by tmuxserver.Incarnation.String()
// is only a real implementation of the plan's recycled-pane rule if the lookup
// and the liveness check actually use it.

// TestARecycledPaneCannotInheritADeadRunsLane is the scenario the deviation has
// to survive, and it did not before.
//
// tmux hands out %N from a per-server counter, so after a server restart the
// first pane is %0 again. The blocked and idle freshness windows are measured
// in hours, so a stored record from the previous server — matched on pane id
// alone — would still be fresh and would hand a brand new shell the dead run's
// blocked lane.
func TestARecycledPaneCannotInheritADeadRunsLane(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	store, err := lifecyclestore.OpenPath(filepath.Join(dir, lifecyclestore.FileName))
	if err != nil {
		t.Fatal(err)
	}
	store.SetClock(func() time.Time { return now })

	// A blocked report on the OLD tmux server, for pane %0.
	if _, err := store.Append(agentlifecycle.Report{
		SchemaVersion: agentlifecycle.SchemaVersion,
		ID:            "old-1",
		Kind:          agentlifecycle.KindState,
		Identity: agentlifecycle.Identity{
			Host: testHost, ServerIncarnation: "pid=1000", PaneID: "%0",
			Provider: OpenCodeProvider, RunID: "run-dead", ProcessGeneration: testGen,
		},
		Source: OpenCodeSource, SourceVersion: OpenCodeAssetVersion,
		Sequence: 1, State: agentactivity.StateBlocked,
		ObservedAt: now, Reason: agentlifecycle.ReasonPermissionRequest,
	}); err != nil {
		t.Fatal(err)
	}

	source := NewStoreSource(dir)
	source.resolvePane = func(string) string { return "%0" }
	source.providerVersion = func(string) string { return "1.18.25" }
	source.host = func() string { return testHost }
	// The dead run's process happens to still look alive — a recycled PID — so
	// liveness alone must not be what saves us.
	source.processAlive = func(string) bool { return true }
	// tmux has restarted: same pane id, different server.
	source.serverID = func() string { return "pid=2000" }

	ev, ok := source.Evidence(agentresolve.PaneRef{Session: testSession})
	if ok && ev.Latest != nil {
		t.Fatalf("a record from a previous tmux server was matched to a recycled pane: %+v", ev.Latest.Identity)
	}

	// End to end, well inside the 8h blocked freshness window so staleness is
	// not doing the work either: the pane resolves from the screen.
	in := agentlifecycle.Input{
		Now:    now.Add(30 * time.Minute),
		Screen: agentactivity.Result{State: agentactivity.StateIdle, Evidence: "opencode.screen.idle"},
	}
	if ok {
		in.Live, in.ProcessAlive = ev.Live, ev.ProcessAlive
		in.Capability, in.Status, in.Latest = ev.Capability, ev.Status, ev.Latest
	}
	dec := agentlifecycle.Resolve(in)
	if dec.Result.State != agentactivity.StateIdle {
		t.Fatalf("recycled pane resolved to %q; it inherited the dead run", dec.Result.State)
	}
	if dec.Explanation.Authority != agentlifecycle.AuthorityScreen {
		t.Fatalf("authority = %q, want screen", dec.Explanation.Authority)
	}
}

// TestALiveIdentityIsNeverCopiedFromTheRecord is the narrower regression guard.
// If the source hands the resolver an identity taken from the record, every
// identity check compares a value with itself and the arbitration silently
// stops arbitrating.
func TestALiveIdentityIsNeverCopiedFromTheRecord(t *testing.T) {
	dir := t.TempDir()
	now := time.Date(2026, 8, 30, 12, 0, 0, 0, time.UTC)

	store, err := lifecyclestore.OpenPath(filepath.Join(dir, lifecyclestore.FileName))
	if err != nil {
		t.Fatal(err)
	}
	store.SetClock(func() time.Time { return now })
	if _, err := store.Append(agentlifecycle.Report{
		SchemaVersion: agentlifecycle.SchemaVersion,
		ID:            "r1",
		Kind:          agentlifecycle.KindState,
		Identity: agentlifecycle.Identity{
			Host: "some-other-host", ServerIncarnation: testServer, PaneID: testPane,
			Provider: OpenCodeProvider, RunID: "run-1", ProcessGeneration: testGen,
		},
		Source: OpenCodeSource, SourceVersion: OpenCodeAssetVersion,
		Sequence: 1, State: agentactivity.StateBlocked,
		ObservedAt: now, Reason: agentlifecycle.ReasonPermissionRequest,
	}); err != nil {
		t.Fatal(err)
	}

	source := NewStoreSource(dir)
	source.resolvePane = func(string) string { return testPane }
	source.processAlive = func(string) bool { return true }
	source.providerVersion = func(string) string { return "1.18.25" }
	source.serverID = func() string { return testServer }
	source.host = func() string { return testHost }

	ev, ok := source.Evidence(agentresolve.PaneRef{Session: testSession})
	if !ok {
		t.Fatal("no evidence returned")
	}
	if ev.Live.Host != testHost {
		t.Fatalf("live host = %q; it was copied from the record instead of observed", ev.Live.Host)
	}

	// The record claims another host, so arbitration must refuse it.
	dec := agentlifecycle.Resolve(agentlifecycle.Input{
		Now: now, Live: ev.Live, ProcessAlive: ev.ProcessAlive,
		Capability: ev.Capability, Status: ev.Status,
		ProviderInTestedRange: ev.ProviderInTestedRange, Latest: ev.Latest,
		Screen: agentactivity.Result{State: agentactivity.StateIdle},
	})
	if dec.Explanation.FallbackReason != agentlifecycle.ReasonHostMismatch {
		t.Fatalf("fallback reason = %q, want host_mismatch", dec.Explanation.FallbackReason)
	}
}

// TestGenerationLivenessHonoursTheStartTime covers the other half: the start=
// component is recorded precisely to disambiguate PID reuse, and checking only
// the pid throws that away.
func TestGenerationLivenessHonoursTheStartTime(t *testing.T) {
	self := os.Getpid()
	start := processStartToken(self)
	if start == "" {
		t.Skip("ps did not report a start time; the recycled-pid guard cannot be exercised here")
	}

	if !generationAlive("pid=" + strconv.Itoa(self) + ",start=" + start) {
		t.Fatal("this process reported as dead")
	}
	// Same pid, a start time that is not this process's: a recycled PID.
	if generationAlive("pid=" + strconv.Itoa(self) + ",start=Mon-Jan-1-00-00-00-2001") {
		t.Fatal("a recycled pid with a different start time reported as the same live process")
	}
	// A pid that cannot be running.
	if generationAlive("pid=999999,start=whenever") {
		t.Fatal("an impossible pid reported as alive")
	}
	// An unparseable generation cannot be shown to be dead, and treating it as
	// dead would silently disable a working integration.
	if !generationAlive("something-else") {
		t.Fatal("an unparseable generation was treated as dead")
	}
}
