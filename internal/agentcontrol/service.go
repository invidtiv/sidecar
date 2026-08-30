package agentcontrol

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentstatus"
)

// Snapshot is one terminal observation. Identity fields are re-read on every
// observation so a wait cannot be satisfied by a replacement process.
type Snapshot struct {
	Target
	Dead            bool
	CopyMode        bool
	PaneCount       int
	CurrentCommand  string
	ProcessIdentity string
	ShellReady      bool
	Title           string
	Screen          string
	CapturedAt      time.Time
}

type Terminal interface {
	Inspect(context.Context, Target) (Snapshot, error)
	Launch(context.Context, Snapshot, []string) error
}

type Detector func(Snapshot, *agentactivity.Tracker) AgentState

type Service struct {
	Terminal       Terminal
	Now            func() time.Time
	Poll           time.Duration
	ShellStableFor time.Duration
	Detect         Detector
}

func (s Service) defaults() Service {
	if s.Now == nil {
		s.Now = time.Now
	}
	if s.Poll <= 0 {
		s.Poll = 100 * time.Millisecond
	}
	if s.ShellStableFor <= 0 {
		s.ShellStableFor = 200 * time.Millisecond
	}
	if s.Detect == nil {
		s.Detect = detect
	}
	return s
}

func (s Service) Get(ctx context.Context, target Target) (Agent, error) {
	s = s.defaults()
	if s.Terminal == nil {
		return Agent{}, &Error{Code: ErrTransport, Message: "terminal adapter is unavailable"}
	}
	snap, err := s.Terminal.Inspect(ctx, target)
	if err != nil {
		return Agent{}, transport(target, err)
	}
	var tracker agentactivity.Tracker
	// Get is deliberately a one-shot passive read. Once Inspect has positively
	// identified a live provider process, seed the tracker as a process-change
	// observation so providers whose stable composer has no explicit idle
	// marker can report inferred idle without an impossible second observation.
	// This remains quiet (never "done") and does not relax provider identity.
	tracker.ResetForProcessChange(snap.CapturedAt)
	state := s.Detect(snap, &tracker)
	return Agent{Target: snap.Target, Agent: state}, nil
}

type StartRequest struct {
	Target  Target
	Kind    string
	Argv    []string
	Timeout time.Duration
}

// WaitShellReady waits for a shell that the caller has just created and may
// still be seeding with Sidecar-owned setup commands. It pins the first live
// pane/server identity and never follows a replacement. Existing-target
// callers must use Start directly: its preflight refuses a foreground command
// rather than waiting for somebody else's work to finish.
func (s Service) WaitShellReady(ctx context.Context, target Target, timeout time.Duration) (Snapshot, error) {
	s = s.defaults()
	if s.Terminal == nil {
		return Snapshot{}, &Error{Code: ErrTransport, Message: "terminal adapter is unavailable"}
	}
	if timeout <= 0 {
		return Snapshot{}, &Error{Code: ErrNotReady, Message: "a positive shell-ready timeout is required", Target: &target}
	}
	initial, err := s.Terminal.Inspect(ctx, target)
	if err != nil {
		return Snapshot{}, transport(target, err)
	}
	if initial.PaneCount != 1 || initial.Dead || initial.CopyMode || initial.PaneID == "" || initial.PanePID <= 0 || initial.ServerIncarnation == "" {
		return Snapshot{}, shellReady(initial)
	}

	pinned := initial.Target
	readySince := time.Time{}
	if shellReady(initial) == nil {
		readySince = s.Now()
	}
	waitCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	ticker := time.NewTicker(s.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			code := ErrTransport
			if waitCtx.Err() == context.DeadlineExceeded {
				code = ErrTimeout
			}
			return Snapshot{}, &Error{Code: code, Message: fmt.Sprintf("managed shell did not become ready: %v", waitCtx.Err()), Target: &pinned, Err: waitCtx.Err()}
		case <-ticker.C:
			next, inspectErr := s.Terminal.Inspect(waitCtx, pinned)
			if inspectErr != nil {
				return Snapshot{}, transport(pinned, inspectErr)
			}
			if !sameOccupant(pinned, next.Target) {
				return Snapshot{}, &Error{Code: ErrReplaced, Message: "managed pane was replaced while setup completed", Target: &pinned}
			}
			if next.PaneCount != 1 || next.Dead || next.CopyMode {
				return Snapshot{}, shellReady(next)
			}
			if err := shellReady(next); err == nil {
				if readySince.IsZero() {
					readySince = s.Now()
				}
				if s.Now().Sub(readySince) >= s.ShellStableFor {
					return next, nil
				}
			} else {
				readySince = time.Time{}
			}
		}
	}
}

