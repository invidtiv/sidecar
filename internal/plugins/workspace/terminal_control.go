package workspace

import (
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
)

type workspaceControlRole string

const (
	workspaceControlPrimary workspaceControlRole = "primary"
	workspaceControlPanel   workspaceControlRole = "panel"
)

type workspaceControlManager interface {
	Subscribe(tty.ControlRequest) (*tty.ControlSubscription, error)
	SetAppFocused(bool)
	Stop()
}

type workspaceControlEvent struct {
	Role     workspaceControlRole
	Token    uint64
	Session  string
	Pane     string
	Snapshot *tty.ControlSnapshot
	Fallback error
}

type workspaceControlDeliveryMsg struct {
	Events []workspaceControlEvent
}

type workspaceControlAgentStatusMsg struct {
	Token     uint64
	Session   string
	SourceID  string
	Status    WorktreeStatus
	Available bool
}

// workspaceControlMailbox coalesces callback-thread notifications into the
// Bubble Tea update loop without mutating plugin state from transport goroutines.
type workspaceControlMailbox struct {
	mu     sync.Mutex
	latest map[workspaceControlRole]workspaceControlEvent
	wake   chan struct{}
	closed bool
}

func newWorkspaceControlMailbox() *workspaceControlMailbox {
	return &workspaceControlMailbox{
		latest: make(map[workspaceControlRole]workspaceControlEvent),
		wake:   make(chan struct{}, 1),
	}
}

