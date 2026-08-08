package workspace

import (
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/tty"
)

type fakeWorkspaceControlManager struct {
	requests []tty.ControlRequest
	focus    []bool
	stopped  int
	err      error
}

func (f *fakeWorkspaceControlManager) Subscribe(request tty.ControlRequest) (*tty.ControlSubscription, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return nil, f.err
	}
	return &tty.ControlSubscription{}, nil
}

func (f *fakeWorkspaceControlManager) SetAppFocused(focused bool) {
	f.focus = append(f.focus, focused)
}

func (f *fakeWorkspaceControlManager) Stop() { f.stopped++ }

func terminalPanelControlPlugin(manager workspaceControlManager) *Plugin {
	p := New()
	p.focused = true
	p.width = 120
	p.height = 40
	p.activePane = PanePreview
	p.viewMode = ViewModeList
	p.previewTab = PreviewTabOutput
	p.termPanelVisible = true
	p.termPanelFocused = true
	p.termPanelSession = "panel-session"
	p.termPanelPaneID = "%7"
	p.termPanelOutput = tty.NewOutputBuffer(outputBufferCap)
	p.controlManager = manager
	p.controlMailbox = newWorkspaceControlMailbox()
	p.controlConsumers = make(map[workspaceControlRole]*workspaceControlConsumer)
	return p
}

func primaryControlPlugin(manager workspaceControlManager) *Plugin {
	p := New()
	p.focused = true
	p.width = 120
	p.height = 40
	p.activePane = PanePreview
	p.viewMode = ViewModeList
	p.previewTab = PreviewTabOutput
	p.controlManager = manager
	p.controlMailbox = newWorkspaceControlMailbox()
	p.controlConsumers = make(map[workspaceControlRole]*workspaceControlConsumer)
	p.worktrees = []*Worktree{{
		Name: "agent-worktree", Path: tTempPath,
		Status: StatusActive,
		Agent: &Agent{
			Type: AgentCustom, TmuxSession: "agent-session", TmuxPane: "%11",
			OutputBuf: tty.NewOutputBuffer(outputBufferCap),
		},
	}}
	return p
}

const tTempPath = "/review-only/nonexistent-worktree"

func TestTerminalPanelControlStartsVisibleAndKeepsPollUntilSnapshot(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := terminalPanelControlPlugin(manager)
	cmds := p.reconcileTerminalControls()
	if len(manager.requests) != 1 {
		t.Fatalf("control subscriptions = %d, want 1", len(manager.requests))
	}
	request := manager.requests[0]
	if request.Session != "panel-session" || request.Pane != "%7" ||
		!request.Visible || !request.Focused || request.Scrollback != captureLineCount {
		t.Fatalf("control request = %#v", request)
	}
	if len(cmds) != 1 || cmds[0] == nil {
		t.Fatal("fallback poll was not retained while control starts")
	}
	if p.terminalControlUsing(workspaceControlPanel) {
		t.Fatal("control reported active before first snapshot")
	}

	consumer := p.controlConsumers[workspaceControlPanel]
	generationBefore := p.pollScheduler.Current(termPanelPollKey())
	p.applyTerminalControlDelivery(workspaceControlDeliveryMsg{Events: []workspaceControlEvent{{
		Role: workspaceControlPanel, Token: consumer.Token,
		Session: consumer.Session, Pane: consumer.Pane,
		Snapshot: &tty.ControlSnapshot{
			Session: "panel-session", Pane: "%7", Output: "one\ntwo",
			HistorySize: 900, CaptureBase: 300, HasHistory: true,
			CursorRow: 1, CursorCol: 2, CursorVisible: true,
			PaneHeight: 20, PaneWidth: 80,
		},
	}}})
	if !p.terminalControlUsing(workspaceControlPanel) {
		t.Fatal("first accepted snapshot did not activate control")
	}
	if got := p.pollScheduler.Current(termPanelPollKey()); got <= generationBefore {
		t.Fatalf("poll generation = %d, want > %d", got, generationBefore)
	}
	base, _, absolute := p.termPanelOutput.AbsoluteRange()
	if !absolute || base != 300 || p.termPanelOutput.Lines()[1] != "two" {
		t.Fatalf("panel snapshot base=%d absolute=%v lines=%q", base, absolute, p.termPanelOutput.Lines())
	}
	if p.scheduleTermPanelPoll(0) != nil {
		t.Fatal("panel polling continued after control activation")
	}
}

