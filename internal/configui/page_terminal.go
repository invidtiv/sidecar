package configui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/tty"
)

// Terminal is the behavior Sidecar itself owns inside the embedded terminal:
// the chords that leave it, copy from it, and paste into it, and how much of a
// pane it captures for a preview.
//
// Every chord here is resolved into a terminal host when that host is built
// (internal/app.TerminalConfig), so a change reaches the next terminal rather
// than the one already on screen. The page says that once.

const (
	regionExitKey      = "config-terminal-exit-key"
	regionAttachKey    = "config-terminal-attach-key"
	regionCopyKey      = "config-terminal-copy-key"
	regionPasteKey     = "config-terminal-paste-key"
	regionCopyOnSelect = "config-terminal-copy-on-select"
	regionCaptureLimit = "config-terminal-capture-limit"

	keyFieldWidth = 24
)

// terminalState is the page's editors and the last validation complaint.
type terminalState struct {
	fields map[string]*textinput.Model
	// invalid is the control whose value was refused, with the reason.
	invalid string
	reason  string
}

func (m *Model) terminal() *terminalState {
	if m.terminalState == nil {
		m.terminalState = &terminalState{fields: map[string]*textinput.Model{}}
	}
	return m.terminalState
}

func (m *Model) keyField(id string) *textinput.Model {
	state := m.terminal()
	input, ok := state.fields[id]
	if !ok {
		field := textinput.New()
		field.Prompt = ""
		field.CharLimit = 40
		input = &field
		state.fields[id] = input
	}
	return input
}

// attachAvailable reports whether attaching to tmux is something the user can
// configure. It mirrors app.TerminalConfig exactly: without the full-attach
// feature the attach chord is cleared before any terminal sees it, so offering
// an editable value here would be a setting that does nothing.
func attachAvailable() bool { return features.IsEnabled(features.TmuxFullAttach.Name) }

// terminalKey resolves a configured chord, falling back to the same default
// tty.Config uses so the page shows what a terminal would actually answer to.
func terminalKey(configured, fallback string) string {
	if trimmed := strings.TrimSpace(configured); trimmed != "" {
		return trimmed
	}
	return fallback
}

// editKey opens a chord for editing. A value Sidecar could never match is
// refused with the reason, and the editor stays open on it.
func (m *Model) editKey(id, current string, save func(*config.WorkspacePluginConfig, string)) {
	input := m.keyField(id)
	input.SetValue(current)
	state := m.terminal()
	state.invalid, state.reason = "", ""
	m.openEditor(&editorState{
		id:    id,
		input: input,
		submit: func(m *Model) (tea.Cmd, bool) {
			value := strings.TrimSpace(m.keyField(id).Value())
			state := m.terminal()
			if err := ValidateInteractiveKey(value); err != nil {
				state.invalid, state.reason = id, err.Error()
				return nil, true
			}
			state.invalid, state.reason = "", ""
			return SaveCmd("Saved "+FormatKeyLabel(value), func() error {
				return config.SaveWorkspace(func(ws *config.WorkspacePluginConfig) { save(ws, value) })
			}), false
		},
		cancel: func(m *Model) {
			state := m.terminal()
			state.invalid, state.reason = "", ""
		},
	})
	m.focusControlByID(id)
}

// keyRow paints one chord: its current value, editable in place.
func (m *Model) keyRow(b *paneBuilder, id, label, current string, save func(*config.WorkspacePluginConfig, string)) {
	b.row(id, "", func(m *Model) tea.Cmd {
		m.editKey(id, current, save)
		return nil
	}, func(s State) string {
		if m.editingID() == id {
			s.Focused = true
			return FormRow(label, Field(m.keyField(id), keyFieldWidth, s), s)
		}
		return FormRow(label, StaticField(FormatKeyLabel(current), b.controlWidth(keyFieldWidth), s), s)
	})
	if state := m.terminal(); state.invalid == id {
		b.help(Warning(state.reason))
	}
}

func (m *Model) buildTerminal(b *paneBuilder) {
	ws := m.Config().Plugins.Workspace
	defaults := tty.DefaultConfig()

	b.text(PaneTitle(PageTitle(PageTerminal)), "")
	b.lead("Set the terminal behavior Sidecar owns.")

	b.text(SectionHeader("Interaction"))

	m.keyRow(b, regionExitKey, "Exit interactive mode", terminalKey(ws.InteractiveExitKey, defaults.ExitKey),
		func(ws *config.WorkspacePluginConfig, value string) { ws.InteractiveExitKey = value })

	if attachAvailable() {
		m.keyRow(b, regionAttachKey, "Attach to tmux", terminalKey(ws.InteractiveAttachKey, defaults.AttachKey),
			func(ws *config.WorkspacePluginConfig, value string) { ws.InteractiveAttachKey = value })
	} else {
		// Force-disabled, and honest about it: the chord is cleared for every
		// terminal host until the feature preview is on, so the control is not
		// pretending to be editable.
		state := b.declare(regionAttachKey, "", false, nil)
		state.Disabled = true
		b.text(FormRow("Attach to tmux", StaticField("Disabled", b.controlWidth(keyFieldWidth), state), state))
	}
	b.help("Opens the full tmux client instead of Sidecar's embedded terminal.")
	b.help("Leave this off unless you rely on tmux's own interface and shortcuts.")
	if !attachAvailable() {
		b.help("Turn on Full tmux attach under Advanced to configure this chord.")
	}

	m.keyRow(b, regionCopyKey, "Copy selection", terminalKey(ws.InteractiveCopyKey, defaults.CopyKey),
		func(ws *config.WorkspacePluginConfig, value string) { ws.InteractiveCopyKey = value })
	m.keyRow(b, regionPasteKey, "Paste", terminalKey(ws.InteractivePasteKey, defaults.PasteKey),
		func(ws *config.WorkspacePluginConfig, value string) { ws.InteractivePasteKey = value })

	b.row(regionCopyOnSelect, "", func(m *Model) tea.Cmd {
		enabled := !m.Config().Plugins.Workspace.CopyOnSelect
		return SaveCmd(toggleNotice("Copy on select", enabled), func() error {
			return config.SaveWorkspace(func(ws *config.WorkspacePluginConfig) { ws.CopyOnSelect = enabled })
		})
	}, func(s State) string {
		return FormRow("Copy on select", Toggle(ws.CopyOnSelect, s), s)
	})

	b.text(SectionHeader("Capture"))
	// The stored limit need not be a rung of the ladder — Advanced accepts a
	// typed size — so the closed control reports the configured value rather than
	// the nearest choice, and the list opens on nothing when it is off-ladder.
	b.selectRowValue(regionCaptureLimit, "Preview limit", FormatCaptureLimit(ws.TmuxCaptureMaxBytes),
		keyFieldWidth, captureLimitOptions(), strconv.Itoa(NearestCaptureLimit(ws.TmuxCaptureMaxBytes)),
		saveCaptureLimit)
	b.note("Capture limits are advanced controls; Sidecar uses a safe default.")

	b.blank()
	b.lead("These chords are resolved when a terminal is created, so they apply to the next one you open.")
}
