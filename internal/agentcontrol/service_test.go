package agentcontrol

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

type sequenceTerminal struct {
	snapshots []Snapshot
	launched  []string
	launchErr error
}

func (t *sequenceTerminal) Inspect(context.Context, Target) (Snapshot, error) {
	if len(t.snapshots) == 0 {
		return Snapshot{}, errors.New("no observation")
	}
	s := t.snapshots[0]
	if len(t.snapshots) > 1 {
		t.snapshots = t.snapshots[1:]
	}
	return s, nil
}
func (t *sequenceTerminal) Launch(_ context.Context, _ Snapshot, argv []string) error {
	t.launched = append([]string(nil), argv...)
	return t.launchErr
}

func pinnedSnapshot(screen string) Snapshot {
	snapshot := Snapshot{Target: Target{Host: "local", Project: "p", Session: "s", Namespace: "n", PaneID: "%1", PanePID: 42, ServerIncarnation: "server-1"}, PaneCount: 1, CurrentCommand: "zsh", ProcessIdentity: "shell", ShellReady: true, Screen: screen, CapturedAt: time.Unix(10, 0)}
	if screen == "working" || screen == "idle" || screen == "blocked" {
		snapshot.CurrentCommand = "fake"
		snapshot.ProcessIdentity = "fake"
		snapshot.ShellReady = false
	}
	return snapshot
}
func fakeDetect(s Snapshot, _ *agentactivity.Tracker) AgentState {
	state := StatusUnknown
	if s.Screen == "working" {
		state = StatusWorking
	}
	if s.Screen == "idle" {
		state = StatusIdle
	}
	if s.Screen == "blocked" {
		state = StatusBlocked
	}
	return AgentState{Kind: "fake", Status: state, Freshness: "current", Evidence: "fake." + s.Screen, CapturedAt: s.CapturedAt}
}

func TestStartPinsTargetAndReturnsOnlyAtPositiveReady(t *testing.T) {
	terminal := &sequenceTerminal{snapshots: []Snapshot{pinnedSnapshot("shell"), pinnedSnapshot("working"), pinnedSnapshot("idle")}}
	svc := Service{Terminal: terminal, Poll: time.Millisecond, Detect: fakeDetect}
	got, err := svc.Start(context.Background(), StartRequest{Target: Target{Session: "s"}, Kind: "fake", Argv: []string{"fake", "--model", "x y"}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Status != StatusIdle || !got.Agent.InteractiveReady {
		t.Fatalf("agent = %+v", got)
	}
	if len(terminal.launched) != 3 || terminal.launched[2] != "x y" {
		t.Fatalf("argv = %#v", terminal.launched)
	}
}

func TestGetReportsKnownLiveFallbackAsInferredIdle(t *testing.T) {
	snapshot := pinnedSnapshot("stable composer without an explicit prompt marker")
	snapshot.CurrentCommand = "node"
	snapshot.ProcessIdentity = "codex"
	snapshot.ShellReady = false

	got, err := (Service{Terminal: &sequenceTerminal{snapshots: []Snapshot{snapshot}}}).Get(context.Background(), snapshot.Target)
	if err != nil {
		t.Fatal(err)
	}
	if got.Agent.Kind != "codex" || got.Agent.Status != StatusIdle || !got.Agent.InteractiveReady || got.Agent.Evidence != "codex.known-live-fallback" {
		t.Fatalf("Get() = %+v, want positively identified inferred idle Codex", got)
	}
}

func TestStartRefusesBusyAndReplacement(t *testing.T) {
	busy := pinnedSnapshot("")
	busy.CurrentCommand = "vim"
	busy.ProcessIdentity = ""
	busy.ShellReady = false
	_, err := (Service{Terminal: &sequenceTerminal{snapshots: []Snapshot{busy}}, Poll: time.Millisecond}).Start(context.Background(), StartRequest{Target: Target{Session: "s"}, Kind: "fake", Argv: []string{"fake"}, Timeout: time.Second})
	var typed *Error
	if !AsError(err, &typed) || typed.Code != ErrPaneBusy {
		t.Fatalf("busy err = %T %v", err, err)
	}
	replaced := pinnedSnapshot("idle")
	replaced.PaneID = "%2"
	_, err = (Service{Terminal: &sequenceTerminal{snapshots: []Snapshot{pinnedSnapshot(""), replaced}}, Poll: time.Millisecond, Detect: fakeDetect}).Start(context.Background(), StartRequest{Target: Target{Session: "s"}, Kind: "fake", Argv: []string{"fake"}, Timeout: time.Second})
	if !AsError(err, &typed) || typed.Code != ErrReplaced {
		t.Fatalf("replacement err = %T %v", err, err)
	}
}

func TestShellReadyStrictRefusalTable(t *testing.T) {
	base := pinnedSnapshot("")
	tests := map[string]func(*Snapshot){
		"dead":       func(s *Snapshot) { s.Dead = true },
		"copy mode":  func(s *Snapshot) { s.CopyMode = true },
		"multi pane": func(s *Snapshot) { s.PaneCount = 2 },
		"editor": func(s *Snapshot) {
			s.CurrentCommand = "vim"
			s.ShellReady = false
		},
		"unknown foreground": func(s *Snapshot) { s.ShellReady = false },
		"missing pane":       func(s *Snapshot) { s.PaneID = "" },
		"missing server":     func(s *Snapshot) { s.ServerIncarnation = "" },
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			snapshot := base
			mutate(&snapshot)
			var typed *Error
			if err := shellReady(snapshot); !AsError(err, &typed) || typed.Code != ErrPaneBusy {
				t.Fatalf("err = %T %v", err, err)
			}
		})
	}
}

