package overview

import (
	"context"
	"errors"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mouse"
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

func TestGlobalWorktreeWithoutAgentStillLaunchesPlainWorktreeSession(t *testing.T) {
	m := catalogModel(t)
	m.OpenCreateWorktree("")
	plan := &workspaceops.WorktreePlan{MainWorktree: t.TempDir(), Path: t.TempDir(), Branch: "created"}
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
		t.Fatal("plain worktree closed and refreshed without launching its session")
	}
	if _, ok := launchCmd().(globalWorkspaceLaunchedMsg); !ok {
		t.Fatalf("plain worktree launch returned unexpected message")
	}
	if spec.StartAgent || spec.AgentCommand != "" || spec.WorkDir != plan.Path {
		t.Fatalf("plain worktree launch spec = %+v", spec)
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

func renderCreateModal(t *testing.T, m *Model) string {
	t.Helper()
	if m.width < 1 {
		m.width = 80
	}
	if m.height < 1 {
		m.height = 30
	}
	m.ensureCreateModal()
	if m.createModal == nil {
		t.Fatal("create modal is nil")
	}
	return m.createModal.Render(m.width, m.height, m.createMouse)
}

func createKey(k string) tea.KeyPressMsg {
	switch k {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "left":
		return tea.KeyPressMsg{Code: tea.KeyLeft}
	case "right":
		return tea.KeyPressMsg{Code: tea.KeyRight}
	default:
		r := []rune(k)
		if len(r) == 1 {
			return tea.KeyPressMsg{Code: r[0], Text: k}
		}
		return tea.KeyPressMsg{Text: k}
	}
}

func createHit(m *Model, id string) (mouse.Region, bool) {
	for _, region := range m.createMouse.HitMap.Regions() {
		if region.ID == id {
			return region, true
		}
	}
	return mouse.Region{}, false
}

func TestCreateModalProjectComboFiltersAndSubmitUsesProject(t *testing.T) {
	m := catalogModel(t)
	original := createManagedShell
	defer func() { createManagedShell = original }()
	var got workspaceops.ManagedShellSpec
	createManagedShell = func(spec workspaceops.ManagedShellSpec) (workspaceops.ShellResult, error) {
		got = spec
		return workspaceops.ShellResult{SessionName: spec.SessionName}, nil
	}
	m.OpenCreateShell("sidecar")
	renderCreateModal(t, m)
	m.createModal.SetFocus(globalCreateProjectID)
	m.createProjectInput.SetValue("")
	renderCreateModal(t, m)

	m.handleCreateShellKey(createKey("b"))
	if m.createBusy || !m.CreateOpen() {
		t.Fatal("typing a project filter submitted")
	}
	if m.createProjectInput.Value() != "b" {
		t.Fatalf("filter = %q, want b", m.createProjectInput.Value())
	}
	if project, ok := m.selectedCreateProject(); !ok || project.Name != "braid" {
		t.Fatalf("filtered project = %+v ok=%v, want braid", project, ok)
	}

	renderCreateModal(t, m)
	item, ok := createHit(m, globalCreateProjectID+"/item/0")
	if !ok {
		t.Fatal("expected project overlay row")
	}
	if cmd := m.handleCreateShellMouse(tea.MouseClickMsg{X: item.Rect.X + 1, Y: item.Rect.Y, Button: tea.MouseLeft}); cmd != nil {
		t.Fatal("overlay click submitted")
	}
	if project, ok := m.selectedCreateProject(); !ok || project.Path != "/tmp/braid" {
		t.Fatalf("clicked project = %+v", project)
	}

	cmd := m.submitCreateShell()
	if cmd == nil {
		t.Fatal("expected submit cmd")
	}
	msg := cmd().(globalShellCreatedMsg)
	if msg.Project.Path != "/tmp/braid" || got.ProjectRoot != "/tmp/braid" || got.WorkDir != "/tmp/braid" {
		t.Fatalf("submit used wrong project: msg=%+v spec=%+v", msg.Project, got)
	}
}

func TestCreateModalKindClickChangesKindAndPlaceholder(t *testing.T) {
	m := catalogModel(t)
	m.OpenCreateShell("sidecar")
	renderCreateModal(t, m)
	if m.createKindIndex != globalCreateShell {
		t.Fatalf("kind = %d, want shell", m.createKindIndex)
	}
	if m.createNameInput.Placeholder != m.defaultShellDisplayName("sidecar") {
		t.Fatalf("shell placeholder = %q", m.createNameInput.Placeholder)
	}

	region, ok := createHit(m, globalCreateKindID)
	if !ok {
		t.Fatal("kind control was not hit-tested")
	}
	if cmd := m.handleCreateShellMouse(tea.MouseClickMsg{
		X: region.Rect.X + region.Rect.W - 1, Y: region.Rect.Y, Button: tea.MouseLeft,
	}); cmd != nil {
		t.Fatalf("kind click submitted: %v", cmd)
	}
	if m.createKindIndex != globalCreateWorktree {
		t.Fatalf("kind after click = %d, want worktree", m.createKindIndex)
	}
	if m.createNameInput.Placeholder != "feature-name" {
		t.Fatalf("worktree placeholder = %q", m.createNameInput.Placeholder)
	}

	renderCreateModal(t, m)
	region, ok = createHit(m, globalCreateKindID)
	if !ok {
		t.Fatal("kind control missing after rebuild")
	}
	m.handleCreateShellMouse(tea.MouseClickMsg{
		X: region.Rect.X + 1, Y: region.Rect.Y, Button: tea.MouseLeft,
	})
	if m.createKindIndex != globalCreateShell {
		t.Fatalf("kind after left click = %d, want shell", m.createKindIndex)
	}
}

func TestCreateModalAgentComboSelectedAgentIsUsed(t *testing.T) {
	m := catalogModel(t)
	m.config = &config.Config{Plugins: config.PluginsConfig{Workspace: config.WorkspacePluginConfig{
		DefaultAgentType: "claude",
		Agents:           []string{"claude", "grok"},
		AgentStart:       map[string]string{"grok": "grok-custom"},
	}}}
	originalCreate, originalStart := createManagedShell, startGlobalShellAgent
	defer func() { createManagedShell, startGlobalShellAgent = originalCreate, originalStart }()
	var spec workspaceops.ManagedShellSpec
	createManagedShell = func(got workspaceops.ManagedShellSpec) (workspaceops.ShellResult, error) {
		spec = got
		return workspaceops.ShellResult{SessionName: got.SessionName}, nil
	}
	var session, command string
	started := 0
	startGlobalShellAgent = func(_ context.Context, gotSession, gotCommand string) error {
		started++
		session, command = gotSession, gotCommand
		return nil
	}

	m.OpenCreateShell("sidecar")
	renderCreateModal(t, m)
	if m.createAgentTypes()[0] != "" {
		t.Fatalf("shell agent list should lead with None: %v", m.createAgentTypes())
	}
	m.createModal.SetFocus(globalCreateAgentID)
	renderCreateModal(t, m)
	m.handleCreateShellKey(createKey("down"))
	if m.selectedCreateAgent() != "grok" {
		t.Fatalf("selected agent = %q, want grok (input=%q idx=%d)", m.selectedCreateAgent(), m.createAgentInput.Value(), m.createAgentIndex)
	}

	msg := m.submitCreateShell()().(globalShellCreatedMsg)
	if msg.Err != nil || spec.AgentType != "grok" || session == "" || !strings.Contains(command, "grok-custom") {
		t.Fatalf("chosen agent not used: spec=%+v session=%q command=%q err=%v", spec, session, command, msg.Err)
	}
	if started != 1 {
		t.Fatalf("start count = %d", started)
	}

	m.OpenCreateWorktree("sidecar")
	m.createNameInput.SetValue("feature")
	if got := m.createAgentTypes(); got[len(got)-1] != "" {
		t.Fatalf("worktree agent list should end with None: %v", got)
	}
	originalPlan := resolveGlobalWorktree
	defer func() { resolveGlobalWorktree = originalPlan }()
	resolveGlobalWorktree = func(context.Context, string, string, string, string, bool, config.WorktreeSetupConfig) (*workspaceops.WorktreePlan, error) {
		return &workspaceops.WorktreePlan{Branch: "feature", Path: "/tmp/feature"}, nil
	}
	m.createAgentType = ""
	m.rematchCreateAgentIndex()
	planMsg := m.planCreateWorktree()().(globalWorktreePlannedMsg)
	if planMsg.Plan == nil || planMsg.Plan.AgentType != "" {
		t.Fatalf("None should leave plan.AgentType empty: %+v", planMsg.Plan)
	}
}

func TestCreateModalNoneDoesNotStartAgent(t *testing.T) {
	m := catalogModel(t)
	m.config = &config.Config{Plugins: config.PluginsConfig{Workspace: config.WorkspacePluginConfig{
		DefaultAgentType: "claude", AgentStart: map[string]string{"claude": "claude-custom"},
	}}}
	originalCreate, originalStart := createManagedShell, startGlobalShellAgent
	defer func() { createManagedShell, startGlobalShellAgent = originalCreate, originalStart }()
	var spec workspaceops.ManagedShellSpec
	createManagedShell = func(got workspaceops.ManagedShellSpec) (workspaceops.ShellResult, error) {
		spec = got
		return workspaceops.ShellResult{SessionName: got.SessionName}, nil
	}
	started := 0
	startGlobalShellAgent = func(context.Context, string, string) error {
		started++
		return nil
	}
	m.OpenCreateShell("sidecar")
	m.createAgentType = ""
	m.rematchCreateAgentIndex()
	msg := m.submitCreateShell()().(globalShellCreatedMsg)
	if msg.Err != nil || spec.AgentType != "" || started != 0 {
		t.Fatalf("None started an agent: spec=%+v started=%d err=%v", spec, started, msg.Err)
	}
}

func TestCreateModalTabCyclesWithoutTrapping(t *testing.T) {
	m := catalogModel(t)
	m.OpenCreate("")
	renderCreateModal(t, m)
	if m.createModal.FocusedID() != globalCreateKindID {
		t.Fatalf("chooser focus = %q, want kind", m.createModal.FocusedID())
	}
	want := []string{globalCreateProjectID, globalCreateAgentID, globalCreateNameID, globalCreateSubmitID, globalCreateCancelID, globalCreateKindID}
	for i, id := range want {
		m.handleCreateShellKey(createKey("tab"))
		if got := m.createModal.FocusedID(); got != id {
			t.Fatalf("tab %d focus = %q, want %q", i+1, got, id)
		}
	}
	m.handleCreateShellKey(createKey("shift+tab"))
	if got := m.createModal.FocusedID(); got != globalCreateCancelID {
		t.Fatalf("shift+tab focus = %q, want cancel", got)
	}
}

func TestCreateModalEscClosesOverlayThenModal(t *testing.T) {
	m := catalogModel(t)
	m.OpenCreateShell("sidecar")
	renderCreateModal(t, m)
	m.createModal.SetFocus(globalCreateProjectID)
	renderCreateModal(t, m)

	handled, cmd := m.handleCreateShellKey(createKey("esc"))
	if !handled || cmd != nil || !m.CreateOpen() {
		t.Fatalf("first esc closed modal: handled=%v cmd=%v open=%v", handled, cmd != nil, m.CreateOpen())
	}
	handled, cmd = m.handleCreateShellKey(createKey("esc"))
	if !handled || cmd != nil || m.CreateOpen() {
		t.Fatalf("second esc should cancel: handled=%v cmd=%v open=%v", handled, cmd != nil, m.CreateOpen())
	}
}

func TestCreateModalLastProjectAndAgentPersist(t *testing.T) {
	var lastProject, lastAgent string
	origLoadP, origSaveP := loadLastGlobalCreateProject, saveLastGlobalCreateProject
	origLoadA, origSaveA := loadLastCreateAgent, saveLastCreateAgent
	defer func() {
		loadLastGlobalCreateProject, saveLastGlobalCreateProject = origLoadP, origSaveP
		loadLastCreateAgent, saveLastCreateAgent = origLoadA, origSaveA
	}()
	loadLastGlobalCreateProject = func() string { return lastProject }
	saveLastGlobalCreateProject = func(v string) error { lastProject = v; return nil }
	loadLastCreateAgent = func() string { return lastAgent }
	saveLastCreateAgent = func(v string) error { lastAgent = v; return nil }

	m := catalogModel(t)
	m.config = &config.Config{Plugins: config.PluginsConfig{Workspace: config.WorkspacePluginConfig{
		DefaultAgentType: "claude",
	}}}
	original := createManagedShell
	defer func() { createManagedShell = original }()
	createManagedShell = func(spec workspaceops.ManagedShellSpec) (workspaceops.ShellResult, error) {
		return workspaceops.ShellResult{SessionName: spec.SessionName}, nil
	}
	m.OpenCreateShell("braid")
	m.createAgentType = "grok"
	m.rematchCreateAgentIndex()
	_ = m.submitCreateShell()()
	if lastProject != "/tmp/braid" || lastAgent != "grok" {
		t.Fatalf("persisted project=%q agent=%q", lastProject, lastAgent)
	}

	fresh := catalogModel(t)
	fresh.config = m.config
	fresh.workspaces.SetItems(nil)
	fresh.OpenCreate("")
	if project, ok := fresh.selectedCreateProject(); !ok || project.Name != "braid" {
		t.Fatalf("fresh project = %+v ok=%v, want braid", project, ok)
	}
	if fresh.createAgentType != "grok" {
		t.Fatalf("fresh agent = %q, want grok", fresh.createAgentType)
	}
}

func TestCreateModalComboQuerySurvivesUnrelatedRebuild(t *testing.T) {
	m := catalogModel(t)
	m.OpenCreateShell("sidecar")
	renderCreateModal(t, m)
	m.createModal.SetFocus(globalCreateAgentID)
	m.createAgentInput.SetValue("")
	renderCreateModal(t, m)
	m.handleCreateShellKey(createKey("g"))
	if m.createAgentInput.Value() != "g" {
		t.Fatalf("agent query = %q, want g", m.createAgentInput.Value())
	}
	m.width = 40
	renderCreateModal(t, m)
	if m.createAgentInput.Value() != "g" {
		t.Fatalf("rebuild wiped agent query: %q", m.createAgentInput.Value())
	}
	if m.createModal.FocusedID() != globalCreateAgentID {
		t.Fatalf("rebuild dropped agent focus: %q", m.createModal.FocusedID())
	}
}

func TestCreateModalOverlayDoesNotChangeHeight(t *testing.T) {
	m := catalogModel(t)
	m.OpenCreateShell("sidecar")
	m.createModal.SetFocus(globalCreateNameID)
	closed := renderCreateModal(t, m)
	m.createModal.SetFocus(globalCreateProjectID)
	open := renderCreateModal(t, m)
	if lipgloss.Height(closed) != lipgloss.Height(open) {
		t.Fatalf("combo overlay changed height: closed=%d open=%d", lipgloss.Height(closed), lipgloss.Height(open))
	}
}

func createdShellWorkspace(project Project) workspaceinventory.Workspace {
	return workspaceinventory.Workspace{
		ID: "created", ProjectKey: projectKey(project), ProjectName: project.Name, ProjectRoot: project.Path,
		Kind: workspaceinventory.KindShell, Key: "sidecar-sh-sidecar-9", TmuxName: "sidecar-sh-sidecar-9", Name: "Shell 9", Live: true,
	}
}

func TestPendingCreatedShellSurvivesMissThenHits(t *testing.T) {
	m := catalogModel(t)
	prev := m.workspaces.SelectedID()
	m.pendingCreatedTmux = "sidecar-sh-sidecar-9"
	project := m.projects[0]
	miss := m.results[projectKey(project)]
	m.applyProjectMutationRefresh(projectMutationRefreshMsg{Project: project, Result: miss})
	if m.pendingCreatedTmux != "sidecar-sh-sidecar-9" {
		t.Fatal("pending cleared by a refresh that omitted the session")
	}
	if got := m.workspaces.SelectedID(); got != prev {
		t.Fatalf("missed refresh stole selection: %q -> %q", prev, got)
	}

	hit := miss
	hit.Workspaces = append(append([]workspaceinventory.Workspace(nil), miss.Workspaces...), createdShellWorkspace(project))
	m.applyProjectMutationRefresh(projectMutationRefreshMsg{Project: project, Result: hit})
	if m.pendingCreatedTmux != "" || m.pendingCreatedPath != "" {
		t.Fatalf("pending still set after hit: tmux=%q path=%q", m.pendingCreatedTmux, m.pendingCreatedPath)
	}
	if got := m.workspaces.SelectedID(); got != "created" {
		t.Fatalf("selected = %q, want created", got)
	}
}

func TestPendingCreatedShellSurvivesPollWithoutSession(t *testing.T) {
	m := catalogModel(t)
	m.ctx, m.cancel = context.WithCancel(context.Background())
	t.Cleanup(func() {
		if m.cancel != nil {
			m.cancel()
		}
	})
	m.generation = 3
	prev := m.workspaces.SelectedID()
	m.pendingCreatedTmux = "sidecar-sh-sidecar-9"
	project := m.projects[0]
	without := m.results[projectKey(project)]

	m.Update(pollMsg{Generation: 2})
	if m.pendingCreatedTmux != "sidecar-sh-sidecar-9" {
		t.Fatal("stale poll generation cleared pending")
	}

	_ = m.start(m.projects, "poll")
	if m.pendingCreatedTmux != "sidecar-sh-sidecar-9" {
		t.Fatal("start poll cleared pending before the session existed")
	}
	if got := m.workspaces.SelectedID(); got != prev {
		t.Fatalf("start poll stole selection: %q -> %q", prev, got)
	}

	m.Update(projectMsg{Generation: m.generation, Project: project, Phase: phaseStatus, Result: without})
	if m.pendingCreatedTmux != "sidecar-sh-sidecar-9" {
		t.Fatal("poll status without the session cleared pending")
	}
	if got := m.workspaces.SelectedID(); got != prev {
		t.Fatalf("poll status stole selection: %q -> %q", prev, got)
	}

	with := without
	with.Workspaces = append(append([]workspaceinventory.Workspace(nil), without.Workspaces...), createdShellWorkspace(project))
	m.Update(projectMsg{Generation: m.generation, Project: project, Phase: phaseStatus, Result: with})
	if m.pendingCreatedTmux != "" {
		t.Fatal("pending not cleared after poll presented the session")
	}
	if got := m.workspaces.SelectedID(); got != "created" {
		t.Fatalf("selected = %q, want created", got)
	}
}

func TestFailedShellCreateClearsPendingWithoutMovingSelection(t *testing.T) {
	m := catalogModel(t)
	prev := m.workspaces.SelectedID()
	m.pendingCreatedTmux = "sidecar-sh-sidecar-9"
	m.Update(globalShellCreatedMsg{Project: m.projects[0], Tmux: "sidecar-sh-sidecar-9", Err: errors.New("tmux failed")})
	if m.pendingCreatedTmux != "" {
		t.Fatal("failed create left pending set")
	}
	if got := m.workspaces.SelectedID(); got != prev {
		t.Fatalf("failed create moved selection to %q", got)
	}
}

func TestNewerCreateReplacesPendingTmux(t *testing.T) {
	m := catalogModel(t)
	original := createManagedShell
	defer func() { createManagedShell = original }()
	createManagedShell = func(spec workspaceops.ManagedShellSpec) (workspaceops.ShellResult, error) {
		return workspaceops.ShellResult{SessionName: spec.SessionName}, nil
	}
	m.pendingCreatedTmux = "old-session"
	m.pendingCreatedPath = "/tmp/stale"
	m.OpenCreateShell("sidecar")
	_ = m.submitCreateShell()
	if m.pendingCreatedTmux == "" || m.pendingCreatedTmux == "old-session" {
		t.Fatalf("pending tmux = %q, want the newer session", m.pendingCreatedTmux)
	}
	if m.pendingCreatedPath != "" {
		t.Fatalf("newer shell create left stale path %q", m.pendingCreatedPath)
	}
}

func TestCancelCreateClearsPending(t *testing.T) {
	m := catalogModel(t)
	m.OpenCreateShell("sidecar")
	m.pendingCreatedTmux = "should-clear"
	m.applyCreateAction(globalCreateCancelID, m.createProjectIndex, m.createKindIndex)
	if m.pendingCreatedTmux != "" {
		t.Fatal("cancel-without-create left pending set")
	}
}

func TestPendingCreatedWorktreeSurvivesMissThenHits(t *testing.T) {
	m := catalogModel(t)
	m.showIdleWorktrees = true
	prev := m.workspaces.SelectedID()
	m.pendingCreatedPath = "/tmp/created"
	project := m.projects[0]
	miss := m.results[projectKey(project)]
	m.applyProjectMutationRefresh(projectMutationRefreshMsg{Project: project, Result: miss})
	if m.pendingCreatedPath != "/tmp/created" {
		t.Fatal("worktree pending cleared by a miss")
	}
	if got := m.workspaces.SelectedID(); got != prev {
		t.Fatalf("worktree miss stole selection: %q -> %q", prev, got)
	}

	hit := miss
	hit.Workspaces = append(append([]workspaceinventory.Workspace(nil), miss.Workspaces...), workspaceinventory.Workspace{
		ID: "created-worktree", ProjectKey: projectKey(project), ProjectName: project.Name, ProjectRoot: project.Path,
		Kind: workspaceinventory.KindWorktree, Key: "created", Path: "/tmp/created", Name: "Created",
	})
	m.applyProjectMutationRefresh(projectMutationRefreshMsg{Project: project, Result: hit})
	if m.pendingCreatedPath != "" {
		t.Fatal("worktree pending not cleared after hit")
	}
	if got := m.workspaces.SelectedID(); got != "created-worktree" {
		t.Fatalf("selected = %q, want created worktree", got)
	}
}
