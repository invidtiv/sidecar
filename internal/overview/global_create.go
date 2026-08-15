package overview

import (
	"context"
	"fmt"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

const (
	globalCreateProjectID = "global-create-project"
	globalCreateNameID    = "global-create-name"
	globalCreateSubmitID  = "global-create-submit"
	globalCreateCancelID  = "global-create-cancel"
	globalCreateActionID  = "global-create-shell"
)

var createManagedShell = workspaceops.CreateManagedShell

type globalShellCreatedMsg struct {
	Project Project
	Tmux    string
	Err     error
}

type projectMutationRefreshMsg struct {
	Project Project
	Result  workspaceinventory.ProjectResult
	Err     error
}

func (m *Model) CreateOpen() bool { return m.createOpen }

func (m *Model) OpenCreateShell(projectKey string) tea.Cmd {
	if m.PreviewInteractive() || len(m.projects) == 0 {
		return nil
	}
	m.closeViewFlyout()
	m.closeRenameShell()
	m.createOpen = true
	m.createProjectKey = m.defaultCreateProject(projectKey)
	m.createProjectIndex = m.projectIndex(m.createProjectKey)
	m.createNameInput = textinput.New()
	m.createNameInput.Prompt = ""
	m.createNameInput.CharLimit = shellstate.MaxNameBytes
	m.createNameInput.Placeholder = m.defaultShellDisplayName(m.createProjectKey)
	m.createNameInput.SetWidth(30)
	m.createError = ""
	m.createBusy = false
	m.createModal = nil
	m.createModalWidth = 0
	m.ensureCreateShellModal()
	if m.createModal != nil {
		w, h := m.width, m.height
		if w < 1 {
			w = 80
		}
		if h < 1 {
			h = 24
		}
		_ = m.createModal.Render(w, h, m.createMouse)
		m.createModal.Reset()
		m.createModal.SetFocus(globalCreateProjectID)
	}
	return nil
}

func (m *Model) defaultCreateProject(explicit string) string {
	if m.projectIndex(explicit) >= 0 {
		return explicit
	}
	if selected, ok := m.SelectedWorkspace(); ok && m.projectIndex(selected.ProjectKey) >= 0 {
		return selected.ProjectKey
	}
	if last := loadLastGlobalCreateProject(); m.projectIndex(last) >= 0 {
		return last
	}
	if len(m.projects) > 0 {
		return projectKey(m.projects[0])
	}
	return ""
}

func (m *Model) projectIndex(key string) int {
	for i, project := range m.projects {
		if projectKey(project) == key || project.Path == key {
			return i
		}
	}
	return -1
}

func (m *Model) selectedCreateProject() (Project, bool) {
	if m.createProjectIndex < 0 || m.createProjectIndex >= len(m.projects) {
		return Project{}, false
	}
	return m.projects[m.createProjectIndex], true
}

func (m *Model) shellDefinitions(key string) []shellstate.Definition {
	result := m.results[key]
	defs := make([]shellstate.Definition, 0)
	for _, workspace := range result.Workspaces {
		if workspace.Kind != workspaceinventory.KindShell {
			continue
		}
		defs = append(defs, shellstate.Definition{TmuxName: workspace.TmuxName, DisplayName: workspace.Name, Namespace: workspace.Namespace, WorkDir: workspace.Path})
	}
	return defs
}

func (m *Model) defaultShellDisplayName(key string) string {
	idx := m.projectIndex(key)
	if idx < 0 {
		return "Shell 1"
	}
	display, _ := workspaceops.ShellNames(m.projects[idx].Path, m.shellDefinitions(projectKey(m.projects[idx])))
	return display
}

func (m *Model) ensureCreateShellModal() {
	if !m.createOpen {
		return
	}
	modalW := 52
	if m.width > 0 && modalW > m.width-4 {
		modalW = m.width - 4
	}
	if modalW < 24 {
		modalW = 24
	}
	if m.createModal != nil && m.createModalWidth == modalW {
		return
	}
	m.createModalWidth = modalW
	items := make([]modal.ListItem, 0, len(m.projects))
	for _, project := range m.projects {
		items = append(items, modal.ListItem{ID: "project:" + projectKey(project), Label: project.Name, Data: projectKey(project)})
	}
	sections := []modal.Section{
		modal.Text("Choose the project that will own the new shell."),
		modal.List(globalCreateProjectID, items, &m.createProjectIndex, modal.WithMaxVisible(6)),
		modal.Spacer(),
		modal.InputWithLabel(globalCreateNameID, "Name:", &m.createNameInput),
	}
	if m.createError != "" {
		sections = append(sections, modal.Spacer(), modal.Text("Error: "+m.createError))
	}
	if m.createBusy {
		sections = append(sections, modal.Spacer(), modal.Text("Creating shell…"))
	}
	sections = append(sections, modal.Spacer(), modal.Buttons(
		modal.Btn(" Create ", globalCreateSubmitID, modal.BtnPrimary()),
		modal.Btn(" Cancel ", globalCreateCancelID),
	))
	m.createModal = modal.New("Create Shell", modal.WithWidth(modalW), modal.WithPrimaryAction(globalCreateSubmitID))
	for _, section := range sections {
		m.createModal.AddSection(section)
	}
}

func (m *Model) overlayCreateShell(background string, width, height int) string {
	m.ensureCreateShellModal()
	if m.createModal == nil {
		return background
	}
	rendered := m.createModal.Render(width, height, m.createMouse)
	return ui.OverlayModal(background, rendered, width, height)
}

func (m *Model) closeCreateShell() {
	m.createOpen = false
	m.createBusy = false
	m.createError = ""
	m.createModal = nil
	m.createModalWidth = 0
}

func (m *Model) CreatePaste(value string) bool {
	if !m.createOpen || m.createBusy {
		return false
	}
	m.createNameInput.SetValue(m.createNameInput.Value() + value)
	m.createError = ""
	m.createModal = nil
	return true
}

func (m *Model) handleCreateShellKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	m.ensureCreateShellModal()
	if m.createModal == nil {
		return true, nil
	}
	if m.createBusy {
		if msg.String() == "esc" {
			return true, nil
		}
		return true, nil
	}
	before := m.createProjectIndex
	action, cmd := m.createModal.HandleKey(msg)
	return true, tea.Batch(cmd, m.applyCreateShellAction(action, before))
}

