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
func (p *hostedTestPlugin) FocusContext() string {
	if p.context == "" {
		return p.id
	}
	return p.context
}

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
		{"global tab 2", tea.KeyPressMsg{Code: '2', Text: "2"}},
		{"cycle global", tea.KeyPressMsg{Code: '`', Text: "`"}},
		{"back to project", tea.KeyPressMsg{Code: 'q', Text: "q"}},
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

	// Entering the global space on the Agents tab starts exactly one cycle.
	updated, cmd := m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if cmd == nil {
		t.Fatal("entering the Agents tab started no collection")
	}
	cmd()
	if runner.calls != 1 {
		t.Fatalf("entry ran the collector %d times, want 1", runner.calls)
	}

	// The Workspaces placeholder collects nothing, and neither does re-entering
	// the global space while it is the remembered tab.
	updated, cmd = m.Update(tea.KeyPressMsg{Code: '2', Text: "2"})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = asAppModel(t, updated)
	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if cmd != nil {
		cmd()
	}
	if m.globalTab != GlobalWorkspaces {
		t.Fatalf("re-entry forgot the last global tab: %v", m.globalTab)
	}
	if runner.calls != 1 {
		t.Fatalf("the Workspaces tab collected: %d calls", runner.calls)
	}
}

func TestGlobalWorkspacesTabIsAnHonestPlaceholder(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	m.scope = ScopeGlobal
	m.globalTab = GlobalWorkspaces
	m.updateContext()

	content := ansi.Strip(m.renderContent(m.width, 20))
	if !strings.Contains(content, "Workspaces") || !strings.Contains(content, "Nothing is being collected") {
		t.Fatalf("placeholder does not say what it is:\n%s", content)
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

	// Number 3 selects it, and it then owns keys, footer status, and context.
	updated, _ := m.Update(tea.KeyPressMsg{Code: '3', Text: "3"})
	m = asAppModel(t, updated)
	if !m.globalTasksFocused() {
		t.Fatalf("3 did not select the Tasks tab: tab=%v", m.globalTab)
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

	// q still returns to the exact project destination.
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'q', Text: "q"})
	m = asAppModel(t, updated)
	if m.inGlobalScope() || m.activePlugin != 2 || m.showQuitConfirm {
		t.Fatalf("q from the Tasks tab: global=%v plugin=%d quit=%v",
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

	projectHints := m.footerHints()
	if !hasHint(projectHints, "1-4", "plugins") {
		t.Fatalf("project footer lost its plugin range: %#v", projectHints)
	}

	m.scope = ScopeGlobal
	m.updateContext()
	globalHints := m.footerHints()
	if !hasHint(globalHints, "1-3", "tabs") {
		t.Fatalf("global footer advertises the wrong tab range: %#v", globalHints)
	}
	for _, hint := range globalHints {
		if hint.label == "quit" {
			t.Fatal("q quits from the global space instead of returning to the project")
		}
	}

	if title, ctx := m.helpSurface(); title != "Agents" || ctx != "overview" {
		t.Fatalf("help documents %q/%q, want the visible global tab", title, ctx)
	}
	m.globalTab = GlobalWorkspaces
	if title, ctx := m.helpSurface(); title != "Workspaces" || ctx != "global-workspaces" {
		t.Fatalf("help documents %q/%q on the Workspaces tab", title, ctx)
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

func hasHint(hints []footerHint, keys, label string) bool {
	for _, hint := range hints {
		if hint.keys == keys && hint.label == label {
			return true
		}
	}
	return false
}
