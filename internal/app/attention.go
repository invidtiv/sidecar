package app

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/uirequest"
)

type attentionRefreshMsg struct{}

type attentionSnapshot struct {
	published bool
	focused   bool
	origin    uirequest.Origin
}

func (m Model) currentAttention() attentionSnapshot {
	snapshot := attentionSnapshot{published: true, focused: m.applicationFocused}
	if !m.applicationFocused || m.configOpen() || m.hasModal() || m.notificationCentreOpen {
		return snapshot
	}
	if m.globalWorkspacesVisible() {
		if origin, ok := m.overview.AttentionOrigin(); ok {
			snapshot.origin = attentionOriginTransport(origin)
		}
		return snapshot
	}
	if m.inGlobalScope() {
		return snapshot
	}
	active := m.ActivePlugin()
	if active == nil || active.ID() != workspacePluginID || !active.IsFocused() {
		return snapshot
	}
	if provider, ok := active.(plugin.AttentionOriginProvider); ok {
		if origin, visible := provider.AttentionOrigin(); visible {
			snapshot.origin = attentionOriginTransport(origin)
		}
	}
	return snapshot
}

func attentionOriginTransport(origin plugin.AttentionOrigin) uirequest.Origin {
	return uirequest.Origin{
		TmuxSession: origin.TmuxSession, ProjectKey: origin.ProjectKey,
		WorkDir: origin.WorkDir, HostID: origin.HostID,
	}
}

func (m *Model) publishAttentionIfChanged() tea.Cmd {
	if !m.attentionTracking {
		return nil
	}
	next := m.currentAttention()
	if m.attentionPublished == next {
		return nil
	}
	m.attentionPublished = next
	record := uirequest.Attention{
		PID: os.Getpid(), Host: uirequest.HostName(), Focused: next.focused,
		VisibleOrigin: next.origin, UpdatedAt: time.Now().UTC(),
	}
	return func() tea.Msg {
		_ = uirequest.PublishAttention(config.StateDir(), record)
		return nil
	}
}

// attachAttentionPublish is a postlude around the app's many established
// Update returns. It compares in-memory state only; filesystem publication is
// always a tea.Cmd and runs after Update returns.
func attachAttentionPublish(model tea.Model, cmd tea.Cmd) (tea.Model, tea.Cmd) {
	var attentionCmd tea.Cmd
	switch typed := model.(type) {
	case Model:
		attentionCmd = (&typed).publishAttentionIfChanged()
		model = typed
	case *Model:
		if typed != nil {
			attentionCmd = typed.publishAttentionIfChanged()
		}
	}
	if attentionCmd == nil {
		return model, cmd
	}
	return model, tea.Batch(cmd, attentionCmd)
}

func quitWithInstanceWithdrawal() tea.Cmd {
	return tea.Sequence(func() tea.Msg {
		_ = uirequest.WithdrawAttention(config.StateDir(), os.Getpid())
		_ = uirequest.Withdraw(config.StateDir(), os.Getpid())
		return nil
	}, tea.Quit)
}
