package app

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// Slice 1 of docs/plans/active/global-overview-workspaces.md: the global and
// project spaces each own their own tabs, the Tasks host leaves the project
// plugin registry, and moving between spaces stays free of project work.
//
// Behaviour already covered elsewhere is not restated here: K/brand toggling
// and exact destination restoration (scope_baseline_test.go,
// TestLogoClickAndKToggleOverview), key/paste containment over an interactive
// plugin (TestOverviewSwallows*), narrow-header truncation
// (TestCompactOverviewKeepsAppHeaderAndFooterAt72x30), and the Agents board
// itself (internal/overview).

// hostedTestPlugin stands in for the embedded Tasks surface so these tests
// never open a real task store or start an agent queue.
type hostedTestPlugin struct {
	id      string
	context string
	focused bool

	// blocks stands in for an open Tasks overlay, picker, or prompt: the
	// surface owns the keyboard and sidecar's globals must not fire under it.
	blocks bool

	inits   int
	starts  int
	stops   int
	updates int
	keys    int
}

func (p *hostedTestPlugin) ID() string                 { return p.id }
func (p *hostedTestPlugin) Name() string               { return p.id }
func (p *hostedTestPlugin) Icon() string               { return "" }
func (p *hostedTestPlugin) Init(*plugin.Context) error { p.inits++; return nil }
func (p *hostedTestPlugin) Start() tea.Cmd             { p.starts++; return nil }
func (p *hostedTestPlugin) Stop()                      { p.stops++ }
func (p *hostedTestPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	p.updates++
	if _, ok := msg.(tea.KeyPressMsg); ok {
		p.keys++
	}
	return p, nil
}
func (p *hostedTestPlugin) View(int, int) string       { return "hosted " + p.id }
func (p *hostedTestPlugin) IsFocused() bool            { return p.focused }
func (p *hostedTestPlugin) SetFocused(f bool)          { p.focused = f }
func (p *hostedTestPlugin) Commands() []plugin.Command { return nil }
func (p *hostedTestPlugin) BlocksGlobalKeys() bool     { return p.blocks }
func (p *hostedTestPlugin) FocusContext() string {
	if p.context == "" {
		return p.id
	}
	return p.context
}

func (p *hostedTestPlugin) ClaimsKey(string) bool { return false }
func (p *hostedTestPlugin) QuitKeyExits() bool    { return true }

// scopeModelWithTasks is the baseline four-plugin project model with a hosted
// Tasks stand-in attached to the global tab owner.
func scopeModelWithTasks(t *testing.T) (Model, *hostedTestPlugin) {
	t.Helper()
	m, _ := scopeBaselineModel(t, "git")
	host := &hostedTestPlugin{id: "tasks", context: "tasks-list"}
	m.globalTasks = &globalTasksHost{plugin: host, ctx: &plugin.Context{Keymap: m.keymap}}
	m.updateContext()
	return m, host
}

func TestScopeTransitionsNeverReinitializeTheProject(t *testing.T) {
	m, plugins := scopeBaselineModel(t, "git")
	inits := totalInits(plugins)
	workDir, projectRoot, active := m.ui.WorkDir, m.ui.ProjectRoot, m.activePlugin

	steps := []struct {
		name string
		msg  tea.Msg
	}{
		{"enter global", tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift}},
		{"global tab Activity", tea.KeyPressMsg{Code: '9', Text: "9"}},
		// Backwards, so the step stays inside the global entries: the header
		// ring runs Sessions, Activity, then the project tabs, and this test is
		// about what a scope change costs, not about where the ring goes next.
		{"cycle global", tea.KeyPressMsg{Code: '[', Text: "["}},
		{"back to project", tea.KeyPressMsg{Code: tea.KeyEsc}},
		{"enter global again", tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift}},
		{"escape to project", tea.KeyPressMsg{Code: tea.KeyEsc}},
	}
	for _, step := range steps {
		updated, _ := m.Update(step.msg)
		m = asAppModel(t, updated)
		if got := totalInits(plugins); got != inits {
			t.Fatalf("%s reinitialized project plugins: inits %d -> %d", step.name, inits, got)
		}
		if m.ui.WorkDir != workDir || m.ui.ProjectRoot != projectRoot || m.activePlugin != active {
			t.Fatalf("%s moved the project underneath: work=%q root=%q plugin=%d",
				step.name, m.ui.WorkDir, m.ui.ProjectRoot, m.activePlugin)
		}
	}
	if m.inGlobalScope() {
		t.Fatal("the last step should have returned to project space")
	}
}

