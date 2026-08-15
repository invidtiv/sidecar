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

	local := offsetMouseY(msg, -headerHeight)
	wheel, ok := local.(tea.MouseWheelMsg)
	if !ok {
		return false
	}
	if m.inGlobalScope() {
		if m.globalWorkspacesVisible() && m.overview != nil {
			return m.overview.WorkspacesWheelAtBoundary(wheel)
		}
		if consumer, ok := m.globalTasksPlugin().(plugin.WheelBoundaryConsumer); ok {
			return consumer.WheelAtBoundary(wheel)
		}
		return false
	}
	consumer, ok := m.ActivePlugin().(plugin.WheelBoundaryConsumer)
	return ok && consumer.WheelAtBoundary(wheel)
}
