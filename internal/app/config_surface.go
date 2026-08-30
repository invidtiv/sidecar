package app

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/clip"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configchecks"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/notifydelivery"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/theme"
	"github.com/marcus/sidecar/internal/uirequest"
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

// openConfiguration takes over the content area on a destination. An empty page
// means "wherever the user last was" — what the gear, `,`, and the palette
// command all ask for, so toggling the surface off and on again is not a way to
// lose your place. A named page — `sidecar setup`, an empty state's prompt — is
// honored exactly.
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
	if page == "" {
		m.config.Reopen()
	} else {
		m.config.Open(page)
	}
	m.config.SetHostState(m.configHostState())
	m.updateContext()
	// Readiness is answered fresh every time Configuration opens, in a command:
	// PATH, tmux, the config file, and the project's instruction file can all
	// have changed since the last look, and none of them may be touched on the
	// render path.
	m.config.SetCheckInput(m.configCheckInput())
	return tea.Batch(m.config.Recheck(), m.config.TakePending())
}

// toggleConfiguration is what every "settings" control does: the gear, the
// settings key, and the palette command all open the surface when it is away
// and put it back when it is on screen, rather than re-opening what is already
// there.
func (m *Model) toggleConfiguration() tea.Cmd {
	if m.configOpen() {
		return m.closeConfiguration()
	}
	return m.openConfiguration("")
}

// refreshConfigContext re-points an open Configuration surface at the project
// the app has just switched to. Configuration survives a project switch by
// design, which is exactly why it must be told: its host state, its check input,
// and its cached readiness answers are all about a working directory that is no
// longer the current one. Refreshing beats closing — the user is mid-setting,
// and the settings on screen are still theirs.
func (m *Model) refreshConfigContext() tea.Cmd {
	if !m.configOpen() {
		return nil
	}
	m.config.SetHostState(m.configHostState())
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
	state.RemoteHosts = m.configRemoteHosts()
	return state
}

// configRemoteHosts is each registered machine's condition as the running host
// registry knows it. Configuration reports what the browser already learned
// over its existing connections; it never opens one of its own.
func (m *Model) configRemoteHosts() []configui.RemoteHost {
	if m.overview == nil {
		return nil
	}
	conditions := m.overview.HostConditions()
	remotes := make([]configui.RemoteHost, 0, len(conditions))
	for _, condition := range conditions {
		remotes = append(remotes, configui.RemoteHost{
			ID:        condition.ID,
			State:     string(condition.Health.State),
			Detail:    condition.Health.Detail,
			Fix:       condition.Health.Fix(),
			Connected: condition.Health.State.Healthy(),
		})
	}
	return remotes
}