func TestNoCrossProjectCollectionUntilTheBoardIsVisible(t *testing.T) {
	runner := &countingOverviewRunner{}
	m, _ := scopeBaselineModel(t, "git")
	m.overview = overview.New(workspaceinventory.Collector{Runner: runner})

	// Startup and ordinary project work collect nothing.
	if cmd := m.Init(); cmd != nil {
		cmd()
	}
	for _, key := range []tea.KeyPressMsg{{Code: '2', Text: "2"}, {Code: '`', Text: "`"}} {
		updated, cmd := m.Update(key)
		m = asAppModel(t, updated)
		if cmd != nil {
			cmd()
		}
	}
	if runner.calls != 0 {
		t.Fatalf("project space ran the cross-project collector %d times", runner.calls)
	}

	// Entering the global space on the primary Sessions tab starts one cycle.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if cmd == nil {
		t.Fatal("entering the Sessions tab started no collection")
	}
	cmd()
	if runner.calls != 1 {
		t.Fatalf("entry ran the collector %d times, want 1", runner.calls)
	}

	// Activity is the second projection of the same catalog,
	// so switching onto it — and back — reuses the running cycle rather than
	// starting a second one.
	updated, cmd = m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	updated, cmd = m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	if runner.calls != 1 {
		t.Fatalf("switching between the two catalog tabs collected again: %d calls", runner.calls)
	}

	// Leaving the global space stops collection, so returning to the remembered
	// Sessions tab starts exactly one new shared cycle.
	updated, cmd = m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	updated, cmd = m.Update(tea.KeyPressMsg{Code: '1', Text: "1"})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if cmd == nil {
		t.Fatal("re-entering the Workspaces tab started no collection")
	}
	cmd()
	if m.globalTab != GlobalSessions {
		t.Fatalf("re-entry forgot the last global tab: %v", m.globalTab)
	}
	if runner.calls != 2 {
		t.Fatalf("re-entry collector calls = %d, want exactly one more shared cycle", runner.calls)
	}
}

func TestPersistedGlobalTabRestoresAfterRestart(t *testing.T) {
	isolateAppState(t)
	m, _ := newScopeBaselineModel(t, "git")

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	updated, _ = m.Update(tea.KeyPressMsg{Code: '9', Text: "9"})
	m = asAppModel(t, updated)
	updated, _ = m.Update(tea.KeyPressMsg{Code: '8', Text: "8"})
	m = asAppModel(t, updated)
	if m.globalTab != GlobalSessions {
		t.Fatalf("setup: tab = %v, want Sessions", m.globalTab)
	}
	if got := state.GetLastGlobalTab(); got != "sessions" {
		t.Fatalf("persisted tab = %q, want sessions", got)
	}

	// A new Model is what a Sidecar relaunch constructs.
	restarted, _ := newScopeBaselineModel(t, "git")
	if restarted.inGlobalScope() {
		t.Fatal("restart landed in the global space")
	}
	if restarted.globalTab != GlobalSessions {
		t.Fatalf("New() did not restore the persisted tab: %v", restarted.globalTab)
	}

	updated, _ = restarted.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	restarted = asAppModel(t, updated)
	if !restarted.inGlobalScope() || restarted.globalTab != GlobalSessions {
		t.Fatalf("K after restart: global=%v tab=%v", restarted.inGlobalScope(), restarted.globalTab)
	}
}

