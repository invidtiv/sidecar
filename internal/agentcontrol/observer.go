package agentcontrol

import (
	"context"
	"fmt"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// Targeted lifecycle observation.
//
// M0 measured the two candidates against the same isolated tmux server: 100 ms
// bounded polling spawned 6 tmux children and burned 17.6 ms of child CPU in a
// 300 ms idle window and saw an output burst 110 ms late, while one pooled
// tty.ControlManager client produced no idle snapshots, no extra spawns, no
// measurable CPU, and saw the same burst in 42 ms. The decision recorded in the
// plan is to use the control manager for sustained prompt/wait observation.
//
// What the control stream cannot do is prove who owns the pane. Its events
// carry the screen, the pane title, and the current command; they do not carry
// pane identity, pane death, copy mode, or the tmux server incarnation. So the
// two roles are split deliberately:
//
//	the control stream is the signal — it decides *when* to look;
//	Inspect is the truth — it decides *what is so*.
//
// Every verdict this file returns is confirmed by a full Inspect against the
// pinned target immediately before it is returned, and a slow verification
// heartbeat runs regardless of signals so a pane that dies or is replaced in
// silence cannot hold a wait open. A wait therefore cannot be satisfied by a
// replacement occupant even though the cheap path never re-reads identity.

// Signal is one event-driven observation of a pinned pane, carrying only what
// the control stream can actually see.
type Signal struct {
	Screen         string
	Title          string
	CurrentCommand string
	At             time.Time
}

// Signaler is an optional Terminal capability: sustained, event-driven change
// notification for one pinned pane. A Terminal that does not implement it is
// observed by bounded polling instead, which is correct but costlier.
//
// stop must release every client, subscription, and goroutine the call made.
// It is always called exactly once, including on cancellation and timeout.
type Signaler interface {
	Signal(ctx context.Context, snap Snapshot) (signals <-chan Signal, stop func(), err error)
}

// watchOutcome is what an accept function decides about one observation.
type watchOutcome uint8

const (
	// watchContinue keeps observing.
	watchContinue watchOutcome = iota
	// watchSettled ends the watch successfully with this observation.
	watchSettled
)

// watch drives one bounded lifecycle observation of an already-pinned target.
//
// initial and state are the verified observation the caller pinned, already
// read through tracker — the state is passed rather than re-derived because
// running the detector twice over one snapshot with a differently seeded
// tracker can produce two different answers, and a caller comparing "before"
// against the watch's first reading would then be comparing two frames.
// tracker carries lifecycle history across observations so a working→idle
// transition can be read as completion. accept sees every observation in order
// and says whether the watch is done. It may also refuse outright.
//
// ctx must already carry the caller's explicit deadline. There is no implicit
// timeout anywhere in this package.
func (s Service) watch(ctx context.Context, initial Snapshot, state AgentState, tracker *agentactivity.Tracker, accept func(Snapshot, AgentState) (watchOutcome, error)) (Snapshot, AgentState, error) {
	s = s.defaults()
	pinned := initial.Target

	// The caller's own pinned observation is the first thing accept sees; a
	// standalone wait whose target is already settled must not pay for a tick.
	switch outcome, err := accept(initial, state); {
	case err != nil:
		return Snapshot{}, AgentState{}, err
	case outcome == watchSettled:
		return initial, state, nil
	}

	var signals <-chan Signal
	stop := func() {}
	if signaler, ok := s.Terminal.(Signaler); ok {
		if ch, release, err := signaler.Signal(ctx, initial); err == nil {
			signals, stop = ch, release
		}
		// A control client that cannot start is not a failure of the wait. The
		// loop below degrades to bounded polling, which is what M1's start wait
		// already uses and what the plan permits as the fallback.
	}
	defer stop()

	cadence, verifyEvery := s.Poll, time.Duration(0)
	if signals != nil {
		cadence, verifyEvery = s.Observe, s.Verify
	}
	ticker := time.NewTicker(cadence)
	defer ticker.Stop()

	lastVerified := s.Now()
	pending := false
	for {
		select {
		case <-ctx.Done():
			return Snapshot{}, AgentState{}, waitDeadline(ctx, pinned)
		case signal, ok := <-signals:
			if !ok {
				// The control client died. Keep observing by heartbeat rather
				// than failing a wait the target may still satisfy.
				signals = nil
				cadence, verifyEvery = s.Poll, 0
				ticker.Reset(cadence)
				continue
			}
			// A cheap observation cannot settle the watch on its own; it can
			// only decide that a full Inspect is now worth its two processes.
			cheap := initial
			cheap.Screen = signal.Screen
			cheap.Title = signal.Title
			cheap.CurrentCommand = signal.CurrentCommand
			cheap.ProcessIdentity = agentactivity.ResolveForegroundProcess(pinned.PanePID)
			cheap.CapturedAt = signal.At
			probe := *tracker
			if outcome, err := accept(cheap, s.Detect(cheap, &probe)); err != nil || outcome == watchSettled {
				pending = true
			}
		case <-ticker.C:
			if !pending && s.Now().Sub(lastVerified) < verifyEvery {
				continue
			}
			pending = false
			lastVerified = s.Now()
			snap, err := s.Terminal.Inspect(ctx, pinned)
			if err != nil {
				// A deadline or cancellation that lands while an observation is
				// in flight is still the caller's, not the transport's. Without
				// this, one outcome reported two different codes depending on
				// where the clock fell: agent_timeout when the select noticed
				// first, transport_failed when the tmux call did.
				if ctx.Err() != nil {
					return Snapshot{}, AgentState{}, waitDeadline(ctx, pinned)
				}
				return Snapshot{}, AgentState{}, transport(pinned, err)
			}
			if !sameOccupant(pinned, snap.Target) {
				return Snapshot{}, AgentState{}, &Error{Code: ErrReplaced, Message: "managed pane was replaced while the caller was waiting", Target: &pinned}
			}
			if snap.Dead {
				return Snapshot{}, AgentState{}, &Error{Code: ErrReplaced, Message: "managed pane died while the caller was waiting", Target: &pinned}
			}
			state := s.Detect(snap, tracker)
			outcome, acceptErr := accept(snap, state)
			if acceptErr != nil {
				return Snapshot{}, AgentState{}, acceptErr
			}
			if outcome == watchSettled {
				return snap, state, nil
			}
		}
	}
}

// waitDeadline turns a finished context into the frozen error vocabulary. A
// caller's own cancellation is not a timeout and must not be reported as one.
func waitDeadline(ctx context.Context, pinned Target) error {
	if ctx.Err() == context.DeadlineExceeded {
		return &Error{Code: ErrTimeout, Message: "timed out waiting for the agent to settle", Target: &pinned, Err: ctx.Err()}
	}
	return &Error{Code: ErrTransport, Message: fmt.Sprintf("wait ended: %v", ctx.Err()), Target: &pinned, Err: ctx.Err()}
}
