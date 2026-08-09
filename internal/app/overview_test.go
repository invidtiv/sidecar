package app

import (
	"context"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

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