func TestGlobalTabPersistenceReadsLegacyNamesAndWritesNewNames(t *testing.T) {
	for _, tc := range []struct {
		id   string
		want GlobalTab
	}{
		{"sessions", GlobalSessions},
		{"workspaces", GlobalSessions},
		{"activity", GlobalActivity},
		{"agents", GlobalActivity},
		{"tasks", GlobalTasks},
	} {
		got, ok := parseGlobalTabID(tc.id)
		if !ok || got != tc.want {
			t.Errorf("parseGlobalTabID(%q) = %v, %v; want %v, true", tc.id, got, ok, tc.want)
		}
	}
	if got := GlobalSessions.persistID(); got != "sessions" {
		t.Errorf("Sessions persist ID = %q", got)
	}
	if got := GlobalActivity.persistID(); got != "activity" {
		t.Errorf("Activity persist ID = %q", got)
	}
}

func TestPersistedGlobalTabFallsBackWhenFeatureDisabled(t *testing.T) {
	isolateAppState(t)
	if err := state.SetLastGlobalTab("workspaces"); err != nil {
		t.Fatal(err)
	}

	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = false
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}}

	registry := plugin.NewRegistry(nil)
	for _, name := range []string{"files", "workspaces", "git", "notes"} {
		if err := registry.Register(&navigationPlugin{id: name}); err != nil {
			t.Fatal(err)
		}
	}
	m := New(registry, keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", "git")
	m.intro.Active, m.intro.Done = false, true
	m.width, m.height, m.ready = 140, 40, true
	host := &hostedTestPlugin{id: "tasks", context: "tasks-list"}
	m.globalTasks = &globalTasksHost{plugin: host, ctx: &plugin.Context{Keymap: m.keymap}}
	m.updateContext()

	if m.overview != nil {
		t.Fatal("the Overview model was built while its feature is disabled")
	}
	if m.globalTab != GlobalWorkspaces {
		t.Fatalf("New() did not load the persisted tab: %v", m.globalTab)
	}

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() || m.globalTab != GlobalTasks {
		t.Fatalf("disabled persisted tab did not fall back: global=%v tab=%v",
			m.inGlobalScope(), m.globalTab)
	}
}

func TestGlobalWorkspacesTabIsAnHonestEmptyList(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.scope = ScopeGlobal
	m.globalTab = GlobalWorkspaces
	m.updateContext()

	// With nothing collected the list says so rather than drawing a blank pane
	// that reads as "no workspaces exist".
	content := ansi.Strip(m.renderContent(m.width, 20))
	if !strings.Contains(content, "Workspaces") || !strings.Contains(content, "Activity") {
		t.Fatalf("global Workspaces list header is missing:\n%s", content)
	}
	if !strings.Contains(content, "no sessions") {
		t.Fatalf("empty list is not honest about being empty:\n%s", content)
	}
	if m.activeContext != "global-workspaces" {
		t.Fatalf("activeContext = %q, want the placeholder's own context", m.activeContext)
	}
	// It is a global view sidecar draws itself, so keys stop at it rather than
	// reaching the project plugin underneath.
	if !m.globalOverlayOwnsKeys() {
		t.Fatal("the placeholder does not own the keyboard")
	}
}

// "i" is Sidecar's find-TD-task shortcut on the Workspaces list. E still
// starts typing and must not open the issue modal.
func TestIssueLookupOpensFromTheGlobalWorkspacesList(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.scope = ScopeGlobal
	m.globalTab = GlobalWorkspaces
	m.updateContext()
	if m.activeContext != "global-workspaces" {
		t.Fatalf("test premise: activeContext = %q", m.activeContext)
	}

	m.handleKeyMsg(tea.KeyPressMsg{Code: 'i', Text: "i"})
	if !m.showIssueInput {
		t.Fatal("i did not open the issue lookup from the global Workspaces list")
	}
}

func TestQOpensQuitModalFromGlobalWorkspacesList(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.scope = ScopeGlobal
	m.globalTab = GlobalWorkspaces
	m.updateContext()

	updated, _ := m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() || !m.showQuitConfirm || m.globalTab != GlobalWorkspaces {
		t.Fatalf("q from Workspaces list: global=%v quit=%v tab=%v",
			m.inGlobalScope(), m.showQuitConfirm, m.globalTab)
	}
}

