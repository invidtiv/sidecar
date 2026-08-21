package app

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/configui"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/gitinit"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
)

func emptyProjectsModel(t *testing.T, workDir string) Model {
	t.Helper()
	isolateAppState(t)
	cfg := config.Default()
	cfg.Projects.List = nil
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	registry := plugin.NewRegistry(nil)
	if err := registry.Register(&navigationPlugin{id: "git"}); err != nil {
		t.Fatal(err)
	}
	m := New(registry, keymap.NewRegistry(), cfg, "", workDir, workDir, "git")
	m.intro.Active, m.intro.Done = false, true
	m.width, m.height, m.ready = 140, 40, true
	m.updateContext()
	return m
}

func TestFirstRunNonGitEmptyProjectsOpensAddProject(t *testing.T) {
	dir := t.TempDir()
	m := emptyProjectsModel(t, dir)
	if !m.firstRunProbePending {
		t.Fatal("New did not mark the first-run probe pending")
	}

	view := m.viewContent()
	if !strings.Contains(view, "Set up a project") || !strings.Contains(view, "Git repositories") {
		t.Fatalf("pending first-run view missing guided copy:\n%s", view)
	}

	updated, _ := m.Update(firstRunProbeMsg{NeedsSetup: true})
	m = asAppModel(t, updated)
	if !m.configOpen() {
		t.Fatal("first-run did not open Configuration")
	}
	if m.config.Page() != configui.PageProjects {
		t.Fatalf("page = %q, want projects", m.config.Page())
	}
	if m.config.Route().Child != configui.ChildAddProject {
		t.Fatalf("route = %#v, want Add Project", m.config.Route())
	}
	if got := m.config.AddProjectLocation(); got != dir {
		t.Fatalf("Location = %q, want cwd %q", got, dir)
	}
	formView := m.config.View(160, 45)
	for _, want := range []string{"Git repositories", "Location", "worktrees"} {
		if !strings.Contains(formView, want) {
			t.Fatalf("Add Project missing %q:\n%s", want, formView)
		}
	}
}

func TestInitEmitsFirstRunProbeForEmptyNonGit(t *testing.T) {
	dir := t.TempDir()
	m := emptyProjectsModel(t, dir)
	var probe *firstRunProbeMsg
	for _, msg := range collectMsgs(m.Init()) {
		if typed, ok := msg.(firstRunProbeMsg); ok {
			probe = &typed
		}
	}
	if probe == nil {
		t.Fatal("Init did not schedule a first-run probe")
	}
	if !probe.NeedsSetup {
		t.Fatal("non-git directory with no projects did not ask for setup")
	}
}

func TestFirstRunSkippedWhenProjectsConfigured(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")
	if m.firstRunProbePending {
		t.Fatal("configured projects still scheduled first-run onboarding")
	}
	for _, msg := range collectMsgs(m.Init()) {
		if _, ok := msg.(firstRunProbeMsg); ok {
			t.Fatal("Init probed for first-run with projects configured")
		}
		if typed, ok := msg.(OpenConfigurationMsg); ok && typed.AddProject {
			t.Fatal("Init opened Add Project with projects configured")
		}
	}
}

func TestFirstRunSkippedInsideAGitRepository(t *testing.T) {
	dir := t.TempDir()
	if _, err := gitinit.Init(dir); err != nil {
		t.Fatalf("git init: %v", err)
	}
	m := emptyProjectsModel(t, dir)
	if !m.firstRunProbePending {
		t.Fatal("empty projects should still probe, even in a git repo")
	}

	var probe *firstRunProbeMsg
	for _, msg := range collectMsgs(m.Init()) {
		if typed, ok := msg.(firstRunProbeMsg); ok {
			probe = &typed
		}
	}
	if probe == nil {
		t.Fatal("Init did not schedule a first-run probe")
	}
	if probe.NeedsSetup {
		t.Fatal("git repository with no projects still routed to Add Project")
	}

	updated, _ := m.Update(*probe)
	m = asAppModel(t, updated)
	if m.configOpen() {
		t.Fatal("git repository opened Configuration")
	}
	if m.firstRunProbePending {
		t.Fatal("probe left the loading state on")
	}
}
