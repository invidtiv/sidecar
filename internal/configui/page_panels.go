package configui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

// Panels & Integrations is which Sidecar surfaces exist. Every switch here is
// read once, when Sidecar assembles its plugins at startup (assembly.Plan), so
// a change is honest about needing a restart rather than pretending to take
// effect and quietly doing nothing.
//
// The switches are not all the same setting underneath — Git, Files, and td are
// plugin config; Notes and Tasks are feature flags; Conversations is both — but
// a user is choosing one thing, so the page offers one control per surface and
// keeps the underlying pair consistent itself.

const (
	regionPanel            = "config-panel-"
	regionPanelGitRefresh  = "config-panel-git-refresh"
	regionPanelTDPath      = "config-panel-td-path"
	regionPanelTDRefresh   = "config-panel-td-refresh"
	regionPanelConvDir     = "config-panel-conversations-dir"
	regionPanelNotesEditor = "config-panel-notes-editor"
	panelInputWidth        = 40
	panelRestartNote       = "Takes effect after Sidecar restarts."
	panelIDGit             = "git"
	panelIDFiles           = "files"
	panelIDTD              = "td"
	panelIDNotes           = "notes"
	panelIDConversations   = "conversations"
	panelIDTasks           = "tasks"
	tdCommandName          = "td"
	conversationsFlagLabel = "Conversations panel"
)

// refreshChoices are the poll intervals the refresh selectors offer.
// They are a ladder rather than a free number because the only meaningful
// choices here are "keep up" and "stay out of the way".
var refreshChoices = []time.Duration{
	500 * time.Millisecond,
	time.Second,
	2 * time.Second,
	5 * time.Second,
	15 * time.Second,
	30 * time.Second,
}

func formatRefresh(d time.Duration) string {
	if d <= 0 {
		return "default"
	}
	return d.String()
}

// panelsState holds the page's path editors.
type panelsState struct {
	fields map[string]*textinput.Model
}

func (m *Model) panels() *panelsState {
	if m.panelsState == nil {
		m.panelsState = &panelsState{fields: map[string]*textinput.Model{}}
	}
	return m.panelsState
}

func (m *Model) panelField(id, placeholder string) *textinput.Model {
	state := m.panels()
	input, ok := state.fields[id]
	if !ok {
		field := textinput.New()
		field.Prompt = ""
		field.CharLimit = 200
		field.Placeholder = placeholder
		input = &field
		state.fields[id] = input
	}
	return input
}

// noteRestart records that the change just made needs a restart, next to the
// page that made it.
func (m *Model) noteRestart() { m.restartNote = panelRestartNote }