func (m *workspaceControlMailbox) publish(event workspaceControlEvent) {
	if m == nil {
		return
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.latest[event.Role] = event
	select {
	case m.wake <- struct{}{}:
	default:
	}
	m.mu.Unlock()
}

func (m *workspaceControlMailbox) next() tea.Msg {
	if m == nil {
		return nil
	}
	if _, ok := <-m.wake; !ok {
		return nil
	}
	m.mu.Lock()
	events := make([]workspaceControlEvent, 0, len(m.latest))
	for _, event := range m.latest {
		events = append(events, event)
	}
	m.latest = make(map[workspaceControlRole]workspaceControlEvent)
	m.mu.Unlock()
	return workspaceControlDeliveryMsg{Events: events}
}

func (m *workspaceControlMailbox) close() {
	if m == nil {
		return
	}
	m.mu.Lock()
	if !m.closed {
		m.closed = true
		close(m.wake)
	}
	m.mu.Unlock()
}

type workspaceControlConsumer struct {
	Role     workspaceControlRole
	Session  string
	Pane     string
	Width    int
	Height   int
	Source   string
	SourceID string
	Token    uint64
	Using    bool
	Degraded bool
	Sub      *tty.ControlSubscription
}

type workspaceControlDesired struct {
	Role             workspaceControlRole
	Session, Pane    string
	Width, Height    int
	Source, SourceID string
}

// Update reconciles persistent terminal subscriptions after every plugin state
// transition. Control callbacks re-enter through workspaceControlDeliveryMsg.
func (p *Plugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	switch msg.(type) {
	case tea.FocusMsg:
		p.applicationFocused = true
		if p.controlManager != nil {
			p.controlManager.SetAppFocused(true)
		}
	case tea.BlurMsg:
		p.applicationFocused = false
		if p.controlManager != nil {
			p.controlManager.SetAppFocused(false)
		}
	}

	var cmd tea.Cmd
	switch typed := msg.(type) {
	case workspaceControlDeliveryMsg:
		cmd = p.applyTerminalControlDelivery(typed)
	case workspaceControlAgentStatusMsg:
		cmd = p.applyControlAgentStatus(typed)
	default:
		_, cmd = p.update(msg)
	}

	var cmds []tea.Cmd
	if cmd != nil {
		cmds = append(cmds, cmd)
	}
	if _, ok := msg.(workspaceControlDeliveryMsg); ok {
		cmds = append(cmds, p.listenForTerminalControl())
	}
	cmds = append(cmds, p.reconcileTerminalControls()...)
	return p, tea.Batch(cmds...)
}

func (p *Plugin) listenForTerminalControl() tea.Cmd {
	mailbox := p.controlMailbox
	if mailbox == nil {
		return nil
	}
	return mailbox.next
}

func (p *Plugin) stopTerminalControls() {
	if p.controlManager != nil {
		p.controlManager.Stop()
		p.controlManager = nil
	}
	if p.controlMailbox != nil {
		p.controlMailbox.close()
		p.controlMailbox = nil
	}
	p.controlConsumers = nil
}

func (p *Plugin) terminalOutputSurfaceVisible() bool {
	if !p.focused || p.activePane != PanePreview {
		return false
	}
	if p.viewMode != ViewModeList && p.viewMode != ViewModeInteractive {
		return false
	}
	return p.shellSelected || p.previewTab == PreviewTabOutput
}

func (p *Plugin) desiredPanelControl() (workspaceControlDesired, bool) {
	if p.controlManager == nil || !p.termPanelVisible || !p.terminalOutputSurfaceVisible() ||
		p.termPanelSession == "" || p.termPanelPaneID == "" || p.termPanelOutput == nil {
		return workspaceControlDesired{}, false
	}
	width, height := p.calculateTermPanelDimensions()
	return workspaceControlDesired{
		Role: workspaceControlPanel, Session: p.termPanelSession, Pane: p.termPanelPaneID,
		Width: width, Height: height, Source: "panel", SourceID: p.termPanelSession,
	}, true
}

func (p *Plugin) desiredPrimaryControl() (workspaceControlDesired, bool) {
	if p.controlManager == nil || !p.terminalOutputSurfaceVisible() {
		return workspaceControlDesired{}, false
	}
	width, height := p.calculateAgentPaneDimensions()
	if p.shellSelected {
		shell := p.getSelectedShell()
		if shell == nil || shell.IsOrphaned || shell.Agent == nil || shell.Agent.OutputBuf == nil ||
			shell.Agent.TmuxSession == "" || shell.Agent.TmuxPane == "" {
			return workspaceControlDesired{}, false
		}
		return workspaceControlDesired{
			Role: workspaceControlPrimary, Session: shell.Agent.TmuxSession, Pane: shell.Agent.TmuxPane,
			Width: width, Height: height, Source: "shell", SourceID: shell.TmuxName,
		}, true
	}
	wt := p.selectedWorktree()
	if wt == nil || wt.IsOrphaned || wt.Agent == nil || wt.Agent.OutputBuf == nil ||
		wt.Agent.TmuxSession == "" || wt.Agent.TmuxPane == "" {
		return workspaceControlDesired{}, false
	}
	return workspaceControlDesired{
		Role: workspaceControlPrimary, Session: wt.Agent.TmuxSession, Pane: wt.Agent.TmuxPane,
		Width: width, Height: height, Source: "agent", SourceID: wt.Name,
	}, true
}

func (p *Plugin) reconcileTerminalControls() []tea.Cmd {
	if p.controlManager == nil {
		return nil
	}
	var cmds []tea.Cmd
	panel, panelWanted := p.desiredPanelControl()
	cmds = append(cmds, p.reconcileTerminalControl(workspaceControlPanel, panel, panelWanted)...)
	primary, primaryWanted := p.desiredPrimaryControl()
	cmds = append(cmds, p.reconcileTerminalControl(workspaceControlPrimary, primary, primaryWanted)...)
	return cmds
}

func (p *Plugin) reconcileTerminalControl(role workspaceControlRole, desired workspaceControlDesired, wanted bool) []tea.Cmd {
	current := p.controlConsumers[role]
	if !wanted {
		if current != nil {
			cmds := p.resumeControlPolling(current)
			if current.Sub != nil {
				current.Sub.Close()
			}
			delete(p.controlConsumers, role)
			return cmds
		}
		return nil
	}
	if current != nil && current.Session == desired.Session && current.Pane == desired.Pane &&
		current.Source == desired.Source && current.SourceID == desired.SourceID {
		if current.Sub != nil && (current.Width != desired.Width || current.Height != desired.Height) {
			current.Sub.Resize(desired.Width, desired.Height)
			current.Width = desired.Width
			current.Height = desired.Height
		}
		return nil
	}
	var cmds []tea.Cmd
	if current != nil {
		cmds = append(cmds, p.resumeControlPolling(current)...)
		if current.Sub != nil {
			current.Sub.Close()
		}
		delete(p.controlConsumers, role)
	}

	p.controlNextToken++
	consumer := &workspaceControlConsumer{
		Role: role, Session: desired.Session, Pane: desired.Pane,
		Width: desired.Width, Height: desired.Height,
		Source: desired.Source, SourceID: desired.SourceID, Token: p.controlNextToken,
	}
	p.controlConsumers[role] = consumer
	mailbox := p.controlMailbox
	token := consumer.Token
	sub, err := p.controlManager.Subscribe(tty.ControlRequest{
		Session: desired.Session, Pane: desired.Pane, Width: desired.Width, Height: desired.Height,
		Scrollback: captureLineCount, Visible: true, Focused: true,
		OnSnapshot: func(snapshot tty.ControlSnapshot) {
			copy := snapshot
			mailbox.publish(workspaceControlEvent{
				Role: role, Token: token, Session: desired.Session, Pane: desired.Pane,
				Snapshot: &copy,
			})
		},
		OnFallback: func(err error) {
			mailbox.publish(workspaceControlEvent{
				Role: role, Token: token, Session: desired.Session, Pane: desired.Pane,
				Fallback: err,
			})
		},
	})
	if err != nil {
		consumer.Degraded = true
		return append(cmds, p.scheduleControlPoll(consumer))
	}
	consumer.Sub = sub
	// Preserve the existing polling owner until the first accepted snapshot
	// proves that this subscription is live.
	return append(cmds, p.scheduleControlPoll(consumer))
}

func (p *Plugin) terminalControlUsing(role workspaceControlRole) bool {
	consumer := p.controlConsumers[role]
	return consumer != nil && consumer.Using && !consumer.Degraded
}

func (p *Plugin) primaryControlUsing(source, sourceID string) bool {
	consumer := p.controlConsumers[workspaceControlPrimary]
	return consumer != nil && consumer.Source == source && consumer.SourceID == sourceID &&
		consumer.Using && !consumer.Degraded
}

func (p *Plugin) applyTerminalControlDelivery(msg workspaceControlDeliveryMsg) tea.Cmd {
	var cmds []tea.Cmd
	for _, event := range msg.Events {
		consumer := p.controlConsumers[event.Role]
		if consumer == nil || consumer.Token != event.Token ||
			consumer.Session != event.Session || consumer.Pane != event.Pane {
			continue
		}
		if event.Fallback != nil {
			if consumer.Degraded {
				continue
			}
			consumer.Degraded = true
			consumer.Using = false
			if consumer.Sub != nil {
				consumer.Sub.Close()
				consumer.Sub = nil
			}
			cmds = append(cmds, p.scheduleControlPoll(consumer))
			continue
		}
		if event.Snapshot == nil || consumer.Degraded {
			continue
		}
		if !consumer.Using {
			consumer.Using = true
			p.invalidateControlPoll(consumer)
			if consumer.Source == "agent" {
				// Exclude the native provider from legacy batch captures as
				// soon as ownership transfers. Fallback polling marks it active
				// again on its first direct capture.
				globalActiveRegistry.remove(consumer.Session)
				if statusCmd := p.scheduleControlAgentStatus(consumer); statusCmd != nil {
					cmds = append(cmds, statusCmd)
				}
			}
		}
		switch event.Role {
		case workspaceControlPanel:
			p.applyPanelControlSnapshot(*event.Snapshot)
		case workspaceControlPrimary:
			p.applyPrimaryControlSnapshot(consumer, *event.Snapshot)
		}
	}
	return tea.Batch(cmds...)
}

func (p *Plugin) scheduleControlAgentStatus(consumer *workspaceControlConsumer) tea.Cmd {
	if consumer == nil || consumer.Source != "agent" || !consumer.Using || consumer.Degraded {
		return nil
	}
	wt := p.findWorktree(consumer.SourceID)
	if wt == nil || wt.Agent == nil || wt.Agent.TmuxSession != consumer.Session {
		return nil
	}
	token := consumer.Token
	session := consumer.Session
	sourceID := consumer.SourceID
	agentType := wt.Agent.Type
	worktreePath := wt.Path
	delay := pollIntervalIdle
	if !p.applicationFocused {
		delay = pollIntervalUnfocused
	}
	return tea.Tick(delay, func(time.Time) tea.Msg {
		status, available := detectAgentSessionStatus(agentType, worktreePath)
		return workspaceControlAgentStatusMsg{
			Token: token, Session: session, SourceID: sourceID,
			Status: status, Available: available,
		}
	})
}

func (p *Plugin) applyControlAgentStatus(msg workspaceControlAgentStatusMsg) tea.Cmd {
	consumer := p.controlConsumers[workspaceControlPrimary]
	if consumer == nil || consumer.Token != msg.Token || consumer.Session != msg.Session ||
		consumer.Source != "agent" || consumer.SourceID != msg.SourceID ||
		!consumer.Using || consumer.Degraded {
		return nil
	}
	wt := p.findWorktree(msg.SourceID)
	if wt == nil || wt.Agent == nil || wt.Agent.OutputBuf == nil ||
		wt.Agent.TmuxSession != msg.Session {
		return nil
	}
	// Preserve the legacy detector precedence: session files only refine
	// active/waiting. Tmux-rendered thinking/done/error states stay authoritative.
	if msg.Available && (wt.Status == StatusActive || wt.Status == StatusWaiting) {
		wt.Status = msg.Status
		if msg.Status == StatusWaiting {
			wt.Agent.WaitingFor = extractPrompt(wt.Agent.OutputBuf.String())
			if wt.Agent.WaitingFor == "" {
				wt.Agent.WaitingFor = "Waiting for input"
			}
		} else {
			wt.Agent.WaitingFor = ""
		}
	}
	return p.scheduleControlAgentStatus(consumer)
}

func (p *Plugin) invalidateControlPoll(consumer *workspaceControlConsumer) {
	if consumer == nil {
		return
	}
	switch consumer.Source {
	case "panel":
		p.pollScheduler.Invalidate(termPanelPollKey())
	case "agent":
		p.pollScheduler.Invalidate(agentPollKey(consumer.SourceID))
	case "shell":
		p.pollScheduler.Invalidate(shellPollKey(consumer.SourceID))
	}
}

func (p *Plugin) scheduleControlPoll(consumer *workspaceControlConsumer) tea.Cmd {
	if consumer == nil {
		return nil
	}
	switch consumer.Source {
	case "panel":
		return p.scheduleTermPanelPoll(0)
	case "agent":
		return p.scheduleAgentPoll(consumer.SourceID, 0)
	case "shell":
		return p.scheduleShellPollByName(consumer.SourceID, 0)
	default:
		return nil
	}
}

func (p *Plugin) resumeControlPolling(consumer *workspaceControlConsumer) []tea.Cmd {
	if consumer == nil || !consumer.Using || consumer.Degraded {
		return nil
	}
	consumer.Using = false
	if cmd := p.scheduleControlPoll(consumer); cmd != nil {
		return []tea.Cmd{cmd}
	}
	return nil
}

func (p *Plugin) applyPanelControlSnapshot(snapshot tty.ControlSnapshot) {
	if p.termPanelOutput == nil {
		return
	}
	output := snapshot.Output
	removedRows := 0
	output, removedRows = trimCapturedOutputRows(output, p.tmuxCaptureMaxBytes)
	if snapshot.HasHistory {
		p.termPanelOutput.UpdateSnapshot(output, snapshot.CaptureBase+removedRows)
		p.recordTerminalHistory("panel", snapshot.Session, snapshot.HistorySize)
	} else {
		p.termPanelOutput.Update(output)
	}
	if p.interactiveState != nil && p.interactiveState.Active && p.interactiveState.TermPanel {
		p.interactiveState.CursorRow = snapshot.CursorRow
		p.interactiveState.CursorCol = snapshot.CursorCol
		p.interactiveState.CursorVisible = snapshot.CursorVisible
		p.interactiveState.PaneHeight = snapshot.PaneHeight
		p.interactiveState.PaneWidth = snapshot.PaneWidth
		p.interactiveState.CursorHistorySize = snapshot.HistorySize
		p.interactiveState.HasCursorHistory = snapshot.HasHistory
		p.updateBracketedPasteMode(output)
		p.updateMouseReportingMode(output)
	}
}

func (p *Plugin) applyPrimaryControlSnapshot(consumer *workspaceControlConsumer, snapshot tty.ControlSnapshot) {
	var buffer *tty.OutputBuffer
	switch consumer.Source {
	case "agent":
		if wt := p.findWorktree(consumer.SourceID); wt != nil && wt.Agent != nil &&
			wt.Agent.TmuxSession == consumer.Session {
			buffer = wt.Agent.OutputBuf
		}
	case "shell":
		if shell := p.findShellByName(consumer.SourceID); shell != nil && shell.Agent != nil &&
			shell.Agent.TmuxSession == consumer.Session {
			buffer = shell.Agent.OutputBuf
		}
	}
	if buffer == nil {
		return
	}
	output, removedRows := trimCapturedOutputRows(snapshot.Output, p.tmuxCaptureMaxBytes)
	changed := false
	if snapshot.HasHistory {
		changed = buffer.UpdateSnapshot(output, snapshot.CaptureBase+removedRows)
		historyTarget := consumer.Session
		if consumer.Source == "shell" {
			historyTarget = consumer.SourceID
		}
		p.recordTerminalHistory(consumer.Source, historyTarget, snapshot.HistorySize)
	} else {
		changed = buffer.Update(output)
	}
	if changed {
		switch consumer.Source {
		case "agent":
			if wt := p.findWorktree(consumer.SourceID); wt != nil && wt.Agent != nil {
				wt.Agent.LastOutput = time.Now()
				wt.Status = detectStatus(output)
				wt.Agent.WaitingFor = ""
				if wt.Status == StatusWaiting {
					wt.Agent.WaitingFor = extractPrompt(output)
				}
			}
		case "shell":
			if shell := p.findShellByName(consumer.SourceID); shell != nil && shell.Agent != nil {
				shell.Agent.LastOutput = time.Now()
			}
		}
	}
	if p.interactiveState != nil && p.interactiveState.Active && !p.interactiveState.TermPanel &&
		p.interactiveState.TargetSession == consumer.Session {
		p.interactiveState.CursorRow = snapshot.CursorRow
		p.interactiveState.CursorCol = snapshot.CursorCol
		p.interactiveState.CursorVisible = snapshot.CursorVisible
		p.interactiveState.PaneHeight = snapshot.PaneHeight
		p.interactiveState.PaneWidth = snapshot.PaneWidth
		p.interactiveState.CursorHistorySize = snapshot.HistorySize
		p.interactiveState.HasCursorHistory = snapshot.HasHistory
		p.updateBracketedPasteMode(output)
		p.updateMouseReportingMode(output)
	}
}