func TestEDoesNotOpenTheIssueModalFromTheGlobalWorkspacesList(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.scope = ScopeGlobal
	m.globalTab = GlobalWorkspaces
	m.updateContext()

	m.handleKeyMsg(tea.KeyPressMsg{Code: 'E', Text: "E"})
	if m.showIssueInput {
		t.Fatal("E opened the issue lookup instead of starting to type")
	}
}

func TestTasksIsAGlobalTabOutsideTheProjectRegistry(t *testing.T) {
	m, host := scopeModelWithTasks(t)
	if m.registry.Get("tasks") != nil {
		t.Fatal("tasks is registered as a project plugin")
	}

	// Project space shows plugin tabs only; the global space shows all three
	// global tabs with Tasks last.
	for _, ref := range m.visibleTabs() {
		if ref.scope != ScopeProject {
			t.Fatalf("project space showed a global tab: %#v", ref)
		}
	}
	m.scope = ScopeGlobal
	m.updateContext()
	tabs := m.visibleTabs()
	if len(tabs) != 3 || tabs[2].global != GlobalTasks {
		t.Fatalf("global tabs = %#v, want Agents/Workspaces/Tasks", tabs)
	}

	// `0` selects it, and it then owns keys, footer status, and context.
	updated, _ := m.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	m = asAppModel(t, updated)
	if !m.globalTasksFocused() {
		t.Fatalf("0 did not select the Tasks tab: tab=%v", m.globalTab)
	}
	if m.activeContext != "tasks-list" {
		t.Fatalf("activeContext = %q, want the hosted surface's own", m.activeContext)
	}
	if m.globalOverlayOwnsKeys() {
		t.Fatal("the hosted Tasks tab must receive its own keys")
	}
	before := host.keys
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = asAppModel(t, updated)
	if host.keys != before+1 {
		t.Fatalf("hosted surface received %d keys, want %d", host.keys, before+1)
	}
	if !strings.Contains(m.renderContent(m.width, 20), "hosted tasks") {
		t.Fatal("the Tasks tab did not render the hosted surface")
	}

	// q opens the quit modal and stays in the global space.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() || !m.showQuitConfirm || m.globalTab != GlobalTasks {
		t.Fatalf("q from the Tasks tab: global=%v quit=%v tab=%v",
			m.inGlobalScope(), m.showQuitConfirm, m.globalTab)
	}
}

func TestEscInAHostedTasksOverlayGoesToTasksNotTheScopeToggle(t *testing.T) {
	m, host := scopeModelWithTasks(t)
	m.scope = ScopeGlobal
	m.globalTab = GlobalTasks
	m.updateContext()

	// An overlay is open inside Tasks: esc is its dismissal, so it must reach
	// the surface instead of pulling the user out of the global space and
	// leaving the overlay open underneath.
	host.blocks = true
	keys := host.keys
	updated, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() || m.globalTab != GlobalTasks {
		t.Fatalf("esc left the global space with a Tasks overlay open: global=%v tab=%v",
			m.inGlobalScope(), m.globalTab)
	}
	if host.keys != keys+1 {
		t.Fatalf("the hosted surface received %d keys, want %d", host.keys, keys+1)
	}

	// In a Tasks root context nothing wants esc, so it still leaves the space.
	host.blocks = false
	keys = host.keys
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if m.inGlobalScope() {
		t.Fatal("esc in a Tasks root context did not return to the project")
	}
	if host.keys != keys {
		t.Fatalf("esc was forwarded to a root Tasks context: keys %d -> %d", keys, host.keys)
	}
}

