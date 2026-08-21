package configui

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configchecks"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/version"
)

// stubRunner records the commands a route would run and answers them without a
// process. A test that reached a real package manager would be a bug in the
// test and a hazard on the machine running it.
type stubRunner struct {
	commands []string
	err      error
	// onRun lets a case change the world the way a successful install does.
	onRun func()
}

func (s *stubRunner) Run(_ context.Context, name string, args ...string) (string, error) {
	s.commands = append(s.commands, strings.TrimSpace(name+" "+strings.Join(args, " ")))
	if s.onRun != nil {
		s.onRun()
	}
	return "stub output", s.err
}

// stubEnvironment is a machine described rather than inspected. present names
// the commands on PATH.
func stubEnvironment(present map[string]bool) *version.Environment {
	runner := &stubRunner{}
	return stubEnvironmentWith(runner, present)
}

func stubEnvironmentWith(runner version.Runner, present map[string]bool) *version.Environment {
	return &version.Environment{
		Runner: runner,
		LookPath: func(name string) (string, error) {
			if present[name] {
				return "/stub/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
		EvalSymlinks: func(path string) (string, error) { return path, nil },
		Self:         func() (string, error) { return "/stub/bin/sidecar", nil },
	}
}

// panelsFixture is a model on Panels & Integrations with a described machine.
func panelsFixture(t *testing.T, present map[string]bool, mutate func(*config.Config)) *Model {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	m, _ := configFixture(t, cfg)
	m.SetCheckInput(configchecks.Input{Config: cfg, Env: configchecks.Env{
		LookPath: func(name string) (string, error) {
			if present[name] {
				return "/stub/bin/" + name, nil
			}
			return "", errors.New("not found")
		},
	}})
	// Probe and PlanInstall must describe the same machine. Tests that run an
	// install replace this with a runner of their own.
	m.SetInstallEnvironment(stubEnvironment(present))
	// The probe is a command; run it the way the host does.
	if msg := m.ProbeCmd()(); msg != nil {
		m.Handle(msg.(Msg))
	}
	m.Open(PagePanels)
	return m
}

func TestPanelsPageListsEverySurface(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"td": true}, nil)
	view := ansi.Strip(m.View(160, 45))

	for _, want := range []string{
		"Panels & Integrations",
		"Choose the Sidecar surfaces you want available.",
		"Git", "Status, commits, branches, and diffs",
		"Files", "Project browser and inline editing",
		"td", "Issues and task state from the current project",
		"Notes", "Project notes, kept inside Sidecar",
		"Conversations", "Session history from supported agent harnesses",
		"Tasks", "Embedded Tasks global tab, backed by the Tasks command",
		"ON", "OFF",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("Panels is missing %q:\n%s", want, view)
		}
	}
}

// Each surface writes the key (or keys) Sidecar's own assembly actually reads.
func TestPanelTogglesPersistTheirRealKeys(t *testing.T) {
	cases := []struct {
		region string
		check  func(*config.Config) bool
		want   bool
	}{
		{regionPanel + panelIDGit, func(c *config.Config) bool { return c.Plugins.GitStatus.Enabled }, false},
		{regionPanel + panelIDFiles, func(c *config.Config) bool { return c.Plugins.FileBrowser.Enabled }, false},
		{regionPanel + panelIDTD, func(c *config.Config) bool { return c.Plugins.TDMonitor.Enabled }, false},
		{regionPanel + panelIDNotes, func(c *config.Config) bool {
			return c.Features.Flags[features.NotesPlugin.Name]
		}, false},
	}
	for _, tc := range cases {
		t.Run(tc.region, func(t *testing.T) {
			m := panelsFixture(t, map[string]bool{"td": true}, nil)
			activate(t, m, tc.region)
			if got := tc.check(loadSaved(t)); got != tc.want {
				t.Fatalf("%s stored %v, want %v", tc.region, got, tc.want)
			}
			if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, panelRestartNote) {
				t.Fatalf("a restart-scoped toggle did not say so:\n%s", view)
			}
		})
	}
}

