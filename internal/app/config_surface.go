package app

import (
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configchecks"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/theme"
	"github.com/marcus/sidecar/internal/version"
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
	m.config.SetHostState(m.configHostState())
	m.updateContext()
	// Readiness is answered fresh every time Configuration opens, in a command:
	// PATH, tmux, the config file, and the project's instruction file can all
	// have changed since the last look, and none of them may be touched on the
	// render path.
	m.config.SetCheckInput(m.configCheckInput())
	return m.config.Recheck()
}

// configHostState describes this Sidecar to Configuration: the configuration it
// is running with, where it is working, which configured project that is, and
// the applications it can open a project in. Configuration reads settings from
// here rather than keeping a copy of its own.
func (m *Model) configHostState() configui.HostState {
	state := configui.HostState{
		Config:     m.cfg,
		ProjectDir: m.ui.WorkDir,
		OpenInApps: openInChoices(),
		Version:    m.currentVersion,
		Update:     m.configUpdateStatus(),
	}
	if project := m.currentProjectConfig(); project != nil {
		state.ProjectPath = project.Path
	}
	return state
}

// configUpdateStatus reports the release check the app already runs from Init.
// Configuration renders it; it never runs a check of its own, and an unknown
// answer stays unknown rather than becoming "up to date".
func (m *Model) configUpdateStatus() configui.UpdateStatus {
	target := m.productTarget(version.ProductSidecar)
	if target == nil {
		return configui.UpdateStatus{}
	}
	return configui.UpdateStatus{
		Checked:       true,
		Failed:        target.CheckFailed,
		Available:     target.HasUpdate,
		LatestVersion: target.LatestVersion,
	}
}

// openInChoices names the applications Sidecar knows how to open a project in.
// It deliberately does not detect installations: naming a preference is a
// configuration choice, and the Open-in flow still shows only what is present.
func openInChoices() []configui.OpenInApp {
	choices := make([]configui.OpenInApp, 0, len(openInRegistry))
	for _, app := range openInRegistry {
		choices = append(choices, configui.OpenInApp{ID: app.ID, Name: app.Name})
	}
	return choices
}

