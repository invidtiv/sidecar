package overview

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspacelist"
	"github.com/marcus/sidecar/internal/workspaceops"
)

func createActionPoint(m *Model, kind workspacelist.RegionKind) (int, int, string, bool) {
	split := m.previewSplit(previewWide)
	m.syncCreateActions()
	rendered := m.workspaces.Render(workspacelist.RenderOptions{Width: split.SidebarContentWidth, Height: previewTall - 2, Title: "Workspaces", Focused: true})
	for _, region := range rendered.Regions {
		if region.Kind == kind {
			return globalContentInset + region.X, 1 + region.Y, region.ID, true
		}
	}
	return 0, 0, "", false
}

func TestGlobalWorktreePlanUsesOnlyTargetProjectConfig(t *testing.T) {
	m := catalogModel(t)
	m.projects = []Project{{Name: "one", Path: "/tmp/one", Key: "one"}, {Name: "two", Path: "/tmp/two", Key: "two"}}
	m.config = &config.Config{Projects: config.ProjectsConfig{List: []config.ProjectConfig{
		{Name: "one", Path: "/tmp/one", WorktreeSetup: &config.WorktreeSetupConfig{HookPath: "one.sh"}},
		{Name: "two", Path: "/tmp/two", WorktreeSetup: &config.WorktreeSetupConfig{HookPath: "two.sh", RunHook: true}},
	}}}
	original := resolveGlobalWorktree
	defer func() { resolveGlobalWorktree = original }()
	var gotDir string
	var gotSetup config.WorktreeSetupConfig
	resolveGlobalWorktree = func(_ context.Context, workDir, projectRoot, name, base string, dirPrefix bool, setup config.WorktreeSetupConfig) (*workspaceops.WorktreePlan, error) {
		gotDir, gotSetup = workDir, setup
		return &workspaceops.WorktreePlan{SourceWorktree: workDir, MainWorktree: projectRoot, Branch: name, Path: "/tmp/two-feature", SourceRef: "HEAD", SourceOID: "abc"}, nil
	}
	m.OpenCreateWorktree("two")
	m.createNameInput.SetValue("feature")
	msg := m.planCreateWorktree()().(globalWorktreePlannedMsg)
	if gotDir != "/tmp/two" || gotSetup.HookPath != "two.sh" || !gotSetup.RunHook {
		t.Fatalf("target config leaked: dir=%q setup=%+v", gotDir, gotSetup)
	}
	if msg.Plan.RepoKey != "two" || msg.Project.Path != "/tmp/two" {
		t.Fatalf("planned identity = %+v project=%+v", msg.Plan, msg.Project)
	}
}

func TestGlobalWorktreeCancellationBeforeAndAfterMutation(t *testing.T) {
	m := catalogModel(t)
	m.OpenCreateWorktree("")
	m.createPlan = &workspaceops.WorktreePlan{Path: "/tmp/created", Branch: "created"}
	originalExecute, originalRemove := executeGlobalWorktree, removeGlobalJournal
	defer func() { executeGlobalWorktree, removeGlobalJournal = originalExecute, originalRemove }()
	executed := 0
	executeGlobalWorktree = func(context.Context, string, *workspaceops.WorktreePlan) (*workspaceops.WorktreeRecord, error) {
		executed++
		return nil, nil
	}
	m.applyCreateAction(globalCreateCancelID, m.createProjectIndex, m.createKindIndex)
	if executed != 0 || m.CreateOpen() {
		t.Fatalf("pre-mutation cancel executed=%d open=%v", executed, m.CreateOpen())
	}

	m.OpenCreateWorktree("")
	plan := &workspaceops.WorktreePlan{Path: "/tmp/created", Branch: "created", AgentType: "codex"}
	record := &workspaceops.WorktreeRecord{Path: plan.Path, Branch: plan.Branch, HEADOID: "abc"}
	m.Update(globalWorktreeCreatedMsg{Project: m.projects[0], Plan: plan, Record: record, Outcomes: []workspaceops.SetupOutcome{{Action: "optional hook", Err: errors.New("boom")}}})
	if !m.CreateOpen() || m.createRecord == nil || m.createWarning == "" {
		t.Fatalf("post-mutation cancel path lost recovery: open=%v record=%+v warning=%q", m.CreateOpen(), m.createRecord, m.createWarning)
	}
	removeGlobalJournal = func(*workspaceops.WorktreePlan) error { return nil }
	if cmd := m.applyCreateAction(globalCreateCancelID, m.createProjectIndex, m.createKindIndex); cmd == nil || !m.createBusy || m.createRecord == nil {
		t.Fatalf("post-mutation cancel did not retain and launch created identity: cmd=%v busy=%v record=%+v", cmd, m.createBusy, m.createRecord)
	}
}

