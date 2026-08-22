package configui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
)

// Advanced is the technical limits, kept away from ordinary settings on
// purpose. The one performance limit documents the range it will accept before
// a value is saved. Feature flags used to live here too; they are their own
// page now, because the list grows with the registry and this pane truncates.

const (
	regionAdvancedCapture = "config-advanced-capture"

	advancedControlWidth = 48
)

// advancedState is the page's typed capture-limit editor.
type advancedState struct {
	capture textinput.Model
}

func (m *Model) advanced() *advancedState {
	if m.advancedState == nil {
		field := textinput.New()
		field.Prompt = ""
		field.CharLimit = 16
		field.Placeholder = FormatCaptureLimit(CaptureLimitDefault)
		m.advancedState = &advancedState{capture: field}
	}
	return m.advancedState
}

func (m *Model) buildAdvanced(b *paneBuilder) {
	b.text(PaneTitle(PageTitle(PageAdvanced)), "")
	b.lead("Technical controls. Most people never need these.")
	b.blank()
	b.note("Feature flags moved to " + PageTitle(PageFlags) + ".")
	b.blank()

	b.text(SectionHeader("Performance"))
	m.captureRow(b)

	b.blank()
	if m.restartNote != "" {
		b.note(m.restartNote)
	}
	b.lead("Any setting that needs a reload is called out before it is saved.")
}

// captureRow paints the embedded terminal's capture limit. It is the same
// setting the Terminal page steps through — one clamp, one formatter, one
// stored value — offered here as a typed field for a user who wants an exact
// number rather than the next rung.
func (m *Model) captureRow(b *paneBuilder) {
	current := m.Config().Plugins.Workspace.TmuxCaptureMaxBytes
	b.row(regionAdvancedCapture, "", func(m *Model) tea.Cmd {
		m.editCaptureLimit()
		return nil
	}, func(s State) string {
		if m.editingID() == regionAdvancedCapture {
			s.Focused = true
			return FormRow("Terminal preview capture", Field(&m.advanced().capture, advancedControlWidth, s), s)
		}
		return FormRow("Terminal preview capture", StaticField(FormatCaptureLimit(current), b.controlWidth(advancedControlWidth), s), s)
	})
	b.help("Maximum output Sidecar retains to render terminal previews.")
	b.help("Accepts " + CaptureLimitRange() + ". Anything outside that is brought inside it,")
	b.help("and a blank or unreadable value keeps the safe default of " + FormatCaptureLimit(CaptureLimitDefault) + ".")
}

// editCaptureLimit opens the typed field. Whatever is typed is read, clamped,
// and only then saved: this control cannot store a value Sidecar would refuse.
func (m *Model) editCaptureLimit() {
	state := m.advanced()
	state.capture.SetValue(FormatCaptureLimit(m.Config().Plugins.Workspace.TmuxCaptureMaxBytes))
	m.openEditor(&editorState{
		id:    regionAdvancedCapture,
		input: &state.capture,
		submit: func(m *Model) (tea.Cmd, bool) {
			value := ParseCaptureLimit(strings.TrimSpace(m.advanced().capture.Value()))
			return SaveCmd("Preview capture: "+FormatCaptureLimit(value), func() error {
				return config.SaveWorkspace(func(ws *config.WorkspacePluginConfig) {
					ws.TmuxCaptureMaxBytes = value
				})
			}), false
		},
	})
	m.focusControlByID(regionAdvancedCapture)
}
