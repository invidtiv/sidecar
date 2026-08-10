package app

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

type navigationPlugin struct {
	id        string
	focused   bool
	inits     int
	keyInputs int
	pending   *plugin.PendingWorkspaceSelection
}

func (p *navigationPlugin) ID() string                 { return p.id }
func (p *navigationPlugin) Name() string               { return p.id }
func (p *navigationPlugin) Icon() string               { return "" }
func (p *navigationPlugin) Init(*plugin.Context) error { p.inits++; return nil }
func (p *navigationPlugin) Start() tea.Cmd             { return nil }
func (p *navigationPlugin) Stop()                      {}
func (p *navigationPlugin) Update(msg tea.Msg) (plugin.Plugin, tea.Cmd) {
	if _, ok := msg.(tea.KeyPressMsg); ok {
		p.keyInputs++
	}
	return p, nil
}
func (p *navigationPlugin) View(int, int) string       { return "" }
func (p *navigationPlugin) IsFocused() bool            { return p.focused }
func (p *navigationPlugin) SetFocused(f bool)          { p.focused = f }
func (p *navigationPlugin) Commands() []plugin.Command { return nil }
func (p *navigationPlugin) FocusContext() string       { return p.id }
func (p *navigationPlugin) SetPendingWorkspaceSelection(s plugin.PendingWorkspaceSelection) {
	p.pending = &s
}

type countingOverviewRunner struct{ calls int }

func (r *countingOverviewRunner) Output(context.Context, string, ...string) ([]byte, error) {
	r.calls++
	return nil, nil
}

func TestCrossProjectOverviewFlagOffPreservesSwitcherAndDoesNoWork(t *testing.T) {
	cfg := config.Default()
	if cfg.Features.Flags == nil {
		cfg.Features.Flags = map[string]bool{}
	}
	cfg.Features.Flags[features.CrossProjectOverview.Name] = false
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}}
	m := New(plugin.NewRegistry(nil), keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", "")
	if m.overview != nil {
		t.Fatal("overview constructed while feature is disabled")
	}
	m.initProjectSwitcher()
	if len(m.projectSwitcherFiltered) != 1 || m.projectSwitcherFiltered[0].Kind != destinationProject {
		t.Fatalf("flag-off destinations = %#v", m.projectSwitcherFiltered)
	}
}

func asAppModel(t *testing.T, model tea.Model) Model {
	t.Helper()
	switch m := model.(type) {
	case Model:
		return m
	case *Model:
		return *m
	default:
		t.Fatalf("unexpected model type %T", model)
		return Model{}
	}
}

func TestLogoClickAndKToggleOverview(t *testing.T) {
	cfg := config.Default()
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}}
	m := New(plugin.NewRegistry(nil), keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", "")
	if m.overview == nil {
		t.Fatal("overview should be constructed when feature defaults on")
	}
	m.intro.Active = false
	m.intro.Done = true
	m.width, m.height, m.ready = 120, 40, true

	start, end, ok := m.getLogoBounds()
	if !ok || end <= start {
		t.Fatalf("logo bounds = %d-%d ok=%v", start, end, ok)
	}
	// Click in the middle of "Sidecar"
	x := (start + end) / 2
	updated, cmd := m.Update(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft})
	m = asAppModel(t, updated)
	if !m.overviewActive {
		t.Fatal("logo click did not open overview")
	}
	if cmd == nil {
		t.Fatal("logo click should start overview load")
	}

	// K toggles closed (handleKeyMsg returns *Model)
	updated, _ = m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if m.overviewActive {
		t.Fatal("K did not close overview")
	}

	// K opens again
	updated, cmd = m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if !m.overviewActive || cmd == nil {
		t.Fatal("K did not reopen overview")
	}

	// Clicking logo while open toggles closed
	updated, _ = m.Update(tea.MouseClickMsg{X: x, Y: 0, Button: tea.MouseLeft})
	m = asAppModel(t, updated)
	if m.overviewActive {
		t.Fatal("logo click while open should close overview")
	}
}