func TestGlobalWorktreeRequiredSetupFailureOffersRecovery(t *testing.T) {
	m := catalogModel(t)
	m.OpenCreateWorktree("")
	plan := &workspaceops.WorktreePlan{Path: "/tmp/created", Branch: "created"}
	record := &workspaceops.WorktreeRecord{Path: plan.Path, Branch: plan.Branch, HEADOID: "abc"}
	m.Update(globalWorktreeCreatedMsg{Project: m.projects[0], Plan: plan, Record: record, Outcomes: []workspaceops.SetupOutcome{{Action: "required hook", Required: true, Err: errors.New("boom")}}})
	m.width, m.height = 80, 30
	m.ensureCreateModal()
	if m.createModal == nil || m.createError == "" || m.createRecord == nil {
		t.Fatalf("required failure did not retain recovery state: error=%q record=%+v", m.createError, m.createRecord)
	}
}

func TestGlobalWorktreePartialMutationAndJournalFailuresStayRecoverable(t *testing.T) {
	m := catalogModel(t)
	m.OpenCreateWorktree("")
	plan := &workspaceops.WorktreePlan{MainWorktree: "/tmp/main", Path: "/tmp/created", Branch: "created", AgentType: "codex"}
	record := &workspaceops.WorktreeRecord{Path: plan.Path, Branch: plan.Branch, HEADOID: "abc"}
	originalRemove, originalLaunch, originalExecute := removeGlobalJournal, launchGlobalSession, executeGlobalWorktree
	originalJournal, originalSetup := persistGlobalJournal, runGlobalSetup
	defer func() {
		removeGlobalJournal, launchGlobalSession, executeGlobalWorktree = originalRemove, originalLaunch, originalExecute
		persistGlobalJournal, runGlobalSetup = originalJournal, originalSetup
	}()
	removed, launched := 0, 0
	removeGlobalJournal = func(*workspaceops.WorktreePlan) error { removed++; return nil }
	launchGlobalSession = func(context.Context, workspaceops.AgentLaunchSpec) (workspaceops.AgentLaunchResult, error) {
		launched++
		return workspaceops.AgentLaunchResult{}, nil
	}
	executeGlobalWorktree = func(context.Context, string, *workspaceops.WorktreePlan) (*workspaceops.WorktreeRecord, error) {
		return record, errors.New("repair failed")
	}
	persistGlobalJournal = func(context.Context, *workspaceops.WorktreePlan, *workspaceops.WorktreeRecord) error { return nil }
	setupRuns := 0
	runGlobalSetup = func(context.Context, *workspaceops.WorktreePlan) []workspaceops.SetupOutcome { setupRuns++; return nil }
	m.createPlan = plan
	msg := m.executeCreateWorktree()().(globalWorktreeCreatedMsg)
	if setupRuns != 0 || msg.Record == nil || msg.Err == nil {
		t.Fatalf("partial execute ran setup or lost identity: setup=%d msg=%+v", setupRuns, msg)
	}
	m.Update(msg)
	if m.createError == "" || m.createRecord == nil || removed != 0 || launched != 0 || !m.CreateOpen() {
		t.Fatalf("partial mutation escaped recovery: error=%q record=%+v removed=%d launched=%d open=%v", m.createError, m.createRecord, removed, launched, m.CreateOpen())
	}

	m.createError = ""
	removeGlobalJournal = func(*workspaceops.WorktreePlan) error { removed++; return errors.New("sync failed") }
	m.Update(globalWorktreeCreatedMsg{Project: m.projects[0], Plan: plan, Record: record})
	if !strings.Contains(m.createError, "finalize pending creation journal") || launched != 0 || m.createRecord == nil {
		t.Fatalf("journal failure escaped recovery: error=%q launched=%d record=%+v", m.createError, launched, m.createRecord)
	}
	m.createError = ""
	if cmd := m.openCreatedWorktreeAnyway(); cmd != nil || !strings.Contains(m.createError, "before opening") {
		t.Fatalf("open-anyway ignored journal failure: cmd=%v error=%q", cmd, m.createError)
	}
}