func (s Service) Start(ctx context.Context, req StartRequest) (Agent, error) {
	s = s.defaults()
	if s.Terminal == nil {
		return Agent{}, &Error{Code: ErrTransport, Message: "terminal adapter is unavailable"}
	}
	if strings.TrimSpace(req.Kind) == "" || len(req.Argv) == 0 {
		return Agent{}, &Error{Code: ErrNotReady, Message: "agent kind and launch argv are required"}
	}
	startCtx := ctx
	cancel := func() {}
	if req.Timeout > 0 {
		startCtx, cancel = context.WithTimeout(ctx, req.Timeout)
	}
	defer cancel()
	initial, err := s.Terminal.Inspect(startCtx, req.Target)
	if err != nil {
		return Agent{}, transport(req.Target, err)
	}
	initial, err = s.waitShellReady(startCtx, initial, req.Timeout)
	if err != nil {
		return Agent{}, err
	}
	pinned := initial.Target
	if err := s.Terminal.Launch(startCtx, initial, req.Argv); err != nil {
		return Agent{}, transport(pinned, err)
	}
	launchedAt := s.Now()

	ticker := time.NewTicker(s.Poll)
	defer ticker.Stop()
	var tracker agentactivity.Tracker
	providerObserved := false
	for {
		select {
		case <-startCtx.Done():
			code := ErrTransport
			if startCtx.Err() == context.DeadlineExceeded {
				code = ErrTimeout
			}
			return Agent{}, &Error{Code: code, Message: fmt.Sprintf("agent %s did not become ready: %v", req.Kind, startCtx.Err()), Target: &pinned, Err: startCtx.Err()}
		case <-ticker.C:
			snap, inspectErr := s.Terminal.Inspect(startCtx, pinned)
			if inspectErr != nil {
				return Agent{}, transport(pinned, inspectErr)
			}
			if !sameOccupant(pinned, snap.Target) {
				return Agent{}, &Error{Code: ErrReplaced, Message: "managed pane was replaced while the agent was starting", Target: &pinned}
			}
			if snap.Dead {
				return Agent{}, &Error{Code: ErrStartFailed, Message: fmt.Sprintf("agent %s exited while starting", req.Kind), Target: &pinned}
			}
			if err := shellReady(snap); err == nil && (providerObserved || s.Now().Sub(launchedAt) >= 500*time.Millisecond) {
				return Agent{}, &Error{Code: ErrStartFailed, Message: fmt.Sprintf("agent %s exited before it became ready", req.Kind), Target: &pinned}
			}
			state := s.Detect(snap, &tracker)
			if state.Kind != "" && state.Kind != req.Kind {
				return Agent{}, &Error{Code: ErrKindMismatch, Message: fmt.Sprintf("expected %s, found %s", req.Kind, state.Kind), Target: &pinned}
			}
			if state.Kind != req.Kind {
				continue
			}
			providerObserved = true
			switch state.Status {
			case StatusIdle, StatusDone:
				state.InteractiveReady = true
				return Agent{Target: snap.Target, Agent: state}, nil
			case StatusBlocked:
				return Agent{}, &Error{Code: ErrNotReady, Message: "agent became blocked before it was ready", Target: &pinned}
			}
		}
	}
}

