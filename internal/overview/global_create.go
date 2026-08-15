package overview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

const (
	globalCreateProjectID = "global-create-project"
	globalCreateKindID    = "global-create-kind"
	globalCreateNameID    = "global-create-name"
	globalCreateSubmitID  = "global-create-submit"
	globalCreateConfirmID = "global-create-confirm"
	globalCreateRetryID   = "global-create-retry"
	globalCreateOpenID    = "global-create-open"
	globalCreateDeleteID  = "global-create-delete"
	globalCreateCancelID  = "global-create-cancel"
	globalCreateActionID  = "global-create-shell"
)

const (
	globalCreateShell = iota
	globalCreateWorktree
)

var (
	createManagedShell    = workspaceops.CreateManagedShell
	resolveGlobalWorktree = workspaceops.ResolveWorktreePlan
	executeGlobalWorktree = workspaceops.ExecuteWorktree
	persistGlobalJournal  = workspaceops.PersistPendingCreation
	persistGlobalIdentity = workspaceops.PersistWorktreeIdentity
	runGlobalSetup        = workspaceops.RunConfiguredSetup
	removeGlobalJournal   = workspaceops.RemovePendingCreation
	deleteGlobalWorktree  = workspaceops.DeleteCreatedWorktree
	launchGlobalSession   = workspaceops.LaunchWorktreeSession
	startGlobalShellAgent = workspaceops.StartAgentInShell
)

type globalShellCreatedMsg struct {
	Project Project
	Tmux    string
	Err     error
}

type globalWorktreePlannedMsg struct {
	Project Project
	Plan    *workspaceops.WorktreePlan
	Err     error
}

type globalWorktreeCreatedMsg struct {
	Project  Project
	Plan     *workspaceops.WorktreePlan
	Record   *workspaceops.WorktreeRecord
	Outcomes []workspaceops.SetupOutcome
	Err      error
}

type globalWorktreeDeletedMsg struct {
	Project Project
	Err     error
}

type globalWorkspaceLaunchedMsg struct {
	Project Project
	Plan    *workspaceops.WorktreePlan
	Record  *workspaceops.WorktreeRecord
	Result  workspaceops.AgentLaunchResult
	Err     error
}

type projectMutationRefreshMsg struct {
	Project Project
	Result  workspaceinventory.ProjectResult
	Err     error
}

func (m *Model) CreateOpen() bool { return m.createOpen }

func (m *Model) OpenCreateShell(projectKey string) tea.Cmd {
	return m.openCreate(projectKey, globalCreateShell, false)
}

func (m *Model) OpenCreateWorktree(projectKey string) tea.Cmd {
	return m.openCreate(projectKey, globalCreateWorktree, false)
}

// OpenCreate opens the shared chooser used by header and section + actions.
// A section supplies the project answer but leaves the capability choice live.
func (m *Model) OpenCreate(projectKey string) tea.Cmd {
	return m.openCreate(projectKey, globalCreateShell, true)
}

func (m *Model) openCreate(projectKey string, kind int, focusKind bool) tea.Cmd {
	if m.PreviewInteractive() || len(m.projects) == 0 {
		return nil
	}
	m.closeViewFlyout()
	m.closeRenameShell()
	m.createOpen = true
	m.createProjectKey = m.defaultCreateProject(projectKey)
	m.createProjectIndex = m.projectIndex(m.createProjectKey)
	m.createKindIndex = kind
	m.createNameInput = textinput.New()
	m.createNameInput.Prompt = ""
	m.createNameInput.CharLimit = shellstate.MaxNameBytes
	m.updateCreatePlaceholder()
	m.createNameInput.SetWidth(30)
	m.createError = ""
	m.createWarning = ""
	m.createBusy = false
	m.createPlan = nil
	m.createRecord = nil
	m.createModal = nil
	m.createModalWidth = 0
	m.ensureCreateModal()
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
		focus := globalCreateProjectID
		if focusKind {
			focus = globalCreateKindID
		}
		m.createModal.SetFocus(focus)
	}
	return nil
}

