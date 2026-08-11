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

	// loadError is Tasks' most recent store-read failure, as reported by
	// Model.LoadError(). It is a different condition from `unavailable`: the
	// model exists and renders, but what it renders cannot be trusted as a
	// complete picture of the user's tasks. See Diagnostics for why sidecar
	// reports it rather than repainting the tab.
	loadError error

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

	// newEmbedded builds the Tasks model. Production leaves it nil and gets
	// tasksui.NewEmbedded. It exists so a test can prove *when* the build
	// happens: the no-I/O-before-the-first-frame guarantee is about the call
	// never being made until Start's command runs, which no amount of
	// after-the-fact filesystem inspection can establish.
	newEmbedded func(tasksui.EmbeddedOptions) (*tasksui.Model, error)
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
	// so a Close() that panicked leaves a live model here. Release it rather
	// than dropping it: its agent queue and provider processes would outlive us.
	//
	// Discard, not Close: reaching here means the ordinary Stop path did not
	// complete, so this model's view state is not the state the user last saw.
	// Saving it would write a doomed model's session over the one its
	// replacement is about to load.
	if p.model != nil {
		_ = p.model.Discard()
		p.model = nil
	}
	p.unavailable = ""
	p.loadError = nil
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
		// Sidecar renders the unified key-hint row (Packet 1.3), so Tasks must
		// not paint a second one. SuppressKeyHints (Tasks v1.3.0) removes
		// exactly that line.
		//
		// SuppressFooter stays false deliberately: it is the blunt switch, and
		// the rest of Tasks' footer stack is not sidecar's to erase. It carries
		// the prompt input itself (suppressing it makes `tab` focus an invisible
		// caret), the agent transcript, the store-read banner, and the filter
		// and context-filter lines — all of which Packet 1.3 requires be
		// preserved.
		SuppressFooter:   false,
		SuppressKeyHints: true,
		// Sidecar owns the number row for tab switching, so Tasks' view bar
		// must stop offering `1 Agenda 2 Next …`. Advertisement only: `1`-`6`
		// still jump views inside Tasks, they just never reach it from here.
		SuppressViewKeyHints: true,
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
		build := p.newEmbedded
		if build == nil {
			build = tasksui.NewEmbedded
		}
		model, err := build(options)
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

	// Register Tasks' shortcut registry with sidecar's keymap, the way the td
	// monitor does on adoption. These bindings carry no handler: sidecar uses
	// them as the metadata behind the merged palette and the unified footer,
	// while the keys themselves are routed by KeyRouter and executed by Tasks.
	p.registerBindings()

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

	init := p.model.Init()
	p.refreshLoadError()

	return combine(init, seed)
}

