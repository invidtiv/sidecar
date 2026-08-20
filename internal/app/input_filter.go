package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
)

// FilterInput is the Bubble Tea pre-update filter. Boundary wheel events are
// discarded here so trackpad and Magic Mouse inertia cannot make Update and
// View repaint an already stationary surface hundreds of times.
func FilterInput(model tea.Model, msg tea.Msg) tea.Msg {
	wheel, ok := msg.(tea.MouseWheelMsg)
	if !ok {
		return msg
	}

	var m Model
	switch current := model.(type) {
	case Model:
		m = current
	case *Model:
		if current == nil {
			return msg
		}
		m = *current
	default:
		return msg
	}
	if m.wheelAtBoundary(wheel) {
		return nil
	}
	return msg
}

func (m Model) wheelAtBoundary(msg tea.MouseWheelMsg) bool {
	// An open app-level overlay owns every mouse event: the plugin underneath
	// it must never be consulted. Each ModalKind answers for itself, in the same
	// precedence order Update and View use.
	if m.hasModal() {
		return m.activeModalWheelAtBoundary(msg)
	}
	// The centre is a reserved column, not a modal: a wheel over it must not
	// be answered by the plugin underneath, and an inertial tail at its
	// boundary must be dropped here the same way every other surface's is.
	if bounded, over := (&m).notificationCentreWheelAtBoundary(msg); over {
		return bounded
	}

	local := offsetMouseY(msg, -headerHeight)
	wheel, ok := local.(tea.MouseWheelMsg)
	if !ok {
		return false
	}
	if m.configOpen() {
		return m.config.WheelAtBoundary(wheel)
	}
	if m.inGlobalScope() {
		// Mirror globalMouse's precedence exactly. Asking a surface that is not
		// the visible tab would answer for something off screen: once Tasks
		// gains a boundary contract, a wheel over the Agents board would be
		// answered by Tasks and legitimately swallowed.
		switch {
		case m.globalTasksFocused():
			if consumer, ok := m.globalTasksPlugin().(plugin.WheelBoundaryConsumer); ok {
				return consumer.WheelAtBoundary(wheel)
			}
			return false
		case m.globalTab == GlobalActivity && m.overview != nil:
			return m.overview.BoardWheelAtBoundary(wheel)
		case m.globalWorkspacesVisible():
			return m.overview.WorkspacesWheelAtBoundary(wheel)
		}
		return false
	}
	consumer, ok := m.ActivePlugin().(plugin.WheelBoundaryConsumer)
	return ok && consumer.WheelAtBoundary(wheel)
}