func TestGlobalWorktreeLaunchesConfiguredAgentBeforeRefresh(t *testing.T) {
	m := catalogModel(t)
	m.config = &config.Config{Plugins: config.PluginsConfig{Workspace: config.WorkspacePluginConfig{AgentStart: map[string]string{"codex": "codex-custom"}}}}
	m.OpenCreateWorktree("")
	plan := &workspaceops.WorktreePlan{MainWorktree: t.TempDir(), Path: t.TempDir(), Branch: "created", AgentType: "codex"}
	record := &workspaceops.WorktreeRecord{Path: plan.Path, Name: "Created", Branch: plan.Branch, HEADOID: "abc"}
	originalRemove, originalLaunch := removeGlobalJournal, launchGlobalSession
	defer func() { removeGlobalJournal, launchGlobalSession = originalRemove, originalLaunch }()
	removeGlobalJournal = func(*workspaceops.WorktreePlan) error { return nil }
	var spec workspaceops.AgentLaunchSpec
	launchGlobalSession = func(_ context.Context, got workspaceops.AgentLaunchSpec) (workspaceops.AgentLaunchResult, error) {
		spec = got
		return workspaceops.AgentLaunchResult{SessionName: got.SessionName, PaneID: "%1"}, nil
	}
	launchCmd := m.update(globalWorktreeCreatedMsg{Project: m.projects[0], Plan: plan, Record: record})
	if launchCmd == nil {
		t.Fatal("successful setup refreshed before launching")
	}
	launchMsg := launchCmd().(globalWorkspaceLaunchedMsg)
	if !spec.StartAgent || !strings.Contains(spec.AgentCommand, "codex-custom") || spec.WorkDir != plan.Path {
		t.Fatalf("launch spec = %+v", spec)
	}
	if refresh := m.update(launchMsg); refresh == nil || m.CreateOpen() {
		t.Fatalf("launch success did not close and refresh: refresh=%v open=%v", refresh, m.CreateOpen())
	}
}

func TestCreatedWorktreeSelectionFollowsAcrossSortsAndTinyViewport(t *testing.T) {
	for _, sortMode := range workspacelist.SortModes {
		t.Run(sortMode.Label(), func(t *testing.T) {
			m := catalogModel(t)
			m.showIdleWorktrees = true
			m.workspaces.SetSort(sortMode)
			m.pendingCreatedPath = "/tmp/created"
			project := m.projects[0]
			result := m.results[projectKey(project)]
			result.Workspaces = append(result.Workspaces, workspaceinventory.Workspace{
				ID: "created-worktree", ProjectKey: projectKey(project), ProjectName: project.Name, ProjectRoot: project.Path,
				Kind: workspaceinventory.KindWorktree, Key: "created", Path: "/tmp/created", Name: "Created",
			})
			m.applyProjectMutationRefresh(projectMutationRefreshMsg{Project: project, Result: result})
			_ = m.workspaces.Render(workspacelist.RenderOptions{Width: 24, Height: 4, Title: "Workspaces", Focused: true})
			if got := m.workspaces.SelectedID(); got != "created-worktree" {
				t.Fatalf("selected = %q, want created worktree", got)
			}
		})
	}
}

func TestProjectMutationRefreshReplacesOnlyTargetProject(t *testing.T) {
	m := catalogModel(t)
	otherKey := "untouched"
	untouched := workspaceinventory.ProjectResult{ProjectKey: otherKey, ProjectName: "Untouched", Workspaces: []workspaceinventory.Workspace{{ID: "keep-me", ProjectKey: otherKey}}}
	m.results[otherKey] = untouched
	target := m.projects[0]
	replacement := workspaceinventory.ProjectResult{ProjectKey: projectKey(target), ProjectName: target.Name, Workspaces: []workspaceinventory.Workspace{{ID: "replacement", ProjectKey: projectKey(target)}}}
	m.applyProjectMutationRefresh(projectMutationRefreshMsg{Project: target, Result: replacement})
	got := m.results[otherKey]
	if len(got.Workspaces) != 1 || got.Workspaces[0].ID != "keep-me" {
		t.Fatalf("targeted refresh changed unrelated project: %+v", got)
	}
}

func TestGlobalCreateHeaderAndProjectSectionActionsRouteTypedProjects(t *testing.T) {
	m := catalogModel(t)
	m.width, m.height = previewWide, previewTall
	m.workspaces.SetSort(workspacelist.SortProject)
	_ = m.WorkspacesView(previewWide, previewTall)

	x, y, _, ok := createActionPoint(m, workspacelist.RegionHeaderAction)
	if !ok {
		t.Fatal("header create action was not rendered")
	}
	m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if !m.CreateOpen() {
		t.Fatal("clicking the rendered header action did not open create")
	}
	selected, _ := m.SelectedWorkspace()
	if m.createProjectKey != selected.ProjectKey {
		t.Fatalf("header default project = %q, want selected row project %q", m.createProjectKey, selected.ProjectKey)
	}
	m.closeCreateShell()
	_ = m.WorkspacesView(previewWide, previewTall)

	x, y, id, ok := createActionPoint(m, workspacelist.RegionSectionAction)
	if !ok {
		t.Fatal("project section create action was not rendered")
	}
	m.WorkspacesMouse(tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft})
	if !m.CreateOpen() {
		t.Fatal("clicking the rendered section action did not open create")
	}
	if want := createProjectKeyFromAction(id); m.createProjectKey != want {
		t.Fatalf("section preselected %q, want typed action project %q", m.createProjectKey, want)
	}
}