// refreshLoadError samples Tasks' store-read health. LoadError is only
// meaningful after a read, so this is called after every model interaction
// rather than once at adoption: a store can break — or be repaired — while the
// tab is open, and Tasks re-reads on its own file-watch tick.
func (p *Plugin) refreshLoadError() {
	if p.model == nil {
		return
	}
	previous := p.loadError
	p.loadError = p.model.LoadError()
	if p.loadError != nil && previous == nil && p.ctx != nil && p.ctx.Logger != nil {
		p.ctx.Logger.Warn("tasks: cannot read the task store", "error", p.loadError)
	}
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

// unavailableReason renders the build failure the plugin will show and report.
// A store that exists but cannot be read is a different condition: it builds a
// model fine, so it is carried by p.loadError and reported by Diagnostics
// rather than replacing the Tasks frame.
func unavailableReason(err error) string {
	if err == nil {
		return "tasks returned no model"
	}
	return err.Error()
}

// Stop closes the embedded model, which saves the sidecar session and shuts
// down the Tasks agent queue.
//
// Close, not Discard: this is the model the host actually presented, so the
// view and filters the user left it on are exactly the state the next lifecycle
// should reload.
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
			// down. Release the orphan: dropping it on the floor would leak
			// its agent queue and the provider processes behind it.
			//
			// Discard, not Close. Every model built for this namespace shares
			// one session file, and this one was never presented to anyone, so
			// Close would write its untouched default state over the session
			// the model the user actually used just saved.
			if ready.Model != nil {
				_ = ready.Model.Discard()
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

	// Tasks answers clicks from the geometry it was last rendered at, which is
	// the panel interior. The host's coordinates are relative to the plugin box,
	// so shift them past the frame.
	//
	// Only a press is dropped when it lands on the frame: a press there belongs
	// to no row, and folding it onto the nearest one acts on a task the user
	// never pointed at. Motion, release, and wheel are clamped and forwarded
	// instead. Tasks clears its rail drag only on release, so a release
	// swallowed at the edge of a horizontal drag would leave the pane stuck to
	// the pointer for every gesture after it.
	if mm, ok := msg.(tea.MouseMsg); ok {
		shifted, inside := p.offsetMouse(mm)
		if _, isPress := msg.(tea.MouseClickMsg); isPress && !inside {
			return p, nil
		}
		msg = shifted
	}

	_, cmd := p.model.Update(msg)
	p.refreshLoadError()

	// A nested quit never terminates sidecar. Tasks is built with SuppressQuit,
	// so `q` latches a request instead of returning tea.Quit. Clear the latch so
	// Tasks stays usable and a second `q` latches again.
	//
	// Sidecar has no exported "request quit" command to forward the latched
	// request into, so today the request is acknowledged and dropped: `q` in the
	// Tasks tab is a no-op rather than a host quit. Translating it into
	// sidecar's own quit flow needs a host-side affordance that does not exist
	// yet; it belongs with the unified footer/command work in Packet 1.3/1.4.
	if p.model.QuitRequested() {
		p.model.ClearQuitRequest()
	}
	return p, suppressQuit(cmd)
}

// suppressQuit wraps a command so no tea.Quit it produces can reach sidecar's
// runtime, including one buried in a tea.Batch or tea.Sequence.
//
// Tasks now latches its own quit rather than returning tea.Quit, so this is no
// longer the only thing standing between `q` and sidecar exiting — but it
// stays. Sidecar forwards every message it receives into Tasks, Tasks composes
// commands from widgets it does not fully own, and the cost of the guard is one
// closure against a failure mode of killing the host.
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

	// Tasks stopped drawing its own frame, which is right when it runs solo on
	// the alternate screen. Inside sidecar every other plugin sits in the shared
	// gradient panel, so the frame is drawn here instead and Tasks is rendered
	// at the panel's interior size.
	// Only Tasks itself is framed. The unavailable and loading states are
	// sidecar's own copy, already inset and sized against the plugin box, and a
	// second frame around them would only steal rows from the diagnosis.
	var framed string
	switch {
	case p.model != nil:
		innerWidth, innerHeight := innerSize(width, height)
		framed = styles.RenderPanel(p.model.View(innerWidth, innerHeight), width, height, true)
	case p.unavailable != "":
		framed = renderUnavailable(p.unavailable, width, height)
	case p.loading:
		framed = styles.Muted.Render("Loading tasks…")
	default:
		framed = renderUnavailable("tasks has not been started", width, height)
	}

	// Constrain the output to the allocated box so the sidecar header and
	// footer cannot be scrolled away by a taller Tasks frame.
	return lipgloss.NewStyle().
		Width(width).
		Height(height).
		MaxWidth(width).
		MaxHeight(height).
		Render(framed)
}

// Panel chrome: one border cell plus one padding cell on the left and right,
// one border row top and bottom. RenderPanel refuses to draw below 3x3 and
// returns the content bare, so the interior is the full box at those sizes.
const (
	panelBorderX = 2 // border + padding, each side
	panelBorderY = 1 // border, top and bottom
)

// offsetMouse translates a host mouse event into the panel interior Tasks was
// rendered at, reporting whether the pointer was inside that interior at all.
// Bubble Tea v2 mouse messages are interfaces, so the concrete type is rebuilt
// around the shifted Mouse value.
func (p *Plugin) offsetMouse(msg tea.MouseMsg) (tea.MouseMsg, bool) {
	innerWidth, innerHeight := innerSize(p.width, p.height)
	if innerWidth == p.width && innerHeight == p.height {
		// Panel too small to be drawn: no frame, no shift.
		return msg, true
	}

	mm := msg.Mouse()
	mm.X -= panelBorderX
	mm.Y -= panelBorderY
	inside := mm.X >= 0 && mm.Y >= 0 && mm.X < innerWidth && mm.Y < innerHeight
	mm.X = min(max(mm.X, 0), max(innerWidth-1, 0))
	mm.Y = min(max(mm.Y, 0), max(innerHeight-1, 0))

	switch msg.(type) {
	case tea.MouseClickMsg:
		return tea.MouseClickMsg(mm), inside
	case tea.MouseReleaseMsg:
		return tea.MouseReleaseMsg(mm), inside
	case tea.MouseWheelMsg:
		return tea.MouseWheelMsg(mm), inside
	case tea.MouseMotionMsg:
		return tea.MouseMotionMsg(mm), inside
	}
	return msg, inside
}