func (m *Model) buildPanels(b *paneBuilder) {
	cfg := m.Config()

	b.text(PaneTitle(PageTitle(PagePanels)), "")
	b.lead("Choose the Sidecar surfaces you want available.")
	b.blank()

	// Git ------------------------------------------------------------------
	m.panelRow(b, panelIDGit, "Git", "", "Status, commits, branches, and diffs",
		cfg.Plugins.GitStatus.Enabled, func(m *Model) tea.Cmd {
			enabled := !m.Config().Plugins.GitStatus.Enabled
			m.noteRestart()
			return SaveCmd(toggleNotice("Git panel", enabled), func() error {
				return config.SavePlugins(func(p *config.PluginsConfig) { p.GitStatus.Enabled = enabled })
			})
		})
	if cfg.Plugins.GitStatus.Enabled {
		m.refreshRow(b, regionPanelGitRefresh, cfg.Plugins.GitStatus.RefreshInterval,
			func(p *config.PluginsConfig, next time.Duration) { p.GitStatus.RefreshInterval = next })
	}
	b.blank()

	// Files ----------------------------------------------------------------
	m.panelRow(b, panelIDFiles, "Files", "", "Project browser and inline editing",
		cfg.Plugins.FileBrowser.Enabled, func(m *Model) tea.Cmd {
			enabled := !m.Config().Plugins.FileBrowser.Enabled
			m.noteRestart()
			return SaveCmd(toggleNotice("Files panel", enabled), func() error {
				return config.SavePlugins(func(p *config.PluginsConfig) { p.FileBrowser.Enabled = enabled })
			})
		})
	b.blank()

	// td -------------------------------------------------------------------
	m.panelRow(b, panelIDTD, "td", "", "Issues and task state from the current project",
		cfg.Plugins.TDMonitor.Enabled, func(m *Model) tea.Cmd {
			enabled := !m.Config().Plugins.TDMonitor.Enabled
			m.noteRestart()
			return SaveCmd(toggleNotice("td panel", enabled), func() error {
				return config.SavePlugins(func(p *config.PluginsConfig) { p.TDMonitor.Enabled = enabled })
			})
		})
	// A panel whose supporting tool is missing is said out loud rather than
	// left to render an empty tab the user has to interpret.
	if m.probed && !m.commandFound(tdCommandName) {
		b.note("td is not on PATH, so the panel can only offer setup guidance.")
	}
	if cfg.Plugins.TDMonitor.Enabled {
		m.pathRow(b, regionPanelTDPath, "Database", cfg.Plugins.TDMonitor.DBPath, ".todos/issues.db",
			func(p *config.PluginsConfig, value string) { p.TDMonitor.DBPath = value })
		b.help("Relative paths are resolved inside the current project.")
		m.refreshRow(b, regionPanelTDRefresh, cfg.Plugins.TDMonitor.RefreshInterval,
			func(p *config.PluginsConfig, next time.Duration) { p.TDMonitor.RefreshInterval = next })
	}
	b.blank()

	// Notes ----------------------------------------------------------------
	notes := NotesIntegration()
	notesDetail := "Project notes, kept inside Sidecar"
	if !cfg.Plugins.TDMonitor.Enabled {
		notesDetail += "; available when the td panel is on"
	}
	m.panelRow(b, panelIDNotes, notes.Name, "", notesDetail,
		m.flagEnabled(notes.Flag), func(m *Model) tea.Cmd {
			// Notes ships inside Sidecar: there is no command to look for and
			// nothing to install, so the toggle is the whole story.
			enabled := !m.flagEnabled(notes.Flag)
			m.noteRestart()
			return saveFlagCmd(toggleNotice("Notes panel", enabled), notes.Flag, enabled)
		})
	b.selectRow(regionPanelNotesEditor, "Default editor", panelInputWidth, []dropdownOption{
		{id: config.NotesEditorBuiltin, label: "Built-in"},
		{id: config.NotesEditorPane, label: "$EDITOR in pane"},
	}, cfg.Plugins.Notes.DefaultEditor, func(m *Model, option dropdownOption) tea.Cmd {
		return SaveCmd("Notes editor: "+option.label, func() error {
			return config.SavePlugins(func(p *config.PluginsConfig) {
				p.Notes.DefaultEditor = option.id
			})
		})
	})
	b.help("Enter and note-body clicks use this editor; i, e, and E remain explicit choices.")
	b.blank()

	// Conversations --------------------------------------------------------
	m.panelRow(b, panelIDConversations, "Conversations", "", "Session history from supported agent harnesses",
		m.conversationsOn(), func(m *Model) tea.Cmd { return m.toggleConversations() })
	if m.conversationsOn() {
		m.pathRow(b, regionPanelConvDir, "Source directory", cfg.Plugins.Conversations.ClaudeDataDir, "~/.claude",
			func(p *config.PluginsConfig, value string) { p.Conversations.ClaudeDataDir = value })
		b.help("Where Sidecar looks for agent session history.")
	}
	b.blank()

	// Tasks ----------------------------------------------------------------
	tasks := TasksIntegration()
	m.panelRow(b, panelIDTasks, tasks.Name, BetaBadge(), "Embedded Tasks global tab, backed by the Tasks command",
		m.flagEnabled(tasks.Flag), func(m *Model) tea.Cmd { return m.toggleIntegration(tasks) })
	if m.flagEnabled(tasks.Flag) && m.probed && !m.commandFound(tasks.Descriptor.Executable) {
		b.note("The tasks command is not on PATH, so the tab will have nothing to show.")
	}

	b.blank()
	if m.restartNote != "" {
		b.note(m.restartNote)
	}
	b.lead("Sidecar decides which panels to build when it starts, so these switches apply on the next launch.")
}

