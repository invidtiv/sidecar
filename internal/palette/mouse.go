package palette

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	sharedscroll "github.com/marcus/sidecar/internal/scroll"
)

// Mouse region identifiers
const (
	regionPaletteEntry = "palette-entry" // Individual command entry (Data: entry index int)
)

// rebuildMouseAt returns a copy of the mouse message with X/Y replaced,
// preserving the concrete message type.
func rebuildMouseAt(msg tea.MouseMsg, x, y int) tea.MouseMsg {
	mm := msg.Mouse()
	mm.X, mm.Y = x, y
	switch msg.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg(mm)
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg(mm)
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg(mm)
	case tea.MouseMotionMsg:
		return tea.MouseMotionMsg(mm)
	}
	return msg
}

// handleMouse processes mouse events for the command palette.
func (m *Model) handleMouse(msg tea.MouseMsg) (Model, tea.Cmd) {
	// Ignore events outside modal bounds; the palette absorbs them.
	relX, relY, ok := m.modalCoords(msg)
	if !ok {
		return *m, nil
	}

	// Create adjusted message for hit testing (v2 mouse msgs are interfaces, so
	// rebuild the matching concrete type with relative coordinates).
	action := m.mouseHandler.HandleMouse(rebuildMouseAt(msg, relX, relY))

	switch action.Type {
	case mouse.ActionClick:
		return m.handleMouseClick(action)
	case mouse.ActionDoubleClick:
		return m.handleMouseDoubleClick(action)
	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		return m.handleMouseScroll(action)
	}

	return *m, nil
}

// handleMouseClick handles single click on palette entries.
func (m *Model) handleMouseClick(action mouse.MouseAction) (Model, tea.Cmd) {
	if action.Region == nil || action.Region.ID != regionPaletteEntry {
		return *m, nil
	}

	if idx, ok := action.Region.Data.(int); ok {
		m.cursor = idx
		m.ensureCursorVisible()
	}

	return *m, nil
}

// handleMouseDoubleClick handles double click to execute command.
func (m *Model) handleMouseDoubleClick(action mouse.MouseAction) (Model, tea.Cmd) {
	if action.Region == nil || action.Region.ID != regionPaletteEntry {
		return *m, nil
	}

	if idx, ok := action.Region.Data.(int); ok {
		m.cursor = idx
		// Execute the selected command
		if entry := m.SelectedEntry(); entry != nil {
			return *m, func() tea.Msg {
				return CommandSelectedMsg{
					CommandID: entry.CommandID,
					Context:   entry.Context,
				}
			}
		}
	}

	return *m, nil
}

// handleMouseScroll handles scroll wheel for navigation.
func (m *Model) handleMouseScroll(action mouse.MouseAction) (Model, tea.Cmd) {
	delta := 3
	if action.Type == mouse.ActionScrollUp {
		delta = -3
	}
	m.moveCursor(delta)
	return *m, nil
}

// ensureCursorVisible adjusts offset to keep cursor in view.
func (m *Model) ensureCursorVisible() {
	if m.cursor < m.offset {
		m.offset = m.cursor
	}
	if m.cursor >= m.offset+m.maxVisible {
		m.offset = m.cursor - m.maxVisible + 1
	}
}

// WheelAtBoundary reports whether a wheel event certainly cannot change the
// palette. The palette's wheel moves the cursor over the currently filtered
// entries, so the bound is that cursor against the filtered count. Events
// outside the modal bounds are absorbed by handleMouse and are known no-ops.
//
// It is read-only: no filtering is re-run and no visible state changes. The
// app layer owns whether to call it.
func (m *Model) WheelAtBoundary(msg tea.MouseWheelMsg) bool {
	if m == nil || m.mouseHandler == nil {
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
// reports whether it landed inside the modal. Wheel routing and the boundary
// query share this one geometry.
func (m *Model) modalCoords(msg tea.MouseMsg) (relX, relY int, inside bool) {
	modalWidth := min(80, m.width-4)
	if modalWidth < 40 {
		modalWidth = 40
	}
	modalHeight := 3 + m.maxVisible + 6
	modalX := (m.width - modalWidth) / 2
	modalY := (m.height - modalHeight) / 2
	mi := msg.Mouse()
	relX = mi.X - modalX
	relY = mi.Y - modalY
	inside = relX >= 0 && relY >= 0 && relX < modalWidth && relY < modalHeight
	return relX, relY, inside
}
