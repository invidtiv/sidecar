package app

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/hosts"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
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

// persistID is the stable state.json value for a scope. Together with
// GlobalTab.persistID it records the whole top-level selection: which space,
// and which tab inside it.
func (s AppScope) persistID() string {
	switch s {
	case ScopeGlobal:
		return "global"
	case ScopeProject:
		return "project"
	}
	return ""
}

// parseAppScopeID reads a persisted scope back. An empty or unrecognised value
// is not an error and not a scope: the caller keeps the project space, which is
// what a first run and an upgrade from a version that never wrote the key both
// have to get.
func parseAppScopeID(id string) (AppScope, bool) {
	switch id {
	case "global":
		return ScopeGlobal, true
	case "project":
		return ScopeProject, true
	}
	return ScopeProject, false
}

// persistScope records the top-level space the user is now in, so the next
// launch reopens it. It is called only from the two transitions that change the
// scope, so crossing the boundary costs one small write and moving between
// project tabs costs none.
func (m *Model) persistScope() {
	_ = state.SetLastScope(m.scope.persistID())
}

// The two app-owned global surfaces. Everything else in the global space is a
// plugin, named by its descriptor ID.
const (
	GlobalSessions = "sessions"
	GlobalActivity = "activity"

	// Deprecated compatibility names. Session state and older callers used the
	// surface names before the header established the fleet vocabulary.
	GlobalWorkspaces = GlobalSessions
	GlobalAgents     = GlobalActivity
)

// globalSurface is one entry in the global space's tab row.
//
// The row used to be an enum with three values and named keys, which meant a
// second global plugin was a new enum value and a new case in a dozen switches.
// It is a descriptor-driven ordered slice instead: Sessions and Activity are
// app-owned and always first, then one entry per enabled global-scope plugin
// descriptor, in descriptor order.
//
// The identity is the ID, never the position: a disabled tab would otherwise
// shift an index onto the wrong action, and a persisted index would name
// something else after the plugin behind it was turned off.
type globalSurface struct {
	// id is the descriptor ID, and the value persisted in state.json.
	id string
	// name is the header label.
	name string
	// key is the number-row key that addresses this entry directly, or empty
	// when the entry has none. It is a property of the entry, not of its
	// position, so a disabled tab does not slide another one onto its key.
	key string
	// context is the keymap context an app-owned surface owns while visible. A
	// hosted plugin reports its own, so this is empty for one.
	context string
	// host is the hosted plugin behind the entry, nil for an app-owned surface.
	host *globalPluginHost
}

// globalTabKeys are the number-row keys the global entries take, in order:
// Sessions, Activity, then the first plugin-provided tab. There is no fourth —
// a further global plugin is reachable through `[`/`]`, the palette, and the
// project switcher, which is the same answer an eighth project tab gets.
var globalTabKeys = []string{"8", "9", "0"}

// parseGlobalTabID reads a persisted global tab back, normalising the surface
// names state.json used before the header established the fleet vocabulary.
// Any other non-empty value is taken as a plugin descriptor ID and checked
// against the tabs that actually exist by ensureVisibleGlobalTab.
func parseGlobalTabID(id string) (string, bool) {
	switch id {
	case "":
		return "", false
	case "workspaces":
		return GlobalSessions, true
	case "agents":
		return GlobalActivity, true
	}
	return id, true
}

// tabRef identifies one header tab together with the scope that owns it.
// Header rendering, hit regions, numeric shortcuts, and cycling all speak in
// tabRefs so a click or a key can never activate a tab from the other scope.
type tabRef struct {
	scope  AppScope
	plugin int    // meaningful when scope == ScopeProject
	global string // surface ID, meaningful when scope == ScopeGlobal
}

func projectTabRef(index int) tabRef {
	return tabRef{scope: ScopeProject, plugin: index}
}