func TestGlobalSpaceStaysReachableWhenOnlyTasksIsEnabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = false
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}}

	registry := plugin.NewRegistry(nil)
	for _, name := range []string{"files", "workspaces", "git", "notes"} {
		if err := registry.Register(&navigationPlugin{id: name}); err != nil {
			t.Fatal(err)
		}
	}
	m := New(registry, keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", "git")
	m.intro.Active, m.intro.Done = false, true
	m.width, m.height, m.ready = 140, 40, true
	host := &hostedTestPlugin{id: "tasks", context: "tasks-list"}
	m.globalTasks = &globalTasksHost{plugin: host, ctx: &plugin.Context{Keymap: m.keymap}}
	m.updateContext()

	if m.overview != nil {
		t.Fatal("the Overview model was built while its feature is disabled")
	}
	if !m.globalScopeAvailable() {
		t.Fatal("an enabled Tasks host left the global space unreachable")
	}

	// K enters the space and lands on the only tab that exists — not the
	// remembered Agents tab, which has nothing behind it.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if !m.globalTasksFocused() {
		t.Fatalf("K did not reach the Tasks tab: global=%v tab=%v", m.inGlobalScope(), m.globalTab)
	}
	tabs := m.visibleTabs()
	if len(tabs) != 1 || tabs[0].global != GlobalTasks {
		t.Fatalf("global tabs = %#v, want Tasks alone", tabs)
	}

	// The brand click and the switcher's Overview destination stay live too.
	if _, _, ok := m.getLogoBounds(); !ok {
		t.Fatal("the brand toggle is dead while the global space has a tab")
	}
	destinations := m.projectSwitcherDestinations("")
	if len(destinations) == 0 || destinations[0].Kind != destinationOverview {
		t.Fatalf("the switcher dropped the global destination: %#v", destinations)
	}

	// And q opens the quit modal without leaving the global space.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() || !m.showQuitConfirm || m.activePlugin != 2 {
		t.Fatalf("q from the Tasks-only global space: global=%v plugin=%d quit=%v",
			m.inGlobalScope(), m.activePlugin, m.showQuitConfirm)
	}
}