func TestCompactOverviewKeepsAppHeaderAndFooterAt72x30(t *testing.T) {
	cfg := config.Default()
	registry := plugin.NewRegistry(nil)
	for _, name := range []string{"td", "git", "files", "conversations", "workspaces"} {
		if err := registry.Register(&navigationPlugin{id: name}); err != nil {
			t.Fatal(err)
		}
	}
	m := New(registry, keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", "git")
	m.overview = overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}})
	m.overviewActive = true
	m.intro.Active = false
	m.width, m.height, m.ready = 72, 30, true
	panes := m.overview.Start(nil)()
	_ = m.overview.Update(panes) // build the production five-lane empty board; leave poll timer unexecuted

	view := m.viewContent()
	if got := lipgloss.Height(view); got != 30 {
		t.Fatalf("app height = %d, want 30\n%s", got, view)
	}
	lines := strings.Split(view, "\n")
	if !strings.Contains(lines[0], "Sidecar") || !strings.Contains(lines[0], "Overview") || lines[1] != "" || !strings.Contains(lines[len(lines)-1], "Open") {
		t.Fatalf("compact viewport hid app chrome: first=%q last=%q", lines[0], lines[len(lines)-1])
	}
	if got := lipgloss.Width(lines[0]); got != 72 {
		t.Fatalf("header width = %d, want 72: %q", got, lines[0])
	}
	plainHeader := ansi.Strip(lines[0])
	if !strings.Contains(plainHeader, "td") || !strings.Contains(plainHeader, "git") || !strings.Contains(plainHeader, "files") || !strings.Contains(plainHeader, "conversations") {
		t.Fatalf("narrow header omitted discoverable tabs: %q", plainHeader)
	}
	if strings.Contains(plainHeader, "workspaces") || len(m.getTabBounds()) != 4 {
		t.Fatalf("narrow header did not deterministically omit the final inactive tab: %q bounds=%#v", plainHeader, m.getTabBounds())
	}
	wide := m
	wide.width = 140
	if header := ansi.Strip(wide.renderHeader()); !strings.Contains(header, "workspaces") || len(wide.getTabBounds()) != 5 {
		t.Fatalf("wide header changed existing full-tab layout: %q bounds=%#v", header, wide.getTabBounds())
	}
	active := m
	active.overviewActive = false
	active.activePlugin = 4
	active.intro.RepoName = strings.Repeat("x", 50)
	active.width = 60
	var activeBounds TabBounds
	foundActive := false
	for _, bounds := range active.getTabBounds() {
		if bounds.Plugin == 4 {
			activeBounds, foundActive = bounds, true
		}
	}
	renderedHeader := active.renderHeader()
	header := ansi.Strip(renderedHeader)
	if !strings.Contains(header, "Sidecar / xxxxx") || !strings.Contains(header, "…") || !strings.Contains(header, "workspaces") || strings.Contains(header, strings.Repeat("x", 50)) || !foundActive {
		t.Fatalf("narrow header lost active project/plugin: %q bounds=%#v", header, active.getTabBounds())
	}
	if got := lipgloss.Width(renderedHeader); got != active.width {
		t.Fatalf("long-title header width = %d, want %d", got, active.width)
	}
	if activeBounds.Start < 0 || activeBounds.End > active.width || activeBounds.Start >= activeBounds.End {
		t.Fatalf("active tab bounds outside fitted header: width=%d bounds=%#v", active.width, activeBounds)
	}
	clicked, _ := active.Update(tea.MouseClickMsg{X: (activeBounds.Start + activeBounds.End) / 2, Y: 0, Button: tea.MouseLeft})
	clickedModel := clicked.(Model)
	if clickedModel.showProjectSwitcher || clickedModel.activePlugin != 4 {
		t.Fatalf("fitted active tab click misrouted: plugin=%d switcher=%v", clickedModel.activePlugin, clickedModel.showProjectSwitcher)
	}
	if !strings.Contains(ansi.Strip(lines[2]), "Agent Overview") {
		t.Fatalf("content did not begin at global row 2: %q", lines[2])
	}
	// App mouse routing subtracts the one header row plus explicit spacing row;
	// local compact card row 1 therefore begins at global row 3.
	adjusted := offsetMouseY(tea.MouseClickMsg{X: 1, Y: 3, Button: tea.MouseLeft}, -headerHeight)
	if got := adjusted.Mouse().Y; got != 1 {
		t.Fatalf("compact content mouse local Y = %d, want 1", got)
	}
	updatedModel, _ := m.Update(tea.MouseClickMsg{X: 1, Y: 3, Button: tea.MouseLeft})
	if !updatedModel.(Model).overviewActive {
		t.Fatal("content-row click was misrouted as wrapped header")
	}
}

