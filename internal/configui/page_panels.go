package configui

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
)

// Panels & Integrations is which Sidecar surfaces exist. Every switch here is
// read once, when Sidecar assembles its plugins at startup (assembly.Plan), so
// a change is honest about needing a restart rather than pretending to take
// effect and quietly doing nothing.
//
// The page is one loop over plugin descriptors. It used to be a hand-written
// block per surface, which is why the switches underneath were not all the same
// setting: some were plugin config, some were feature flags, and one was both.
// Enablement is plugins.<id>.enabled for every plugin now, and the descriptor
// answers both "is it on" and "how do I write that" — so a new plugin is a
// descriptor, not another block here.

const (
	regionPanel             = "config-panel-"
	regionPanelGitRefresh   = "config-panel-git-refresh"
	regionPanelTDPath       = "config-panel-td-path"
	regionPanelTDRefresh    = "config-panel-td-refresh"
	regionPanelConvDir      = "config-panel-conversations-dir"
	regionPanelNotesEditor  = "config-panel-notes-editor"
	panelInputWidth         = 40
	panelRestartNote        = "Takes effect after Sidecar restarts."
	panelInstallSuffix      = "-install"
	regionPanelTasksInstall = regionPanel + panelIDTasks + panelInstallSuffix
	tdCommandName           = "td"
	conversationsFlagLabel  = "Conversations panel"
)