func TestTasksTabIsAbsentWhenItsFeatureIsDisabled(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[features.TasksPlugin.Name] = false
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	m := New(plugin.NewRegistry(nil), keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", "")
	if m.globalTasks != nil || m.globalTasksPlugin() != nil {
		t.Fatal("the Tasks host was built while its feature is disabled")
	}
	m.scope = ScopeGlobal
	for _, ref := range m.visibleTabs() {
		if ref.global == GlobalTasks {
			t.Fatal("a disabled Tasks tab is still in the global tab row")
		}
	}
	// Nothing to start, nothing to stop.
	if cmd := m.globalTasks.start(); cmd != nil {
		t.Fatal("a nil host produced a start command")
	}
	m.globalTasks.stop()
}

func TestTasksHostSurvivesProjectSwitchesAndClosesOnceAtShutdown(t *testing.T) {
	// A registry with a real context, so Reinit can rewrite it the way a
	// project switch does.
	km := keymap.NewRegistry()
	registry := plugin.NewRegistry(&plugin.Context{Keymap: km, WorkDir: "/tmp/one", ProjectRoot: "/tmp/one"})
	for _, id := range []string{"files", "workspaces", "git", "notes"} {
		if err := registry.Register(&navigationPlugin{id: id}); err != nil {
			t.Fatal(err)
		}
	}
	cfg := config.Default()
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	m := New(registry, km, cfg, "", "/tmp/one", "/tmp/one", "git")
	host := &hostedTestPlugin{id: "tasks", context: "tasks-list"}
	m.globalTasks = &globalTasksHost{plugin: host, ctx: &plugin.Context{Keymap: km}}
	m.width, m.height, m.ready = 140, 40, true

	if cmd := m.globalTasks.start(); cmd != nil {
		cmd()
	}
	if host.inits != 1 || host.starts != 1 {
		t.Fatalf("host lifecycle after start: inits=%d starts=%d", host.inits, host.starts)
	}
	// Starting twice is a no-op: the model is built once, after the first frame.
	if cmd := m.globalTasks.start(); cmd != nil {
		t.Fatal("a second start rebuilt the host")
	}

	// A project switch reinitializes registry plugins and must not touch the
	// host, which keeps receiving forwarded messages afterwards.
	updates := host.updates
	m.registry.Reinit("/tmp/two", "/tmp/two")
	if host.inits != 1 || host.starts != 1 || host.stops != 0 {
		t.Fatalf("project switch disturbed the host: inits=%d starts=%d stops=%d",
			host.inits, host.starts, host.stops)
	}
	updated, _ := m.Update(tea.WindowSizeMsg{Width: 140, Height: 40})
	m = asAppModel(t, updated)
	if host.updates <= updates {
		t.Fatal("the host stopped receiving messages after a project switch")
	}

	// It closes exactly once, however many quit paths run.
	m.shutdown()
	m.shutdown()
	if host.stops != 1 {
		t.Fatalf("host stops = %d, want exactly one", host.stops)
	}
}

func TestGlobalScopeOwnsFooterAndHelp(t *testing.T) {
	m, _ := scopeModelWithTasks(t)
	keymap.RegisterDefaults(m.keymap)

	projectHints := m.footerHints()
	if !hasHint(projectHints, "1-4", "plugins") || !hasHint(projectHints, "8/9/0", "global") {
		t.Fatalf("project footer lost its tab hints: %#v", projectHints)
	}

	m.scope = ScopeGlobal
	m.updateContext()
	globalHints := m.footerHints()
	// The number row addresses the whole header from either scope, so the
	// global space advertises the same two hints the project space does.
	if !hasHint(globalHints, "1-4", "plugins") || !hasHint(globalHints, "8/9/0", "global") {
		t.Fatalf("global footer advertises the wrong tab range: %#v", globalHints)
	}
	if !hasHint(globalHints, "q", "quit") {
		t.Fatalf("global footer lost quit: %#v", globalHints)
	}

	if title, ctx := m.helpSurface(); title != "Sessions" || ctx != "global-workspaces" {
		t.Fatalf("help documents %q/%q, want the visible global tab", title, ctx)
	}
	m.globalTab = GlobalActivity
	if title, ctx := m.helpSurface(); title != "Activity" || ctx != "overview" {
		t.Fatalf("help documents %q/%q on the Activity tab", title, ctx)
	}
	m.globalTab = GlobalTasks
	if title, ctx := m.helpSurface(); title != "tasks" || ctx != "tasks-list" {
		t.Fatalf("help documents %q/%q on the Tasks tab", title, ctx)
	}
	m.scope = ScopeProject
	if title, _ := m.helpSurface(); title != "git" {
		t.Fatalf("project help documents %q, want the active plugin", title)
	}
}

// A tab whose keys exist only as footer hints is a tab whose keys nobody can
// look up. Help and the palette both read the keymap, so the global Workspaces
// contexts have to be registered there — and what they document has to be the
// read-only set the browser actually answers.
func TestGlobalWorkspacesKeysAreDiscoverableInHelpAndPalette(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	keymap.RegisterDefaults(m.keymap)
	m.scope = ScopeGlobal
	m.globalTab = GlobalWorkspaces
	m.updateContext()

	title, ctx := m.helpSurface()
	if title != "Sessions" || ctx != "global-workspaces" {
		t.Fatalf("help surface = %q/%q", title, ctx)
	}
	var help strings.Builder
	m.renderBindingSection(&help, ctx, 60)
	rendered := ansi.Strip(help.String())
	if strings.TrimSpace(rendered) == "" {
		t.Fatal("the help modal renders an empty section for the global Workspaces tab")
	}
	for _, want := range []string{"enter", "/", "s", "p", "r", "\\", "R", "O"} {
		if !strings.Contains(rendered, want) {
			t.Fatalf("help does not document %q:\n%s", want, rendered)
		}
	}

	m.palette.SetSize(120, 40)
	m.palette.Open(m.keymap, m.surfacePlugins(), m.activeContext, "global")
	var found int
	for _, entry := range m.palette.Filtered() {
		if entry.Context == "global-workspaces" {
			found++
		}
	}
	if found == 0 {
		t.Fatal("the palette offers no global Workspaces commands in the global Workspaces context")
	}

	// The filter's own context is registered too, so the query's exits are
	// discoverable while it owns the keyboard.
	if len(m.keymap.BindingsForContext("global-workspaces-filter")) == 0 {
		t.Fatal("the global filter context documents nothing")
	}
}

func TestGlobalWorkspaceContextFollowsMouseFocusAndSidebarToggle(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	keymap.RegisterDefaults(m.keymap)
	m.scope = ScopeGlobal
	m.globalTab = GlobalWorkspaces
	m.updateContext()
	if m.activeContext != "global-workspaces" {
		t.Fatalf("initial context = %q", m.activeContext)
	}

	// A preview press from the list does not enter watched-preview. Context
	// stays on the list until a completed click starts typing.
	split := m.overview.WorkspacesView(m.width, 20)
	_ = split
	previewX := m.width - 5
	updated, _ := m.Update(tea.MouseClickMsg{X: previewX, Y: headerHeight + 5, Button: tea.MouseLeft})
	m = asAppModel(t, updated)
	if m.activeContext != "global-workspaces" {
		t.Fatalf("context after preview press = %q", m.activeContext)
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: '\\', Text: "\\"})
	m = asAppModel(t, updated)
	if m.overview.WorkspaceSidebarVisible() || m.activeContext != "global-workspaces" {
		t.Fatalf("hidden visible=%v context=%q", m.overview.WorkspaceSidebarVisible(), m.activeContext)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: '\\', Text: "\\"})
	m = asAppModel(t, updated)
	if !m.overview.WorkspaceSidebarVisible() || m.activeContext != "global-workspaces" {
		t.Fatalf("restored visible=%v context=%q", m.overview.WorkspaceSidebarVisible(), m.activeContext)
	}
}