func TestOverviewPinnedFilteredAndActivationIsLazy(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}, {Name: "two", Path: "/tmp/two"}, {Name: "three", Path: "/tmp/three"}}
	runner := &countingOverviewRunner{}
	m := New(plugin.NewRegistry(nil), keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", "")
	m.overview = overview.New(workspaceinventory.Collector{Runner: runner})
	m.initProjectSwitcher()
	if got := m.projectSwitcherFiltered[0]; got.Kind != destinationOverview || got.Name != "Overview" {
		t.Fatalf("first destination = %#v", got)
	}
	m.projectSwitcherInput.SetValue("two")
	m.projectSwitcherFiltered = m.projectSwitcherDestinations("two")
	if len(m.projectSwitcherFiltered) != 2 || m.projectSwitcherFiltered[0].Kind != destinationOverview || m.projectSwitcherFiltered[1].Name != "two" {
		t.Fatalf("filtered destinations = %#v", m.projectSwitcherFiltered)
	}
	if runner.calls != 0 {
		t.Fatalf("collector ran before activation: %d", runner.calls)
	}
	if got := m.overviewProjects(); len(got) != 3 {
		t.Fatalf("Overview projects = %#v, want all configured projects", got)
	}
	workDir, projectRoot, pluginIndex := m.ui.WorkDir, m.ui.ProjectRoot, m.activePlugin
	cmd := m.activateProjectSwitcherDestination(m.projectSwitcherFiltered[0])
	if cmd == nil || !m.overviewActive {
		t.Fatal("Overview activation did not start its loading command")
	}
	if runner.calls != 0 {
		t.Fatalf("collector ran synchronously during activation: %d", runner.calls)
	}
	if m.ui.WorkDir != workDir || m.ui.ProjectRoot != projectRoot || m.activePlugin != pluginIndex {
		t.Fatal("Overview activation changed underlying project/plugin state")
	}
	if got := m.activeDestinationName(); got != "Overview" {
		t.Fatalf("header destination = %q", got)
	}
}

func TestProjectSwitcherLinkedWorktreeCursorFlagParity(t *testing.T) {
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "other", Path: "/repo/other"}, {Name: "main", Path: "/repo/main"}}
	m := Model{cfg: cfg, ui: &UIState{WorkDir: "/repo/worktrees/topic", ProjectRoot: "/repo/main"}}
	m.initProjectSwitcher()
	if m.projectSwitcherCursor != 0 {
		t.Fatalf("flag-off linked-worktree cursor = %d, want pre-feature zero", m.projectSwitcherCursor)
	}
	m.overview = overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}})
	m.initProjectSwitcher()
	if m.projectSwitcherCursor != 2 {
		t.Fatalf("enabled linked-worktree cursor = %d, want configured main after pinned Overview", m.projectSwitcherCursor)
	}
	m.overviewActive = true
	m.initProjectSwitcher()
	if m.projectSwitcherCursor != 0 {
		t.Fatalf("active Overview cursor = %d, want pinned Overview", m.projectSwitcherCursor)
	}
}

