package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
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
