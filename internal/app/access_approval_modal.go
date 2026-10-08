package app

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/accessmodal"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/ui"
)

// accessApprovalContext is the keymap context while the approval modal holds
// the keyboard.
const accessApprovalContext = "access-approval"

// accessApprovalClient is the seam tests use instead of the Local socket.
var accessApprovalClient accessmodal.Client

// openAccessApproval opens the browser-access approval modal: the TUI's
// projection of the UI API's access-request routes. It is reached by
// activating an access_request notification and nowhere else.
func (m *Model) openAccessApproval() tea.Cmd {
	if m.hasModal() {
		return nil
	}
	m.accessApproval = accessmodal.New(config.StateDir(), accessApprovalClient)
	m.accessApproval.Ensure(m.width)
	m.updateContext()
	return m.accessApproval.Load()
}

func (m *Model) closeAccessApproval() {
	m.accessApproval = nil
	m.updateContext()
}

// isAccessRequest reports whether n is a browser waiting for approval.
func isAccessRequest(n notify.Notification) bool {
	return n.Source == notify.SourceAccessRequest
}

func (m *Model) handleAccessApprovalKey(msg tea.KeyPressMsg) tea.Cmd {
	if m.accessApproval == nil {
		return nil
	}
	if isMouseEscapeSequence(msg) {
		return nil
	}
	action, cmd := m.accessApproval.HandleKey(msg, m.width)
	return m.accessApprovalAction(action, cmd)
}

func (m *Model) handleAccessApprovalMouse(msg tea.MouseMsg) tea.Cmd {
	if m.accessApproval == nil {
		return nil
	}
	return m.accessApprovalAction(m.accessApproval.HandleMouse(msg, m.width), nil)
}

func (m *Model) accessApprovalAction(action string, cmd tea.Cmd) tea.Cmd {
	switch action {
	case accessmodal.ActionCancel:
		m.closeAccessApproval()
		return nil
	case accessmodal.ActionApprove:
		return m.accessApproval.Submit()
	}
	return cmd
}

func (m *Model) applyAccessApproved(msg accessmodal.ApprovedMsg) tea.Cmd {
	if m.accessApproval == nil {
		return nil
	}
	done, flash, cmd := m.accessApproval.ApplyApproved(msg)
	if !done {
		return cmd
	}
	m.closeAccessApproval()
	return ShowFlashFrom(string(notify.SourceAccessRequest), flash)
}

func (m *Model) renderAccessApproval(content string) string {
	if m.accessApproval == nil {
		return content
	}
	return ui.OverlayModal(content, m.accessApproval.Render(m.width, m.height), m.width, m.height)
}