func (m *Model) updateCreatePlaceholder() {
	if m.createKindIndex == globalCreateWorktree {
		m.createNameInput.Placeholder = "feature-name"
		return
	}
	m.createNameInput.Placeholder = m.defaultShellDisplayName(m.createProjectKey)
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

func (m *Model) ensureCreateShellModal() { m.ensureCreateModal() }

func (m *Model) ensureCreateModal() {
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
	if m.createPlan != nil {
		m.ensureCreatePlanModal(modalW)
		return
	}
	items := make([]modal.ListItem, 0, len(m.projects))
	for _, project := range m.projects {
		items = append(items, modal.ListItem{ID: "project:" + projectKey(project), Label: project.Name, Data: projectKey(project)})
	}
	sections := []modal.Section{
		modal.Text("Choose what to create and which project will own it."),
		modal.List(globalCreateKindID, []modal.ListItem{
			{ID: "kind:shell", Label: "Shell"},
			{ID: "kind:worktree", Label: "Worktree"},
		}, &m.createKindIndex),
		modal.Spacer(),
		modal.List(globalCreateProjectID, items, &m.createProjectIndex, modal.WithMaxVisible(6)),
		modal.Spacer(),
		modal.InputWithLabel(globalCreateNameID, "Name:", &m.createNameInput),
	}
	if m.createError != "" {
		sections = append(sections, modal.Spacer(), modal.Text("Error: "+m.createError))
	}
	if m.createBusy {
		sections = append(sections, modal.Spacer(), modal.Text("Preparing…"))
	}
	sections = append(sections, modal.Spacer(), modal.Buttons(
		modal.Btn(" Create ", globalCreateSubmitID, modal.BtnPrimary()),
		modal.Btn(" Cancel ", globalCreateCancelID),
	))
	m.createModal = modal.New("Create Workspace", modal.WithWidth(modalW), modal.WithPrimaryAction(globalCreateSubmitID))
	for _, section := range sections {
		m.createModal.AddSection(section)
	}
}

func (m *Model) ensureCreatePlanModal(modalW int) {
	plan := m.createPlan
	if plan == nil {
		return
	}
	sections := []modal.Section{
		modal.Text(fmt.Sprintf("Create %s at\n%s\n\nFrom %s (%s)\n%s", plan.Branch, plan.Path, plan.SourceRef, shortCreateOID(plan.SourceOID), plan.RemotePolicy)),
	}
	if m.createError != "" {
		sections = append(sections, modal.Spacer(), modal.Text("Error: "+m.createError))
	}
	if m.createWarning != "" {
		sections = append(sections, modal.Spacer(), modal.Text("Warning: "+m.createWarning))
	}
	if m.createBusy {
		sections = append(sections, modal.Spacer(), modal.Text("Creating worktree and running setup…"))
	}
	var buttons modal.Section
	primary := globalCreateConfirmID
	if m.createRecord != nil {
		primary = globalCreateRetryID
		buttons = modal.Buttons(
			modal.Btn(" Retry setup ", globalCreateRetryID, modal.BtnPrimary()),
			modal.Btn(" Open anyway ", globalCreateOpenID),
			modal.Btn(" Delete ", globalCreateDeleteID),
		)
	} else {
		buttons = modal.Buttons(modal.Btn(" Create ", globalCreateConfirmID, modal.BtnPrimary()), modal.Btn(" Cancel ", globalCreateCancelID))
	}
	sections = append(sections, modal.Spacer(), buttons)
	m.createModal = modal.New("Confirm Worktree", modal.WithWidth(modalW), modal.WithPrimaryAction(primary))
	for _, section := range sections {
		m.createModal.AddSection(section)
	}
}

func shortCreateOID(oid string) string {
	if len(oid) > 8 {
		return oid[:8]
	}
	return oid
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
	m.createWarning = ""
	m.createModal = nil
	m.createModalWidth = 0
	m.createPlan = nil
	m.createRecord = nil
}

func (m *Model) CreatePaste(value string) bool {
	if !m.createOpen || m.createBusy || m.createPlan != nil {
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
	beforeKind := m.createKindIndex
	action, cmd := m.createModal.HandleKey(msg)
	return true, tea.Batch(cmd, m.applyCreateAction(action, before, beforeKind))
}

func (m *Model) handleCreateShellMouse(msg tea.MouseMsg) tea.Cmd {
	m.ensureCreateShellModal()
	if m.createModal == nil || m.createBusy {
		return nil
	}
	before := m.createProjectIndex
	beforeKind := m.createKindIndex
	action := m.createModal.HandleMouse(msg, m.createMouse)
	return m.applyCreateAction(action, before, beforeKind)
}

func (m *Model) applyCreateShellAction(action string, previousProject int) tea.Cmd {
	return m.applyCreateAction(action, previousProject, m.createKindIndex)
}

func (m *Model) applyCreateAction(action string, previousProject, previousKind int) tea.Cmd {
	if m.createProjectIndex != previousProject || m.createKindIndex != previousKind {
		if project, ok := m.selectedCreateProject(); ok {
			m.createProjectKey = projectKey(project)
		}
		m.updateCreatePlaceholder()
		m.createModal = nil
	}
	switch action {
	case "cancel", globalCreateCancelID:
		if m.createRecord != nil {
			// Once Git has mutated, escape/cancel means retain the usable
			// worktree; it must never silently abandon recovery state.
			return m.openCreatedWorktreeAnyway()
		}
		m.closeCreateShell()
		return nil
	case globalCreateSubmitID:
		if m.createKindIndex == globalCreateWorktree {
			return m.planCreateWorktree()
		}
		return m.submitCreateShell()
	case globalCreateConfirmID:
		return m.executeCreateWorktree()
	case globalCreateRetryID:
		return m.retryCreateSetup()
	case globalCreateOpenID:
		return m.openCreatedWorktreeAnyway()
	case globalCreateDeleteID:
		return m.deleteCreatedWorktree()
	}
	return nil
}

func (m *Model) planCreateWorktree() tea.Cmd {
	project, ok := m.selectedCreateProject()
	if !ok {
		m.createError = "Choose a project"
		m.createModal = nil
		return nil
	}
	name := strings.TrimSpace(m.createNameInput.Value())
	if name == "" {
		m.createError = "Workspace name is required"
		m.createModal = nil
		return nil
	}
	setup := config.WorktreeSetupConfig{}
	dirPrefix := true
	agent := ""
	if m.config != nil {
		setup = m.config.WorktreeSetupForProject(project.Path)
		dirPrefix = m.config.Plugins.Workspace.DirPrefix
		agent = strings.TrimSpace(m.config.Plugins.Workspace.DefaultAgentType)
	}
	m.createBusy = true
	m.createError = ""
	m.createModal = nil
	_ = saveLastGlobalCreateProject(project.Path)
	return func() tea.Msg {
		plan, err := resolveGlobalWorktree(context.Background(), project.Path, project.Path, name, "HEAD", dirPrefix, setup)
		if plan != nil {
			plan.RepoKey = projectKey(project)
			plan.OperationID = fmt.Sprintf("global-%d", time.Now().UnixNano())
			plan.AgentType = agent
		}
		return globalWorktreePlannedMsg{Project: project, Plan: plan, Err: err}
	}
}

func (m *Model) executeCreateWorktree() tea.Cmd {
	project, ok := m.selectedCreateProject()
	if !ok || m.createPlan == nil {
		return nil
	}
	plan := m.createPlan
	m.createBusy = true
	m.createError = ""
	m.createModal = nil
	return func() tea.Msg {
		record, err := executeGlobalWorktree(context.Background(), projectKey(project), plan)
		if record == nil {
			return globalWorktreeCreatedMsg{Project: project, Plan: plan, Err: err}
		}
		outcomes := make([]workspaceops.SetupOutcome, 0)
		if journalErr := persistGlobalJournal(context.Background(), plan, record); journalErr != nil {
			outcomes = append(outcomes, workspaceops.SetupOutcome{Kind: "journal", Action: "persist recovery", Required: true, Err: journalErr})
		}
		if err != nil {
			return globalWorktreeCreatedMsg{Project: project, Plan: plan, Record: record, Outcomes: outcomes, Err: err}
		}
		outcomes = append(outcomes, persistGlobalIdentity(context.Background(), plan)...)
		outcomes = append(outcomes, runGlobalSetup(context.Background(), plan)...)
		return globalWorktreeCreatedMsg{Project: project, Plan: plan, Record: record, Outcomes: outcomes, Err: err}
	}
}

func failedCreateOutcomes(outcomes []workspaceops.SetupOutcome, requiredOnly bool) []workspaceops.SetupOutcome {
	var failed []workspaceops.SetupOutcome
	for _, outcome := range outcomes {
		if outcome.Err != nil && (!requiredOnly || outcome.Required) {
			failed = append(failed, outcome)
		}
	}
	return failed
}

func summarizeCreateOutcomes(outcomes []workspaceops.SetupOutcome) string {
	parts := make([]string, 0, len(outcomes))
	for _, outcome := range outcomes {
		parts = append(parts, outcome.Action+": "+outcome.Err.Error())
	}
	return strings.Join(parts, "; ")
}

func (m *Model) retryCreateSetup() tea.Cmd {
	if m.createPlan == nil || m.createRecord == nil {
		return nil
	}
	project, ok := m.selectedCreateProject()
	if !ok {
		return nil
	}
	plan, record := m.createPlan, m.createRecord
	m.createBusy = true
	m.createError, m.createWarning = "", ""
	m.createModal = nil
	return func() tea.Msg {
		outcomes := append(persistGlobalIdentity(context.Background(), plan), runGlobalSetup(context.Background(), plan)...)
		return globalWorktreeCreatedMsg{Project: project, Plan: plan, Record: record, Outcomes: outcomes}
	}
}

func (m *Model) openCreatedWorktreeAnyway() tea.Cmd {
	project, ok := m.selectedCreateProject()
	if !ok || m.createPlan == nil || m.createRecord == nil {
		return nil
	}
	if err := removeGlobalJournal(m.createPlan); err != nil {
		m.createError = "finalize pending creation journal before opening: " + err.Error()
		m.createModal = nil
		return nil
	}
	return m.launchCreatedWorktree(project, m.createPlan, m.createRecord)
}

func (m *Model) launchCreatedWorktree(project Project, plan *workspaceops.WorktreePlan, record *workspaceops.WorktreeRecord) tea.Cmd {
	if plan == nil || record == nil {
		return nil
	}
	if plan.AgentType == "" {
		m.pendingCreatedPath = record.Path
		m.showIdleWorktrees = true
		m.closeCreateShell()
		return m.refreshProjectAfterMutation(project)
	}
	configured := map[string]string(nil)
	if m.config != nil {
		configured = m.config.Plugins.Workspace.AgentStart
	}
	command := workspaceops.ResolveAgentCommand(record.Path, plan.AgentType, configured, plan.SkipPerms)
	spec := workspaceops.AgentLaunchSpec{
		SessionName: workspaceops.WorktreeSessionName(record.Path, record.Name), WorkDir: record.Path,
		AgentCommand: command, TaskID: plan.TaskID, Env: workspaceops.BuildEnvOverrides(plan.MainWorktree),
		StartAgent: true,
	}
	m.createBusy = true
	m.createModal = nil
	return func() tea.Msg {
		result, err := launchGlobalSession(context.Background(), spec)
		return globalWorkspaceLaunchedMsg{Project: project, Plan: plan, Record: record, Result: result, Err: err}
	}
}

func (m *Model) deleteCreatedWorktree() tea.Cmd {
	project, ok := m.selectedCreateProject()
	if !ok || m.createPlan == nil || m.createRecord == nil {
		return nil
	}
	plan, record := m.createPlan, m.createRecord
	m.createBusy = true
	m.createModal = nil
	return func() tea.Msg {
		err := deleteGlobalWorktree(context.Background(), plan, record)
		if err == nil {
			_ = removeGlobalJournal(plan)
		}
		return globalWorktreeDeletedMsg{Project: project, Err: err}
	}
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
		if err == nil && agent != "" {
			configured := map[string]string(nil)
			if m.config != nil {
				configured = m.config.Plugins.Workspace.AgentStart
			}
			command := workspaceops.ResolveAgentCommand(project.Path, agent, configured, false)
			command = withGlobalShellNaming(command, agent)
			err = startGlobalShellAgent(context.Background(), session, command)
		}
		return globalShellCreatedMsg{Project: project, Tmux: session, Err: err}
	}
}

func withGlobalShellNaming(command, agent string) string {
	flag := ""
	switch agent {
	case "claude":
		flag = "--append-system-prompt"
	case "grok":
		flag = "--rules"
	}
	if flag == "" {
		return command
	}
	return command + " " + flag + " " + workspaceops.ShellQuote(shellstate.NamingInstruction)
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
		createdShell := workspace.Kind == workspaceinventory.KindShell && workspace.TmuxName == m.pendingCreatedTmux
		createdWorktree := workspace.Kind == workspaceinventory.KindWorktree && m.pendingCreatedPath != "" && workspace.Path == m.pendingCreatedPath
		if createdShell || createdWorktree {
			m.workspaces.SelectID(workspace.ID)
			break
		}
	}
	m.pendingCreatedTmux = ""
	m.pendingCreatedPath = ""
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
