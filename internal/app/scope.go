package app

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/tasks"
	"github.com/marcus/sidecar/internal/state"
)

// AppScope is the space the user is currently in. Sidecar has exactly two: the
// project the plugins are initialized against, and the global space whose data
// spans every configured project.
//
// It replaces the implicit `overviewActive bool`: the scope decides which tabs
// the header owns, which surface receives keys and mouse events, and what the
// destination title says. The project underneath is untouched by a scope
// change — no registry.Reinit, no worktree move, no plugin lifecycle.
type AppScope uint8

const (
	ScopeProject AppScope = iota
	ScopeGlobal
)

// GlobalTab is a tab owned by the global space. These are not plugin indices
// and must never be encoded as such: a disabled tab would otherwise shift an
// index onto the wrong action.
type GlobalTab uint8

const (
	GlobalAgents GlobalTab = iota
	GlobalWorkspaces
	GlobalTasks
)

// Name is the header label for a global tab.
func (t GlobalTab) Name() string {
	switch t {
	case GlobalAgents:
		return "Agents"
	case GlobalWorkspaces:
		return "Workspaces"
	case GlobalTasks:
		return "Tasks"
	}
	return ""
}

// context is the keymap context the tab owns while it is visible. The Tasks tab
// is absent: its context is reported by the Tasks model itself.
func (t GlobalTab) context() string {
	switch t {
	case GlobalAgents:
		return "overview"
	case GlobalWorkspaces:
		return "global-workspaces"
	}
	return "overview"
}

// tabRef identifies one header tab together with the scope that owns it.
// Header rendering, hit regions, numeric shortcuts, and cycling all speak in
// tabRefs so a click or a key can never activate a tab from the other scope.
type tabRef struct {
	scope  AppScope
	plugin int       // meaningful when scope == ScopeProject
	global GlobalTab // meaningful when scope == ScopeGlobal
}

func projectTabRef(index int) tabRef {
	return tabRef{scope: ScopeProject, plugin: index}
}

func globalTabRef(tab GlobalTab) tabRef {
	return tabRef{scope: ScopeGlobal, global: tab}
}

func (r tabRef) same(other tabRef) bool {
	if r.scope != other.scope {
		return false
	}
	if r.scope == ScopeGlobal {
		return r.global == other.global
	}
	return r.plugin == other.plugin
}

// inGlobalScope reports whether the global space owns the screen.
func (m Model) inGlobalScope() bool { return m.scope == ScopeGlobal }

// globalTabsVisible lists the global tabs in header order. Tasks appears only
// while its feature is enabled — the host is nil otherwise.
func (m Model) globalTabsVisible() []GlobalTab {
	tabs := []GlobalTab{GlobalAgents, GlobalWorkspaces}
	if m.globalTasks != nil {
		tabs = append(tabs, GlobalTasks)
	}
	return tabs
}

// visibleTabs returns the tabs owned by the active scope, in header order.
func (m Model) visibleTabs() []tabRef {
	if m.inGlobalScope() {
		global := m.globalTabsVisible()
		refs := make([]tabRef, 0, len(global))
		for _, tab := range global {
			refs = append(refs, globalTabRef(tab))
		}
		return refs
	}
	plugins := m.registry.Plugins()
	refs := make([]tabRef, 0, len(plugins))
	for i := range plugins {
		refs = append(refs, projectTabRef(i))
	}
	return refs
}

// tabLabel is the text painted in the header for a tab.
func (m Model) tabLabel(ref tabRef) string {
	if ref.scope == ScopeGlobal {
		return ref.global.Name()
	}
	plugins := m.registry.Plugins()
	if ref.plugin < 0 || ref.plugin >= len(plugins) {
		return ""
	}
	return plugins[ref.plugin].Name()
}

// activeTab is the tab currently drawn active. It always belongs to the active
// scope, so nothing in the other scope can render as current.
func (m Model) activeTab() tabRef {
	if m.inGlobalScope() {
		return globalTabRef(m.globalTab)
	}
	return projectTabRef(m.activePlugin)
}

// activateTab performs the scope-correct activation for a tab the user clicked,
// numbered, or cycled onto.
func (m *Model) activateTab(ref tabRef) tea.Cmd {
	if ref.scope == ScopeGlobal {
		return m.setGlobalTab(ref.global)
	}
	m.exitOverview()
	return m.SetActivePlugin(ref.plugin)
}

// setGlobalTab switches the visible global tab. Switching tabs is cheap: it
// never reloads a project, and it starts collection only for the tab that
// becomes visible.
func (m *Model) setGlobalTab(tab GlobalTab) tea.Cmd {
	if !m.inGlobalScope() {
		return nil
	}
	if m.globalTab == tab {
		return nil
	}
	previous := m.globalTab
	m.globalTab = tab
	if previous == GlobalAgents && m.overview != nil {
		// Leaving the board: stop its collection rather than polling projects
		// behind a tab nobody is looking at. Slice 2 shares one catalog between
		// Agents and Workspaces; until then, only the visible tab collects.
		m.overview.Stop()
	}
	m.updateContext()
	return m.startVisibleGlobalTab()
}

// startVisibleGlobalTab starts whatever collection the visible global tab
// needs. Workspaces is a placeholder in this slice and collects nothing; Tasks
// is already alive and owned by the global tab host, not by tab visibility.
func (m *Model) startVisibleGlobalTab() tea.Cmd {
	if !m.inGlobalScope() {
		return nil
	}
	if m.globalTab == GlobalAgents && m.overview != nil {
		return m.overview.Start(m.overviewProjects())
	}
	return nil
}