func hasHint(hints []footerHint, keys, label string) bool {
	for _, hint := range hints {
		if hint.keys == keys && hint.label == label {
			return true
		}
	}
	return false
}

// TestGlobalWorkspacesFilterOwnsItsKeysAndPastes covers slice 2 item 3's app
// side: while the cross-project filter has focus it is a text-input context, so
// sidecar's own tab, number, quit, and scope shortcuts cannot take characters
// out of a query.
func TestGlobalWorkspacesFilterOwnsItsKeysAndPastes(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.scope = ScopeGlobal
	m.globalTab = GlobalWorkspaces
	m.updateContext()

	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = asAppModel(t, updated)
	if !m.overview.WorkspacesFilterFocused() {
		t.Fatal("`/` did not focus the global filter")
	}
	if m.activeContext != "global-workspaces-filter" || !m.consumesTextInput() {
		t.Fatalf("filter context = %q consumes=%v", m.activeContext, m.consumesTextInput())
	}

	// Keys that mean "quit", "switch tab", and "leave the global space"
	// everywhere else are query text here.
	for _, key := range []tea.KeyPressMsg{
		{Code: 'q', Text: "q"},
		{Code: '1', Text: "1"},
		{Code: '`', Text: "`"},
		{Code: 'k', Text: "K", Mod: tea.ModShift},
	} {
		updated, _ = m.Update(key)
		m = asAppModel(t, updated)
	}
	if !m.inGlobalScope() || m.globalTab != GlobalWorkspaces {
		t.Fatalf("typing left the tab: scope=%v tab=%v", m.scope, m.globalTab)
	}

	// Pastes go to the query too, not to a hidden project plugin.
	updated, _ = m.Update(tea.PasteMsg{Content: "sidecar"})
	m = asAppModel(t, updated)

	// Escape clears, then releases focus, and only then leaves the global space.
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() {
		t.Fatal("the first escape left the global space instead of clearing the query")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() || m.overview.WorkspacesFilterFocused() {
		t.Fatal("the second escape should release filter focus and stay in the global space")
	}
	if m.activeContext != "global-workspaces" || m.consumesTextInput() {
		t.Fatalf("context after exit = %q consumes=%v", m.activeContext, m.consumesTextInput())
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if m.inGlobalScope() {
		t.Fatal("escape on an unfiltered list should return to the project")
	}
}

// Slice 3 item 2: the read-only selected-pane preview polls only while its own
// tab is visible, so the scope has to be the thing that tells it. Nothing else
// can: the preview must not infer visibility from renders, or a background
// frame would keep it capturing.
func TestOnlyTheVisibleWorkspacesTabDrivesTheSelectedPreview(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.overview = overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}})
	if cmd := m.Init(); cmd != nil {
		cmd()
	}

	if m.overview.WorkspacesPreviewVisible() {
		t.Fatal("the preview was live before the global space was ever entered")
	}

	// Entering on Sessions wakes the session preview.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	if !m.overview.WorkspacesPreviewVisible() {
		t.Fatal("the Sessions tab did not wake its preview")
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: '9', Text: "9"})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	if m.overview.WorkspacesPreviewVisible() {
		t.Fatal("switching to Activity did not put the Sessions preview to sleep")
	}

	// Another global tab, and leaving the space entirely, both put it back to
	// sleep — cancelling the in-flight capture and dropping what it captured.
	updated, cmd = m.Update(tea.KeyPressMsg{Code: '8', Text: "8"})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	if !m.overview.WorkspacesPreviewVisible() {
		t.Fatal("switching back to Sessions did not wake its preview")
	}

	updated, cmd = m.Update(tea.KeyPressMsg{Code: '9', Text: "9"})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if m.inGlobalScope() {
		t.Fatal("esc should have returned to project space")
	}
	if m.overview.WorkspacesPreviewVisible() {
		t.Fatal("leaving the global space left the preview polling behind it")
	}
}

