package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/hostnotify"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/notify"
	"time"
)

func (m *Model) forwardHostNotifications(update hosts.Update) tea.Cmd {
	events := hostnotify.Adapt(update, m.managedHostNotificationsEnabled(), time.Now().UTC())
	var cmds []tea.Cmd
	for _, n := range events.Post {
		cmds = append(cmds, func() tea.Msg { return notify.PostMsg{Notification: n} })
	}
	for _, id := range events.Dismiss {
		cmds = append(cmds, func() tea.Msg { return notify.DismissMsg{ID: id} })
	}
	for _, key := range events.DismissTransitions {
		cmds = append(cmds, func() tea.Msg { return notify.DismissTransitionMsg{DedupeKey: key} })
	}
	if len(cmds) == 0 {
		return nil
	}
	return tea.Batch(cmds...)
}
func (m *Model) managedHostNotificationsEnabled() bool {
	return m.config != nil && m.config.Notifications.SSH.ManagedHosts
}