func TestNotesIsStableAndExplainsTDDependencyWithoutChangingPreference(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"td": true}, func(cfg *config.Config) {
		cfg.Plugins.TDMonitor.Enabled = false
		cfg.Features.Flags[features.NotesPlugin.Name] = true
	})
	view := ansi.Strip(m.View(160, 45))
	if strings.Contains(view, "Notes  BETA") {
		t.Fatalf("stable Notes still has a beta badge:\n%s", view)
	}
	if !strings.Contains(view, "available when the td panel is on") {
		t.Fatalf("td dependency is not explained:\n%s", view)
	}
	if !m.flagEnabled(features.NotesPlugin.Name) {
		t.Fatal("td-off view rewrote the Notes preference")
	}
}

func TestFocusNotesPreferenceTargetsExistingToggle(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"td": true}, nil)
	m.Navigate(PageSetup)
	m.FocusNotesPreference()
	_ = m.View(160, 45)
	if m.Page() != PagePanels || !m.detailFocus {
		t.Fatalf("focus route = page %q detail=%v", m.Page(), m.detailFocus)
	}
	if got := m.focusedID; got != regionPanel+panelIDNotes {
		t.Fatalf("focused control = %q, want Notes toggle", got)
	}
}

func TestNotesDefaultEditorSelectorPersistsBothChoices(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"td": true}, nil)
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "Default editor") || !strings.Contains(view, "Built-in") {
		t.Fatalf("Notes editor selector is missing:\n%s", view)
	}

	choose(t, m, regionPanelNotesEditor, config.NotesEditorPane)
	if got := loadSaved(t).Plugins.Notes.DefaultEditor; got != config.NotesEditorPane {
		t.Fatalf("pane choice saved %q", got)
	}
	choose(t, m, regionPanelNotesEditor, config.NotesEditorBuiltin)
	if got := loadSaved(t).Plugins.Notes.DefaultEditor; got != config.NotesEditorBuiltin {
		t.Fatalf("built-in choice saved %q", got)
	}
}

// Conversations is two switches behind one control, and both have to agree.
func TestConversationsToggleKeepsBothSwitchesConsistent(t *testing.T) {
	m := panelsFixture(t, nil, nil)
	// Default: the plugin bool is on but the feature flag is off, so the panel
	// is not built and the page must report OFF.
	if m.conversationsOn() {
		t.Fatal("Conversations reported ON with its feature flag off")
	}

	activate(t, m, regionPanel+panelIDConversations)
	saved := loadSaved(t)
	if !saved.Plugins.Conversations.Enabled {
		t.Fatal("turning Conversations on did not set plugins.conversations.enabled")
	}
	if !saved.Features.Flags[features.ConversationsPlugin.Name] {
		t.Fatal("turning Conversations on did not set the conversations_plugin flag")
	}
	if !m.conversationsOn() {
		t.Fatal("the page still reports Conversations off after enabling it")
	}

	activate(t, m, regionPanel+panelIDConversations)
	saved = loadSaved(t)
	if saved.Plugins.Conversations.Enabled {
		t.Fatal("turning Conversations off did not clear plugins.conversations.enabled")
	}
	if !saved.Features.Flags[features.ConversationsPlugin.Name] {
		t.Fatal("turning the panel off revoked the user's feature-preview opt-in")
	}
	if m.conversationsOn() {
		t.Fatal("Conversations still reports ON after being turned off")
	}
}