func TestStartRefusesWrongKindAndBlockedBeforeReady(t *testing.T) {
	wrongDetect := func(s Snapshot, _ *agentactivity.Tracker) AgentState {
		return AgentState{Kind: "claude", Status: StatusIdle, CapturedAt: s.CapturedAt}
	}
	_, err := (Service{Terminal: &sequenceTerminal{snapshots: []Snapshot{pinnedSnapshot(""), pinnedSnapshot("idle")}}, Poll: time.Millisecond, Detect: wrongDetect}).Start(context.Background(), StartRequest{Target: Target{Session: "s"}, Kind: "codex", Argv: []string{"codex"}, Timeout: time.Second})
	var typed *Error
	if !AsError(err, &typed) || typed.Code != ErrKindMismatch {
		t.Fatalf("kind mismatch = %T %v", err, err)
	}

	_, err = (Service{Terminal: &sequenceTerminal{snapshots: []Snapshot{pinnedSnapshot(""), pinnedSnapshot("blocked")}}, Poll: time.Millisecond, Detect: fakeDetect}).Start(context.Background(), StartRequest{Target: Target{Session: "s"}, Kind: "fake", Argv: []string{"fake"}, Timeout: time.Second})
	if !AsError(err, &typed) || typed.Code != ErrNotReady {
		t.Fatalf("blocked = %T %v", err, err)
	}
}

func TestStartReportsProviderExitInsteadOfTimingOut(t *testing.T) {
	returnedToShell := pinnedSnapshot("")
	terminal := &sequenceTerminal{snapshots: []Snapshot{pinnedSnapshot(""), pinnedSnapshot("working"), returnedToShell}}
	_, err := (Service{Terminal: terminal, Poll: time.Millisecond, Detect: fakeDetect}).Start(context.Background(), StartRequest{Target: Target{Session: "s"}, Kind: "fake", Argv: []string{"fake"}, Timeout: time.Second})
	var typed *Error
	if !AsError(err, &typed) || typed.Code != ErrStartFailed {
		t.Fatalf("returned-to-shell err = %T %v", err, err)
	}

	dead := pinnedSnapshot("")
	dead.Dead = true
	dead.ShellReady = false
	terminal = &sequenceTerminal{snapshots: []Snapshot{pinnedSnapshot(""), dead}}
	_, err = (Service{Terminal: terminal, Poll: time.Millisecond, Detect: fakeDetect}).Start(context.Background(), StartRequest{Target: Target{Session: "s"}, Kind: "fake", Argv: []string{"fake"}, Timeout: time.Second})
	if !AsError(err, &typed) || typed.Code != ErrStartFailed {
		t.Fatalf("dead-pane err = %T %v", err, err)
	}
}