func TestGlobalCreateResolvesNamesAndConfigInsideTargetProject(t *testing.T) {
	m := catalogModel(t)
	m.projects = []Project{{Name: "one", Path: "/tmp/one", Key: "one"}, {Name: "two", Path: "/tmp/two", Key: "two"}}
	m.results["one"] = workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{
		{Kind: workspaceinventory.KindShell, TmuxName: "sidecar-sh-one-7", Name: "Shell 7"},
	}}
	m.results["two"] = workspaceinventory.ProjectResult{Workspaces: []workspaceinventory.Workspace{
		{Kind: workspaceinventory.KindShell, TmuxName: "sidecar-sh-two-2", Name: "Shell 2"},
	}}

	original := createManagedShell
	defer func() { createManagedShell = original }()
	var got workspaceops.ManagedShellSpec
	createManagedShell = func(spec workspaceops.ManagedShellSpec) (workspaceops.ShellResult, error) {
		got = spec
		return workspaceops.ShellResult{SessionName: spec.SessionName}, nil
	}
	m.OpenCreateShell("two")
	cmd := m.submitCreateShell()
	msg := cmd().(globalShellCreatedMsg)
	if msg.Project.Path != "/tmp/two" || got.ProjectRoot != "/tmp/two" || got.WorkDir != "/tmp/two" {
		t.Fatalf("target project leaked: msg=%+v spec=%+v", msg.Project, got)
	}
	if got.SessionName != "sidecar-sh-two-3" || got.DisplayName != "Shell 3" {
		t.Fatalf("target names = %q/%q, want sidecar-sh-two-3/Shell 3", got.SessionName, got.DisplayName)
	}
}

func TestGlobalShellCreateLaunchesConfiguredAgent(t *testing.T) {
	m := catalogModel(t)
	m.config = &config.Config{Plugins: config.PluginsConfig{Workspace: config.WorkspacePluginConfig{
		DefaultAgentType: "codex", AgentStart: map[string]string{"codex": "codex-custom"},
	}}}
	originalCreate, originalStart := createManagedShell, startGlobalShellAgent
	defer func() { createManagedShell, startGlobalShellAgent = originalCreate, originalStart }()
	createManagedShell = func(spec workspaceops.ManagedShellSpec) (workspaceops.ShellResult, error) {
		return workspaceops.ShellResult{SessionName: spec.SessionName}, nil
	}
	var session, command string
	startGlobalShellAgent = func(_ context.Context, gotSession, gotCommand string) error {
		session, command = gotSession, gotCommand
		return nil
	}
	m.OpenCreateShell("sidecar")
	msg := m.submitCreateShell()().(globalShellCreatedMsg)
	if msg.Err != nil || session == "" || !strings.Contains(command, "codex-custom") {
		t.Fatalf("configured agent launch: session=%q command=%q err=%v", session, command, msg.Err)
	}
}

func TestCreatedShellSelectionFollowsAcrossSortsAndTinyViewport(t *testing.T) {
	for _, sortMode := range workspacelist.SortModes {
		t.Run(sortMode.Label(), func(t *testing.T) {
			m := catalogModel(t)
			m.workspaces.SetSort(sortMode)
			m.pendingCreatedTmux = "sidecar-sh-sidecar-9"
			project := m.projects[0]
			result := m.results[projectKey(project)]
			result.Workspaces = append(result.Workspaces, workspaceinventory.Workspace{
				ID: "created", ProjectKey: projectKey(project), ProjectName: project.Name, ProjectRoot: project.Path,
				Kind: workspaceinventory.KindShell, Key: "sidecar-sh-sidecar-9", TmuxName: "sidecar-sh-sidecar-9", Name: "Shell 9", Live: true,
			})
			m.applyProjectMutationRefresh(projectMutationRefreshMsg{Project: project, Result: result})
			_ = m.workspaces.Render(workspacelist.RenderOptions{Width: 24, Height: 4, Title: "Workspaces", Focused: true})
			if got := m.workspaces.SelectedID(); got != "created" {
				t.Fatalf("selected = %q, want created", got)
			}
		})
	}
}
