package tasks

import (
	"fmt"
	"reflect"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/styles"
	tasksui "github.com/marcus/tasks/pkg/tui"
)

const (
	pluginID   = "tasks"
	pluginName = "tasks"
	pluginIcon = "K" // T belongs to the td monitor tab

	// sessionNamespace keeps the embedded model's view/filter state in
	// <state>/tasks/hosts/sidecar/tui.json. Sharing the standalone session
	// would let a sidecar tab silently rewrite `tasks-tui`'s own tui.json.
	sessionNamespace = "sidecar"
)

// Plugin wraps the Tasks TUI as a sidecar plugin. Tasks keeps ownership of its
// configuration, storage, rendering, overlays, and agent queue; the plugin owns
// only placement and lifecycle.
type Plugin struct {
	ctx     *plugin.Context
	focused bool

	// Embedded Tasks model, nil until TasksReadyMsg is adopted.
	model *tasksui.Model

	// unavailable holds the reason the model could not be built — usually
	// Tasks' own configuration-required message.
	unavailable string

	// View dimensions, replayed into the model when it arrives late.
	width  int
	height int

	// loading is true between Start() and TasksReadyMsg. Building the model
	// resolves configuration, opens the task store, and starts the agent
	// queue, none of which belongs before sidecar's first frame.
	loading bool

	// generation counts plugin lifecycles. Init and Stop both bump it, and
	// every in-flight build carries the value it was started under, so a model
	// built for a previous lifecycle is discarded even when the host recycles
	// the plugin without bumping ctx.Epoch. The epoch check still runs: it
	// catches project switches that leave the plugin itself untouched.
	generation uint64

	// environment overrides the process environment Tasks resolves its
	// configuration from. Production leaves it nil (Tasks snapshots os.Environ);
	// tests set it so they never touch the developer's real task data.
	environment map[string]string
}

// New creates a new Tasks plugin.
func New() *Plugin {
	return &Plugin{}
}

// ID returns the plugin identifier.
func (p *Plugin) ID() string { return pluginID }

// Name returns the plugin display name.
func (p *Plugin) Name() string { return pluginName }

// Icon returns the plugin icon character.
func (p *Plugin) Icon() string { return pluginIcon }

// Init initializes the plugin with context. It deliberately performs no I/O:
// no configuration walk, no store open, no agent process. All of that happens
// in the command returned by Start(), after the first frame.
func (p *Plugin) Init(ctx *plugin.Context) error {
	p.ctx = ctx

	// Clear state from a previous initialization (project switching). Normally
	// Registry.Reinit has already called Stop(), but safeStop recovers panics,
	// so a Close() that panicked leaves a live model here. Close it rather than
	// dropping it: its agent queue and provider processes would outlive us.
	//
	// TODO(tasks): Close() saves the session. Once Tasks exposes a
	// discard-without-save path, use it here so a doomed model cannot clobber
	// the session of the one that replaces it.
	if p.model != nil {
		_ = p.model.Close()
		p.model = nil
	}
	p.unavailable = ""
	p.loading = true
	p.generation++

	return nil
}

// Start begins plugin operation.
func (p *Plugin) Start() tea.Cmd {
	if p.loading {
		return p.buildModel()
	}
	if p.model == nil {
		return nil
	}
	return p.model.Init()
}

