package tdmonitor

import (
	"fmt"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/clip"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/workspace"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/tdroot"
	"github.com/marcus/sidecar/internal/tdsetup"
	"github.com/marcus/td/pkg/monitor"
)

const (
	pluginID   = "td-monitor"
	pluginName = "td"
	pluginIcon = "T"

	// pollInterval is the fallback when no refresh interval is configured. Each
	// poll costs four `git` forks inside td's monitor (rev-parse HEAD,
	// rev-parse --abbrev-ref, status --porcelain, rev-parse --show-toplevel) on
	// top of a task-database read, and it runs whether or not the td panel is
	// on screen. Two seconds meant ~7000 subprocess spawns an hour in a session
	// left open all day; task data does not change that fast.
	pollInterval = 10 * time.Second
)

// refreshInterval returns the configured td poll interval, falling back to
// pollInterval when there is no config or the value is unset.
func (p *Plugin) refreshInterval() time.Duration {
	if p.ctx == nil || p.ctx.Config == nil {
		return pollInterval
	}
	if d := p.ctx.Config.Plugins.TDMonitor.RefreshInterval; d > 0 {
		return d
	}
	return pollInterval
}

// Plugin wraps td's monitor TUI as a sidecar plugin.
// This provides full feature parity with the standalone `td monitor` command.
type Plugin struct {
	ctx     *plugin.Context
	focused bool

	// Embedded td monitor model
	model *monitor.Model

	// Not-installed view (shown when td binary not found on system)
	notInstalled *NotInstalledModel

	// Setup modal (shown when td is on PATH but project not initialized)
	setupModal *SetupModel

	// todosConflict is set when .todos exists as a file instead of a directory
	todosConflict bool

	// tdOnPath tracks whether td binary is available on the system
	tdOnPath bool

	// View dimensions (passed to model on each render)
	width  int
	height int

	// Track StatusMessage changes to surface as sidecar toasts
	lastStatusMessage string

	// started tracks whether Init() has been called to prevent duplicate poll chains (td-023577)
	started bool

	// loadingModel is true between Start() and MonitorReadyMsg. Building the
	// embedded monitor opens td's SQLite database, which is slow enough to be
	// worth keeping off the pre-first-frame path (td-9c7bf2).
	loadingModel bool
}

// New creates a new TD Monitor plugin.
func New() *Plugin {
	return &Plugin{}
}

// ID returns the plugin identifier.
func (p *Plugin) ID() string { return pluginID }

// Name returns the plugin display name.
func (p *Plugin) Name() string { return pluginName }

// Icon returns the plugin icon character.
func (p *Plugin) Icon() string { return pluginIcon }

// Init initializes the plugin with context.
func (p *Plugin) Init(ctx *plugin.Context) error {
	p.ctx = ctx

	// Clear any stale state from previous initialization (important for project switching)
	p.model = nil
	p.notInstalled = nil
	p.setupModal = nil
	p.todosConflict = false
	p.started = false

	// Check if .todos exists as a file instead of a directory (#194).
	// This must happen before attempting to create the monitor or showing
	// the setup modal, since td init will fail in this state.
	if err := tdroot.CheckTodosConflict(ctx.WorkDir); err != nil {
		p.ctx.Logger.Warn("td monitor: .todos path conflict", "error", err)
		p.todosConflict = true
		return nil
	}

	// Check if td binary is available on PATH
	_, err := exec.LookPath("td")
	p.tdOnPath = err == nil

	// The embedded monitor itself is built in Start(), off the startup path.
	p.loadingModel = true

	return nil
}

// Start begins plugin operation.
func (p *Plugin) Start() tea.Cmd {
	if p.loadingModel {
		return p.buildMonitor()
	}
	if p.model == nil {
		// Start animation for not-installed view
		if p.notInstalled != nil {
			return p.notInstalled.Init()
		}
		// Setup modal doesn't need animation init
		if p.setupModal != nil {
			return p.setupModal.Init()
		}
		return nil
	}
	// Delegate to monitor's Init which starts data fetch and tick
	// Mark as started to prevent duplicate poll chains on focus (td-023577)
	p.started = true
	return p.model.Init()
}