// configCheckInput describes this Sidecar to the checks: the configuration the
// app is running with, the file it came from, and the active project.
func (m *Model) configCheckInput() configchecks.Input {
	in := configchecks.Input{
		Config:     m.cfg,
		ConfigPath: config.ConfigPath(),
		ProjectDir: m.ui.WorkDir,
		Env:        configchecks.DefaultEnv(),
	}
	if project := m.currentProjectConfig(); project != nil {
		in.ProjectName = project.Name
	}
	if in.ProjectName == "" && m.ui.WorkDir != "" {
		in.ProjectName = filepath.Base(m.ui.WorkDir)
	}
	return in
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
	// The surface answers for everything it owns — the field being typed into,
	// a pending confirmation, an inline picker, the search, a child route — and
	// only when none of those needed dismissing does esc close Configuration.
	if m.config.Escape() {
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

// configSurfaceMsg answers the requests Configuration makes of the host. The
// surface decides what should happen; the host owns the clipboard, the toast,
// the shell, and the file browser, so it is the one that does it.
//
// It reports whether the message was addressed to Configuration; anything else
// carries on to the plugins as usual.
func (m *Model) configSurfaceMsg(msg tea.Msg) (tea.Cmd, bool) {
	switch msg := msg.(type) {
	case configui.ChecksMsg:
		if m.config != nil {
			m.config.ApplyChecks(msg)
			m.updateContext()
		}
		return nil, true

	case configui.NoticeMsg:
		return toast(msg.Message), true

	case configui.CopyMsg:
		if err := clipboard.WriteAll(msg.Text); err != nil {
			return toast("Copy failed: " + err.Error()), true
		}
		notice := msg.Notice
		if notice == "" {
			notice = "Copied"
		}
		return toast(notice), true

	case configui.OpenShellMsg:
		// The command is typed into a new ordinary shell and left there. Sidecar
		// does not run it, and the user returns to the repair and rechecks.
		cmds := []tea.Cmd{m.closeConfiguration(), FocusPlugin("workspace-manager")}
		command := msg.Command
		cmds = append(cmds, func() tea.Msg { return OpenPrefilledShellMsg{Command: command} })
		return tea.Batch(cmds...), true

	case configui.OpenFileMsg:
		return m.openConfigFile(msg.Path), true

	case configui.OpenURLMsg:
		// A documentation link is handed to the desktop's opener; Sidecar owns
		// no browser and renders no web content.
		return openPathCmd(msg.URL), true

	case configui.OpenUpdaterMsg:
		// Configuration hands an available update to the updater that already
		// exists. It duplicates none of its confirmation, progress, or install
		// behavior, and Configuration stays open underneath, so closing the
		// updater returns the user to About.
		m.openUpdatePreview()
		m.updateContext()
		return nil, true

	case configui.CheckUpdatesMsg:
		cmds := m.productCheckCmds(true)
		cmds = append(cmds, toast("Checking for updates…"))
		return tea.Batch(cmds...), true

	case configui.OpenPaletteMsg:
		_, cmd := m.togglePaletteFromConfig()
		return cmd, true

	case configui.ConfigSavedMsg:
		return m.applyConfigSaved(msg), true

	case configui.Msg:
		// Work the surface started for itself — a directory listing, so far.
		if m.config == nil {
			return nil, true
		}
		return m.config.Handle(msg), true
	}
	return nil, false
}

// applyConfigSaved reloads after Configuration wrote a setting and reapplies the
// parts of it the app snapshots at startup, so a saved setting takes effect
// without a restart wherever that is honest. Configuration never assumes a save
// worked; the running state comes from the file that was just written.
func (m *Model) applyConfigSaved(msg configui.ConfigSavedMsg) tea.Cmd {
	if msg.Err != "" {
		return func() tea.Msg {
			return ToastMsg{Message: "Save failed: " + msg.Err, Duration: 4 * time.Second, IsError: true}
		}
	}
	if cfg, err := config.Load(); err == nil {
		m.cfg = cfg
		m.showClock = cfg.UI.ShowClock
		m.titleTemplate = cfg.UI.TerminalTitle
		// Nerd Font glyphs are read from one package-level flag at startup;
		// assigning it here is all "applies immediately" means for it.
		styles.PillTabsEnabled = cfg.UI.NerdFontsEnabled
		theme.ApplyResolved(theme.ResolveTheme(cfg, m.ui.WorkDir))
	}
	if m.config != nil {
		m.config.SetHostState(m.configHostState())
		m.config.SetCheckInput(m.configCheckInput())
	}
	cmds := []tea.Cmd{m.syncTerminalTitle(true)}
	if msg.Notice != "" {
		cmds = append(cmds, toast(msg.Notice))
	}
	if m.config != nil {
		cmds = append(cmds, m.config.Recheck())
	}
	return tea.Batch(cmds...)
}

// openConfigFile puts a file in front of the user. A file inside the project
// opens in Sidecar's own file browser; anything outside it — the config file
// above all — is handed to the OS opener, because the browser is scoped to the
// project and would have nowhere to show it.
func (m *Model) openConfigFile(path string) tea.Cmd {
	if path == "" {
		return nil
	}
	if relative, err := filepath.Rel(m.ui.WorkDir, path); err == nil && !strings.HasPrefix(relative, "..") {
		return tea.Batch(
			m.closeConfiguration(),
			FocusPlugin("file-browser"),
			func() tea.Msg { return NavigateToFileMsg{Path: relative} },
		)
	}
	return openPathCmd(path)
}

// openPathCmd hands a path to the desktop's opener. Sidecar never edits the
// file itself here; it only makes it easy to reach.
func openPathCmd(path string) tea.Cmd {
	return func() tea.Msg {
		var command *exec.Cmd
		switch runtime.GOOS {
		case "darwin":
			command = exec.Command("open", path)
		case "windows":
			command = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
		default:
			command = exec.Command("xdg-open", path)
		}
		if err := command.Start(); err != nil {
			return ToastMsg{Message: "Could not open " + path, Duration: 3 * time.Second}
		}
		return ToastMsg{Message: "Opened " + path, Duration: 2 * time.Second}
	}
}

func toast(message string) tea.Cmd {
	return func() tea.Msg { return ToastMsg{Message: message, Duration: 3 * time.Second} }
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