// Notes is stable and has no external install route. Its existing switch can
// opt out directly even though the built-in default is now on.
func TestNotesTogglesWithoutTheEnableRoute(t *testing.T) {
	m := panelsFixture(t, nil, nil)
	activate(t, m, regionPanel+panelIDNotes)
	if m.Route().IsChild() {
		t.Fatalf("Notes opened a dependency route: %#v", m.Route())
	}
	if loadSaved(t).Features.Flags[features.NotesPlugin.Name] {
		t.Fatal("Notes opt-out did not persist")
	}
	if view := ansi.Strip(m.View(160, 45)); strings.Contains(view, "Notes  BETA") {
		t.Fatalf("stable Notes regained a BETA badge:\n%s", view)
	}
}

// Tasks with its command already present is enabled directly.
func TestTasksEnablesDirectlyWhenTheCommandIsPresent(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"tasks": true}, nil)
	activate(t, m, regionPanel+panelIDTasks)
	if m.Route().IsChild() {
		t.Fatalf("an installed Tasks opened the enable route: %#v", m.Route())
	}
	if !loadSaved(t).Features.Flags[features.TasksPlugin.Name] {
		t.Fatal("Tasks did not persist")
	}
}

// Tasks missing, Homebrew available: the route offers a confirmed install.
func TestTasksEnableRouteOffersHomebrewInstall(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"brew": true}, nil)
	activate(t, m, regionPanel+panelIDTasks)
	if m.Route().Child != ChildEnableIntegration {
		t.Fatalf("a missing Tasks command did not open the enable route: %#v", m.Route())
	}
	if loadSaved(t).Features.Flags[features.TasksPlugin.Name] {
		t.Fatal("opening the dependency check already enabled the panel")
	}
	view := ansi.Strip(m.View(160, 45))
	for _, want := range []string{
		"Enable Tasks", "BETA", "Tasks needs to be installed", "System check",
		"Tasks command", "Not found on PATH", "Homebrew", "Available",
		"Install Tasks", "brew install marcus/tap/tasks",
		"waits for your confirmation", "never uses sudo",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the enable route is missing %q:\n%s", want, view)
		}
	}
}

// Without Homebrew, go install is the fallback when the toolchain is present.
func TestTasksEnableRouteOffersGoInstallWhenBrewMissing(t *testing.T) {
	present := map[string]bool{"go": true}
	m := panelsFixture(t, present, nil)
	runner := &stubRunner{onRun: func() { present["tasks"] = true }}
	m.SetInstallEnvironment(stubEnvironmentWith(runner, present))
	activate(t, m, regionPanel+panelIDTasks)
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "Install Tasks") {
		t.Fatalf("go fallback did not offer install:\n%s", view)
	}
	if !strings.Contains(view, "go install github.com/marcus/tasks/cmd/tasks@latest") {
		t.Fatalf("go fallback did not show the command:\n%s", view)
	}
	cmd := runByID(t, m, regionEnableInstall)
	msg := cmd().(installResultMsg)
	if !msg.outcome.Installed {
		t.Fatalf("go install failed: %+v", msg.outcome)
	}
	if len(runner.commands) == 0 {
		t.Fatal("go install ran nothing")
	}
	for _, pkg := range version.TasksDescriptor().GoPackages {
		want := "go install " + pkg + "@latest"
		found := false
		for _, c := range runner.commands {
			if c == want {
				found = true
				break
			}
		}
		if !found {
			t.Fatalf("missing %s in %v", want, runner.commands)
		}
	}
}

// Without Homebrew or Go there is nothing safe to run, so the route says what to do.
func TestTasksEnableRouteFallsBackToManualInstructions(t *testing.T) {
	m := panelsFixture(t, nil, nil)
	activate(t, m, regionPanel+panelIDTasks)
	view := ansi.Strip(m.View(160, 45))
	for _, want := range []string{
		"Homebrew", "Not found",
		"Sidecar cannot install Tasks for you on this machine.",
		"brew install marcus/tap/tasks",
		"Copy install command",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("the manual route is missing %q:\n%s", want, view)
		}
	}
	for _, c := range m.controls {
		if c.id == regionEnableInstall {
			t.Fatal("an install action was offered without Homebrew or Go")
		}
	}
}