func TestTerminalControlsReserveScrollbarColumn(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := terminalPanelControlPlugin(manager)
	_, panelHeight := p.calculateTermPanelDimensions()
	p.termPanelOutput.Update(strings.Repeat("line\n", panelHeight+1))
	panel, ok := p.desiredPanelControl()
	if !ok {
		t.Fatal("terminal panel control was not available")
	}
	panelBoxWidth, _ := p.calculateTermPanelDimensions()
	if panel.Width != panelBoxWidth-1 {
		t.Fatalf("panel control width = %d, want %d with scrollbar", panel.Width, panelBoxWidth-1)
	}

	p = primaryControlPlugin(manager)
	_, primaryHeight := p.calculateAgentPaneDimensions()
	p.worktrees[0].Agent.OutputBuf.Update(strings.Repeat("line\n", primaryHeight+1))
	agent, ok := p.desiredPrimaryControl()
	if !ok {
		t.Fatal("agent control was not available")
	}
	primaryBoxWidth, _ := p.calculateAgentPaneDimensions()
	if agent.Width != primaryBoxWidth-1 {
		t.Fatalf("agent control width = %d, want %d with scrollbar", agent.Width, primaryBoxWidth-1)
	}

	p.shells = []*ShellSession{{
		TmuxName: "shell-session",
		Agent: &Agent{TmuxSession: "shell-session", TmuxPane: "%12",
			OutputBuf: tty.NewOutputBuffer(outputBufferCap)},
	}}
	p.shellSelected = true
	shellBoxWidth, shellHeight := p.calculateAgentPaneDimensions()
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("line\n", shellHeight+1))
	shell, ok := p.desiredPrimaryControl()
	if !ok {
		t.Fatal("shell control was not available")
	}
	if shell.Width != shellBoxWidth-1 {
		t.Fatalf("shell control width = %d, want %d with scrollbar", shell.Width, shellBoxWidth-1)
	}
}

func TestTerminalPanelControlFallbackRestartsOneKeyedPoll(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := terminalPanelControlPlugin(manager)
	p.reconcileTerminalControls()
	consumer := p.controlConsumers[workspaceControlPanel]
	consumer.Using = true

	cmd := p.applyTerminalControlDelivery(workspaceControlDeliveryMsg{Events: []workspaceControlEvent{{
		Role: workspaceControlPanel, Token: consumer.Token,
		Session: consumer.Session, Pane: consumer.Pane,
		Fallback: errors.New("reader EOF"),
	}}})
	if cmd == nil || consumer.Using || !consumer.Degraded {
		t.Fatalf("fallback state = using:%v degraded:%v cmd:%v", consumer.Using, consumer.Degraded, cmd)
	}
	firstGeneration := p.pollScheduler.Current(termPanelPollKey())
	if _, ok := cmd().(termPanelPollMsg); !ok {
		t.Fatalf("fallback command returned %T", cmd())
	}
	duplicate := p.applyTerminalControlDelivery(workspaceControlDeliveryMsg{Events: []workspaceControlEvent{{
		Role: workspaceControlPanel, Token: consumer.Token,
		Session: consumer.Session, Pane: consumer.Pane,
		Fallback: errors.New("duplicate"),
	}}})
	if duplicate != nil {
		t.Fatal("duplicate fallback scheduled another poll")
	}
	if got := p.pollScheduler.Current(termPanelPollKey()); got != firstGeneration {
		t.Fatalf("duplicate fallback advanced generation to %d, want %d", got, firstGeneration)
	}
}

func TestTerminalPanelControlTracksVisibilityAndApplicationFocus(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := terminalPanelControlPlugin(manager)
	p.reconcileTerminalControls()

	p.termPanelVisible = false
	p.reconcileTerminalControls()
	if p.controlConsumers[workspaceControlPanel] != nil {
		t.Fatal("hidden panel retained control subscription")
	}

	p.Update(tea.BlurMsg{})
	p.Update(tea.FocusMsg{})
	if p.applicationFocused != true || len(manager.focus) != 2 ||
		manager.focus[0] != false || manager.focus[1] != true {
		t.Fatalf("focus propagation = %#v appFocused=%v", manager.focus, p.applicationFocused)
	}
}

