package configui

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

// Advanced is the feature previews and the technical limits, kept away from
// ordinary settings on purpose. Every control here says what it costs: a
// preview that is only read at startup says so, and the one performance limit
// documents the range it will accept before a value is saved.

const (
	regionAdvancedFlag    = "config-advanced-flag-"
	regionAdvancedCapture = "config-advanced-capture"

	advancedControlWidth = 48
)

// preview is one feature flag offered on Advanced.
type preview struct {
	// flag is the feature name in features.flags.
	flag string
	// label is what the user reads.
	label string
	// help is the input-aligned explanation under the control.
	help string
	// restart marks a flag whose consumer reads it once at startup, so the
	// change is real but not visible until Sidecar is restarted. It is set per
	// flag from what actually consumes it, never as blanket caution.
	restart bool
	// note is an honest scope line for a flag that applies live but not
	// retroactively.
	note string
}

// previews are the four flags the mockup lists, in its order.
//
// Restart accuracy, checked against each flag's consumers:
//   - cross_project_overview is read once in app.New to decide whether the
//     cross-project surface is constructed at all → restart.
//   - workspace_doc_panes is checked live wherever a pane or a diff is opened
//     (workspace.Plugin, internal/overview) → immediate.
//   - tmux_full_attach is checked live, but a terminal resolves its chords when
//     it is created (app.TerminalConfig) → immediate, next terminal.
//   - workspace_terminal_panel is checked live every time the split panel is
//     shown or toggled → immediate.
func previews() []preview {
	return []preview{
		{
			flag:    features.CrossProjectOverview.Name,
			label:   "Cross-project Activity",
			help:    "Show workspaces from every configured project in Activity.",
			restart: true,
		},
		{
			flag:  features.WorkspaceDocPanes.Name,
			label: "Document panes",
			help:  "Open files, issues, and diffs beside your active workspace.",
		},
		{
			flag:  features.TmuxFullAttach.Name,
			label: "Full tmux attach",
			help:  "Hand the terminal over to tmux's native client and shortcuts.",
			note:  "Applies to terminals opened after the change, and unlocks the attach chord on Terminal.",
		},
		{
			flag:  features.WorkspaceTerminalPanel.Name,
			label: "Split workspace terminal",
			help:  "Show a dedicated terminal next to the workspace list.",
		},
	}
}

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
	b.lead("Feature previews and technical controls. Most people never need these.")

	b.text(SectionHeader("Feature previews"))
	for _, item := range previews() {
		m.previewRow(b, item)
	}

	b.text(SectionHeader("Performance"))
	m.captureRow(b)

	b.blank()
	if m.restartNote != "" {
		b.note(m.restartNote)
	}
	b.lead("Any setting that needs a reload is called out before it is saved.")
}

// previewRow paints one flag with its aligned toggle and its explanation.
func (m *Model) previewRow(b *paneBuilder, item preview) {
	enabled := m.flagEnabled(item.flag)
	b.row(regionAdvancedFlag+item.flag, "", func(m *Model) tea.Cmd {
		next := !m.flagEnabled(item.flag)
		// The restart requirement is stated at save time, next to the control
		// that needs it, and only for the flags that genuinely need it.
		if item.restart {
			m.noteRestart()
		}
		return saveFlagCmd(toggleNotice(item.label, next), item.flag, next)
	}, func(s State) string {
		return FormRow(item.label, ToggleWidth(enabled, b.controlWidth(advancedControlWidth), s), s)
	})
	b.help(item.help)
	if item.restart {
		b.help("Read once when Sidecar starts, so a change takes effect after a restart.")
	} else if item.note != "" {
		b.help(item.note)
	}
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
