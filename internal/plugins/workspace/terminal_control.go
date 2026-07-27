package workspace

import (
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
)

type workspaceControlRole string

const workspaceControlPanel workspaceControlRole = "panel"

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
	Token    uint64
	Using    bool
	Degraded bool
	Sub      *tty.ControlSubscription
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
	if delivery, ok := msg.(workspaceControlDeliveryMsg); ok {
		cmd = p.applyTerminalControlDelivery(delivery)
	} else {
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

func (p *Plugin) desiredPanelControl() (session, pane string, width, height int, ok bool) {
	if p.controlManager == nil || !p.termPanelVisible || !p.terminalOutputSurfaceVisible() ||
		p.termPanelSession == "" || p.termPanelPaneID == "" || p.termPanelOutput == nil {
		return "", "", 0, 0, false
	}
	width, height = p.calculateTermPanelDimensions()
	return p.termPanelSession, p.termPanelPaneID, width, height, true
}

func (p *Plugin) reconcileTerminalControls() []tea.Cmd {
	if p.controlManager == nil {
		return nil
	}
	session, pane, width, height, wanted := p.desiredPanelControl()
	current := p.controlConsumers[workspaceControlPanel]
	if !wanted {
		if current != nil {
			current.Sub.Close()
			delete(p.controlConsumers, workspaceControlPanel)
		}
		return nil
	}
	if current != nil && current.Session == session && current.Pane == pane {
		if current.Sub != nil && (current.Width != width || current.Height != height) {
			current.Sub.Resize(width, height)
			current.Width = width
			current.Height = height
		}
		return nil
	}
	if current != nil {
		current.Sub.Close()
		delete(p.controlConsumers, workspaceControlPanel)
	}

	p.controlNextToken++
	consumer := &workspaceControlConsumer{
		Role: workspaceControlPanel, Session: session, Pane: pane,
		Width: width, Height: height, Token: p.controlNextToken,
	}
	p.controlConsumers[workspaceControlPanel] = consumer
	mailbox := p.controlMailbox
	token := consumer.Token
	sub, err := p.controlManager.Subscribe(tty.ControlRequest{
		Session: session, Pane: pane, Width: width, Height: height,
		Scrollback: captureLineCount, Visible: true, Focused: true,
		OnSnapshot: func(snapshot tty.ControlSnapshot) {
			copy := snapshot
			mailbox.publish(workspaceControlEvent{
				Role: workspaceControlPanel, Token: token, Session: session, Pane: pane,
				Snapshot: &copy,
			})
		},
		OnFallback: func(err error) {
			mailbox.publish(workspaceControlEvent{
				Role: workspaceControlPanel, Token: token, Session: session, Pane: pane,
				Fallback: err,
			})
		},
	})
	if err != nil {
		consumer.Degraded = true
		return []tea.Cmd{p.scheduleTermPanelPoll(0)}
	}
	consumer.Sub = sub
	// Preserve the existing polling owner until the first accepted snapshot
	// proves that this subscription is live.
	return []tea.Cmd{p.scheduleTermPanelPoll(0)}
}

func (p *Plugin) terminalControlUsing(role workspaceControlRole) bool {
	consumer := p.controlConsumers[role]
	return consumer != nil && consumer.Using && !consumer.Degraded
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
			if event.Role == workspaceControlPanel {
				cmds = append(cmds, p.scheduleTermPanelPoll(0))
			}
			continue
		}
		if event.Snapshot == nil || consumer.Degraded {
			continue
		}
		if !consumer.Using {
			consumer.Using = true
			if event.Role == workspaceControlPanel {
				p.pollScheduler.Invalidate(termPanelPollKey())
			}
		}
		if event.Role == workspaceControlPanel {
			p.applyPanelControlSnapshot(*event.Snapshot)
		}
	}
	return tea.Batch(cmds...)
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
		p.updateBracketedPasteMode(output)
		p.updateMouseReportingMode(output)
	}
}