func TestPrimaryControlSwitchesAgentAndShellWithoutCompetingPolls(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := primaryControlPlugin(manager)
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{
		Active: true, TargetSession: "agent-session", TargetPane: "%11",
	}
	p.shells = []*ShellSession{{
		Name: "Shell", TmuxName: "shell-session",
		Agent: &Agent{
			TmuxSession: "shell-session", TmuxPane: "%12",
			OutputBuf: tty.NewOutputBuffer(outputBufferCap),
		},
	}}

	cmds := p.reconcileTerminalControls()
	if len(manager.requests) != 1 || len(cmds) != 1 {
		t.Fatalf("agent startup requests=%d cmds=%d, want one each", len(manager.requests), len(cmds))
	}
	request := manager.requests[0]
	if request.Session != "agent-session" || request.Pane != "%11" ||
		request.Scrollback != captureLineCount || !request.Visible || !request.Focused {
		t.Fatalf("agent control request = %#v", request)
	}
	agentPollGeneration := p.pollScheduler.Current(agentPollKey("agent-worktree"))
	request.OnSnapshot(tty.ControlSnapshot{
		Session: "agent-session", Pane: "%11", Output: "one\ntwo",
		HistorySize: 900, CaptureBase: 300, HasHistory: true,
		CursorRow: 1, CursorCol: 2, CursorVisible: true,
		PaneHeight: 20, PaneWidth: 80,
	})
	delivery := p.controlMailbox.next().(workspaceControlDeliveryMsg)
	p.applyTerminalControlDelivery(delivery)
	if !p.primaryControlUsing("agent", "agent-worktree") {
		t.Fatal("agent control did not own output after first snapshot")
	}
	if p.pollScheduler.Current(agentPollKey("agent-worktree")) <= agentPollGeneration {
		t.Fatal("agent poll chain was not invalidated")
	}
	if p.scheduleAgentPoll("agent-worktree", 0) != nil {
		t.Fatal("agent polling competed with active control provider")
	}
	base, _, absolute := p.worktrees[0].Agent.OutputBuf.AbsoluteRange()
	if !absolute || base != 300 || p.worktrees[0].Agent.OutputBuf.String() != "one\ntwo" {
		t.Fatalf("agent snapshot base=%d absolute=%v output=%q", base, absolute, p.worktrees[0].Agent.OutputBuf.String())
	}
	if got := p.terminalHistory[terminalHistoryKey("agent", "agent-session")].HistorySize; got != 900 {
		t.Fatalf("agent history size = %d, want 900", got)
	}
	if p.interactiveState.CursorRow != 1 || p.interactiveState.CursorCol != 2 ||
		!p.interactiveState.CursorVisible || p.interactiveState.PaneHeight != 20 ||
		p.interactiveState.PaneWidth != 80 {
		t.Fatalf("interactive cursor state = %#v", p.interactiveState)
	}

	p.shellSelected = true
	agentResumeGeneration := p.pollScheduler.Current(agentPollKey("agent-worktree"))
	switchCmds := p.reconcileTerminalControls()
	if len(manager.requests) != 2 || len(switchCmds) != 2 {
		t.Fatalf("shell switch requests=%d cmds=%d, want second subscription and two poll handoffs",
			len(manager.requests), len(switchCmds))
	}
	if p.pollScheduler.Current(agentPollKey("agent-worktree")) <= agentResumeGeneration {
		t.Fatal("background agent polling did not resume after source switch")
	}
	shellConsumer := p.controlConsumers[workspaceControlPrimary]
	if shellConsumer == nil || shellConsumer.Source != "shell" || shellConsumer.SourceID != "shell-session" {
		t.Fatalf("shell consumer = %#v", shellConsumer)
	}

	// A callback already queued by the prior agent subscription must not cross
	// the source/token handoff.
	request.OnSnapshot(tty.ControlSnapshot{
		Session: "agent-session", Pane: "%11", Output: "stale",
	})
	p.applyTerminalControlDelivery(p.controlMailbox.next().(workspaceControlDeliveryMsg))
	if got := p.worktrees[0].Agent.OutputBuf.String(); got != "one\ntwo" {
		t.Fatalf("stale agent snapshot mutated output: %q", got)
	}

	shellRequest := manager.requests[1]
	shellPollGeneration := p.pollScheduler.Current(shellPollKey("shell-session"))
	shellRequest.OnSnapshot(tty.ControlSnapshot{
		Session: "shell-session", Pane: "%12", Output: "shell output",
		HistorySize: 40, CaptureBase: 0, HasHistory: true,
	})
	p.applyTerminalControlDelivery(p.controlMailbox.next().(workspaceControlDeliveryMsg))
	if !p.primaryControlUsing("shell", "shell-session") ||
		p.pollScheduler.Current(shellPollKey("shell-session")) <= shellPollGeneration {
		t.Fatal("shell control did not take ownership and invalidate polling")
	}
	if p.scheduleShellPollByName("shell-session", 0) != nil {
		t.Fatal("shell polling competed with active control provider")
	}
	if got := p.shells[0].Agent.OutputBuf.String(); got != "shell output" {
		t.Fatalf("shell snapshot output = %q", got)
	}
	if got := p.terminalHistory[terminalHistoryKey("shell", "shell-session")].HistorySize; got != 40 {
		t.Fatalf("shell history size = %d, want 40", got)
	}
}