func (m *Model) handleCreateShellMouse(msg tea.MouseMsg) tea.Cmd {
	m.ensureCreateShellModal()
	if m.createModal == nil || m.createBusy {
		return nil
	}
	before := m.createProjectIndex
	action := m.createModal.HandleMouse(msg, m.createMouse)
	return m.applyCreateShellAction(action, before)
}

func (m *Model) applyCreateShellAction(action string, previousProject int) tea.Cmd {
	if m.createProjectIndex != previousProject {
		if project, ok := m.selectedCreateProject(); ok {
			m.createProjectKey = projectKey(project)
			m.createNameInput.Placeholder = m.defaultShellDisplayName(m.createProjectKey)
		}
		m.createModal = nil
	}
	switch action {
	case "cancel", globalCreateCancelID:
		m.closeCreateShell()
		return nil
	case globalCreateSubmitID:
		return m.submitCreateShell()
	}
	return nil
}

func (m *Model) submitCreateShell() tea.Cmd {
	project, ok := m.selectedCreateProject()
	if !ok {
		m.createError = "Choose a project"
		m.createModal = nil
		return nil
	}
	key := projectKey(project)
	display, session := workspaceops.ShellNames(project.Path, m.shellDefinitions(key))
	if custom := strings.TrimSpace(m.createNameInput.Value()); custom != "" {
		var err error
		display, err = shellstate.NormalizeName(custom)
		if err != nil {
			m.createError = err.Error()
			m.createModal = nil
			return nil
		}
	}
	cols, rows := max(20, m.width/2-4), max(5, m.height-4)
	agent := ""
	if m.config != nil {
		agent = strings.TrimSpace(m.config.Plugins.Workspace.DefaultAgentType)
	}
	spec := workspaceops.ManagedShellSpec{ShellSpec: workspaceops.ShellSpec{WorkDir: project.Path, SessionName: session, DisplayName: display, Cols: cols, Rows: rows}, ProjectRoot: project.Path, AgentType: agent}
	m.createBusy = true
	m.createError = ""
	m.createModal = nil
	m.pendingCreatedTmux = session
	_ = saveLastGlobalCreateProject(project.Path)
	return func() tea.Msg {
		_, err := createManagedShell(spec)
		return globalShellCreatedMsg{Project: project, Tmux: session, Err: err}
	}
}

func (m *Model) refreshProjectAfterMutation(project Project) tea.Cmd {
	collector := m.collector.ForRefresh(maxCaptures, m.shellClaims)
	roots := append([]string(nil), m.roots...)
	return func() tea.Msg {
		ctx := context.Background()
		inventory := collector.CollectProjectInventory(ctx, project.Name, project.Path)
		if inventory.Err != nil {
			return projectMutationRefreshMsg{Project: project, Result: inventory, Err: inventory.Err}
		}
		claimsInputs := make([]workspaceinventory.ProjectResult, 0, len(m.results)+1)
		for key, existing := range m.results {
			if key != projectKey(project) {
				claimsInputs = append(claimsInputs, existing)
			}
		}
		claimsInputs = append(claimsInputs, inventory)
		collector = collector.WithShellClaims(workspaceinventory.BuildShellClaims(claimsInputs))
		panes, err := collector.ListPanes(ctx)
		if err != nil {
			return projectMutationRefreshMsg{Project: project, Result: inventory, Err: err}
		}
		result := collector.RefreshProjectStatus(ctx, inventory, roots, panes)
		return projectMutationRefreshMsg{Project: project, Result: withProjectIdentity(result, project), Err: result.Err}
	}
}

func (m *Model) applyProjectMutationRefresh(msg projectMutationRefreshMsg) tea.Cmd {
	if msg.Err != nil {
		m.createError = msg.Err.Error()
		m.createOpen = true
		m.createBusy = false
		m.createModal = nil
		return nil
	}
	m.results[projectKey(msg.Project)] = msg.Result
	delete(m.projectErrors, projectKey(msg.Project))
	m.syncBoard()
	for _, workspace := range msg.Result.Workspaces {
		if workspace.Kind == workspaceinventory.KindShell && workspace.TmuxName == m.pendingCreatedTmux {
			m.workspaces.SelectID(workspace.ID)
			break
		}
	}
	m.pendingCreatedTmux = ""
	return m.previewSync()
}

func (m *Model) createWheelAtBoundary(msg tea.MouseWheelMsg) bool {
	if !m.createOpen || m.createModal == nil {
		return false
	}
	return m.createModal.WheelAtBoundary(msg, m.createMouse)
}

func createProjectKeyFromAction(id string) string {
	return strings.TrimPrefix(id, globalCreateActionID+":")
}

func (m *Model) debugCreateState() string {
	project, _ := m.selectedCreateProject()
	return fmt.Sprintf("%s:%s", projectKey(project), m.createNameInput.Value())
}
