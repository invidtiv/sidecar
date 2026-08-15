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
	GlobalSessions GlobalTab = iota
	GlobalActivity
	GlobalTasks

	// Deprecated compatibility names. Session state and older callers used the
	// surface names before the header established the fleet vocabulary.
	GlobalWorkspaces = GlobalSessions
	GlobalAgents     = GlobalActivity
)

// Name is the header label for a global tab.
func (t GlobalTab) Name() string {
	switch t {
	case GlobalSessions:
		return "Sessions"
	case GlobalActivity:
		return "Activity"
	case GlobalTasks:
		return "Tasks"
	}
	return ""
}

// persistID is the stable state.json value for a global tab.
func (t GlobalTab) persistID() string {
	switch t {
	case GlobalSessions:
		return "sessions"
	case GlobalActivity:
		return "activity"
	case GlobalTasks:
		return "tasks"
	}
	return ""
}

func parseGlobalTabID(id string) (GlobalTab, bool) {
	switch id {
	case "sessions", "workspaces":
		return GlobalSessions, true
	case "activity", "agents":
		return GlobalActivity, true
	case "tasks":
		return GlobalTasks, true
	}
	return 0, false
}

// context is the keymap context the tab owns while it is visible. The Tasks tab
// is absent: its context is reported by the Tasks model itself.
func (t GlobalTab) context() string {
	switch t {
	case GlobalActivity:
		return "overview"
	case GlobalSessions:
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

// globalTabsVisible lists the global tabs in header order. Each tab appears
// only while the thing behind it exists: Agents and Workspaces are projections
// of the cross-project catalog the Overview model owns, and Tasks is the hosted
// surface. Either feature can be disabled independently, and a tab with nothing
// behind it must not be rendered, numbered, or cycled onto.
func (m Model) globalTabsVisible() []GlobalTab {
	var tabs []GlobalTab
	if m.overview != nil {
		tabs = append(tabs, GlobalSessions, GlobalActivity)
	}
	if m.globalTasks != nil {
		tabs = append(tabs, GlobalTasks)
	}
	return tabs
}

// globalScopeAvailable reports that the global space has at least one tab to
// show. It is the entry gate for K, q, the brand click, and the switcher's
// Overview destination.
//
// Tying entry to the tabs that actually exist — rather than to the Overview
// model alone — is what keeps an enabled Tasks host reachable when the
// cross-project Overview feature is off. Tasks is no longer a project plugin,
// so gating on Overview would leave it running, costing its full lifecycle,
// with no way to look at it.
func (m Model) globalScopeAvailable() bool {
	return len(m.globalTabsVisible()) > 0
}

// ensureVisibleGlobalTab moves off a tab whose feature is disabled. The
// remembered global tab survives the process, so entering the space must not
// land on a tab that no longer exists.
func (m *Model) ensureVisibleGlobalTab() {
	tabs := m.globalTabsVisible()
	if len(tabs) == 0 {
		return
	}
	for _, tab := range tabs {
		if tab == m.globalTab {
			return
		}
	}
	m.globalTab = tabs[0]
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
		if !m.inGlobalScope() {
			enter := m.enterOverview()
			if m.globalTab == ref.global {
				return enter
			}
			return tea.Batch(enter, m.setGlobalTab(ref.global))
		}
		return m.setGlobalTab(ref.global)
	}
	m.leaveOverview(false)
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
	visible := false
	for _, candidate := range m.globalTabsVisible() {
		if candidate == tab {
			visible = true
			break
		}
	}
	if !visible {
		return nil
	}
	previous := m.globalTab
	m.globalTab = tab
	_ = state.SetLastGlobalTab(tab.persistID())
	if catalogTab(previous) && !catalogTab(tab) && m.overview != nil {
		// Leaving the catalog entirely (for Tasks): stop collecting rather than
		// polling projects behind a tab nobody is looking at. Moving between
		// Agents and Workspaces does not stop anything — they are two
		// projections of one cache, and restarting here would be exactly the
		// duplicated fan-out this design exists to avoid.
		m.overview.Stop()
	}
	m.updateContext()
	return m.startVisibleGlobalTab()
}

// catalogTab reports that a global tab is a projection of the cross-project
// catalog. Agents and Workspaces both are; Tasks owns its own store.
func catalogTab(tab GlobalTab) bool {
	return tab == GlobalActivity || tab == GlobalSessions
}

// startVisibleGlobalTab starts whatever collection the visible global tab
// needs. Agents and Workspaces share one collector, so the second of them to
// become visible reuses the cycle the first started instead of launching its
// own. Tasks is already alive and owned by the global tab host, not by tab
// visibility.
func (m *Model) startVisibleGlobalTab() tea.Cmd {
	if !m.inGlobalScope() {
		return nil
	}
	if m.overview == nil {
		return nil
	}
	// The selected preview reads a pane only while its own tab is visible, so
	// the scope tells it directly rather than inferring visibility from renders.
	// The same switch releases a terminal the user was typing into: a tab nobody
	// is looking at holds neither a capture nor a live pane.
	visible := m.overview.SetWorkspacesVisible(m.globalTab == GlobalSessions)
	if catalogTab(m.globalTab) {
		return tea.Batch(m.overview.Ensure(m.overviewProjects()), visible)
	}
	return visible
}

// globalWorkspacesVisible reports that the cross-project Workspaces browser
// owns the screen, and therefore its own keys and mouse events.
func (m Model) globalWorkspacesVisible() bool {
	return m.inGlobalScope() && m.globalTab == GlobalSessions && m.overview != nil
}

// globalWorkspacesFilterFocused reports that the browser's inline filter is
// taking typed text, so the app must keep its printable shortcuts off it.
func (m Model) globalWorkspacesFilterFocused() bool {
	return m.globalWorkspacesVisible() && m.overview.WorkspacesFilterFocused()
}

// globalWorkspacesFilterActive reports that a query is still narrowing the
// browser's list, whether or not the input has focus. An accepted query is
// still visible on screen, so esc has to clear what the user can see before it
// can mean "leave the global space".
func (m Model) globalWorkspacesFilterActive() bool {
	return m.globalWorkspacesVisible() && m.overview.WorkspacesFilterActive()
}

// globalWorkspacesPreviewFocused reports that the browser's preview owns the
// keyboard, so its own keys — including esc — belong to it rather than to
// sidecar's scope exit. That covers both of the preview's states: watching,
// where esc returns focus to the list, and typing into a live pane, where esc
// is the pane's and a second one leaves the mode.
func (m Model) globalWorkspacesPreviewFocused() bool {
	return m.globalWorkspacesVisible() && m.overview.PreviewFocused()
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
	return m.inGlobalScope() && m.globalTab == GlobalActivity
}

// globalCatalogNavigable reports that a projection of the cross-project catalog
// owns the screen, and therefore that an activation from it may still complete.
//
// Both catalog tabs activate the same validated navigation: the Agents board
// through a card, the Workspaces browser through Enter or a double click. Tying
// the in-flight validation to "a catalog tab is visible" — rather than to the
// Agents board alone — is what lets the browser navigate at all, while a
// validation that lands after the user left the catalog is still dropped.
func (m Model) globalCatalogNavigable() bool {
	return m.inGlobalScope() && catalogTab(m.globalTab) && m.overview != nil
}

// globalMouse routes a mouse event (already offset past the header) to the
// visible global tab.
func (m *Model) globalMouse(msg tea.Msg) tea.Cmd {
	switch {
	case m.globalTasksFocused():
		return m.globalTasks.update(msg)
	case m.globalTab == GlobalActivity && m.overview != nil:
		return m.overview.Update(msg)
	case m.globalWorkspacesVisible():
		return m.overview.WorkspacesMouse(msg)
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

// globalSurfaceWantsEsc reports that the focused global surface will handle esc
// itself, so sidecar's scope-exit must not take it first.
//
// Those surfaces are the Workspaces browser — whose filter and preview both
// give esc their own meaning — and the hosted Tasks tab, whose
// overlays, pickers, and prompts are dismissed by esc through precedence level
// 2 (a blocking overlay or text-input context) or level 3 (a live contextual
// binding). All of them run after the modal/esc switch at the top of
// handleKeyMsg, which is why the question has to be asked there. A Tasks root
// context and an unfiltered, list-focused Workspaces tab want none of them, so
// esc there still leaves the global space.
func (m *Model) globalSurfaceWantsEsc() bool {
	// The Workspaces filter answers esc itself: while focused, the first press
	// clears the query and the second releases focus; once accepted, esc still
	// clears the query that is narrowing the list. Only an esc with nothing
	// filtered means "leave the global space".
	if m.globalWorkspacesFilterActive() {
		return true
	}
	// The sort/filter fly-out is a modal overlay: esc closes it and returns
	// to the list. It is not a third browse mode, but it must still keep
	// scope-exit from stealing the key.
	if m.globalWorkspacesVisible() && m.overview != nil && m.overview.ViewFlyoutOpen() {
		return true
	}
	if m.globalWorkspacesVisible() && m.overview != nil && m.overview.RenameShellOpen() {
		return true
	}
	// While typing, esc belongs to the pane (a second one leaves the mode).
	// Only an esc pressed with the list focused means "leave the global space".
	if m.globalWorkspacesPreviewFocused() {
		return true
	}
	if !m.globalTasksFocused() {
		return false
	}
	return m.consumesTextInput() || m.pluginBlocksGlobalKeys() || m.pluginClaimsKey("esc")
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

// shutdown saves the active project plugin and closes everything sidecar owns:
// the project registry, the global Tasks host, and the global browser's
// embedded terminal, whose control subprocess outlives the process otherwise.
// Every quit path calls it, so each is closed exactly once however the user
// leaves.
func (m *Model) shutdown() {
	if activePlugin := m.ActivePlugin(); activePlugin != nil {
		_ = state.SetActivePlugin(m.ui.WorkDir, activePlugin.ID())
	}
	m.registry.Stop()
	m.globalTasks.stop()
	if m.overview != nil {
		m.overview.Stop()
	}
}