// Tasks already enabled but the standalone command is missing: Panels offers
// the same install action rather than a scavenger hunt for brew.
func TestTasksEnabledMissingOffersInstall(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"brew": true}, func(cfg *config.Config) {
		if cfg.Features.Flags == nil {
			cfg.Features.Flags = map[string]bool{}
		}
		cfg.Features.Flags[features.TasksPlugin.Name] = true
	})
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "Install Tasks") {
		t.Fatalf("enabled-but-missing Tasks did not offer install:\n%s", view)
	}
	activate(t, m, regionPanelTasksInstall)
	if m.Route().Child != ChildEnableIntegration {
		t.Fatalf("the install button did not open the enable route: %#v", m.Route())
	}
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "Install Tasks") {
		t.Fatalf("the enable route is missing the install action:\n%s", view)
	}
}

// A confirmed install that succeeds enables the panel; the exact command run is
// the one the route showed.
func TestTasksInstallSuccessEnablesThePanel(t *testing.T) {
	present := map[string]bool{"brew": true}
	m := panelsFixture(t, present, nil)
	runner := &stubRunner{onRun: func() { present["tasks"] = true }}
	m.SetInstallEnvironment(stubEnvironmentWith(runner, present))

	activate(t, m, regionPanel+panelIDTasks)
	m.View(160, 45)
	cmd := runByID(t, m, regionEnableInstall)
	if m.enable.phase != installRunning {
		t.Fatalf("the route did not show the install running: %v", m.enable.phase)
	}
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "Installing Tasks") {
		t.Fatalf("the running state is not on screen:\n%s", view)
	}

	msg, ok := cmd().(installResultMsg)
	if !ok {
		t.Fatalf("the install did not report a result: %#v", cmd())
	}
	if len(runner.commands) != 1 || runner.commands[0] != "brew install marcus/tap/tasks" {
		t.Fatalf("the install ran %v", runner.commands)
	}
	saveCmd := m.Handle(msg)
	if saveCmd == nil {
		t.Fatal("a successful install did not enable the panel")
	}
	reload(t, m, saveCmd())
	if !loadSaved(t).Features.Flags[features.TasksPlugin.Name] {
		t.Fatal("a successful install did not persist the flag")
	}
	if m.Route().IsChild() {
		t.Fatalf("a successful install stayed in the route: %#v", m.Route())
	}
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, panelRestartNote) {
		t.Fatalf("enabling Tasks did not mention the restart:\n%s", view)
	}
}

// A failed install leaves the panel off and says why.
func TestTasksInstallFailureLeavesThePanelOff(t *testing.T) {
	present := map[string]bool{"brew": true}
	m := panelsFixture(t, present, nil)
	runner := &stubRunner{err: errors.New("formula not found")}
	m.SetInstallEnvironment(stubEnvironmentWith(runner, present))

	activate(t, m, regionPanel+panelIDTasks)
	m.View(160, 45)
	cmd := runByID(t, m, regionEnableInstall)
	msg := cmd().(installResultMsg)
	m.Handle(msg)

	if loadSaved(t).Features.Flags[features.TasksPlugin.Name] {
		t.Fatal("a failed install enabled the panel anyway")
	}
	if m.Route().Child != ChildEnableIntegration {
		t.Fatalf("a failed install abandoned the route: %#v", m.Route())
	}
	view := ansi.Strip(m.View(160, 45))
	for _, want := range []string{"The install did not finish", "formula not found", "Tasks is still turned off"} {
		if !strings.Contains(view, want) {
			t.Fatalf("the failure is not explained: %q missing from\n%s", want, view)
		}
	}
}