// innerSize is the content box inside the gradient panel.
func innerSize(width, height int) (int, int) {
	if width < 3 || height < 3 {
		return width, height
	}
	return max(width-2*panelBorderX, 1), max(height-2*panelBorderY, 1)
}

// IsFocused returns whether the plugin is focused.
func (p *Plugin) IsFocused() bool { return p.focused }

// SetFocused sets the focus state.
func (p *Plugin) SetFocused(f bool) { p.focused = f }

// registerBindings publishes Tasks' exported key bindings to sidecar's keymap.
//
// Host-owned commands (quit, help) are withheld: sidecar owns those keys, and
// advertising Tasks' versions in the palette and footer would promise a
// behaviour the router does not deliver.
//
// So are bindings on keys the host structurally refuses to route here:
// keymap.HostReservedKeys, and sidecar globals outside shadowableGlobals. The
// footer and the merged help are both built from registered bindings, so
// registering those keys makes sidecar advertise a key that does something
// else — `1`-`6` switch tabs, `#` opens the theme switcher — on the most
// visible surface in the app. The rule here is deliberately the same predicate
// the router uses (registerableKey → mayShadowGlobal), so what is advertised
// and what fires cannot disagree.
//
// Withholding the binding does not withhold the command: Commands() still
// exports it, and palette.BuildEntries turns a command with no binding into a
// keyless palette entry, invocable by selection through Model.Invoke.
//
// This is only about keys the host refuses structurally. A key a plugin
// declines situationally — `r` claims nothing when there is no proposal — is
// still registered, and rightly: it does fire, just not right now.
func (p *Plugin) registerBindings() {
	if p.ctx == nil || p.ctx.Keymap == nil {
		return
	}
	for _, binding := range tasksui.ExportBindings() {
		if hostOwnedCommands[binding.CommandID] || binding.Key == "" {
			continue
		}
		if !registerableKey(string(binding.Context), binding.Key) {
			continue
		}
		p.ctx.Keymap.RegisterPluginBinding(binding.Key, binding.CommandID, string(binding.Context))
	}
}

// Commands projects Tasks' exported command registry into sidecar commands.
// Tasks is the single source of truth; sidecar adds categorization and the
// handler that runs the command by its stable ID.
//
// Commands belonging to the context Tasks is in right now are filtered by
// CommandAvailable, so the conditional pairs that share a key (`r` is either
// reject-proposal or open-recur-popup, never both) do not both appear in the
// palette. Commands for other contexts are kept: CommandAvailable answers for
// the live focus only, and the palette's other layers are still real help.
func (p *Plugin) Commands() []plugin.Command {
	exported := tasksui.ExportCommands()
	current := p.FocusContext()
	commands := make([]plugin.Command, 0, len(exported))
	for _, cmd := range exported {
		if hostOwnedCommands[cmd.ID] {
			continue
		}
		if p.model != nil && string(cmd.Context) == current && !p.available(cmd.ID) {
			continue
		}
		commands = append(commands, plugin.Command{
			ID:          cmd.ID,
			Name:        cmd.FooterLabel,
			Description: cmd.Description,
			Context:     string(cmd.Context),
			Priority:    cmd.FooterPriority,
			Category:    categorize(cmd.Context),
			Handler:     p.handlerFor(cmd.ID),
		})
	}
	return commands
}

// handlerFor builds the palette action for a Tasks command. It runs the command
// by ID through Model.Invoke rather than replaying its default key, which is
// exactly why that entry point exists: several Tasks keys are ambiguous, and a
// palette entry must run the command the user picked.
func (p *Plugin) handlerFor(commandID string) func() tea.Cmd {
	return func() tea.Cmd {
		return p.invoke(commandID)
	}
}