func TestValidatedCrossProjectNavigationFocusesWorkspaceWithoutInput(t *testing.T) {
	source := newOverviewGitRepo(t, "source")
	target := newOverviewGitRepo(t, "target")
	stateBase := t.TempDir()
	config.SetTestStateDir(stateBase)
	t.Cleanup(config.ResetTestStateDir)
	worktreeState, err := projectdir.WorktreeDirWithBase(stateBase, target, target)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(worktreeState, "agent"), []byte("codex\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Projects.List = []config.ProjectConfig{{Name: "source", Path: source}, {Name: "target", Path: target}}
	config.SetTestConfigPath(filepath.Join(t.TempDir(), "config.json"))
	t.Cleanup(config.ResetTestConfigPath)
	km := keymap.NewRegistry()
	ctx := &plugin.Context{WorkDir: source, ProjectRoot: source, Config: cfg, Keymap: km}
	reg := plugin.NewRegistry(ctx)
	gitPlugin := &navigationPlugin{id: "git"}
	workspacePlugin := &navigationPlugin{id: workspacePluginID}
	if err := reg.Register(gitPlugin); err != nil {
		t.Fatal(err)
	}
	if err := reg.Register(workspacePlugin); err != nil {
		t.Fatal(err)
	}
	m := New(reg, km, cfg, "", source, source, "git")
	m.overview = overview.New(workspaceinventory.Collector{})
	m.overviewActive = true
	workspace := workspaceinventory.Workspace{ProjectKey: workspaceinventory.CanonicalPath(target), ProjectRoot: target, Kind: workspaceinventory.KindWorktree, Key: workspaceinventory.CanonicalPath(target), Path: target}
	navigation := overviewNavigation(t, m.overview, workspace)
	initialGitInits, initialWorkspaceInits := gitPlugin.inits, workspacePlugin.inits
	updatedModel, validationCmd := m.Update(navigation)
	m = updatedModel.(Model)
	if validationCmd == nil || m.ui.WorkDir != source || gitPlugin.inits != initialGitInits || workspacePlugin.inits != initialWorkspaceInits {
		t.Fatalf("pre-validation state: cmd=%v work=%q gitInits=%d workspaceInits=%d", validationCmd != nil, m.ui.WorkDir, gitPlugin.inits, workspacePlugin.inits)
	}
	validation, ok := validationCmd().(overview.ValidationMsg)
	if !ok || validation.Err != nil {
		t.Fatalf("validation result = %#v", validation)
	}
	updatedModel, cmd := m.Update(validation)
	updated := updatedModel.(Model)
	if cmd == nil || updated.activePlugin != 1 || !workspacePlugin.focused {
		t.Fatalf("navigation focus: cmd=%v active=%d focused=%v", cmd != nil, updated.activePlugin, workspacePlugin.focused)
	}
	if workspacePlugin.pending == nil || workspacePlugin.pending.Path != target {
		t.Fatalf("pending selection = %#v", workspacePlugin.pending)
	}
	if workspacePlugin.keyInputs != 0 {
		t.Fatalf("navigation sent %d key inputs", workspacePlugin.keyInputs)
	}
}

func TestOverviewNavigationDoesNotMutateBeforeValidation(t *testing.T) {
	source := newOverviewGitRepo(t, "source")
	target := newOverviewGitRepo(t, "target")
	cfg := config.Default()
	m := New(plugin.NewRegistry(nil), keymap.NewRegistry(), cfg, "", source, source, "")
	m.overview = overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}})
	m.overviewActive = true
	workspace := workspaceinventory.Workspace{
		ProjectKey:  workspaceinventory.CanonicalPath(target),
		ProjectRoot: target,
		Kind:        workspaceinventory.KindWorktree,
		Key:         workspaceinventory.CanonicalPath(target),
		Path:        target,
	}
	navigation := overviewNavigation(t, m.overview, workspace)
	updatedModel, cmd := m.Update(navigation)
	updated := updatedModel.(Model)
	if cmd == nil {
		t.Fatal("navigation did not schedule validation")
	}
	if updated.ui.WorkDir != source || updated.ui.ProjectRoot != source || !updated.overviewActive {
		t.Fatalf("navigation mutated before validation: work=%q root=%q overview=%v", updated.ui.WorkDir, updated.ui.ProjectRoot, updated.overviewActive)
	}
}

