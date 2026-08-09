package app

import (
	"context"
	"errors"
	"os/exec"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/plugin"
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

func TestOverviewPinnedFilteredAndActivationIsLazy(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	cfg.Projects.List = []config.ProjectConfig{{Name: "one", Path: "/tmp/one"}, {Name: "two", Path: "/tmp/two"}}
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
	m.overview = overview.New(workspaceinventory.Collector{Runner: &countingOverviewRunner{}})
	m.overviewActive = true
	workspace := workspaceinventory.Workspace{ProjectKey: workspaceinventory.CanonicalPath(target), ProjectRoot: target, Kind: workspaceinventory.KindWorktree, Key: workspaceinventory.CanonicalPath(target), Path: target}
	updatedModel, cmd := m.Update(overview.ValidationMsg{Workspace: workspace})
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
	updatedModel, cmd := m.Update(overview.NavigateMsg{Workspace: workspace})
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
	updatedModel, cmd := m.Update(overview.ValidationMsg{Err: errors.New("worktree disappeared")})
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