// An install that exits cleanly but leaves nothing on PATH is a failure.
func TestInstallIsNotClaimedFromAnExitCode(t *testing.T) {
	present := map[string]bool{"brew": true}
	env := stubEnvironmentWith(&stubRunner{}, present)
	outcome := version.InstallWithHomebrew(context.Background(), env, version.TasksDescriptor())
	if outcome.Installed {
		t.Fatal("an install was claimed with the command still missing")
	}
	if outcome.Err == nil {
		t.Fatal("a missing command after install was not reported")
	}
}

// Escape from the route returns to Panels with the panel still off.
func TestEnableRouteCancelReturnsWithThePanelOff(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"brew": true}, nil)
	activate(t, m, regionPanel+panelIDTasks)
	m.View(160, 45)
	if !m.Escape() {
		t.Fatal("Escape did not answer inside the enable route")
	}
	if m.Route().IsChild() {
		t.Fatalf("Escape did not return to Panels: %#v", m.Route())
	}
	if loadSaved(t).Features.Flags[features.TasksPlugin.Name] {
		t.Fatal("cancelling enabled the panel")
	}
	if m.enable != nil {
		t.Fatal("the abandoned enable attempt survived the route")
	}
}

// A panel whose supporting tool is missing explains itself.
func TestTDPanelExplainsAMissingCommand(t *testing.T) {
	m := panelsFixture(t, nil, nil)
	view := ansi.Strip(m.View(160, 45))
	if !strings.Contains(view, "td is not on PATH") {
		t.Fatalf("the td row did not explain the missing command:\n%s", view)
	}

	present := panelsFixture(t, map[string]bool{"td": true}, nil)
	if view := ansi.Strip(present.View(160, 45)); strings.Contains(view, "td is not on PATH") {
		t.Fatalf("an installed td was reported as missing:\n%s", view)
	}
}

// The panel inputs that exist are editable and land on their real keys.
func TestPanelInputsPersist(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"td": true}, nil)
	m.View(160, 45)
	m.detailFocus = true

	m.editPanelPath(regionPanelTDPath, "Database", ".todos/issues.db", ".todos/issues.db",
		func(p *config.PluginsConfig, value string) { p.TDMonitor.DBPath = value })
	m.panelField(regionPanelTDPath, "").SetValue("/tmp/issues.db")
	_, cmd := m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
	if cmd == nil {
		t.Fatal("the database path was not saved")
	}
	reload(t, m, cmd())
	if got := loadSaved(t).Plugins.TDMonitor.DBPath; got != "/tmp/issues.db" {
		t.Fatalf("db path saved as %q", got)
	}

	// The refresh interval is chosen from a list rather than stepped.
	choose(t, m, regionPanelTDRefresh, (5 * time.Second).String())
	if got := loadSaved(t).Plugins.TDMonitor.RefreshInterval; got != 5*time.Second {
		t.Fatalf("refresh interval saved as %v", got)
	}
}

// Clicking a panel row focuses it but does not toggle. Only the ON/OFF pill
// itself writes the setting.
func TestPanelRowClickDoesNotToggle(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"td": true}, nil)
	m.View(160, 45)
	before := loadSaved(t).Plugins.GitStatus.Enabled

	row := regionFor(t, m, regionPanel+panelIDGit)
	m.Mouse(tea.MouseClickMsg{X: row.Rect.X + 2, Y: row.Rect.Y, Button: tea.MouseLeft})
	if loadSaved(t).Plugins.GitStatus.Enabled != before {
		t.Fatal("clicking the Git row toggled the setting")
	}

	pill := regionFor(t, m, regionPanel+panelIDGit+toggleSuffix)
	if cmd := func() tea.Cmd {
		return m.Mouse(tea.MouseClickMsg{X: pill.Rect.X, Y: pill.Rect.Y, Button: tea.MouseLeft})
	}(); cmd != nil {
		reload(t, m, cmd())
	}
	if loadSaved(t).Plugins.GitStatus.Enabled == before {
		t.Fatal("clicking the Git ON/OFF pill did not toggle the setting")
	}
}