func TestStaleValidationDoesNotMutateProjectOrReinit(t *testing.T) {
	source := newOverviewGitRepo(t, "source")
	cfg := config.Default()
	km := keymap.NewRegistry()
	ctx := &plugin.Context{WorkDir: source, ProjectRoot: source, Config: cfg, Keymap: km}
	reg := plugin.NewRegistry(ctx)
	p := &navigationPlugin{id: workspacePluginID}
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	m := New(reg, km, cfg, "", source, source, workspacePluginID)
	m.overview = overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}})
	m.overviewActive = true
	initialInits := p.inits
	navigation := overviewNavigation(t, m.overview, workspaceinventory.Workspace{})
	updatedModel, cmd := m.Update(overviewValidation(navigation, errors.New("worktree disappeared")))
	updated := updatedModel.(Model)
	if updated.ui.WorkDir != source || updated.ui.ProjectRoot != source || !updated.overviewActive || p.inits != initialInits {
		t.Fatalf("stale navigation mutated state: work=%q root=%q overview=%v inits=%d", updated.ui.WorkDir, updated.ui.ProjectRoot, updated.overviewActive, p.inits)
	}
	if cmd == nil {
		t.Fatal("stale validation did not return toast")
	}
	if toast, ok := cmd().(ToastMsg); !ok || !toast.IsError {
		t.Fatalf("stale result = %#v", cmd())
	}
}

func TestOverviewExitBeforeNavigateMsgIgnoresLateActivation(t *testing.T) {
	m, p, source := newOverviewRaceModel(t)
	target := newOverviewGitRepo(t, "target")
	navigation := overviewNavigation(t, m.overview, overviewWorkspace(target))
	initialInits := p.inits

	exitedModel, _ := m.handleKeyMsg(tea.KeyPressMsg{Code: '1', Text: "1"})
	exited := *exitedModel.(*Model)
	updatedModel, cmd := exited.Update(navigation)
	updated := updatedModel.(Model)
	if cmd != nil || updated.overviewActive || updated.ui.WorkDir != source || p.inits != initialInits {
		t.Fatalf("late NavigateMsg mutated after numeric exit: cmd=%v active=%v work=%q inits=%d", cmd != nil, updated.overviewActive, updated.ui.WorkDir, p.inits)
	}
}

func TestOverviewExitBeforeValidationMsgIgnoresLateResult(t *testing.T) {
	m, p, source := newOverviewRaceModel(t)
	target := newOverviewGitRepo(t, "target")
	navigation := overviewNavigation(t, m.overview, overviewWorkspace(target))
	updatedModel, validationCmd := m.Update(navigation)
	m = updatedModel.(Model)
	if validationCmd == nil {
		t.Fatal("current navigation did not schedule validation")
	}
	initialInits := p.inits

	exitedModel, _ := m.Update(FocusPluginByIDMsg{PluginID: "git"})
	exited := exitedModel.(Model)
	updatedModel, cmd := exited.Update(overviewValidation(navigation, nil))
	updated := updatedModel.(Model)
	if cmd != nil || updated.overviewActive || updated.ui.WorkDir != source || p.inits != initialInits {
		t.Fatalf("late ValidationMsg mutated after tab exit: cmd=%v active=%v work=%q inits=%d", cmd != nil, updated.overviewActive, updated.ui.WorkDir, p.inits)
	}
}

func TestOverviewNewerActivationSupersedesOlderValidation(t *testing.T) {
	m, p, source := newOverviewRaceModel(t)
	firstTarget := newOverviewGitRepo(t, "first")
	secondTarget := newOverviewGitRepo(t, "second")
	first := overviewNavigation(t, m.overview, overviewWorkspace(firstTarget))
	updatedModel, firstValidation := m.Update(first)
	m = updatedModel.(Model)
	if firstValidation == nil {
		t.Fatal("first navigation did not schedule validation")
	}
	second := overviewNavigation(t, m.overview, overviewWorkspace(secondTarget))
	updatedModel, secondValidation := m.Update(second)
	m = updatedModel.(Model)
	if secondValidation == nil {
		t.Fatal("second navigation did not schedule validation")
	}
	initialInits := p.inits

	updatedModel, toastCmd := m.Update(overviewValidation(second, errors.New("second target disappeared")))
	m = updatedModel.(Model)
	if toastCmd == nil {
		t.Fatal("current validation error did not produce a toast")
	}
	updatedModel, cmd := m.Update(overviewValidation(first, nil))
	updated := updatedModel.(Model)
	if cmd != nil || !updated.overviewActive || updated.ui.WorkDir != source || p.inits != initialInits {
		t.Fatalf("older validation won race: cmd=%v active=%v work=%q inits=%d", cmd != nil, updated.overviewActive, updated.ui.WorkDir, p.inits)
	}
}