// buildMonitor constructs td's embedded monitor asynchronously. Opening the
// task database costs ~100ms here and considerably more on machines where an
// endpoint security agent intercepts file access, so it must not run before
// the first frame (td-9c7bf2).
//
// The renderer closures are built on this goroutine but only invoked later
// during View(), so no TUI state is touched off the main loop.
func (p *Plugin) buildMonitor() tea.Cmd {
	opts := monitor.EmbeddedOptions{
		BaseDir:       p.ctx.WorkDir,
		Interval:      p.refreshInterval(),
		Version:       "", // empty for embedded use (not displayed in this context)
		PanelRenderer: styles.CreateTDPanelRenderer(),
		ModalRenderer: styles.CreateTDModalRenderer(),
		Theme:         buildTheme(),
	}

	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}

	return func() (msg tea.Msg) {
		// Building the monitor used to run inside registry.safeInit, which
		// recovers panics and degrades to "plugin unavailable". Preserve that:
		// a panic here would otherwise tear the whole program down (td-9c7bf2).
		defer func() {
			if rec := recover(); rec != nil {
				msg = MonitorReadyMsg{Epoch: epoch, Err: fmt.Errorf("panic building td monitor: %v", rec)}
			}
		}()
		model, err := monitor.NewEmbeddedWithOptions(opts)
		return MonitorReadyMsg{Epoch: epoch, Model: model, Err: err}
	}
}

// MonitorReadyMsg carries the embedded td monitor once it has been built.
type MonitorReadyMsg struct {
	Epoch uint64
	Model *monitor.Model
	Err   error
}

// GetEpoch implements plugin.EpochMessage.
func (m MonitorReadyMsg) GetEpoch() uint64 { return m.Epoch }

// adoptMonitor installs a freshly built monitor (or the appropriate fallback
// view) and returns the command that starts its polling.
func (p *Plugin) adoptMonitor(msg MonitorReadyMsg) tea.Cmd {
	p.loadingModel = false

	if msg.Err != nil || msg.Model == nil {
		// Database not initialized - decide which view to show
		p.ctx.Logger.Debug("td monitor: database not found", "error", msg.Err)
		if p.tdOnPath {
			// td is installed but project not initialized - show setup modal
			p.setupModal = NewSetupModel(p.ctx.WorkDir, p.ctx.Epoch)
			return p.setupModal.Init()
		}
		// td is not installed on system - show not-installed view
		p.notInstalled = NewNotInstalledModel()
		return p.notInstalled.Init()
	}

	p.model = msg.Model

	// Ensure the adopted monitor has the latest resolved palette in case a
	// theme change occurred while loading.
	_ = p.model.SetTheme(buildTheme())

	// Use sidecar's clipboard (atotto/clipboard) instead of td's built-in one.
	// td's copyToClipboard doesn't handle WSL (tries xclip/xsel only);
	// atotto/clipboard falls through to clip.exe on WSL.
	//
	// This is the one copy in Sidecar that reaches only the system clipboard:
	// td's monitor owns the call and takes a writer, not a command, so there is
	// no way back into the program loop to emit the OSC 52 half. A td yank over
	// SSH copies nothing, exactly as it did before Sidecar embedded it.
	p.model.ClipboardFn = clip.WriteAll

	// Register TD bindings with sidecar's keymap (single source of truth)
	if p.ctx.Keymap != nil && p.model.Keymap != nil {
		for _, b := range p.model.Keymap.ExportBindings() {
			p.ctx.Keymap.RegisterPluginBinding(b.Key, b.Command, b.Context)
		}
	}

	// Seed the monitor with the size the plugin already knows about. The monitor
	// only computes its panel bounds on WindowSizeMsg, and since the model is now
	// built asynchronously (td-9c7bf2) the app's window size arrived before this
	// model existed. Without replaying it, PanelBounds stays empty and every
	// mouse hit test misses, so wheel scrolling and clicks do nothing until the
	// terminal is resized.
	if p.width > 0 && p.height > 0 {
		newModel, _ := p.model.Update(tea.WindowSizeMsg{Width: p.width, Height: p.height})
		if m, ok := newModel.(monitor.Model); ok {
			p.model = &m
		}
	}

	// Delegate to monitor's Init which starts data fetch and tick.
	// Mark as started to prevent duplicate poll chains on focus (td-023577)
	p.started = true
	return p.model.Init()
}