func (p *Plugin) invoke(commandID string) tea.Cmd {
	if p.model == nil {
		return nil
	}
	cmd, err := p.model.Invoke(commandID)
	if err != nil {
		if p.ctx != nil && p.ctx.Logger != nil {
			p.ctx.Logger.Debug("tasks: command not invoked", "command", commandID, "error", err)
		}
		return nil
	}
	p.refreshLoadError()
	return suppressQuit(cmd)
}

// available asks Tasks whether a command can run against the current selection.
func (p *Plugin) available(commandID string) bool {
	if p.model == nil {
		return false
	}
	ok, err := p.model.CommandAvailable(commandID)
	return err == nil && ok
}

// BlocksGlobalKeys implements plugin.KeyRouter. Tasks' own overlays — modals,
// pickers, prompts, the agent activity list, and every text-input layer — own
// the keyboard while they are open, so sidecar's global keys must not fire
// underneath them.
//
// Root contexts are listed (see rootContexts); everything else, INCLUDING an
// unknown context, is an overlay. A new Tasks overlay therefore routes safely
// without a change here, and the cost of the conservative default is a sidecar
// global that does not fire, never a quit out from under an open layer.
func (p *Plugin) BlocksGlobalKeys() bool {
	if p.model == nil {
		return false
	}
	context := p.FocusContext()
	return IsTasksContext(context) && !IsRootContext(context)
}

// ClaimsKey implements plugin.KeyRouter: the key has a live Tasks binding in
// the current context, so Tasks wins it over sidecar's global binding.
//
// This is what makes `@`, `tab`, `M`, and `A` mean what they mean in Tasks
// while the tab is focused. Three rules decide it, in order:
//
//  1. Host-reserved keys are never claimed (see hostReservedKeys; sidecar
//     enforces the same set, this is defence in depth).
//  2. A key sidecar binds globally is claimed only if the plan's conflict table
//     decided that collision (mayShadowGlobal), and then unconditionally for
//     the whole context (claimIsUnconditional) — so `K`, `W`, `#` and the
//     number row keep their sidecar meanings, and `@` keeps its Tasks meaning
//     no matter what is selected. Tasks' views step with `←`/`→` instead.
//  3. Every other key is availability-aware: one whose only Tasks commands are
//     unavailable right now is not claimed and falls through to sidecar. That
//     is how `r` still refreshes when there is no proposal to reject and no
//     recurrence to edit.
func (p *Plugin) ClaimsKey(key string) bool {
	if p.model == nil || hostReservedKeys[key] || !mayShadowGlobal(key) {
		return false
	}
	commands := commandsForKey(p.FocusContext(), key)
	if claimIsUnconditional(key) {
		return len(commands) > 0
	}
	for _, commandID := range commands {
		if p.available(commandID) {
			return true
		}
	}
	return false
}

// QuitKeyExits implements plugin.KeyRouter. In a Tasks root context `q` is
// sidecar's quit; inside a Tasks overlay it is the overlay's own dismissal, and
// BlocksGlobalKeys has already routed it there.
func (p *Plugin) QuitKeyExits() bool {
	if p.model == nil {
		return true
	}
	return IsRootContext(p.FocusContext())
}

// FooterStatus implements plugin.FooterStatusProvider.
//
// Tasks still paints its own store-read banner — sidecar suppresses only the
// key-hint row — but that banner lives inside the tab, which is not where a
// user looks when they are on another tab or reading the chrome. Restating it
// in the host footer is what keeps "what you are looking at is not your task
// list" visible at a glance. It is reported here as well as in Diagnostics
// because the diagnostics modal is a place you have to think to look.
//
// A live sidecar toast outranks it (see renderFooter): the toast expires and
// this condition does not, so it comes straight back.
func (p *Plugin) FooterStatus() (string, bool) {
	if p.model == nil || p.loadError == nil {
		return "", false
	}
	return "tasks: cannot read the task store", true
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
//
// A live model whose store cannot be read reports as an error even though the
// tab still renders. Tasks paints the user-facing banner (see storeReadHint);
// Diagnostics is where sidecar records the same condition for `sidecar doctor`
// and the logs, which is the surface Tasks' banner cannot reach.
func (p *Plugin) Diagnostics() []plugin.Diagnostic {
	status, detail := "ok", ""
	switch {
	case p.model != nil && p.loadError != nil:
		status = "error"
		detail = "cannot read the task store: " + p.loadError.Error() + "; " + storeReadHint
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