// Hiding the sidebar is a layout toggle. The keyboard stays on the list, so
// esc still leaves the global space (or clears a filter first).
func TestEscapeLeavesGlobalAfterHidingTheWorkspacesSidebar(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.overview = overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}})
	if cmd := m.Init(); cmd != nil {
		cmd()
	}
	m.scope = ScopeGlobal
	m.globalTab = GlobalWorkspaces
	m.updateContext()

	updated, _ := m.Update(tea.KeyPressMsg{Code: '\\', Text: "\\"})
	m = asAppModel(t, updated)
	if m.overview.PreviewFocused() || m.overview.WorkspaceFocusContext() != "global-workspaces" {
		t.Fatalf("hiding the sidebar moved the keyboard: focused=%v context=%q",
			m.overview.PreviewFocused(), m.overview.WorkspaceFocusContext())
	}
	if m.globalSurfaceWantsEsc() {
		t.Fatal("a hidden sidebar claimed esc, so scope exit cannot leave global")
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if m.inGlobalScope() {
		t.Fatal("esc after hiding the sidebar should return to the project")
	}
}

// A query that was accepted with enter is still narrowing the list even though
// the input no longer has focus. Escape clears what the user can see before it
// can mean "leave the global space" (slice 4 item 5).
func TestEscapeClearsAnAcceptedGlobalFilterBeforeLeavingTheSpace(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.scope, m.globalTab = ScopeGlobal, GlobalWorkspaces
	m.updateContext()

	updated, _ := m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = asAppModel(t, updated)
	for _, r := range "feature" {
		updated, _ = m.Update(tea.KeyPressMsg{Code: r, Text: string(r)})
		m = asAppModel(t, updated)
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = asAppModel(t, updated)
	if m.overview.WorkspacesFilterFocused() || !m.overview.WorkspacesFilterActive() {
		t.Fatalf("enter did not accept the query: focused=%v active=%v",
			m.overview.WorkspacesFilterFocused(), m.overview.WorkspacesFilterActive())
	}

	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if !m.inGlobalScope() {
		t.Fatal("escape left the global space with a query still narrowing the list")
	}
	if m.overview.WorkspacesFilterActive() {
		t.Fatal("escape did not clear the accepted query")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: tea.KeyEsc})
	m = asAppModel(t, updated)
	if m.inGlobalScope() {
		t.Fatal("escape on an unfiltered list should return to the project")
	}
}

// Quitting has to release the global browser's terminal like every other one
// sidecar owns; a pane left attached outlives the process as an orphaned tmux
// control subprocess.
func TestShutdownReleasesTheGlobalBrowsersTerminal(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.overview = overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}})
	if cmd := m.Init(); cmd != nil {
		cmd()
	}
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	if !m.overview.WorkspacesPreviewVisible() {
		t.Fatal("the Sessions tab never woke its preview")
	}

	m.shutdown()
	if m.overview.WorkspacesPreviewVisible() {
		t.Fatal("quitting left the global browser's preview attached")
	}
}