// Stop cleans up plugin resources.
func (p *Plugin) Stop() {
	if p.model != nil {
		_ = p.model.Close()
		p.model = nil
	}
	p.notInstalled = nil
	p.setupModal = nil
	p.started = false
	// Any monitor still being built belongs to the project we just left; the
	// MonitorReadyMsg handler closes it rather than adopting it.
	p.loadingModel = false
}

// Update handles messages by delegating to the embedded monitor.
func (p *Plugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	// Handle live theme changes
	if _, ok := msg.(app.ThemeChangedMsg); ok {
		if p.model != nil {
			_ = p.model.SetTheme(buildTheme())
		}
		return p, nil
	}

	// Handle the async monitor build kicked off by Start()
	if ready, ok := msg.(MonitorReadyMsg); ok {
		if plugin.IsStale(p.ctx, ready) || !p.loadingModel {
			// Stale project switch, or Stop() already tore this plugin down;
			// drop the monitor rather than adopting one for the old project.
			if ready.Model != nil {
				_ = ready.Model.Close()
			}
			return p, nil
		}
		return p, p.adoptMonitor(ready)
	}

	// A successful init from either td-backed surface makes both refresh. A
	// failed attempt belongs only to the surface that started it.
	if result, ok := msg.(tdsetup.ResultMsg); ok {
		if plugin.IsStale(p.ctx, result) {
			return p, nil
		}
		if result.Err != nil {
			if result.Origin != tdsetup.OriginTDMonitor {
				return p, nil
			}
			return p, appmsg.Alert(notify.SourceTD, notify.SeverityError, result.Err.Error())
		}
		if err := p.Init(p.ctx); err == nil {
			return p, p.Start()
		}
		return p, nil
	}

	// Handle setup skip - show not-installed view
	if _, ok := msg.(SetupSkippedMsg); ok {
		p.setupModal = nil
		p.notInstalled = NewNotInstalledModel()
		return p, p.notInstalled.Init()
	}

	if p.model == nil {
		// Handle setup modal
		if p.setupModal != nil {
			cmd := p.setupModal.Update(msg)
			return p, cmd
		}
		// Handle not-installed animation
		if p.notInstalled != nil {
			cmd := p.notInstalled.Update(msg)
			return p, cmd
		}
		return p, nil
	}

	// Handle window size - store dimensions and forward to TD
	// The app already adjusts height for the header offset
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		p.width = wsm.Width
		p.height = wsm.Height
		newModel, cmd := p.model.Update(wsm)
		if m, ok := newModel.(monitor.Model); ok {
			p.model = &m
		}
		return p, cmd
	}

	// Skip refresh on focus - the existing poll chain handles periodic updates (td-023577).
	// Calling model.Init() on every focus created duplicate poll chains, causing
	// concurrent adapter.Sessions() calls that accumulated file descriptors.
	if _, ok := msg.(app.PluginFocusedMsg); ok {
		return p, nil
	}

	// Intercept TD's SendTaskToWorktree message and route to workspace plugin
	if sendMsg, ok := msg.(monitor.SendTaskToWorktreeMsg); ok {
		return p, tea.Batch(
			app.FocusPlugin("workspace-manager"),
			func() tea.Msg {
				return workspace.OpenCreateModalWithTaskMsg{
					TaskID:    sendMsg.TaskID,
					TaskTitle: sendMsg.TaskTitle,
				}
			},
		)
	}

	// Handle issue preview "Open in TD" request
	if fullMsg, ok := msg.(app.OpenFullIssueMsg); ok {
		if p.model == nil {
			return p, appmsg.Alert(notify.SourceTD, notify.SeverityError, "TD not initialized")
		}
		newModel, cmd := p.model.Update(monitor.OpenIssueByIDMsg{
			IssueID: fullMsg.IssueID,
		})
		if m, ok := newModel.(monitor.Model); ok {
			p.model = &m
		}
		return p, cmd
	}

	// Delegate to monitor
	newModel, cmd := p.model.Update(msg)

	// Update our reference (monitor uses value semantics)
	if m, ok := newModel.(monitor.Model); ok {
		p.model = &m
	}

	// Intercept tea.Quit to prevent monitor from exiting the whole app.
	// The sidecar app handles quit via quit confirmation modal.
	if cmd != nil {
		originalCmd := cmd
		cmd = func() tea.Msg {
			result := originalCmd()
			if _, isQuit := result.(tea.QuitMsg); isQuit {
				return nil // Suppress quit - let app handle via modal
			}
			return result
		}
	}

	// Surface td toasts to sidecar
	var cmds []tea.Cmd
	if cmd != nil {
		cmds = append(cmds, cmd)
	}

	// td's own status line is mixed: a failure is worth keeping, and the
	// routine confirmations behind it are not (audit row 75).
	if p.model != nil && p.model.StatusMessage != "" &&
		p.model.StatusMessage != p.lastStatusMessage {
		p.lastStatusMessage = p.model.StatusMessage
		message := p.model.StatusMessage
		if p.model.StatusIsError {
			cmds = append(cmds, appmsg.Alert(notify.SourceTD, notify.SeverityError, message))
		} else {
			cmds = append(cmds, appmsg.ShowFlashFrom(string(notify.SourceTD), message))
		}
	} else if p.model != nil && p.model.StatusMessage == "" {
		p.lastStatusMessage = ""
	}

	if len(cmds) == 0 {
		return p, nil
	}
	if len(cmds) == 1 {
		return p, cmds[0]
	}
	return p, tea.Batch(cmds...)
}