// FocusNotesPreference opens the page that owns Notes enablement and puts the
// detail cursor on its existing toggle. Setup surfaces use this rather than
// inventing a second Notes setting.
func (m *Model) FocusNotesPreference() {
	m.Navigate(PagePanels)
	m.detailFocus = true
	m.focusControlByID(regionPanel + panelIDNotes)
}

// panelRow paints one surface as a two-line block. The ON/OFF pill is the
// only click target that toggles; the rest of the row is for focus and hover.
func (m *Model) panelRow(b *paneBuilder, id, title, badge, detail string, on bool, run func(*Model) tea.Cmd) {
	b.panelToggle(regionPanel+id, title, badge, detail, on, run)
}

// refreshRow paints a poll-interval selector under its panel.
func (m *Model) refreshRow(b *paneBuilder, id string, current time.Duration, set func(*config.PluginsConfig, time.Duration)) {
	options := make([]dropdownOption, 0, len(refreshChoices))
	for _, choice := range refreshChoices {
		options = append(options, dropdownOption{id: choice.String(), label: formatRefresh(choice)})
	}
	b.selectRowValue(id, "Refresh", formatRefresh(current), panelInputWidth, options, current.String(),
		func(m *Model, option dropdownOption) tea.Cmd {
			next, err := time.ParseDuration(option.id)
			if err != nil {
				return nil
			}
			return SaveCmd("Refresh every "+formatRefresh(next), func() error {
				return config.SavePlugins(func(p *config.PluginsConfig) { set(p, next) })
			})
		})
}

// pathRow paints an editable path under its panel.
func (m *Model) pathRow(b *paneBuilder, id, label, current, placeholder string, set func(*config.PluginsConfig, string)) {
	b.row(id, "", func(m *Model) tea.Cmd {
		m.editPanelPath(id, label, current, placeholder, set)
		return nil
	}, func(s State) string {
		if m.editingID() == id {
			s.Focused = true
			return FormRow(label, Field(m.panelField(id, placeholder), panelInputWidth, s), s)
		}
		value := current
		if strings.TrimSpace(value) == "" {
			value = placeholder
		}
		return FormRow(label, StaticField(value, b.controlWidth(panelInputWidth), s), s)
	})
}

// editPanelPath opens a path for editing. An empty value clears the override,
// which is what returns the setting to Sidecar's default.
func (m *Model) editPanelPath(id, label, current, placeholder string, set func(*config.PluginsConfig, string)) {
	input := m.panelField(id, placeholder)
	input.SetValue(current)
	m.openEditor(&editorState{
		id:    id,
		input: input,
		submit: func(m *Model) (tea.Cmd, bool) {
			value := strings.TrimSpace(m.panelField(id, placeholder).Value())
			notice := label + " saved"
			if value == "" {
				notice = label + " reset to the default"
			}
			m.noteRestart()
			return SaveCmd(notice, func() error {
				return config.SavePlugins(func(p *config.PluginsConfig) { set(p, value) })
			}), false
		},
	})
	m.focusControlByID(id)
}

// conversationsOn reports whether Sidecar would actually build the
// Conversations panel: assembly.ConversationsWanted requires the feature flag
// and the plugin's own enabled bool, so a single ON must mean both.
func (m *Model) conversationsOn() bool {
	return m.flagEnabled(features.ConversationsPlugin.Name) && m.Config().Plugins.Conversations.Enabled
}

// toggleConversations keeps the two switches behind one control consistent.
//
// Turning it ON sets both, because either one alone leaves the panel invisible
// and the user's choice unhonoured. Turning it OFF clears only the plugin's
// enabled bool: that is the setting Sidecar's own assembly reads first, and
// leaving the feature flag alone means a user who opted into the preview keeps
// that opt-in rather than having it silently revoked by a panel toggle.
func (m *Model) toggleConversations() tea.Cmd {
	enabled := !m.conversationsOn()
	m.noteRestart()
	notice := toggleNotice(conversationsFlagLabel, enabled)
	if !enabled {
		return SaveCmd(notice, func() error {
			return config.SavePlugins(func(p *config.PluginsConfig) { p.Conversations.Enabled = false })
		})
	}
	return SaveCmd(notice, func() error {
		if err := config.SavePlugins(func(p *config.PluginsConfig) { p.Conversations.Enabled = true }); err != nil {
			return err
		}
		return features.SetEnabled(features.ConversationsPlugin.Name, true)
	})
}