// runByID runs a control by region ID without asserting a save.
func runByID(t *testing.T, m *Model, id string) tea.Cmd {
	t.Helper()
	for i, c := range m.controls {
		if c.id == id {
			m.focusControlIndex(i)
			return m.runControl(i)
		}
	}
	t.Fatalf("control %q is not on screen", id)
	return nil
}

// A field on these pages owns typed characters: an "r" typed into a path is an
// r, not Recheck, and never a global shortcut.
func TestPanelPathEditorOwnsTypedKeys(t *testing.T) {
	m := panelsFixture(t, map[string]bool{"td": true}, nil)
	m.View(160, 45)
	m.detailFocus = true
	m.editPanelPath(regionPanelTDPath, "Database", "", ".todos/issues.db",
		func(p *config.PluginsConfig, value string) { p.TDMonitor.DBPath = value })

	if m.FocusContext() != ContextConfigEdit {
		t.Fatalf("an open path editor reported context %q", m.FocusContext())
	}
	for _, r := range "rco" {
		handled, _ := m.Key(tea.KeyPressMsg{Code: r, Text: string(r)})
		if !handled {
			t.Fatalf("the editor did not consume %q", string(r))
		}
	}
	if got := m.panelField(regionPanelTDPath, "").Value(); got != "rco" {
		t.Fatalf("typed characters reached a shortcut instead of the field: %q", got)
	}
}

// Escape abandons the route, not the package manager. A confirmed install that
// is still running is the user's own, so its outcome is still reported and a
// successful one still turns the panel on.
func TestInstallLeftRunningStillReportsItsOutcome(t *testing.T) {
	present := map[string]bool{"brew": true}
	m := panelsFixture(t, present, nil)
	runner := &stubRunner{onRun: func() { present["tasks"] = true }}
	m.SetInstallEnvironment(stubEnvironmentWith(runner, present))

	activate(t, m, regionPanel+panelIDTasks)
	m.View(160, 45)
	cmd := runByID(t, m, regionEnableInstall)
	if m.enable == nil || m.enable.phase != installRunning {
		t.Fatal("the install did not start")
	}

	// The user leaves while Homebrew works.
	if !m.Escape() {
		t.Fatal("Escape did not leave the enable route")
	}
	if m.Route().IsChild() {
		t.Fatalf("Escape stayed in the route: %#v", m.Route())
	}
	if m.installing == nil {
		t.Fatal("leaving the route abandoned a running install")
	}

	msg := cmd().(installResultMsg)
	saveCmd := m.Handle(msg)
	if saveCmd == nil {
		t.Fatal("the finished install was never reported")
	}
	reload(t, m, saveCmd())
	if !loadSaved(t).Features.Flags[features.TasksPlugin.Name] {
		t.Fatal("a successful install that outlived its route did not enable the panel")
	}
	if m.installing != nil {
		t.Fatal("the settled install is still pending")
	}
}

// The same attempt failing says so rather than vanishing.
func TestInstallLeftRunningReportsFailure(t *testing.T) {
	present := map[string]bool{"brew": true}
	m := panelsFixture(t, present, nil)
	m.SetInstallEnvironment(stubEnvironmentWith(&stubRunner{err: errors.New("formula not found")}, present))

	activate(t, m, regionPanel+panelIDTasks)
	m.View(160, 45)
	cmd := runByID(t, m, regionEnableInstall)
	m.Escape()

	notice, ok := m.Handle(cmd().(installResultMsg))().(NoticeMsg)
	if !ok {
		t.Fatal("a failed install that outlived its route said nothing")
	}
	if !strings.Contains(notice.Message, "formula not found") {
		t.Fatalf("the notice does not say what happened: %q", notice.Message)
	}
	if loadSaved(t).Features.Flags[features.TasksPlugin.Name] {
		t.Fatal("a failed install enabled the panel anyway")
	}
}