// configUpdateStatus reports the release check the app already runs from Init.
// Configuration renders it; it never runs a check of its own, and an unknown
// answer stays unknown rather than becoming "up to date". AnyPending is
// deliberately independent of Sidecar's own row: another product's pending
// update is still work worth opening the updater for.
func (m *Model) configUpdateStatus() configui.UpdateStatus {
	anyPending := m.hasUpdatesAvailable() || m.updateInProgress || m.needsRestart
	target := m.productTarget(version.ProductSidecar)
	if target == nil {
		return configui.UpdateStatus{Checked: len(m.products) > 0, AnyPending: anyPending}
	}
	return configui.UpdateStatus{
		Checked:       true,
		Failed:        target.CheckFailed,
		Available:     target.HasUpdate,
		LatestVersion: target.LatestVersion,
		AnyPending:    anyPending,
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
	// Where the user was is remembered before the surface is torn down, so the
	// next unnamed open puts them back on it.
	var themeCmd tea.Cmd
	if m.config != nil {
		m.config.Close()
		themeCmd = m.notifyThemeChanged()
	}
	restore := m.configReturn
	m.configReturn = configReturn{}
	if !restore.valid {
		m.updateContext()
		return themeCmd
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
	return tea.Batch(themeCmd, cmd)
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
		return m.notifyThemeChanged()
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
		return m, tea.Batch(cmd, m.notifyThemeChanged())
	}
	if m.config.SearchFocused() {
		return m, nil
	}
	key := msg.String()
	// A key this context binds to close-configuration — esc, q, or whatever a
	// user rebound them to — answers like esc for someone who is not typing into
	// anything. The surface has already had its say above, so reaching here
	// means nothing on screen claimed the key: Search, an editor, a confirmation,
	// and a child route all consume their own, so a typed q never gets this far.
	if command, ok := m.keymap.CommandForContextKey(configui.ContextConfig, key); ok && command == "close-configuration" {
		return m, m.configEscape()
	}
	// The key that opened Configuration closes it, exactly as the gear does. It
	// would otherwise be a silent no-op: the surface it would open is the one
	// already on screen.
	if command, ok := m.keymap.CommandForContextKey("global", key); ok && command == "open-configuration" {
		return m, m.closeConfiguration()
	}
	// The only host key that still works here is help. A tab number, a cycle
	// key, or a refresh would act on a surface the user cannot see, so an
	// unclaimed key stops rather than falling through.
	if key == "?" {
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
		notice := msg.Notice
		if notice == "" {
			notice = "Copied"
		}
		// A copy is self-evident and instant: flash it, do not file it
		// (audit row 5).
		return clip.Copy(msg.Text, func(r clip.Result) tea.Msg {
			return FlashMsg{Text: r.Message(notice)}
		}), true

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
		// updater returns the user to About. Mid-batch this reopens the modal
		// in its current phase instead of restarting or double-starting.
		m.updateContext()
		if m.openUpdateModal() {
			return nil, true
		}
		// A pending Sidecar restart deliberately gates a new confirmation;
		// say that instead of claiming nothing is pending.
		if m.needsRestart && m.hasUpdatesAvailable() {
			return toast("Restart sidecar to finish the pending update first"), true
		}
		return toast("No update is pending right now"), true

	case configui.CloseConfigMsg:
		// The About chip line's Close: the same put-away the global esc does.
		return m.closeConfiguration(), true

	case configui.CheckUpdatesMsg:
		cmds := m.productCheckCmds(true)
		cmds = append(cmds, toast("Checking for updates…"))
		return tea.Batch(cmds...), true

	case configui.OpenPaletteMsg:
		_, cmd := m.togglePaletteFromConfig()
		return cmd, true

	case configui.ConfigSavedMsg:
		return m.applyConfigSaved(msg), true

	case configui.ProbeNotificationDeliveryMsg:
		generation := msg.Generation
		delivery, ok := m.notificationDelivery.(notifydelivery.StatusProvider)
		if !ok {
			return func() tea.Msg {
				return configui.NotificationDeliveryStatusMsg{Generation: generation, Status: notifydelivery.Status{
					Native: notifydelivery.Capability{Reason: "provider status unavailable"},
					Sound:  notifydelivery.Capability{Reason: "provider status unavailable"},
				}}
			}, true
		}
		return func() tea.Msg {
			return configui.NotificationDeliveryStatusMsg{Generation: generation, Status: delivery.Status(context.Background())}
		}, true

	case configui.TestNotificationDeliveryMsg:
		request, err := notifydelivery.ExplicitTestRequest(msg.Event)
		if err == nil && msg.Source != "" {
			if !notify.ValidSource(msg.Source) {
				err = fmt.Errorf("unknown notification source %q", msg.Source)
			} else {
				request.Notification.Source = msg.Source
			}
		}
		if err != nil || m.notificationDelivery == nil {
			return func() tea.Msg {
				result := notifydelivery.Result{
					Native: notifydelivery.ChannelResult{Error: "notification delivery unavailable"},
					Sound:  notifydelivery.ChannelResult{Error: "notification delivery unavailable"},
				}
				if err != nil {
					result.Native.Error, result.Sound.Error = err.Error(), err.Error()
				}
				return configui.NotificationTestResultMsg{Result: result}
			}, true
		}
		delivery := m.notificationDelivery
		return func() tea.Msg {
			return configui.NotificationTestResultMsg{Result: delivery.Deliver(context.Background(), request)}
		}, true

	case configui.NotificationTestResultMsg:
		if m.config != nil {
			_ = m.config.Handle(msg)
		}
		return func() tea.Msg { return FlashMsg{Text: notificationTestFlash(msg.Result)} }, true

	case configui.Msg:
		// Work the surface started for itself — a directory listing, so far.
		if m.config == nil {
			return nil, true
		}
		return m.config.Handle(msg), true
	}
	return nil, false
}

func notificationTestFlash(result notifydelivery.Result) string {
	delivered := 0
	for _, channel := range []notifydelivery.ChannelResult{result.Native, result.Sound} {
		if channel.Delivered && channel.Error != "" {
			return "Notification test delivered; coordination failed: " + channel.Error
		}
	}
	for _, channel := range []notifydelivery.ChannelResult{result.Native, result.Sound} {
		if channel.Delivered {
			delivered++
		}
		if channel.Error != "" {
			return "Notification test failed: " + channel.Error
		}
	}
	if delivered == 0 {
		return "Notification test: no enabled provider delivered"
	}
	if delivered == 1 {
		return "Notification test delivered on 1 channel"
	}
	return "Notification test delivered on 2 channels"
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
	var themeCmd tea.Cmd
	var hostCmd tea.Cmd
	var configPending tea.Cmd
	if cfg, err := config.Load(); err == nil {
		m.cfg = cfg
		features.SetConfig(cfg)
		if m.registry != nil && m.registry.Context() != nil {
			m.registry.Context().Config = cfg
		}
		// Saving the config screen is the moment an edited expiry takes
		// effect; notifications posted afterwards use the new value.
		notify.ApplyConfig(cfg.Notifications)
		m.showClock = cfg.UI.ShowClock
		m.titleTemplate = cfg.UI.TerminalTitle
		// Nerd Font glyphs are read from one package-level flag at startup;
		// assigning it here is all "applies immediately" means for it.
		styles.PillTabsEnabled = cfg.UI.NerdFontsEnabled
		// A live theme preview belongs to the picker. Re-applying the disk
		// theme here would snap it back the moment any other setting saved.
		if m.config == nil || !m.config.PreviewingTheme() {
			themeCmd = m.applyResolvedTheme(theme.ResolveTheme(cfg, m.ui.WorkDir))
		}
		if m.overview != nil {
			m.overview.SetConfig(cfg)
			hostCmd = m.overview.SyncHosts()
		}
	}
	if m.config != nil {
		m.config.SetHostState(m.configHostState())
		m.config.SetCheckInput(m.configCheckInput())
		m.config.RefreshNotificationProbe()
		configPending = m.config.TakePending()
	}
	cmds := []tea.Cmd{m.syncTerminalTitle(true), themeCmd, hostCmd, configPending, m.syncOverviewProjects()}
	if msg.Notice != "" {
		cmds = append(cmds, toast(msg.Notice))
	}
	if m.config != nil {
		cmds = append(cmds, m.config.Recheck())
	}
	return tea.Batch(cmds...)
}

func (m *Model) handleConfigReloadRequest(req uirequest.Request) tea.Cmd {
	cmd := m.applyConfigSaved(configui.ConfigSavedMsg{})
	_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
		Instance: uirequest.InstanceID("app"),
		Host:     uirequest.HostName(),
		PID:      os.Getpid(),
		Status:   uirequest.StatusOpened,
		Surface:  "configuration",
	})
	return cmd
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