// buildModel constructs the embedded Tasks model asynchronously. Resolving
// configuration touches the filesystem and starting the agent queue may spawn a
// provider process, so this must stay off the startup path.
func (p *Plugin) buildModel() tea.Cmd {
	options := tasksui.EmbeddedOptions{
		SessionNamespace: sessionNamespace,
		// Packet 1.3 introduces sidecar's unified footer and flips this to
		// true so Tasks' own footer stops duplicating it. Until that footer
		// exists, suppressing Tasks' would leave the tab with no key hints at
		// all, so keep Tasks' footer for now.
		SuppressFooter: false,
		// Tasks must never terminate the host. Quit requests are surfaced
		// through QuitRequested() instead of tea.Quit.
		SuppressQuit: true,
		Theme:        buildTheme(),
		Environment:  p.environment,
	}

	var epoch uint64
	if p.ctx != nil {
		epoch = p.ctx.Epoch
	}
	generation := p.generation

	return func() (msg tea.Msg) {
		// Registry.safeInit recovers panics and degrades to "plugin
		// unavailable". Building off the Init path loses that net, so
		// reinstate it here rather than tearing the program down.
		defer func() {
			if rec := recover(); rec != nil {
				msg = TasksReadyMsg{Epoch: epoch, Generation: generation, Err: fmt.Errorf("panic building tasks: %v", rec)}
			}
		}()
		model, err := tasksui.NewEmbedded(options)
		return TasksReadyMsg{Epoch: epoch, Generation: generation, Model: model, Err: err}
	}
}

// TasksReadyMsg carries the embedded Tasks model once it has been built.
type TasksReadyMsg struct {
	Epoch uint64
	// Generation is the plugin lifecycle this build belongs to. It makes
	// staleness structural instead of dependent on the host bumping Epoch.
	Generation uint64
	Model      *tasksui.Model
	Err        error
}

// GetEpoch implements plugin.EpochMessage.
func (m TasksReadyMsg) GetEpoch() uint64 { return m.Epoch }

// adoptModel installs a freshly built model and returns the command that starts
// its file-watch tick and first read model.
func (p *Plugin) adoptModel(msg TasksReadyMsg) tea.Cmd {
	p.loading = false

	if msg.Err != nil || msg.Model == nil {
		// Tasks reports its own diagnosis (most often "tasks is not
		// configured"); surface it verbatim rather than an empty list.
		p.unavailable = unavailableReason(msg.Err)
		if p.ctx != nil && p.ctx.Logger != nil {
			p.ctx.Logger.Debug("tasks: embedded model unavailable", "error", msg.Err)
		}
		return nil
	}

	p.model = msg.Model

	// Seed the model with the size the plugin already knows about. The app's
	// WindowSizeMsg arrived before this model existed, and Tasks lays out
	// against the last size it was told about. Whatever the seeding update
	// asks for is real work (Tasks may kick off a re-read at the new size), so
	// batch it with Init rather than dropping it.
	var seed tea.Cmd
	if p.width > 0 && p.height > 0 {
		_, seed = p.model.Update(tea.WindowSizeMsg{Width: p.width, Height: p.height})
	}

	if p.ctx != nil && p.ctx.Logger != nil {
		for _, warning := range p.model.Warnings() {
			p.ctx.Logger.Warn("tasks", "warning", warning)
		}
	}

	return combine(p.model.Init(), seed)
}

// combine batches two commands, either of which may be nil. It exists so the
// seeding update's command cannot be quietly dropped on the floor.
func combine(a, b tea.Cmd) tea.Cmd {
	switch {
	case a == nil:
		return b
	case b == nil:
		return a
	default:
		return tea.Batch(a, b)
	}
}

// unavailableReason renders the failure the plugin will show and report.
//
// TODO(tasks): this only sees errors from NewEmbedded. A store that exists but
// is corrupt or unreadable builds a model fine and renders an authoritative
// empty list. Once Tasks exposes a load-error accessor, consult it here so the
// tab says "your store is broken" instead of "you have nothing to do".
func unavailableReason(err error) string {
	if err == nil {
		return "tasks returned no model"
	}
	return err.Error()
}

