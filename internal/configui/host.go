package configui

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
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
	// RemoteHosts is each registered machine's condition as the running host
	// registry knows it. Configuration reports it and never probes: a settings
	// screen with its own ssh connection would be a second answer to a question
	// the registry is already answering, and the two would disagree the moment
	// one was slower than the other.
	//
	// Note the name. HostState above is about the Sidecar hosting this surface;
	// these are the other machines it watches. The two senses of "host" meet
	// only here, so everything about the remote sense says "remote".
	RemoteHosts []RemoteHost
}

// RemoteHost is one registered machine's live condition.
//
// It carries the state's own fix line rather than leaving the page to write
// one, so the row a user reads in Configuration and the row they read in the
// Sessions browser say the same thing about the same machine.
type RemoteHost struct {
	// ID is the name the host is registered under.
	ID string
	// State is the health state's name, as hosts.State spells it.
	State string
	// Detail is what went wrong, when anything did.
	Detail string
	// Fix names what to do about the state, in the imperative. Empty for the
	// states that need nothing done.
	Fix string
	// Connected reports a machine whose rows are current, so a page can say
	// "watching" without having to know every state name.
	Connected bool
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
	// AnyPending marks any updater work worth opening the surface for:
	// another product's pending update included, not just Sidecar's own.
	AnyPending bool
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

// SetRemoteHosts refreshes only the registered machines' health. The app calls
// it whenever a host's condition changes while Configuration is open.
//
// It is deliberately narrower than SetHostState: health changes on the
// registry's own cadence, several times a minute, and pushing a whole host
// state that often would re-run syncHostState — which would snap a live theme
// preview back to the saved theme while a user was still looking at it.
func (m *Model) SetRemoteHosts(remotes []RemoteHost) {
	m.host.RemoteHosts = remotes
}

// RefreshNotificationProbe invalidates any in-flight result and queues a fresh
// capability and config check against the current host state. The app calls it
// after a successful save, including while an earlier probe is still running.
func (m *Model) RefreshNotificationProbe() {
	if m.Page() != PageNotifications {
		return
	}
	state := m.notifications()
	state.checking = false
	state.configChecking = false
	m.queueNotificationProbe()
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

// ProbeNotificationDeliveryMsg asks the app host to run the lazy provider
// probes. It is emitted only after the Notifications page is entered.
type ProbeNotificationDeliveryMsg struct{ Generation uint64 }

// TestNotificationDeliveryMsg asks the host to exercise enabled channels
// through its shared delivery service. It never creates a centre record.
type TestNotificationDeliveryMsg struct {
	Event  notifydelivery.TestEvent
	Source notify.SourceID
}

// NotificationDeliveryStatusMsg carries a completed read-only probe back to
// the Configuration surface.
type NotificationDeliveryStatusMsg struct {
	Generation uint64
	Status     notifydelivery.Status
}

func (NotificationDeliveryStatusMsg) configMsg() {}

// NotificationConfigValidationMsg reports the asynchronous custom-path and
// notification-config validation requested by Delivery status. Validation may
// touch the filesystem, so it never runs from View.
type NotificationConfigValidationMsg struct {
	Generation uint64
	Err        string
}

func (NotificationConfigValidationMsg) configMsg() {}

// NotificationTestResultMsg carries one explicit test result back to the page.
type NotificationTestResultMsg struct{ Result notifydelivery.Result }

func (NotificationTestResultMsg) configMsg() {}

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
	case cwdGitMsg:
		return m.applyCwdGit(msg)
	case repoInitMsg:
		return m.applyRepoInit(msg)
	case probeMsg:
		m.applyProbe(msg)
	case installationMsg:
		m.applyInstallation(msg)
	case installResultMsg:
		return m.applyInstallResult(msg)
	case installTickMsg:
		return m.tickInstallSpinner()
	case NotificationDeliveryStatusMsg:
		state := m.notifications()
		if msg.Generation != 0 && msg.Generation != state.probeGeneration {
			break
		}
		state.checking, state.checked, state.status = false, true, msg.Status
	case NotificationConfigValidationMsg:
		state := m.notifications()
		if msg.Generation != 0 && msg.Generation != state.probeGeneration {
			break
		}
		state.configChecking, state.configChecked, state.configError = false, true, msg.Err
	case NotificationTestResultMsg:
		state := m.notifications()
		state.testing, state.tested, state.result = false, true, msg.Result
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
