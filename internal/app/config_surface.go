package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/plugin"
)

// Configuration is an app-level surface, not a modal and not a plugin. It
// replaces the central content area the way the global space does, keeping the
// header and footer in place, and it is deliberately absent from the plugin
// registry: it belongs to sidecar itself and must survive a project switch
// without a registry.Reinit.
//
// configReturn is the surface the user was on when Configuration opened.
// Escape restores exactly that — the scope, the tab or plugin behind it, and
// the focus the covered plugin had — rather than guessing a sensible default.
type configReturn struct {
	scope         AppScope
	activePlugin  int
	globalTab     GlobalTab
	pluginFocused bool
	valid         bool
}

// configOpen reports that the Configuration surface owns the content area.
func (m Model) configOpen() bool { return m.configActive && m.config != nil }

// openConfiguration takes over the content area on a destination. The gear, the
// palette command, and `sidecar setup` all pass configui.DefaultPage;
// Configuration never remembers the last section.
func (m *Model) openConfiguration(page configui.PageID) tea.Cmd {
	if m.config == nil {
		m.config = configui.New()
	}
	if !m.configActive {
		m.configReturn = configReturn{
			scope:        m.scope,
			activePlugin: m.activePlugin,
			globalTab:    m.globalTab,
			valid:        true,
		}
		// The covered plugin loses focus for the same reason it does when the
		// global space opens: focus is the visibility contract terminal-owning
		// plugins use to release a pane nobody is looking at.
		if current := m.ActivePlugin(); current != nil {
			m.configReturn.pluginFocused = current.IsFocused()
			current.SetFocused(false)
		}
		m.configActive = true
	}
	m.config.Open(page)
	m.updateContext()
	return nil
}

// closeConfiguration restores the surface Configuration covered.
func (m *Model) closeConfiguration() tea.Cmd {
	if !m.configActive {
		return nil
	}
	m.configActive = false
	restore := m.configReturn
	m.configReturn = configReturn{}
	if !restore.valid {
		m.updateContext()
		return nil
	}
	m.scope = restore.scope
	m.globalTab = restore.globalTab
	if plugins := m.registry.Plugins(); restore.activePlugin >= 0 && restore.activePlugin < len(plugins) {
		m.activePlugin = restore.activePlugin
	}
	var cmd tea.Cmd
	if !m.inGlobalScope() && restore.pluginFocused {
		if current := m.ActivePlugin(); current != nil {
			current.SetFocused(true)
			cmd = PluginFocused()
		}
	}
	m.updateContext()
	return cmd
}

// configEscape answers esc while Configuration is open: it clears an active
// search first, then returns from a focused child route, and only then closes
// the surface. That order is the brief's, and it is why esc never surprises a
// user out of Configuration while something on screen still needs dismissing.
func (m *Model) configEscape() tea.Cmd {
	if m.config.SearchActive() {
		m.config.ClearSearch()
		m.updateContext()
		return nil
	}
	if m.config.Back() {
		m.updateContext()
		return nil
	}
	return m.closeConfiguration()
}

// configKey routes a key to the Configuration surface. Like the global space,
// Configuration covers the plugin pane: an unconsumed key stops here instead of
// reaching a plugin the user cannot see.
func (m *Model) configKey(msg tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		m.initQuitModal()
		m.showQuitConfirm = true
		return m, nil
	}
	// While Search has the keyboard every printable key is query text, so the
	// surface answers first and the host's global switch never sees it.
	handled, cmd := m.config.Key(msg)
	if handled {
		m.updateContext()
		return m, cmd
	}
	if m.config.SearchFocused() {
		return m, nil
	}
	// The only host key that still works here is help. A tab number, a cycle
	// key, or a refresh would act on a surface the user cannot see, so an
	// unclaimed key stops rather than falling through.
	if msg.String() == "?" {
		return m.togglePaletteFromConfig()
	}
	return m, nil
}

func (m *Model) togglePaletteFromConfig() (tea.Model, tea.Cmd) {
	m.showPalette = true
	m.palette.SetSize(m.width, m.height)
	m.palette.Open(m.keymap, m.surfacePlugins(), m.activeContext, "global")
	m.activeContext = "palette"
	return m, nil
}

// configCommands are the surface's footer/palette commands. The footer derives
// its hints from these plus the registered bindings, exactly as a plugin's
// footer does.
func (m Model) configCommands() []plugin.Command {
	if m.config == nil {
		return nil
	}
	return m.config.Commands()
}