func globalTabRef(id string) tabRef {
	return tabRef{scope: ScopeGlobal, global: id}
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
// only while the thing behind it exists: Activity and Sessions are projections
// of the cross-project catalog the Overview model owns, and each further entry
// is a hosted global plugin. Either can be disabled independently, and a tab
// with nothing behind it must not be rendered, numbered, or cycled onto.
//
// Number keys are assigned here, in order, from globalTabKeys: Sessions keeps
// 8 and Activity keeps 9 whether or not any plugin is hosted, and the first
// plugin-provided tab gets 0 whether or not the catalog is on — the key belongs
// to the entry, not to its position in the row.
func (m Model) globalTabsVisible() []globalSurface {
	var tabs []globalSurface
	if m.overview != nil {
		tabs = append(tabs,
			globalSurface{id: GlobalSessions, name: "Sessions", key: globalTabKeys[0], context: "global-workspaces"},
			globalSurface{id: GlobalActivity, name: "Activity", key: globalTabKeys[1], context: "overview"},
		)
	}
	hosted := 0
	for _, host := range m.globalHosts {
		if host == nil || host.plugin == nil {
			continue
		}
		surface := globalSurface{id: host.id(), name: host.label(), host: host}
		if hosted == 0 {
			surface.key = globalTabKeys[2]
		}
		hosted++
		tabs = append(tabs, surface)
	}
	return tabs
}

// globalSurfaceByID finds a visible global tab by ID.
func (m Model) globalSurfaceByID(id string) (globalSurface, bool) {
	for _, tab := range m.globalTabsVisible() {
		if tab.id == id {
			return tab, true
		}
	}
	return globalSurface{}, false
}

// activeGlobalSurface is the visible global tab, if the global space owns the
// screen and the remembered tab still exists.
func (m Model) activeGlobalSurface() (globalSurface, bool) {
	if !m.inGlobalScope() {
		return globalSurface{}, false
	}
	return m.globalSurfaceByID(m.globalTab)
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
		if tab.id == m.globalTab {
			return
		}
	}
	m.globalTab = tabs[0].id
}