// View renders the plugin by delegating to the embedded monitor.
func (p *Plugin) View(width, height int) string {
	p.width = width
	p.height = height

	var content string
	if p.todosConflict {
		content = renderConflictView(width)
	} else if p.model == nil {
		if p.setupModal != nil {
			content = p.setupModal.View(width, height)
		} else if p.notInstalled != nil {
			content = p.notInstalled.View(width, height)
		} else if p.loadingModel {
			content = styles.Muted.Render("Loading tasks…")
		} else {
			content = "No td database found.\nRun 'td init' to initialize."
		}
	} else {
		// Set dimensions on model before rendering. Route a size change through
		// Update rather than assigning the fields, so the monitor recomputes the
		// panel bounds its mouse hit tests depend on; assigning Width/Height
		// alone leaves the bounds stale (or empty, for a model built after the
		// app's last WindowSizeMsg) and silently kills scrolling and clicks.
		if p.model.Width != width || p.model.Height != height {
			newModel, _ := p.model.Update(tea.WindowSizeMsg{Width: width, Height: height})
			if m, ok := newModel.(monitor.Model); ok {
				p.model = &m
			}
		}
		// v2: monitor.View() returns tea.View; extract its string content for
		// composition into sidecar's own view (the app's tea.View owns the
		// terminal-level features like alt-screen/mouse/cursor).
		content = p.model.View().Content
	}

	// Constrain output to allocated height to prevent header scrolling off-screen.
	// MaxHeight truncates content that exceeds the allocated space.
	return lipgloss.NewStyle().Width(width).Height(height).MaxHeight(height).Render(content)
}

// IsFocused returns whether the plugin is focused.
func (p *Plugin) IsFocused() bool { return p.focused }

// SetFocused sets the focus state.
func (p *Plugin) SetFocused(f bool) { p.focused = f }