// Stop closes the embedded model, which saves the sidecar session and shuts
// down the Tasks agent queue.
func (p *Plugin) Stop() {
	if p.model != nil {
		_ = p.model.Close()
		p.model = nil
	}
	// p.unavailable is deliberately preserved: an unconfigured Tasks is still
	// unconfigured after Stop, and clearing it would downgrade Diagnostics from
	// the real reason to "disabled / not started".
	//
	// Any model still being built belongs to the project we just left; the
	// TasksReadyMsg handler closes it rather than adopting it. Bumping the
	// generation is what makes that true without relying on the host to bump
	// ctx.Epoch across a Stop/Init pair.
	p.loading = false
	p.generation++
}

// Update handles messages by delegating to the embedded Tasks model.
func (p *Plugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	// Handle the async build kicked off by Start().
	if ready, ok := msg.(TasksReadyMsg); ok {
		if plugin.IsStale(p.ctx, ready) || ready.Generation != p.generation || !p.loading {
			// Stale project switch, or Stop() already tore this plugin
			// down. Close the orphan: dropping it on the floor would leak
			// its agent queue and the provider processes behind it.
			if ready.Model != nil {
				_ = ready.Model.Close()
			}
			return p, nil
		}
		return p, p.adoptModel(ready)
	}

	// Track the size before the nil-model bail-out. A tab whose model is still
	// building — or building in the background while another tab is shown —
	// must still record the host's size, because that is exactly the value
	// adoptModel seeds the model with. Tracking it after the bail-out would
	// make the seeding path dead code.
	if wsm, ok := msg.(tea.WindowSizeMsg); ok {
		p.width = wsm.Width
		p.height = wsm.Height
	}

	if p.model == nil {
		return p, nil
	}

	// Focus changes are a sidecar concept; Tasks keeps its own tick running
	// and re-initializing it here would duplicate the watch chain.
	if _, ok := msg.(plugin.PluginFocusedMsg); ok {
		return p, nil
	}

	// Window, key, mouse, tick, paste, and Tasks' own queue messages all go
	// through here; Tasks ignores anything it doesn't recognise.

	_, cmd := p.model.Update(msg)

	// A nested quit never terminates sidecar. Tasks is built with SuppressQuit
	// so it latches the request instead of returning tea.Quit; clear the latch
	// so Tasks stays usable, and let sidecar's own quit flow own the decision.
	if p.model.QuitRequested() {
		p.model.ClearQuitRequest()
	}
	return p, suppressQuit(cmd)
}

// suppressQuit wraps a command so no tea.Quit it produces can reach sidecar's
// runtime, including one buried in a tea.Batch or tea.Sequence.
//
// TODO(tasks): Tasks' SuppressQuit does not currently latch QuitRequested(),
// so in practice Tasks still returns tea.Quit and this is the only thing
// standing between `q` in the Tasks tab and sidecar exiting. Once Tasks
// latches, this becomes belt-and-braces — keep it either way; the cost is one
// closure and the failure mode is killing the host.
func suppressQuit(cmd tea.Cmd) tea.Cmd {
	if cmd == nil {
		return nil
	}
	return func() tea.Msg {
		return filterQuit(cmd())
	}
}

// filterQuit strips quit out of a command result. Batches and sequences carry
// commands rather than messages, so their members are rewrapped and the
// filtering happens when the runtime eventually runs them.
func filterQuit(msg tea.Msg) tea.Msg {
	switch typed := msg.(type) {
	case nil:
		return nil
	case tea.QuitMsg:
		return nil
	case tea.BatchMsg:
		wrapped := make(tea.BatchMsg, 0, len(typed))
		for _, c := range typed {
			if c != nil {
				wrapped = append(wrapped, suppressQuit(c))
			}
		}
		return wrapped
	}

	// tea.Sequence yields an unexported sequenceMsg, also a []tea.Cmd. Rebuild
	// it reflectively rather than letting a sequenced quit through.
	value := reflect.ValueOf(msg)
	if value.Kind() == reflect.Slice && value.Type().Elem() == reflect.TypeOf(tea.Cmd(nil)) {
		out := reflect.MakeSlice(value.Type(), 0, value.Len())
		for i := range value.Len() {
			c, _ := value.Index(i).Interface().(tea.Cmd)
			if c == nil {
				continue
			}
			out = reflect.Append(out, reflect.ValueOf(suppressQuit(c)))
		}
		return out.Interface()
	}

	return msg
}

