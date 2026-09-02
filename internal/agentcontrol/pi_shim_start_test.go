package agentcontrol

import (
	"context"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// piShimSnapshot is a pane running Pi installed the way Pi 0.84.3 installs: a
// `#!/usr/bin/env node` bin, so tmux reports the foreground command as `node`
// and only the resolved foreground argv names the agent. The screen carries no
// "Working..." literal, which is the whole of upstream's pi.toml, so the honest
// verdict for it is the low-evidence idle fallback.
func piShimSnapshot() Snapshot {
	snapshot := pinnedSnapshot("▌ pi\n\n> \n")
	snapshot.CurrentCommand = "node"
	snapshot.ProcessIdentity = "pi"
	snapshot.ShellReady = false
	snapshot.PaneHeight = 24
	return snapshot
}

// TestStartOnAPiShimPaneReachesAReadyVerdict is the half of Slice 3's exit gate
// that lives outside agentactivity: `sidecar agent start --kind pi` must stop
// timing out.
//
// Slice 1 measured the failure live and this is its shape. detect() calls
// agentactivity.Detect and consults nothing else — not the lifecycle store, so
// no tier promotion could ever have reached it — and Detect refused the pane
// with pi.process-mismatch because piProcess matched only the literal "pi".
// Start's loop therefore saw state.Kind == "" on every tick, never satisfied
// `state.Kind == req.Kind`, and ran to its timeout with code=timeout. The
// refusal was one step before any manifest rule, which is why the screen lane
// answered `idle` for the same capture the whole time.
//
// The test drives the real detect through the real Start loop rather than a
// fake Detect, because a fake Detect is exactly what would have kept passing
// while the gate refused every live Pi pane.
func TestStartOnAPiShimPaneReachesAReadyVerdict(t *testing.T) {
	// Three readings of the same pane a second apart, because Start does not
	// seed its tracker the way Get does: an inferred idle has to survive
	// agentactivity.IdleDebounce, and a fixture whose CapturedAt never advances
	// can never clear it.
	first, second, third := piShimSnapshot(), piShimSnapshot(), piShimSnapshot()
	second.CapturedAt = first.CapturedAt.Add(time.Second)
	third.CapturedAt = first.CapturedAt.Add(2 * time.Second)
	terminal := &sequenceTerminal{snapshots: []Snapshot{pinnedSnapshot("$ "), first, second, third}}
	svc := Service{Terminal: terminal, Poll: time.Millisecond}
	got, err := svc.Start(context.Background(), StartRequest{
		Target:  Target{Session: "s"},
		Kind:    "pi",
		Argv:    []string{"pi"},
		Timeout: 2 * time.Second,
	})
	if err != nil {
		t.Fatalf("Start(--kind pi) = %v; a live Pi pane must reach a ready verdict, not a timeout", err)
	}
	if got.Agent.Kind != "pi" || got.Agent.Status != StatusIdle || !got.Agent.InteractiveReady {
		t.Fatalf("Start(--kind pi) agent = %+v, want an interactive-ready idle Pi", got.Agent)
	}
	if got.Agent.Evidence != "pi.known-live-fallback" {
		t.Fatalf("evidence = %q, want the screen lane's fallback for a pane with no matching rule", got.Agent.Evidence)
	}
}

// The same snapshot through Get, which is what `sidecar agent list` reads. This
// is the surface that reported `evidence=pi.process-mismatch` in Slice 1.
func TestGetOnAPiShimPaneNoLongerReportsAProcessMismatch(t *testing.T) {
	snapshot := piShimSnapshot()
	got, err := (Service{Terminal: &sequenceTerminal{snapshots: []Snapshot{snapshot}}}).Get(context.Background(), snapshot.Target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Evidence == "pi.process-mismatch" {
		t.Fatalf("Get() = %+v; the gate still refuses a pane whose own argv[0] resolves to pi", got.Agent)
	}
	if got.Agent.Kind != "pi" || got.Agent.Status != StatusIdle || !got.Agent.InteractiveReady {
		t.Fatalf("Get() = %+v, want positively identified inferred idle Pi", got.Agent)
	}
}

// The other direction, at this level: a pane whose resolved identity is another
// agent must not be claimed for Pi. Start refuses it as a kind mismatch rather
// than evaluating pi.toml against somebody else's screen.
func TestStartRefusesAPaneWhoseResolvedIdentityIsAnotherAgent(t *testing.T) {
	occupied := piShimSnapshot()
	occupied.ProcessIdentity = "claude"
	occupied.Screen = "Working...\n"
	terminal := &sequenceTerminal{snapshots: []Snapshot{pinnedSnapshot("$ "), occupied}}
	_, err := (Service{Terminal: terminal, Poll: time.Millisecond}).Start(context.Background(), StartRequest{
		Target:  Target{Session: "s"},
		Kind:    "pi",
		Argv:    []string{"pi"},
		Timeout: 300 * time.Millisecond,
	})
	var typed *Error
	if !AsError(err, &typed) || typed.Code != ErrKindMismatch {
		t.Fatalf("Start(--kind pi on a claude pane) = %v, want %s", err, ErrKindMismatch)
	}
	// And the screen lane never saw it as Pi at all.
	if got := agentactivity.Detect(agentactivity.Observation{
		Agent: "pi", CurrentCommand: occupied.CurrentCommand, ProcessIdentity: occupied.ProcessIdentity, Screen: occupied.Screen,
	}); got.Evidence != "pi.process-mismatch" {
		t.Fatalf("Detect(pi on a claude pane) = %+v, want pi.process-mismatch", got)
	}
}