func TestPrimaryControlFallbackAndSynchronousFailureResumePolling(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := primaryControlPlugin(manager)
	p.reconcileTerminalControls()
	request := manager.requests[0]
	request.OnSnapshot(tty.ControlSnapshot{
		Session: "agent-session", Pane: "%11", Output: "ready",
	})
	p.applyTerminalControlDelivery(p.controlMailbox.next().(workspaceControlDeliveryMsg))
	consumer := p.controlConsumers[workspaceControlPrimary]

	generation := p.pollScheduler.Current(agentPollKey("agent-worktree"))
	request.OnFallback(errors.New("reader EOF"))
	cmd := p.applyTerminalControlDelivery(p.controlMailbox.next().(workspaceControlDeliveryMsg))
	if cmd == nil || consumer.Using || !consumer.Degraded || consumer.Sub != nil {
		t.Fatalf("fallback state using=%v degraded=%v sub=%v cmd=%v",
			consumer.Using, consumer.Degraded, consumer.Sub, cmd)
	}
	if p.pollScheduler.Current(agentPollKey("agent-worktree")) <= generation {
		t.Fatal("fallback did not restart agent polling")
	}
	if p.scheduleAgentPoll("agent-worktree", 0) == nil {
		t.Fatal("degraded provider continued suppressing agent polls")
	}

	failedManager := &fakeWorkspaceControlManager{err: errors.New("manager stopped")}
	failed := primaryControlPlugin(failedManager)
	failed.reconcileTerminalControls()
	failedConsumer := failed.controlConsumers[workspaceControlPrimary]
	if failedConsumer == nil || !failedConsumer.Degraded || failedConsumer.Sub != nil {
		t.Fatalf("synchronous failure consumer = %#v", failedConsumer)
	}
	failed.activePane = PaneSidebar
	failed.reconcileTerminalControls() // Must not call Close on a nil subscription.
	if failed.controlConsumers[workspaceControlPrimary] != nil {
		t.Fatal("hidden failed consumer was retained")
	}
}

func TestPrimaryControlAgentStatusRefreshPreservesDetectorPrecedence(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := primaryControlPlugin(manager)
	p.reconcileTerminalControls()
	consumer := p.controlConsumers[workspaceControlPrimary]
	consumer.Using = true

	cmd := p.applyControlAgentStatus(workspaceControlAgentStatusMsg{
		Token: consumer.Token, Session: consumer.Session, SourceID: consumer.SourceID,
		Status: StatusWaiting, Available: true,
	})
	if cmd == nil || p.worktrees[0].Status != StatusWaiting ||
		p.worktrees[0].Agent.WaitingFor != "Waiting for input" {
		t.Fatalf("waiting refresh status=%v waiting=%q cmd=%v",
			p.worktrees[0].Status, p.worktrees[0].Agent.WaitingFor, cmd)
	}

	p.worktrees[0].Status = StatusThinking
	p.applyControlAgentStatus(workspaceControlAgentStatusMsg{
		Token: consumer.Token, Session: consumer.Session, SourceID: consumer.SourceID,
		Status: StatusWaiting, Available: true,
	})
	if p.worktrees[0].Status != StatusThinking {
		t.Fatalf("session detector overrode rendered thinking status: %v", p.worktrees[0].Status)
	}

	p.worktrees[0].Status = StatusActive
	if stale := p.applyControlAgentStatus(workspaceControlAgentStatusMsg{
		Token: consumer.Token + 1, Session: consumer.Session, SourceID: consumer.SourceID,
		Status: StatusWaiting, Available: true,
	}); stale != nil || p.worktrees[0].Status != StatusActive {
		t.Fatalf("stale status message applied: status=%v cmd=%v", p.worktrees[0].Status, stale)
	}
}

func TestPrimaryControlSupportedAgentRejectsSessionFileAuthority(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := primaryControlPlugin(manager)
	p.worktrees[0].Agent.Type = AgentCodex
	p.worktrees[0].Agent.Activity.State = agentactivity.StateWorking
	p.reconcileTerminalControls()
	consumer := p.controlConsumers[workspaceControlPrimary]
	consumer.Using = true

	if cmd := p.scheduleControlAgentStatus(consumer); cmd != nil {
		t.Fatal("supported provider scheduled legacy session-file authority")
	}
	p.applyControlAgentStatus(workspaceControlAgentStatusMsg{
		Token: consumer.Token, Session: consumer.Session, SourceID: consumer.SourceID,
		Status: StatusError, Available: true,
	})
	if p.worktrees[0].Agent.Activity.State != agentactivity.StateWorking || p.worktrees[0].Status != StatusActive {
		t.Fatalf("legacy control status changed supported provider: activity=%q status=%v",
			p.worktrees[0].Agent.Activity.State, p.worktrees[0].Status)
	}
}

