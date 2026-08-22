package palette

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	sharedscroll "github.com/marcus/sidecar/internal/scroll"
)

// handleMouse processes mouse events for the command palette.
func (m *Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	m.ensureModal()
	if m.modal == nil {
		return *m, nil
	}
	if m.mouseHandler == nil {
		m.mouseHandler = mouse.NewHandler()
	}

	// Handle mouse wheel scrolling directly
	if wheelMsg, ok := msg.(tea.MouseWheelMsg); ok {
		if _, _, inside := m.modalCoords(wheelMsg); inside {
			delta := 3
			if wheelMsg.Button == tea.MouseWheelUp {
				delta = -3
			}
			m.moveCursor(delta)
			m.clearModal()
			return *m, nil
		}
		return *m, nil
	}

	action := m.modal.HandleMouse(msg, m.mouseHandler)

	// The results scrollbar's gestures (view.go) answer through the same
	// dispatch: presses probe its regions directly and drags are visible in
	// the shared handler's state. Anything it claims is already handled.
	if m.handleScrollbarPointer(msg) {
		m.clearModal()
		return *m, nil
	}

	// Single click on a palette item immediately selects and executes it
	if strings.HasPrefix(action, paletteItemPrefix) {
		var idx int
		if _, err := fmt.Sscanf(action, paletteItemPrefix+"%d", &idx); err == nil {
			if idx >= 0 && idx < len(m.filtered) {
				m.cursor = idx
				entry := m.filtered[idx]
				return *m, func() tea.Msg {
					return CommandSelectedMsg{
						CommandID: entry.CommandID,
						Context:   entry.Context,
						Key:       entry.Key,
					}
				}
			}
		}
		return *m, nil
	}

	switch action {
	case "select":
		if entry := m.SelectedEntry(); entry != nil {
			return *m, func() tea.Msg {
				return CommandSelectedMsg{
					CommandID: entry.CommandID,
					Context:   entry.Context,
					Key:       entry.Key,
				}
			}
		}
	}

	return *m, nil
}

// WheelAtBoundary reports whether a wheel event certainly cannot change the
// palette. The palette's wheel moves the cursor over the currently filtered
// entries, so the bound is that cursor against the filtered count. Events
// outside the modal bounds are absorbed and are known no-ops.
func (m *Model) WheelAtBoundary(msg tea.MouseWheelMsg) bool {
	if m == nil {
		return false
	}
	if _, _, ok := m.modalCoords(msg); !ok {
		return true
	}
	delta := 3
	if msg.Button == tea.MouseWheelUp {
		delta = -3
	} else if msg.Button != tea.MouseWheelDown {
		return false
	}
	if len(m.filtered) == 0 {
		return true
	}
	return (sharedscroll.Bounds{Position: m.cursor, Maximum: len(m.filtered) - 1}).AtBoundary(delta)
}

// modalCoords translates a mouse event into palette-modal coordinates and
// reports whether it landed inside the modal.
func (m *Model) modalCoords(msg tea.MouseMsg) (relX, relY int, inside bool) {
	modalWidth := 74
	if modalWidth > m.width-4 {
		modalWidth = m.width - 4
	}
	if modalWidth < 36 {
		modalWidth = 36
	}
	modalHeight := min(m.height-4, 3+m.maxVisible+6)
	modalX := (m.width - modalWidth) / 2
	modalY := (m.height - modalHeight) / 2
	mi := msg.Mouse()
	relX = mi.X - modalX
	relY = mi.Y - modalY
	inside = relX >= 0 && relY >= 0 && relX < modalWidth && relY < modalHeight
	return relX, relY, inside
}
