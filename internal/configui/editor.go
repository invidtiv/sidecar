package configui

import (
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

// An inline editor is any field on a Configuration page that owns typed
// characters: the sidebar's Search, a theme picker's filter, the Add Project
// form's Name and Location, the terminal-title template.
//
// While one is open the surface reports the config-edit context, which the host
// registers as a text-input context — so a typed "r" is an r, not Recheck, and
// never a global shortcut.

type editorState struct {
	// id is the control the editor belongs to, so the page can render the field
	// it is editing as focused.
	id    string
	input *textinput.Model
	// change runs after every keystroke: live filtering, live completion.
	change func(*Model)
	// submit runs on Enter. Returning true keeps the editor open.
	submit func(*Model) (tea.Cmd, bool)
	// cancel runs on Escape, before the editor closes.
	cancel func(*Model)
	// keys is the field's own key handling, consulted before the text input
	// sees anything — Tab accepting a completion, Down leaving for a list.
	keys func(*Model, tea.KeyPressMsg) (bool, tea.Cmd)
}

// editing reports that a field owns the keyboard.
func (m *Model) editing() bool { return m.editor != nil }

// editingID is the control currently being edited, or "".
func (m *Model) editingID() string {
	if m.editor == nil {
		return ""
	}
	return m.editor.id
}

// openEditor gives a field the keyboard.
func (m *Model) openEditor(state *editorState) {
	if m.editor != nil && m.editor.input != nil {
		m.editor.input.Blur()
	}
	m.editor = state
	if state != nil && state.input != nil {
		state.input.Focus()
	}
}

// closeEditor returns the keyboard to the page. It does not run cancel: the
// caller decides whether leaving means abandoning the value.
func (m *Model) closeEditor() {
	if m.editor == nil {
		return
	}
	if m.editor.input != nil {
		m.editor.input.Blur()
	}
	m.editor = nil
}

// cancelEditor abandons the field the way Escape does.
func (m *Model) cancelEditor() bool {
	if m.editor == nil {
		return false
	}
	cancel := m.editor.cancel
	m.closeEditor()
	if cancel != nil {
		cancel(m)
	}
	return true
}

// editorKey routes a key to the open editor.
func (m *Model) editorKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	editor := m.editor
	if editor == nil {
		return false, nil
	}
	if editor.keys != nil {
		if handled, cmd := editor.keys(m, msg); handled {
			return true, cmd
		}
	}
	switch msg.String() {
	case "esc":
		m.cancelEditor()
		return true, nil
	case "enter":
		if editor.submit == nil {
			m.closeEditor()
			return true, nil
		}
		cmd, keepOpen := editor.submit(m)
		if !keepOpen {
			m.closeEditor()
		}
		return true, cmd
	}
	if editor.input == nil {
		return true, nil
	}
	var cmd tea.Cmd
	*editor.input, cmd = editor.input.Update(msg)
	if editor.change != nil {
		editor.change(m)
	}
	return true, cmd
}

// Field renders a text field at the shared control column. An editable field
// looks editable at rest and unmistakably active while it owns the keyboard.
func Field(input *textinput.Model, width int, state State) string {
	value := ""
	if input != nil {
		input.SetWidth(max(1, width-2))
		value = input.View()
	}
	style := lipgloss.NewStyle().
		Foreground(styles.TextPrimary).
		Background(styles.SurfaceRaised).
		Padding(0, 1)
	switch {
	case state.Focused:
		style = style.Background(styles.BgTertiary).Bold(true)
	case state.Hovered:
		style = style.Background(styles.BgTertiary)
	}
	return style.Width(width).Render(value)
}

// StaticField renders a value that is edited elsewhere — a placeholder for an
// unfocused field, drawn like the field it becomes.
func StaticField(value string, width int, state State) string {
	if value == "" {
		value = " "
	}
	if ansi.StringWidth(value) > width-2 {
		value = ansi.Truncate(value, max(1, width-3), "…")
	}
	style := lipgloss.NewStyle().
		Foreground(styles.TextPrimary).
		Background(styles.SurfaceRaised).
		Padding(0, 1)
	switch {
	case state.Focused:
		style = style.Background(styles.BgTertiary).Bold(true)
	case state.Hovered:
		style = style.Background(styles.BgTertiary)
	}
	return style.Width(width).Render(value)
}