func TestPrimaryControlSnapshotUsesSemanticAuthority(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := primaryControlPlugin(manager)
	agent := p.worktrees[0].Agent
	agent.Type = AgentCodex
	agent.Activity = agentactivity.Tracker{State: agentactivity.StateWorking}
	consumer := &workspaceControlConsumer{Source: "agent", SourceID: "agent-worktree", Session: "agent-session"}
	p.applyPrimaryControlSnapshot(consumer, tty.ControlSnapshot{
		Output: "Action Required\nPress enter to confirm\n", PaneTitle: "Action Required", CurrentCommand: "codex",
	})
	if agent.Activity.State != agentactivity.StateBlocked || p.worktrees[0].Status != StatusWaiting {
		t.Fatalf("control snapshot activity=%q evidence=%q status=%v", agent.Activity.State, agent.Activity.Evidence, p.worktrees[0].Status)
	}
	p.applyPrimaryControlSnapshot(consumer, tty.ControlSnapshot{
		Output: "Working (3s • esc to interrupt)\n", PaneTitle: "⠴ sidecar-agent-status", CurrentCommand: "node",
	})
	if agent.Activity.State != agentactivity.StateWorking || p.worktrees[0].Status != StatusActive {
		t.Fatalf("fresh working snapshot retained stale blocker: activity=%q evidence=%q status=%v",
			agent.Activity.State, agent.Activity.Evidence, p.worktrees[0].Status)
	}
}

func TestPrimaryControlFreshProcessIdentityRejectsStaleSupportedState(t *testing.T) {
	providers := []AgentType{AgentCodex, AgentClaude, AgentGrok, AgentAntigravity}
	priorStates := []agentactivity.State{agentactivity.StateWorking, agentactivity.StateBlocked}
	for _, provider := range providers {
		for _, priorState := range priorStates {
			t.Run(string(provider)+"/"+string(priorState), func(t *testing.T) {
				p := primaryControlPlugin(&fakeWorkspaceControlManager{})
				agent := p.worktrees[0].Agent
				agent.Type = provider
				agent.Activity = agentactivity.Tracker{State: priorState, Evidence: string(provider) + ".stale", Seen: false}
				consumer := &workspaceControlConsumer{Source: "agent", SourceID: "agent-worktree", Session: "agent-session"}

				// The visible pane has returned to a shell. Blocker-looking screen/title
				// from the previous provider must not win over the fresh command.
				p.applyPrimaryControlSnapshot(consumer, tty.ControlSnapshot{
					Output:    "Action Required\nPress enter to confirm\n$ ",
					PaneTitle: "Action Required", CurrentCommand: "zsh",
				})
				if agent.Activity.State != agentactivity.StateUnknown {
					t.Fatalf("stale %s remained %q (evidence %q)", provider, agent.Activity.State, agent.Activity.Evidence)
				}
				if agent.Activity.DisplayState() == "done" {
					t.Fatal("process mismatch manufactured done")
				}

				shellAgent := &Agent{Type: provider, TmuxSession: "supported-shell", OutputBuf: tty.NewOutputBuffer(outputBufferCap), Activity: agentactivity.Tracker{
					State: priorState, Evidence: string(provider) + ".stale", Seen: false,
				}}
				shellAgent.OutputBuf.Update("old provider screen")
				shell := &ShellSession{TmuxName: "supported-shell", ChosenAgent: provider, Agent: shellAgent}
				p = &Plugin{shells: []*ShellSession{shell}, tmuxCaptureMaxBytes: outputBufferCap}
				p.applyPrimaryControlSnapshot(&workspaceControlConsumer{
					Source: "shell", SourceID: shell.TmuxName, Session: shell.TmuxName,
				}, tty.ControlSnapshot{
					Output:    "Action Required\nPress enter to confirm\n$ ",
					PaneTitle: "Action Required", CurrentCommand: "zsh",
				})
				if shellAgent.Activity.State != agentactivity.StateUnknown || shellAgent.Activity.DisplayState() == "done" {
					t.Fatalf("shell stale %s became state=%q display=%q", provider, shellAgent.Activity.State, shellAgent.Activity.DisplayState())
				}
			})
		}
	}
}
