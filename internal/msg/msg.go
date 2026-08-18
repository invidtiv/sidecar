package msg

import (
	"time"

	tea "charm.land/bubbletea/v2"
)

// ToastMsg displays a temporary message.
type ToastMsg struct {
	Message  string
	Duration time.Duration
	IsError  bool // true for error toasts (red), false for success (green)
}

// ShowToast returns a command to show a toast message.
func ShowToast(message string, duration time.Duration) tea.Cmd {
	return func() tea.Msg {
		return ToastMsg{
			Message:  message,
			Duration: duration,
		}
	}
}

// ThemeChangedMsg is sent when the active theme palette has changed (via preview,
// restore, confirmation, or project switch).
type ThemeChangedMsg struct{}

// ThemeChanged returns a command that sends ThemeChangedMsg.
func ThemeChanged() tea.Cmd {
	return func() tea.Msg {
		return ThemeChangedMsg{}
	}
}

