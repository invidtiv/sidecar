package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/broadcastmodal"
	"github.com/marcus/sidecar/internal/config"
)

func (m *Model) OpenBroadcast() tea.Cmd { return m.openBroadcast() }

func (m *Model) openBroadcast() tea.Cmd {
	if !broadcastmodal.Enabled() {
		return nil
	}
	m.broadcast = broadcastmodal.New("", config.StateDir(), broadcastmodal.ScopeAllProjects, m.broadcastShowsRemote())
	return m.broadcast.Replan()
}

func (m *Model) broadcastShowsRemote() bool {
	if m == nil {
		return false
	}
	for _, ws := range m.catalog {
		if ws.HostID != "" {
			return true
		}
	}
	return false
}

func (m *Model) handleBroadcastKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if msg.String() != "B" || !broadcastmodal.Enabled() {
		return false, nil
	}
	if m.WorkspaceFocusContext() != ctxGlobalWorkspaces {
		return false, nil
	}
	if m.broadcast != nil {
		return true, nil
	}
	return true, m.openBroadcast()
}

func (m *Model) handleBroadcastModalKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.broadcast == nil {
		return nil
	}
	m.broadcast.Ensure(m.width)
	close, cmd := m.broadcast.HandleKey(msg)
	if close {
		m.broadcast = nil
	}
	return cmd
}

func (m *Model) handleBroadcastModalMouse(msg tea.MouseMsg) tea.Cmd {
	if m.broadcast == nil {
		return nil
	}
	m.broadcast.Ensure(m.width)
	close, cmd := m.broadcast.HandleMouse(msg, m.workspacesMouse)
	if close {
		m.broadcast = nil
	}
	return cmd
}

func (m *Model) applyBroadcastPlan(msg broadcastmodal.PlannedMsg) {
	if m.broadcast != nil {
		m.broadcast.ApplyPlan(msg)
	}
}

func (m *Model) applyBroadcastSent(msg broadcastmodal.SentMsg) tea.Cmd {
	m.broadcast = nil
	if msg.Err != nil {
		return broadcastmodal.NotifyError(msg.Err)
	}
	return broadcastmodal.NotifyCmd(msg.Result)
}