func (s Service) waitShellReady(ctx context.Context, initial Snapshot, timeout time.Duration) (Snapshot, error) {
	if err := shellReady(initial); err == nil {
		return initial, nil
	}
	// Only an otherwise-live pane still reporting its interactive shell can be
	// shell initialization. A command/editor/agent, copy mode, dead pane, or
	// multi-pane session is a semantic busy refusal immediately.
	if initial.PaneCount != 1 || initial.Dead || initial.CopyMode || !interactiveShell(initial.CurrentCommand) || initial.PaneID == "" || initial.PanePID <= 0 || initial.ServerIncarnation == "" {
		return Snapshot{}, shellReady(initial)
	}
	grace := 2 * time.Second
	if timeout > 0 && timeout < grace {
		grace = timeout
	}
	waitCtx, cancel := context.WithTimeout(ctx, grace)
	defer cancel()
	pinned := initial.Target
	ticker := time.NewTicker(s.Poll)
	defer ticker.Stop()
	for {
		select {
		case <-waitCtx.Done():
			if ctx.Err() != nil {
				code := ErrTransport
				if ctx.Err() == context.DeadlineExceeded {
					code = ErrTimeout
				}
				return Snapshot{}, &Error{Code: code, Message: fmt.Sprintf("managed shell did not become ready: %v", ctx.Err()), Target: &pinned, Err: ctx.Err()}
			}
			return Snapshot{}, shellReady(initial)
		case <-ticker.C:
			next, err := s.Terminal.Inspect(waitCtx, pinned)
			if err != nil {
				return Snapshot{}, transport(pinned, err)
			}
			if !sameOccupant(pinned, next.Target) {
				return Snapshot{}, &Error{Code: ErrReplaced, Message: "managed pane was replaced while its shell initialized", Target: &pinned}
			}
			if err := shellReady(next); err == nil {
				return next, nil
			}
			if next.PaneCount != 1 || next.Dead || next.CopyMode || !interactiveShell(next.CurrentCommand) {
				return Snapshot{}, shellReady(next)
			}
			initial = next
		}
	}
}

func shellReady(s Snapshot) error {
	t := s.Target
	refuse := func(message string) error { return &Error{Code: ErrPaneBusy, Message: message, Target: &t} }
	if s.PaneCount != 1 {
		return refuse("managed session must contain exactly one pane")
	}
	if s.Dead {
		return refuse("managed pane is dead")
	}
	if s.CopyMode {
		return refuse("managed pane is in copy or another tmux mode")
	}
	if s.PaneID == "" || s.PanePID <= 0 || s.ServerIncarnation == "" {
		return refuse("managed pane identity is incomplete")
	}
	if !interactiveShell(s.CurrentCommand) || !s.ShellReady {
		return refuse(fmt.Sprintf("pane foreground is %q, not its interactive shell", s.CurrentCommand))
	}
	return nil
}

func interactiveShell(command string) bool {
	command = strings.ToLower(strings.TrimSpace(command))
	for _, shell := range []string{"sh", "bash", "zsh", "fish", "nu", "pwsh"} {
		if command == shell {
			return true
		}
	}
	return false
}

func sameOccupant(a, b Target) bool {
	return a.Host == b.Host && a.Project == b.Project && a.Namespace == b.Namespace && a.Session == b.Session && a.PaneID == b.PaneID && a.PanePID == b.PanePID && a.ServerIncarnation == b.ServerIncarnation
}

func detect(snap Snapshot, tracker *agentactivity.Tracker) AgentState {
	now := snap.CapturedAt
	if now.IsZero() {
		now = time.Now()
	}
	kind := agentactivity.Identify(agentactivity.Observation{Screen: snap.Screen, PaneTitle: snap.Title, CurrentCommand: snap.CurrentCommand, ProcessIdentity: snap.ProcessIdentity, CapturedAt: now})
	if kind == "shell" {
		kind = ""
	}
	if kind == "" {
		return AgentState{Status: StatusUnknown, Freshness: "current", Evidence: "provider-not-identified", CapturedAt: now}
	}
	result := agentactivity.Detect(agentactivity.Observation{Agent: kind, Screen: snap.Screen, PaneTitle: snap.Title, CurrentCommand: snap.CurrentCommand, ProcessIdentity: snap.ProcessIdentity, CapturedAt: now})
	tracker.Apply(result, now)
	p := agentstatus.Resolve(agentstatus.Input{ProviderSupported: agentactivity.Supports(kind), Activity: *tracker, CapturedAt: now, Now: now, StaleAfter: time.Minute})
	status := Status(p.Lane)
	if p.Lane == agentstatus.LanePaused {
		status = StatusUnknown
	}
	return AgentState{Kind: kind, Status: status, Freshness: string(p.Freshness), Attention: p.Attention, Evidence: p.Evidence, ChangedAt: p.ChangedAt, CapturedAt: now, InteractiveReady: kind != "" && (status == StatusIdle || status == StatusDone)}
}

func transport(target Target, err error) error {
	var typed *Error
	if AsError(err, &typed) {
		return typed
	}
	return &Error{Code: ErrTransport, Message: err.Error(), Target: &target, Err: err}
}
