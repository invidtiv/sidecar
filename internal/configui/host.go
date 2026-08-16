package configui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/theme"
)

// Configuration reads the running configuration the host hands it and writes
// through internal/config's Load→mutate→Save boundary in a command. It never
// touches the file on a render path, and it never keeps its own copy of a
// setting: after every save the host reloads and hands the surface the new
// state, so what is on screen is what is on disk.

// HostState is what the running Sidecar tells Configuration about itself.
type HostState struct {
	// Config is the configuration the app is running with.
	Config *config.Config
	// ProjectDir is the active working directory.
	ProjectDir string
	// OpenInApps are the applications the host can open a project in, in the
	// order it offers them. Configuration only names them; opening is the
	// host's job.
	OpenInApps []OpenInApp
	// ProjectPath is the configured project the working directory belongs to,
	// resolved through worktrees. Empty when the current directory is not one of
	// the user's configured projects — which is exactly when a project-scoped
	// setting must not be offered.
	ProjectPath string
	// Version is the version string the running Sidecar resolved for itself.
	Version string
	// Update is the release check the app already runs at startup. Configuration
	// reports it and hands an available update to the existing updater; it never
	// checks, downloads, or installs anything itself.
	Update UpdateStatus
}

// UpdateStatus is the app's release check as About needs to state it. It
// deliberately distinguishes "not checked yet" and "the check failed" from "up
// to date": an unknown answer must never be reported as reassurance.
type UpdateStatus struct {
	// Checked is true once a check has settled, successfully or not.
	Checked bool
	// Failed marks a check that could not complete.
	Failed bool
	// Available marks a real available update.
	Available bool
	// LatestVersion is the release a user would move to.
	LatestVersion string
}

// OpenInApp is one application in the host's "open in" list.
type OpenInApp struct {
	ID   string
	Name string
}

// SetHostState refreshes the surface's view of the running Sidecar. The host
// calls it when Configuration opens and after every save.
func (m *Model) SetHostState(state HostState) {
	m.host = state
	m.syncHostState()
}

// Config is the running configuration, never nil.
func (m *Model) Config() *config.Config {
	if m.host.Config == nil {
		return config.Default()
	}
	return m.host.Config
}

// projects is the configured project list.
func (m *Model) projects() []config.ProjectConfig { return m.Config().Projects.List }

// activeProject is the configured project the user is working in, or nil.
func (m *Model) activeProject() *config.ProjectConfig {
	if m.host.ProjectPath == "" {
		return nil
	}
	list := m.projects()
	for i := range list {
		if list[i].Path == m.host.ProjectPath {
			return &list[i]
		}
	}
	return nil
}

// Msg is a message the Configuration surface sends itself. The host forwards
// anything implementing it back into Handle without needing to know what it is,
// which keeps asynchronous work (a directory listing, a save) in commands
// without adding a host case per message.
type Msg interface{ configMsg() }

// ConfigSavedMsg reports a completed write. The host reloads the configuration,
// reapplies anything it snapshots at startup, and hands the surface fresh state
// — so this is the one message Configuration cannot handle by itself.
type ConfigSavedMsg struct {
	// Notice is the toast text on success.
	Notice string
	// Err is a failure message, empty on success.
	Err string
}

// SaveCmd wraps a Load→mutate→Save call as a command.
func SaveCmd(notice string, save func() error) tea.Cmd {
	return func() tea.Msg {
		if err := save(); err != nil {
			return ConfigSavedMsg{Err: err.Error()}
		}
		return ConfigSavedMsg{Notice: notice}
	}
}

// Handle answers a surface-owned message.
func (m *Model) Handle(msg Msg) tea.Cmd {
	switch msg := msg.(type) {
	case completionsMsg:
		m.applyCompletions(msg)
	case probeMsg:
		m.applyProbe(msg)
	case installationMsg:
		m.applyInstallation(msg)
	case installResultMsg:
		return m.applyInstallResult(msg)
	}
	return nil
}

// syncHostState keeps the parts of the surface that mirror configuration in
// step with it: the theme a picker calls current, and the project a page has
// selected.
func (m *Model) syncHostState() {
	m.clampProjectCursor()
	if state := m.appearanceState; state != nil && state.picker != nil {
		saved := m.appearanceCurrentEntry()
		state.title.SetValue(m.Config().UI.TerminalTitle)
		// A theme save is the new baseline. Any other save — the clock, a
		// nerd-font switch — must leave a live preview alone, or Escape would
		// close Configuration instead of putting the previous theme back.
		if state.picker.previewing && !state.picker.selected().Same(saved) {
			state.picker.current = saved
			state.picker.preview()
			return
		}
		state.picker.current = saved
		state.picker.previewing = false
		state.picker.restore = theme.ResolveTheme(m.Config(), m.host.ProjectDir)
	}
}

// PreviewingTheme reports that a picker has a live preview on screen. The host
// must not apply the disk theme over it when some other setting is saved.
func (m *Model) PreviewingTheme() bool {
	if state := m.appearanceState; state != nil && state.picker != nil && state.picker.previewing {
		return true
	}
	if form := m.addProject; form != nil && form.picker != nil && form.picker.previewing {
		return true
	}
	return false
}