// openerFailWindow is how long the opener is given to fail. A desktop opener
// that has handed the path off exits within milliseconds and a refusal is just
// as quick, so anything still running past this really did open something.
const openerFailWindow = 3 * time.Second

// openPathCmd hands a path to the desktop's opener. Sidecar never edits the
// file itself here; it only makes it easy to reach.
func openPathCmd(path string) tea.Cmd {
	return func() tea.Msg {
		if err := openPath(path); err != nil {
			// The opener usually names the path in its own complaint, so it is
			// only added when the message would otherwise not say what failed.
			message := "Could not open " + path
			if detail := err.Error(); strings.Contains(detail, path) {
				message = "Could not open: " + detail
			} else {
				message += ": " + detail
			}
			return ToastMsg{Message: message, Duration: 4 * time.Second, IsError: true}
		}
		// The app opening is the confirmation (audit row 8).
		return FlashMsg{Text: "Opened " + path}
	}
}

// openPath runs the desktop's opener and waits for its answer. Starting the
// process only proves the binary exists — a refused URL, a missing file, or a
// desktop with no handler all report themselves through the exit status, which
// is why "Opened …" used to appear for things that never opened.
//
// The wait is bounded rather than unlimited: some openers (xdg-open with a
// handler that does not fork) stay alive for as long as the application they
// launched, and Sidecar must not kill a browser it opened for the user or block
// the toast behind it. A child that outlives the window is still reaped by the
// goroutine that owns the Wait.
func openPath(path string) error {
	var command *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		command = exec.Command("open", path)
	case "windows":
		command = exec.Command("rundll32", "url.dll,FileProtocolHandler", path)
	default:
		command = exec.Command("xdg-open", path)
	}
	// The opener must not read from the terminal Sidecar is drawing on.
	command.Stdin = nil
	var stderr strings.Builder
	command.Stderr = &stderr
	if err := command.Start(); err != nil {
		return err
	}
	done := make(chan error, 1)
	go func() { done <- command.Wait() }()
	select {
	case err := <-done:
		if err == nil {
			return nil
		}
		if detail := strings.TrimSpace(stderr.String()); detail != "" {
			return errors.New(detail)
		}
		return err
	case <-time.After(openerFailWindow):
		return nil
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