// visibleTabs returns the tabs owned by the active scope, in header order.
func (m Model) visibleTabs() []tabRef {
	if m.inGlobalScope() {
		global := m.globalTabsVisible()
		refs := make([]tabRef, 0, len(global))
		for _, tab := range global {
			refs = append(refs, globalTabRef(tab.id))
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

// maxNumberedProjectTabs caps the positional number row at 1-7.
//
// The header is one row of entries, and the number row addresses it as one row:
// 1-7 are the project's plugin tabs, 8/9/0 are the global entries the left
// cluster paints (Sessions / Activity / Tasks). Capping the positional keys is
// what buys those three a stable, scope-independent meaning — pressing 8 always
// means Sessions, never "whatever the eighth plugin happens to be".
//
// The design direction is exactly seven project tabs, so the cap costs nothing
// today; an eighth tab is still reachable with [ / ] and from the palette.
const maxNumberedProjectTabs = 7

// headerEntries returns every entry the header row can activate, in the order
// the header paints them: the global entries in the left cluster (Sessions,
// Activity, and Tasks when its feature is on), then the project's plugin tabs
// in the right cluster.
//
// This is the ring `[` and `]` walk, and it is deliberately the SAME ring in
// both scopes. The project tabs are painted only while project scope is active,
// but they stay in the ring from the global space: a cycle you can step into
// and never step out of is a trap, and `]` followed by `[` has to be the
// identity wherever the user is standing.
func (m Model) headerEntries() []tabRef {
	global := m.globalTabsVisible()
	entries := make([]tabRef, 0, len(global)+8)
	for _, tab := range global {
		entries = append(entries, globalTabRef(tab.id))
	}
	if m.registry != nil {
		for i := range m.registry.Plugins() {
			entries = append(entries, projectTabRef(i))
		}
	}
	return entries
}

// numberedProjectTabs is how many project tabs the number row can actually
// reach: one per plugin, never more than maxNumberedProjectTabs.
func (m Model) numberedProjectTabs() int {
	if m.registry == nil {
		return 0
	}
	return min(len(m.registry.Plugins()), maxNumberedProjectTabs)
}

// globalTabForKey maps 8/9/0 back to the entry they address, or reports that
// nothing visible answers that key.
func (m Model) globalTabForKey(key string) (string, bool) {
	if key == "" {
		return "", false
	}
	for _, tab := range m.globalTabsVisible() {
		if tab.key == key {
			return tab.id, true
		}
	}
	return "", false
}

// tabLabel is the text painted in the header for a tab.
func (m Model) tabLabel(ref tabRef) string {
	if ref.scope == ScopeGlobal {
		if tab, ok := m.globalSurfaceByID(ref.global); ok {
			return tab.name
		}
		return ""
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
	// Configuration covers the tab it would switch to, so it closes first. Left
	// open, the tab changed invisibly underneath and a later escape restored the
	// snapshot taken when Configuration opened, undoing the move. Closing is
	// cheap now that the surface remembers where the user was.
	var closed tea.Cmd
	if m.configOpen() {
		closed = m.closeConfiguration()
	}
	if ref.scope == ScopeGlobal {
		if !m.inGlobalScope() {
			enter := m.enterOverview()
			if m.globalTab == ref.global {
				return tea.Batch(closed, enter)
			}
			return tea.Batch(closed, enter, m.setGlobalTab(ref.global))
		}
		return tea.Batch(closed, m.setGlobalTab(ref.global))
	}
	m.leaveOverview(false)
	return tea.Batch(closed, m.SetActivePlugin(ref.plugin))
}

// setGlobalTab switches the visible global tab. Switching tabs is cheap: it
// never reloads a project, and it starts collection only for the tab that
// becomes visible.
func (m *Model) setGlobalTab(id string) tea.Cmd {
	if !m.inGlobalScope() {
		return nil
	}
	if m.globalTab == id {
		return nil
	}
	if _, ok := m.globalSurfaceByID(id); !ok {
		return nil
	}
	previous := m.globalTab
	var deckCmd tea.Cmd
	if h := m.currentContentDeck(); h != nil {
		h.releaseAppContentInputs()
		h.laidOut = false
		h.links = nil
		h.press = nil
		if h.live != nil {
			deckCmd = h.live.Reconcile()
		}
	}
	m.globalTab = id
	_ = state.SetLastGlobalTab(id)
	if catalogTab(previous) && !catalogTab(id) && m.overview != nil {
		// Leaving the catalog entirely (for Tasks): stop collecting rather than
		// polling projects behind a tab nobody is looking at. Moving between
		// Agents and Workspaces does not stop anything — they are two
		// projections of one cache, and restarting here would be exactly the
		// duplicated fan-out this design exists to avoid.
		m.overview.Stop()
	}
	m.updateContext()
	return tea.Batch(deckCmd, m.startVisibleGlobalTab())
}

// catalogTab reports that a global tab is a projection of the cross-project
// catalog. Agents and Workspaces both are; Tasks owns its own store.
func catalogTab(id string) bool {
	return id == GlobalActivity || id == GlobalSessions
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

// sessionsOwnsCreateSplit reports that a create --split request belongs to the
// Sessions preview, not the project workspace plugin. Both surfaces receive
// every uirequest; without this the plugin writes shell:<tmux> while the user
// is looking at the path-prefixed Sessions surface.
func (m Model) sessionsOwnsCreateSplit(req uirequest.Request) bool {
	return m.globalWorkspacesVisible() && req.Action == uirequest.ActionCreate && strings.TrimSpace(req.Options.Split) != ""
}

// uiRequestLanding names which surface is the screen for a uirequest.
type uiRequestLanding int

const (
	uiRequestLandingNone uiRequestLanding = iota
	uiRequestLandingSessions
	uiRequestLandingBoundWorkspace
)

// uiRequestLanding reports which surface is the screen for req. Relayed
// open/layout (Origin.HostID set) never queue: Sessions wins when its matching
// row is on screen, otherwise the bound project workspace wins when this TUI
// is looking at that host project and holds the lease, otherwise nobody.
func (m Model) uiRequestLanding(req uirequest.Request) uiRequestLanding {
	if req.Origin.HostID == "" {
		return uiRequestLandingNone
	}
	if m.globalWorkspacesVisible() && m.overview != nil && m.overview.RelayedRowOnScreen(req) {
		return uiRequestLandingSessions
	}
	if m.boundWorkspaceIsRelayedScreen(req) {
		return uiRequestLandingBoundWorkspace
	}
	return uiRequestLandingNone
}

func (m Model) boundWorkspaceIsRelayedScreen(req uirequest.Request) bool {
	if m.scope != ScopeProject || m.boundDestination.HostID == "" {
		return false
	}
	if req.Origin.HostID != m.boundDestination.HostID {
		return false
	}
	if req.Origin.ProjectKey != "" && !hosts.OriginNamesProject(req.Origin.ProjectKey, m.boundDestination.ProjectKey) {
		return false
	}
	if req.Origin.TmuxSession == "" || !m.boundInventoryHasSession(req.Origin.TmuxSession) {
		return false
	}
	if !tty.ThisInstanceOwnsSession(req.Origin.TmuxSession) {
		return false
	}
	active := m.ActivePlugin()
	return active != nil && active.ID() == workspacePluginID
}

func (m Model) boundInventoryHasSession(session string) bool {
	if session == "" {
		return false
	}
	for _, ws := range m.boundHostWorkspaces() {
		if ws.TmuxName == session {
			return true
		}
	}
	return false
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

// cycleTabs moves forward (+1) or backward (-1) through the whole header row —
// the global entries and the project's plugin tabs — wrapping at both ends.
//
// It used to stop at the scope boundary, which made the header two rings the
// user had to know about: `]` walked the plugin tabs and quietly refused to
// reach Sessions, even though Sessions was painted in the same bar three
// columns to the left. One bar, one ring.
func (m *Model) cycleTabs(delta int) tea.Cmd {
	entries := m.headerEntries()
	if len(entries) == 0 {
		return nil
	}
	current := 0
	active := m.activeTab()
	for i, ref := range entries {
		if ref.same(active) {
			current = i
			break
		}
	}
	next := ((current+delta)%len(entries) + len(entries)) % len(entries)
	return m.activateTab(entries[next])
}

// selectProjectTabByNumber activates the nth project plugin tab (0-based) for
// keys 1-7, entering project scope from the global space if that is where the
// user pressed it. A number with no plugin behind it does nothing — it must
// never slide onto a global entry, which is the whole reason 8/9/0 are named
// rather than positional.
func (m *Model) selectProjectTabByNumber(index int) tea.Cmd {
	if index < 0 || index >= maxNumberedProjectTabs {
		return nil
	}
	if m.registry == nil || index >= len(m.registry.Plugins()) {
		return nil
	}
	return m.activateTab(projectTabRef(index))
}

// selectGlobalTab activates a global header entry directly (8/9/0), from either
// scope.
//
// A tab whose feature is disabled does nothing at all: silently, and above all
// without falling through to anything else. `0` with the Tasks host off is a
// key that addresses an entry the header is not painting, and the honest
// response to that is no response — the same one 1-7 give for a plugin index
// that does not exist. A toast would be noise on a key the user can see has
// nothing behind it.
func (m *Model) selectGlobalTab(id string) tea.Cmd {
	if _, ok := m.globalSurfaceByID(id); ok {
		return m.activateTab(globalTabRef(id))
	}
	return nil
}

// globalPluginHost owns one global-scope embedded plugin.
//
// A global plugin is an app-hosted surface, not a project plugin and not an
// Overview data projection. Keeping it out of the plugin registry is the whole
// point: registry.Reinit stops and rebuilds every plugin it owns on each
// project switch, which for Tasks meant closing the store, tearing down the
// agent queue, and rewriting its session — once per project switch. Here it is
// built once, stays alive across project switches and scope toggles, and closes
// exactly once at shutdown.
//
// There is one of these per global descriptor. Nothing in it knows what the
// plugin is: Tasks used to be the type, and is now one value of it.
type globalPluginHost struct {
	descriptor plugin.Descriptor
	plugin     plugin.Plugin
	ctx        *plugin.Context

	// starts/stops count lifecycle transitions so a test can prove a project
	// switch does neither.
	starts int
	stops  int
}

// newGlobalPluginHost builds a host for one enabled global descriptor.
// Construction performs no I/O: the plugin's model is built by the command
// start returns, after the first frame.
func newGlobalPluginHost(d plugin.Descriptor, base *plugin.Context, km plugin.BindingRegistrar) *globalPluginHost {
	if d.New == nil {
		return nil
	}
	ctx := &plugin.Context{Keymap: km}
	if base != nil {
		copied := *base
		copied.Keymap = km
		ctx = &copied
	}
	return &globalPluginHost{descriptor: d, plugin: d.New(), ctx: ctx}
}

// id is the descriptor ID, which is also the persisted tab ID. It falls back to
// the plugin's own ID so a test that installs a host without a descriptor still
// names its tab.
func (h *globalPluginHost) id() string {
	if h == nil {
		return ""
	}
	if h.descriptor.ID != "" {
		return h.descriptor.ID
	}
	if h.plugin != nil {
		return h.plugin.ID()
	}
	return ""
}

// label is the header text for this plugin's tab.
//
// A protocol plugin's descriptor names it by its configured instance ID,
// because the descriptor exists before anything has run. Its own display name
// arrives with describe, and the surface is what carries it, so the plugin is
// asked first for that class and the descriptor is the fallback. An embedded
// plugin keeps the descriptor's label: it is a Go literal beside the plugin,
// and the two cannot disagree.
func (h *globalPluginHost) label() string {
	if h == nil {
		return ""
	}
	if h.descriptor.Class == plugin.ClassProtocol && h.plugin != nil {
		if name := h.plugin.Name(); name != "" {
			return name
		}
	}
	if h.descriptor.Name != "" {
		return h.descriptor.Name
	}
	if h.plugin != nil {
		return h.plugin.Name()
	}
	return ""
}

// start initializes and starts the host exactly once.
func (h *globalPluginHost) start() tea.Cmd {
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
func (h *globalPluginHost) stop() {
	if h == nil || h.plugin == nil || h.stops > 0 {
		return
	}
	h.stops++
	h.plugin.Stop()
}

// update forwards a message to the hosted surface. It is called for every
// message sidecar forwards to plugins, so a global plugin's file watch, queue,
// and ticks keep running while another tab is visible.
func (h *globalPluginHost) update(msg tea.Msg) tea.Cmd {
	if h == nil || h.plugin == nil {
		return nil
	}
	updated, cmd := h.plugin.Update(msg)
	if updated != nil {
		h.plugin = updated
	}
	return cmd
}

// newGlobalPluginHosts builds one host per enabled global descriptor, in
// descriptor order. It runs before the first frame and must stay free of I/O.
func newGlobalPluginHosts(descriptors []plugin.Descriptor, cfg *config.Config, base *plugin.Context, km plugin.BindingRegistrar) []*globalPluginHost {
	var hosts []*globalPluginHost
	for _, d := range descriptors {
		if d.Scope != plugin.ScopeGlobal || !d.HasPlacement(plugin.PlacementTab) {
			continue
		}
		if !d.IsEnabled(cfg) {
			continue
		}
		if host := newGlobalPluginHost(d, base, km); host != nil {
			hosts = append(hosts, host)
		}
	}
	return hosts
}

// startGlobalHosts starts every hosted global plugin. They outlive the project
// registry: none of them is in it, so a later project switch cannot stop or
// rebuild one. Each model is built by the returned command, after the first
// frame.
func (m *Model) startGlobalHosts() []tea.Cmd {
	var cmds []tea.Cmd
	for _, host := range m.globalHosts {
		if cmd := host.start(); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return cmds
}

// stopGlobalHosts closes every hosted global plugin exactly once.
func (m *Model) stopGlobalHosts() {
	for _, host := range m.globalHosts {
		host.stop()
	}
}

// updateGlobalHosts forwards one message to every hosted global plugin,
// whichever tab is visible.
func (m *Model) updateGlobalHosts(msg tea.Msg) tea.Cmd {
	var cmds []tea.Cmd
	for _, host := range m.globalHosts {
		if cmd := host.update(msg); cmd != nil {
			cmds = append(cmds, cmd)
		}
	}
	return tea.Batch(cmds...)
}

// globalHostByID finds a hosted global plugin by descriptor ID.
func (m Model) globalHostByID(id string) *globalPluginHost {
	for _, host := range m.globalHosts {
		if host != nil && host.id() == id {
			return host
		}
	}
	return nil
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
	case m.globalPluginFocused():
		if mouseMsg, ok := msg.(tea.MouseMsg); ok {
			if handled, cmd := m.handleAppContentEditMouse(mouseMsg); handled {
				return cmd
			}
			if cmd, handled := m.appContentMouse(mouseMsg); handled {
				return cmd
			}
		}
		return m.focusedGlobalHost().update(msg)
	case m.globalTab == GlobalActivity && m.overview != nil:
		return m.overview.Update(msg)
	case m.globalWorkspacesVisible():
		return m.overview.WorkspacesMouse(msg)
	}
	return nil
}

// focusedGlobalHost is the hosted global plugin whose tab is visible, or nil
// when the visible global tab is one of the app-owned surfaces.
func (m Model) focusedGlobalHost() *globalPluginHost {
	tab, ok := m.activeGlobalSurface()
	if !ok || tab.host == nil || tab.host.plugin == nil {
		return nil
	}
	return tab.host
}

// globalPluginPlugin returns the hosted plugin behind the visible global tab,
// or nil when an app-owned surface is showing.
func (m Model) globalPluginPlugin() plugin.Plugin {
	if host := m.focusedGlobalHost(); host != nil {
		return host.plugin
	}
	return nil
}

// globalPluginFocused reports that a hosted global plugin is the visible global
// tab and therefore owns keyboard and mouse input.
func (m Model) globalPluginFocused() bool { return m.focusedGlobalHost() != nil }

// globalSurfaceWantsEsc reports that the focused global surface will handle esc
// itself, so sidecar's scope-exit must not take it first.
//
// Those surfaces are the Workspaces browser — whose filter and preview both
// give esc their own meaning — and a hosted global plugin's tab, whose
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
	if m.globalWorkspacesVisible() && m.overview != nil && m.overview.CreateOpen() {
		return true
	}
	if m.globalWorkspacesVisible() && m.overview != nil && m.overview.DeleteOpen() {
		return true
	}
	// While typing, esc belongs to the pane (a second one leaves the mode).
	// Only an esc pressed with the list focused means "leave the global space".
	if m.globalWorkspacesPreviewFocused() {
		return true
	}
	if !m.globalPluginFocused() {
		return false
	}
	return m.consumesTextInput() || m.pluginBlocksGlobalKeys() || m.pluginClaimsKey("esc")
}

// focusedSurface is the plugin that owns input right now: the hosted global
// plugin while its tab is visible, otherwise the active project plugin.
func (m Model) focusedSurface() plugin.Plugin {
	if host := m.focusedGlobalHost(); host != nil {
		return host.plugin
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
	return m.inGlobalScope() && !m.globalPluginFocused()
}

// surfacePlugins lists every plugin the palette, help, and command lookup can
// reach: the project registry plus every hosted global plugin.
func (m Model) surfacePlugins() []plugin.Plugin {
	plugins := m.registry.Plugins()
	for _, host := range m.globalHosts {
		if host != nil && host.plugin != nil {
			plugins = append(plugins, host.plugin)
		}
	}
	return plugins
}

// shutdown saves the active project plugin and closes everything sidecar owns:
// the project registry, every hosted global plugin, and the global browser's
// embedded terminal, whose control subprocess outlives the process otherwise.
// Every quit path calls it, so each is closed exactly once however the user
// leaves.
func (m *Model) shutdown() {
	if activePlugin := m.ActivePlugin(); activePlugin != nil {
		_ = state.SetActivePlugin(m.ui.WorkDir, activePlugin.ID())
	}
	for _, h := range m.contentDecks {
		h.releaseAppContentInputs()
		if h.live != nil {
			h.live.Stop()
		}
	}
	m.registry.Stop()
	m.stopGlobalHosts()
	if m.overview != nil {
		m.overview.Stop()
		// Stop() runs whenever the global tab is left, so host connections
		// deliberately survive it. Shutdown is the one place they must not:
		// an ssh child that outlives Sidecar is a process the user did not ask
		// for and cannot see.
		m.overview.StopHosts()
	}
}