// View renders the plugin by delegating to the embedded Tasks model.
func (p *Plugin) View(width, height int) string {
	// Lipgloss silently ignores Width/Height/MaxWidth/MaxHeight when the value
	// is <= 0, so the constraint below would vanish and Tasks' full frame would
	// escape into sidecar's header and footer. A non-positive box has no cells
	// to draw in anyway: render nothing. This is reachable in production —
	// app.update subtracts three rows of chrome from the terminal height.
	if width <= 0 || height <= 0 {
		return ""
	}

	p.width = width
	p.height = height

	var content string
	switch {
	case p.model != nil:
		content = p.model.View(width, height)
	case p.unavailable != "":
		content = renderUnavailable(p.unavailable, width, height)
	case p.loading:
		content = styles.Muted.Render("Loading tasks…")
	default:
		content = renderUnavailable("tasks has not been started", width, height)
	}

	// Constrain the output to the allocated box so the sidecar header and
	// footer cannot be scrolled away by a taller Tasks frame.
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxWidth(width).
		MaxHeight(height).
		Render(content)
}

// IsFocused returns whether the plugin is focused.
func (p *Plugin) IsFocused() bool { return p.focused }

// SetFocused sets the focus state.
func (p *Plugin) SetFocused(f bool) { p.focused = f }

// Commands projects Tasks' exported command registry into sidecar commands.
// Tasks is the single source of truth; sidecar adds only categorization.
func (p *Plugin) Commands() []plugin.Command {
	exported := tasksui.ExportCommands()
	commands := make([]plugin.Command, 0, len(exported))
	for _, cmd := range exported {
		commands = append(commands, plugin.Command{
			ID:          cmd.ID,
			Name:        cmd.FooterLabel,
			Description: cmd.Description,
			Context:     string(cmd.Context),
			Priority:    cmd.FooterPriority,
			Category:    categorize(cmd.Context),
		})
	}
	return commands
}

// categorize maps a Tasks focus context onto a sidecar palette grouping. Tasks
// contexts are stable strings, so this stays a projection rather than a second
// command table.
func categorize(context tasksui.FocusContext) plugin.Category {
	switch context {
	case tasksui.FocusList, tasksui.FocusDetail, tasksui.FocusResponseDetail:
		return plugin.CategoryNavigation
	case tasksui.FocusFilter, tasksui.FocusModalFilter, tasksui.FocusAgentActivityFilter:
		return plugin.CategorySearch
	case tasksui.FocusTaskEdit, tasksui.FocusForm:
		return plugin.CategoryEdit
	default:
		return plugin.CategoryActions
	}
}

// FocusContext returns the interaction layer currently consuming keys, as
// reported by Tasks itself.
func (p *Plugin) FocusContext() string {
	if p.model == nil {
		return pluginID
	}
	return string(p.model.FocusContext())
}

// ConsumesTextInput reports whether printable keys belong to Tasks input.
func (p *Plugin) ConsumesTextInput() bool {
	if p.model == nil {
		return false
	}
	return p.model.ConsumesTextInput()
}

// Diagnostics returns plugin health info.
func (p *Plugin) Diagnostics() []plugin.Diagnostic {
	status, detail := "ok", ""
	switch {
	case p.model != nil:
		detail = string(p.model.CurrentView()) + " view"
	case p.unavailable != "":
		status = "error"
		detail = p.unavailable + "; " + setupHint
	case p.loading:
		status = "loading"
		detail = "building tasks model"
	default:
		status = "disabled"
		detail = "not started"
	}
	return []plugin.Diagnostic{{ID: pluginID, Status: status, Detail: detail}}
}