// cycleTabs moves forward (+1) or backward (-1) through the active scope's own
// tabs. It never crosses the scope boundary.
func (m *Model) cycleTabs(delta int) tea.Cmd {
	tabs := m.visibleTabs()
	if len(tabs) == 0 {
		return nil
	}
	current := 0
	active := m.activeTab()
	for i, ref := range tabs {
		if ref.same(active) {
			current = i
			break
		}
	}
	next := ((current+delta)%len(tabs) + len(tabs)) % len(tabs)
	return m.activateTab(tabs[next])
}

// selectTabByNumber activates the nth visible tab of the active scope. A number
// with no tab behind it does nothing rather than falling through to the other
// scope's list.
func (m *Model) selectTabByNumber(index int) tea.Cmd {
	tabs := m.visibleTabs()
	if index < 0 || index >= len(tabs) {
		return nil
	}
	return m.activateTab(tabs[index])
}

// globalTasksHost owns the embedded Tasks surface.
//
// Tasks is an app-global hosted surface, not a project plugin and not an
// Overview data projection. Keeping it out of the plugin registry is the whole
// point: registry.Reinit stops and rebuilds every plugin it owns on each
// project switch, which for Tasks meant closing the store, tearing down the
// agent queue, and rewriting its session — once per project switch. Here it is
// built once, stays alive across project switches and scope toggles, and closes
// exactly once at shutdown.
type globalTasksHost struct {
	plugin plugin.Plugin
	ctx    *plugin.Context

	// starts/stops count lifecycle transitions so a test can prove a project
	// switch does neither.
	starts int
	stops  int
}

// newGlobalTasksHost builds the host when the tasks_plugin feature is enabled.
// Construction performs no I/O: the Tasks model is built by the command Start
// returns, after the first frame.
func newGlobalTasksHost(base *plugin.Context, km plugin.BindingRegistrar) *globalTasksHost {
	ctx := &plugin.Context{Keymap: km}
	if base != nil {
		copied := *base
		copied.Keymap = km
		ctx = &copied
	}
	return &globalTasksHost{plugin: tasks.New(), ctx: ctx}
}

// start initializes and starts the host exactly once.
func (h *globalTasksHost) start() tea.Cmd {
	if h == nil || h.plugin == nil || h.starts > 0 {
		return nil
	}
	h.starts++
	if err := h.plugin.Init(h.ctx); err != nil {
		return nil
	}
	return h.plugin.Start()
}

// stop closes the model once. Sidecar's quit paths all route through
// Model.shutdown, so a second call is a no-op rather than a double Close.
func (h *globalTasksHost) stop() {
	if h == nil || h.plugin == nil || h.stops > 0 {
		return
	}
	h.stops++
	h.plugin.Stop()
}

// update forwards a message to the hosted surface. It is called for every
// message sidecar forwards to plugins, so the Tasks file watch, agent queue,
// and ticks keep running while another tab is visible.
func (h *globalTasksHost) update(msg tea.Msg) tea.Cmd {
	if h == nil || h.plugin == nil {
		return nil
	}
	updated, cmd := h.plugin.Update(msg)
	if updated != nil {
		h.plugin = updated
	}
	return cmd
}

// agentsBoardVisible reports that the cross-project Agents board owns the
// screen. Board navigation and its validated cross-project transitions are
// accepted only while it does.
func (m Model) agentsBoardVisible() bool {
	return m.inGlobalScope() && m.globalTab == GlobalAgents
}

// globalMouse routes a mouse event (already offset past the header) to the
// visible global tab. The Workspaces placeholder has nothing to click.
func (m *Model) globalMouse(msg tea.Msg) tea.Cmd {
	switch {
	case m.globalTasksFocused():
		return m.globalTasks.update(msg)
	case m.globalTab == GlobalAgents && m.overview != nil:
		return m.overview.Update(msg)
	}
	return nil
}

// globalTasksPlugin returns the hosted Tasks surface, or nil when the feature
// is disabled.
func (m Model) globalTasksPlugin() plugin.Plugin {
	if m.globalTasks == nil {
		return nil
	}
	return m.globalTasks.plugin
}

// globalTasksFocused reports that the hosted Tasks surface is the visible
// global tab and therefore owns keyboard and mouse input.
func (m Model) globalTasksFocused() bool {
	return m.inGlobalScope() && m.globalTab == GlobalTasks && m.globalTasksPlugin() != nil
}

// focusedSurface is the plugin that owns input right now: the hosted Tasks
// surface while its global tab is visible, otherwise the active project plugin.
func (m Model) focusedSurface() plugin.Plugin {
	if m.globalTasksFocused() {
		return m.globalTasksPlugin()
	}
	if m.inGlobalScope() {
		return nil
	}
	return m.ActivePlugin()
}

// globalOverlayOwnsKeys reports that a global view sidecar draws itself — the
// Agents board or the Workspaces placeholder — covers the plugin pane. While it
// does, keys must not reach the hidden project plugin. The hosted Tasks tab is
// deliberately excluded: it is a real surface that wants its own keys.
func (m Model) globalOverlayOwnsKeys() bool {
	return m.inGlobalScope() && !m.globalTasksFocused()
}

// surfacePlugins lists every plugin the palette, help, and command lookup can
// reach: the project registry plus the global Tasks host.
func (m Model) surfacePlugins() []plugin.Plugin {
	plugins := m.registry.Plugins()
	if host := m.globalTasksPlugin(); host != nil {
		plugins = append(plugins, host)
	}
	return plugins
}

// shutdown saves the active project plugin and closes everything sidecar owns,
// including the global Tasks host. Every quit path calls it, so the Tasks model
// is closed exactly once however the user leaves.
func (m *Model) shutdown() {
	if activePlugin := m.ActivePlugin(); activePlugin != nil {
		_ = state.SetActivePlugin(m.ui.WorkDir, activePlugin.ID())
	}
	m.registry.Stop()
	m.globalTasks.stop()
}