func TestStartWaitsOnlyPlausibleShellInitialization(t *testing.T) {
	initial := pinnedSnapshot("")
	initial.ShellReady = false
	terminal := &sequenceTerminal{snapshots: []Snapshot{initial, pinnedSnapshot(""), pinnedSnapshot("idle")}}
	_, err := (Service{Terminal: terminal, Poll: time.Millisecond, Detect: fakeDetect}).Start(context.Background(), StartRequest{Target: Target{Session: "s"}, Kind: "fake", Argv: []string{"fake"}, Timeout: time.Second})
	if err != nil {
		t.Fatal(err)
	}
}

func TestStartTimeoutBoundsShellInitializationAndProviderWaitTogether(t *testing.T) {
	initializing := pinnedSnapshot("")
	initializing.ShellReady = false
	_, err := (Service{Terminal: &sequenceTerminal{snapshots: []Snapshot{initializing}}, Poll: time.Millisecond}).Start(context.Background(), StartRequest{Target: Target{Session: "s"}, Kind: "fake", Argv: []string{"fake"}, Timeout: 5 * time.Millisecond})
	var typed *Error
	if !AsError(err, &typed) || typed.Code != ErrTimeout {
		t.Fatalf("shell initialization timeout = %T %v", err, err)
	}
}

func TestWaitShellReadyAllowsCreatedSessionSetupButPinsOccupant(t *testing.T) {
	setup := pinnedSnapshot("")
	setup.CurrentCommand = "td"
	setup.ProcessIdentity = ""
	setup.ShellReady = false
	terminal := &sequenceTerminal{snapshots: []Snapshot{setup, setup, pinnedSnapshot("")}}
	svc := Service{Terminal: terminal, Poll: time.Millisecond, ShellStableFor: time.Millisecond}
	got, err := svc.WaitShellReady(context.Background(), Target{Session: "s"}, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if got.PaneID != "%1" || !got.ShellReady {
		t.Fatalf("ready snapshot = %+v", got)
	}

	replaced := setup
	replaced.PaneID = "%2"
	_, err = (Service{Terminal: &sequenceTerminal{snapshots: []Snapshot{setup, replaced}}, Poll: time.Millisecond, ShellStableFor: time.Millisecond}).WaitShellReady(context.Background(), Target{Session: "s"}, time.Second)
	var typed *Error
	if !AsError(err, &typed) || typed.Code != ErrReplaced {
		t.Fatalf("replacement err = %T %v", err, err)
	}
}

func TestWaitShellReadyRefusesCopyModeAndTimesOutBusySetup(t *testing.T) {
	copyMode := pinnedSnapshot("")
	copyMode.CopyMode = true
	copyMode.ShellReady = false
	_, err := (Service{Terminal: &sequenceTerminal{snapshots: []Snapshot{copyMode}}, Poll: time.Millisecond}).WaitShellReady(context.Background(), Target{Session: "s"}, time.Second)
	var typed *Error
	if !AsError(err, &typed) || typed.Code != ErrPaneBusy {
		t.Fatalf("copy-mode err = %T %v", err, err)
	}

	setup := pinnedSnapshot("")
	setup.CurrentCommand = "td"
	setup.ShellReady = false
	terminal := &sequenceTerminal{snapshots: []Snapshot{setup}}
	_, err = (Service{Terminal: terminal, Poll: time.Millisecond}).WaitShellReady(context.Background(), Target{Session: "s"}, 5*time.Millisecond)
	if !AsError(err, &typed) || typed.Code != ErrTimeout {
		t.Fatalf("timeout err = %T %v", err, err)
	}
}