// Panel IDs are the descriptor IDs, so a region ID names the same plugin the
// config key does.
const (
	panelIDGit           = "git-status"
	panelIDFiles         = "file-browser"
	panelIDTD            = "td-monitor"
	panelIDNotes         = "notes"
	panelIDConversations = "conversations"
	panelIDTasks         = "tasks"
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

// SetPluginDescriptors gives the surface the plugin catalog it renders.
//
// It is injected rather than read directly because the plugin packages import
// internal/app, which owns this surface, so importing the catalog here would be
// an import cycle. The host passes assembly.Descriptors().
func (m *Model) SetPluginDescriptors(descriptors []plugin.Descriptor) {
	m.pluginDescriptors = descriptors
}

// PluginDescriptors is the catalog this surface renders.
func (m *Model) PluginDescriptors() []plugin.Descriptor { return m.pluginDescriptors }

// panelDescriptor finds one descriptor by ID.
func (m *Model) panelDescriptor(id string) (plugin.Descriptor, bool) {
	for _, d := range m.pluginDescriptors {
		if d.ID == id {
			return d, true
		}
	}
	return plugin.Descriptor{}, false
}

// panelOn reports what the user chose for a plugin, which is what the ON/OFF
// pill shows. It is deliberately the preference and not the effective answer:
// Notes needs the td panel, and a Notes row reading OFF because td is off would
// be Sidecar claiming a choice the user never made.
func (m *Model) panelOn(d plugin.Descriptor) bool { return d.IsPreferred(m.Config()) }

// savePanelEnabled writes plugins.<id>.enabled. It never touches a legacy
// feature flag: the flag is a read-only alias, so a user who once set it keeps
// it while the config key becomes the answer.
func (m *Model) savePanelEnabled(d plugin.Descriptor, on bool) tea.Cmd {
	if !d.HasSwitch() {
		return nil
	}
	m.noteRestart()
	return SaveCmd(toggleNotice(d.Name+" panel", on), func() error {
		return config.SavePlugins(func(p *config.PluginsConfig) { d.SetEnabled(p, on) })
	})
}

func (m *Model) buildPanels(b *paneBuilder) {
	cfg := m.Config()

	b.text(PaneTitle(PageTitle(PagePanels)), "")
	b.lead("Choose the Sidecar surfaces you want available.")
	b.blank()

	for _, d := range m.pluginDescriptors {
		// A plugin with no switch has nothing to offer here. Workspaces is
		// exactly that: it is Sidecar's core tab, and a control that cannot
		// change anything is worse than no control.
		if !d.HasSwitch() {
			continue
		}
		m.panelRow(b, d, cfg)
		m.panelExtras(b, d, cfg)
		b.blank()
	}

	if m.restartNote != "" {
		b.note(m.restartNote)
	}
	b.lead("Sidecar decides which panels to build when it starts, so these switches apply on the next launch.")
}

// panelRow paints one plugin's toggle plus whatever the machine has to say
// about it: a missing supporting command, or an install action for a plugin
// that is switched on with nothing behind it.
func (m *Model) panelRow(b *paneBuilder, d plugin.Descriptor, cfg *config.Config) {
	badge := ""
	if d.Beta {
		badge = BetaBadge()
	}
	detail := d.Detail
	if d.ID == panelIDNotes && !cfg.Plugins.TDMonitor.Enabled {
		// td owns Notes persistence, so say why the switch is on and the tab is
		// not, rather than leaving the user to discover it.
		detail += "; available when the td panel is on"
	}
	descriptor := d
	b.panelToggle(regionPanel+d.ID, d.Name, badge, detail, m.panelOn(d), func(m *Model) tea.Cmd {
		return m.togglePanel(descriptor)
	})

	// A panel whose supporting tool is missing is said out loud rather than
	// left to render an empty tab the user has to interpret.
	if d.ID == panelIDTD && m.probed && !m.commandFound(tdCommandName) {
		b.note("td is not on PATH, so the panel can only offer setup guidance.")
	}
	if d.NeedsCommand() && m.panelOn(d) && m.probed && !m.commandFound(d.Integration.Executable) {
		b.note("The " + d.Integration.Executable + " command is not on PATH, so the tab will have nothing to show.")
		b.buttons(buttonSpec{
			id: regionPanel + d.ID + panelInstallSuffix, key: "", label: " Install " + d.Name + " ", primary: true,
			run: func(m *Model) tea.Cmd {
				m.openEnableRoute(integrationFor(descriptor))
				return nil
			},
		})
	}
}

// panelExtras paints the per-plugin settings that live under a toggle. They are
// the one place the loop is not uniform, because a refresh interval, a database
// path, and an editor choice are not the same control.
func (m *Model) panelExtras(b *paneBuilder, d plugin.Descriptor, cfg *config.Config) {
	switch d.ID {
	case panelIDGit:
		if cfg.Plugins.GitStatus.Enabled {
			m.refreshRow(b, regionPanelGitRefresh, cfg.Plugins.GitStatus.RefreshInterval,
				func(p *config.PluginsConfig, next time.Duration) { p.GitStatus.RefreshInterval = next })
		}
	case panelIDTD:
		if cfg.Plugins.TDMonitor.Enabled {
			m.pathRow(b, regionPanelTDPath, "Database", cfg.Plugins.TDMonitor.DBPath, ".todos/issues.db",
				func(p *config.PluginsConfig, value string) { p.TDMonitor.DBPath = value })
			b.help("Relative paths are resolved inside the current project.")
			m.refreshRow(b, regionPanelTDRefresh, cfg.Plugins.TDMonitor.RefreshInterval,
				func(p *config.PluginsConfig, next time.Duration) { p.TDMonitor.RefreshInterval = next })
		}
	case panelIDNotes:
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
	case panelIDConversations:
		if m.conversationsOn() {
			m.pathRow(b, regionPanelConvDir, "Source directory", cfg.Plugins.Conversations.ClaudeDataDir, "~/.claude",
				func(p *config.PluginsConfig, value string) { p.Conversations.ClaudeDataDir = value })
			b.help("Where Sidecar looks for agent session history.")
		}
	}
}

// togglePanel is what a panel's ON/OFF pill does.
//
// Turning a plugin on whose command is missing opens the dependency check
// first: a switch that produces an empty surface is not an answer. Everything
// else is one config write.
func (m *Model) togglePanel(d plugin.Descriptor) tea.Cmd {
	if d.ID == panelIDConversations {
		// Conversations is the one surface whose feature flag is not an alias:
		// it is the preview opt-in, and the panel needs both. Turning it off
		// therefore clears only the plugin key and leaves the opt-in alone.
		return m.toggleConversations()
	}
	on := !m.panelOn(d)
	missing := !m.probed || !m.commandFound(d.Integration.Executable)
	if on && d.NeedsCommand() && missing {
		m.openEnableRoute(integrationFor(d))
		return nil
	}
	return m.savePanelEnabled(d, on)
}

// FocusNotesPreference opens the page that owns Notes enablement and puts the
// detail cursor on its existing toggle. Setup surfaces use this rather than
// inventing a second Notes setting.
func (m *Model) FocusNotesPreference() {
	m.Navigate(PagePanels)
	m.detailFocus = true
	m.focusControlByID(regionPanel + panelIDNotes)
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
// Conversations panel: its descriptor requires the feature flag and the
// plugin's own enabled bool, so a single ON must mean both.
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