func TestOverviewHeaderTabExitInvalidatesPendingNavigation(t *testing.T) {
	m, p, source := newOverviewRaceModel(t)
	target := newOverviewGitRepo(t, "target")
	navigation := overviewNavigation(t, m.overview, overviewWorkspace(target))
	m.width, m.height, m.ready = 120, 40, true
	bounds := m.getTabBounds()
	if len(bounds) == 0 {
		t.Fatal("missing tab bounds")
	}
	initialInits := p.inits
	updatedModel, _ := m.Update(tea.MouseClickMsg{X: bounds[0].Start, Y: 0, Button: tea.MouseLeft})
	exited := updatedModel.(Model)
	updatedModel, cmd := exited.Update(navigation)
	updated := updatedModel.(Model)
	if cmd != nil || updated.overviewActive || updated.ui.WorkDir != source || p.inits != initialInits {
		t.Fatalf("late navigation mutated after header exit: cmd=%v active=%v work=%q inits=%d", cmd != nil, updated.overviewActive, updated.ui.WorkDir, p.inits)
	}
}

func TestOverviewHeaderMouseOpensSwitcher(t *testing.T) {
	cfg := config.Default()
	m := Model{cfg: cfg, registry: plugin.NewRegistry(nil), keymap: keymap.NewRegistry(), ui: &UIState{}, intro: IntroModel{RepoName: "repo", Done: true}, overview: overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}}), overviewActive: true, width: 120, height: 40, ready: true}
	start, end, ok := m.getRepoNameBounds()
	if !ok {
		t.Fatal("Overview header has no switcher bounds")
	}
	updatedModel, _ := m.Update(tea.MouseClickMsg{X: (start + end) / 2, Y: 0, Button: tea.MouseLeft})
	updated := updatedModel.(Model)
	if !updated.showProjectSwitcher || updated.projectSwitcherCursor != 0 {
		t.Fatalf("Overview header click: open=%v cursor=%d", updated.showProjectSwitcher, updated.projectSwitcherCursor)
	}
}

func newOverviewGitRepo(t *testing.T, name string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), name)
	if out, err := exec.Command("git", "init", "-q", "-b", "main", path).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v: %s", err, out)
	}
	return path
}

func newOverviewRaceModel(t *testing.T) (Model, *navigationPlugin, string) {
	t.Helper()
	source := newOverviewGitRepo(t, "source")
	cfg := config.Default()
	km := keymap.NewRegistry()
	ctx := &plugin.Context{WorkDir: source, ProjectRoot: source, Config: cfg, Keymap: km}
	reg := plugin.NewRegistry(ctx)
	p := &navigationPlugin{id: "git"}
	if err := reg.Register(p); err != nil {
		t.Fatal(err)
	}
	m := New(reg, km, cfg, "", source, source, "git")
	m.overview = overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}})
	m.overviewActive = true
	m.overview.Start(nil)
	return m, p, source
}

func overviewWorkspace(path string) workspaceinventory.Workspace {
	canonical := workspaceinventory.CanonicalPath(path)
	return workspaceinventory.Workspace{ProjectKey: canonical, ProjectRoot: path, Kind: workspaceinventory.KindWorktree, Key: canonical, Path: path}
}

func overviewNavigation(t *testing.T, model *overview.Model, workspace workspaceinventory.Workspace) overview.NavigateMsg {
	t.Helper()
	msg, ok := model.RequestNavigation(workspace)().(overview.NavigateMsg)
	if !ok {
		t.Fatal("request did not return NavigateMsg")
	}
	return msg
}

func overviewValidation(navigation overview.NavigateMsg, err error) overview.ValidationMsg {
	return overview.ValidationMsg{
		Workspace:  navigation.Workspace,
		Generation: navigation.Generation,
		RequestID:  navigation.RequestID,
		Err:        err,
	}
}