// Commands returns the available commands by consuming TD's exported command metadata.
func (p *Plugin) Commands() []plugin.Command {
	if p.model == nil || p.model.Keymap == nil {
		return nil
	}

	// Get exported commands from TD (single source of truth)
	exported := p.model.Keymap.ExportCommands()
	commands := make([]plugin.Command, 0, len(exported))

	for _, cmd := range exported {
		commands = append(commands, plugin.Command{
			ID:          cmd.ID,
			Name:        cmd.Name,
			Description: cmd.Description,
			Context:     cmd.Context,
			Priority:    cmd.Priority,
			Category:    categorizeCommand(cmd.ID),
		})
	}

	return commands
}

// categorizeCommand returns the appropriate category for a command ID.
func categorizeCommand(id string) plugin.Category {
	switch id {
	case "open-details", "toggle-closed", "open-stats", "toggle-help":
		return plugin.CategoryView
	case "search", "search-confirm", "search-cancel", "search-clear":
		return plugin.CategorySearch
	case "approve", "mark-for-review", "delete", "confirm", "cancel", "refresh", "copy-to-clipboard":
		return plugin.CategoryActions
	case "cursor-down", "cursor-up", "cursor-top", "cursor-bottom",
		"half-page-down", "half-page-up", "full-page-down", "full-page-up",
		"scroll-down", "scroll-up", "next-panel", "prev-panel",
		"focus-panel-1", "focus-panel-2", "focus-panel-3",
		"navigate-prev", "navigate-next", "close", "back", "select",
		"focus-task-section", "open-epic-task", "open-parent-epic", "open-handoffs":
		return plugin.CategoryNavigation
	case "quit":
		return plugin.CategorySystem
	default:
		return plugin.CategoryActions
	}
}

// FocusContext returns the current focus context by consuming TD's context state.
func (p *Plugin) FocusContext() string {
	if p.model == nil {
		return "td-monitor"
	}

	// Delegate to TD's context tracking (single source of truth)
	return p.model.CurrentContextString()
}

// ConsumesTextInput reports whether TD monitor is in a text-entry context.
func (p *Plugin) ConsumesTextInput() bool {
	if p.model == nil {
		return false
	}
	switch p.model.CurrentContextString() {
	case "td-search", "td-form", "td-board-editor", "td-confirm", "td-close-confirm":
		return true
	default:
		return false
	}
}

// BlocksGlobalKeys reports whether TD's generic modal owns the keyboard.
func (p *Plugin) BlocksGlobalKeys() bool {
	return p.model != nil && p.model.CurrentContextString() == "td-modal"
}

// Diagnostics returns plugin health info.
func (p *Plugin) Diagnostics() []plugin.Diagnostic {
	status := "ok"
	detail := ""

	if p.todosConflict {
		status = "error"
		detail = ".todos is a file, not a directory"
	} else if p.model == nil {
		if p.loadingModel {
			status = "loading"
			detail = "opening database"
		} else {
			status = "disabled"
			detail = "no database"
		}
	} else {
		// Count issues across categories
		total := len(p.model.InProgress) +
			len(p.model.TaskList.Ready) +
			len(p.model.TaskList.Reviewable) +
			len(p.model.TaskList.Blocked)
		if total == 1 {
			detail = "1 issue"
		} else {
			detail = formatCount(total, "issue", "issues")
		}
	}

	return []plugin.Diagnostic{
		{ID: "td-monitor", Status: status, Detail: detail},
	}
}

// formatCount formats a count with singular/plural forms.
func formatCount(n int, singular, plural string) string {
	if n == 1 {
		return "1 " + singular
	}
	return fmt.Sprintf("%d %s", n, plural)
}

// renderConflictView renders the error view when .todos is a file instead of a directory.
func renderConflictView(width int) string {
	theme := styles.GetCurrentTheme()

	title := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(theme.Colors.Error)).
		Render("Cannot initialize td")

	body := lipgloss.NewStyle().
		Width(width - 4).
		Render(
			"Found a .todos file where a directory is expected.\n" +
				"This may have been created by another tool or AI agent.\n\n" +
				"To fix, remove or rename the file:\n\n" +
				"  mv .todos .todos.bak\n" +
				"  td init\n\n" +
				"Then restart sidecar.")

	return lipgloss.JoinVertical(lipgloss.Left, "", title, "", body)
}
