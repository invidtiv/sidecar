package overview

import (
	"context"
	"fmt"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacecreate"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

const (
	globalCreateConfirmID = "global-create-confirm"
	globalCreateRetryID   = "global-create-retry"
	globalCreateOpenID    = "global-create-open"
	globalCreateDeleteID  = "global-create-delete"
	globalCreateCancelID  = "global-create-cancel"
	globalCreateActionID  = "global-create-shell"
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
	listCreateBranches    = workspaceops.ListLocalBranches
	currentCreateBranch   = workspaceops.CurrentBranch
	resolveGlobalAgentCmd = workspaceops.ResolveAgentCommand
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
	// Background marks a refresh nobody asked for — a manifest watcher signal or
	// a sweep tick rather than a create or delete this surface just performed.
	// Its failures must stay silent: raising the create modal's error on a
	// project the user never touched would be an alert about nothing.
	Background bool
	// DispatchedAt dates the durable state this result was built from, so a
	// background refresh that has been overtaken can be recognised and dropped.
	DispatchedAt time.Time
}

type globalCreateBranchesMsg struct {
	ProjectKey string
	Branches   []string
	Current    string
}

func (m *Model) CreateOpen() bool { return m.createOpen }

func (m *Model) OpenCreateShell(projectKey string) tea.Cmd {
	return m.openCreate(projectKey, workspacecreate.KindShell, false, false)
}

func (m *Model) OpenCreateWorktree(projectKey string) tea.Cmd {
	return m.openCreate(projectKey, workspacecreate.KindWorktree, false, false)
}

// OpenCreate opens the shared chooser used by header and section + actions.
// A section supplies the project answer but leaves the capability choice live.
func (m *Model) OpenCreate(projectKey string) tea.Cmd {
	return m.openCreate(projectKey, workspacecreate.KindWorktree, true, false)
}

// OpenPaneSwitcher opens the create modal as the pane switcher: kind list
// focused, on the row it was last left on. It is the global browser's half of
// the entry the project workspace binds — a pane opens beside what you are
// reading without first leaving it — and both surfaces answer the same key in
// the same contexts, which internal/keymap's parity test holds them to.
func (m *Model) OpenPaneSwitcher() tea.Cmd {
	return m.openCreate("", workspacecreate.KindWorktree, true, true)
}

func (m *Model) openCreate(projectKey string, kind workspacecreate.Kind, focusKind, useLastKind bool) tea.Cmd {
	if m.PreviewInteractive() || len(m.projects) == 0 {
		return nil
	}
	m.closeViewFlyout()
	m.closeRenameShell()
	key := m.normalizedCreateProjectKey(projectKey)
	agents := []string(nil)
	defaultAgent := ""
	if m.config != nil {
		agents = m.config.Plugins.Workspace.Agents
		defaultAgent = strings.TrimSpace(m.config.Plugins.Workspace.DefaultAgentType)
	}
	m.createForm = workspacecreate.Open(workspacecreate.OpenOpts{
		Kind:         kind,
		FocusKind:    focusKind,
		UseLastKind:  useLastKind,
		ShowProject:  true,
		ProjectKey:   key,
		Projects:     m.createProjectItems(),
		Agents:       agents,
		NextShell:    m.defaultShellDisplayName(key),
		DefaultAgent: defaultAgent,
		// This surface's preview owns a pane tree, so the switcher's passive
		// rows work here exactly as they do in the project workspace. What it
		// has no place for is a second live terminal, so HostScoped rows stay
		// off — same rule, same flag, as before this milestone.
		AllowTerminalSplit: false,
		ShowNotes:          m.notesWanted(),
		Providers:          m.configuredProviders(),
	})
	m.createOpen = true
	m.createError = ""
	m.createWarning = ""
	m.createBusy = false
	m.createPlan = nil
	m.createRecord = nil
	m.createModal = nil
	m.createModalWidth = 0
	return tea.Batch(m.loadCreateBranches(), m.loadCreatePickerData(), m.loadCreateFileCandidates())
}

func (m *Model) normalizedCreateProjectKey(explicit string) string {
	key := m.defaultCreateProject(explicit)
	if idx := m.projectIndex(key); idx >= 0 {
		return projectKey(m.projects[idx])
	}
	return key
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
	if m.createForm == nil {
		return Project{}, false
	}
	idx := m.projectIndex(m.createForm.ProjectKey())
	if idx < 0 {
		return Project{}, false
	}
	return m.projects[idx], true
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

func (m *Model) createModalContentWidth() int {
	modalW := 52
	if m.width > 0 && modalW > m.width-4 {
		modalW = m.width - 4
	}
	if modalW < 24 {
		modalW = 24
	}
	return modalW
}

func (m *Model) ensureCreateModal() {
	if !m.createOpen {
		return
	}
	modalW := m.createModalContentWidth()
	if m.createPlan != nil {
		if m.createModal != nil && m.createModalWidth == modalW {
			return
		}
		m.createModalWidth = modalW
		m.ensureCreatePlanModal(modalW)
		return
	}
	if m.createForm == nil {
		return
	}
	m.createForm.Build(modalW)
	if m.createError != "" {
		m.createForm.SetError(m.createError)
	}
}

func (m *Model) activeCreateModal() *modal.Modal {
	if m.createPlan != nil {
		return m.createModal
	}
	if m.createForm != nil {
		return m.createForm.Modal()
	}
	return m.createModal
}

func (m *Model) createProjectItems() []workspacecreate.ProjectItem {
	items := make([]workspacecreate.ProjectItem, 0, len(m.projects))
	for _, project := range m.projects {
		items = append(items, workspacecreate.ProjectItem{Key: projectKey(project), Label: project.Name})
	}
	return items
}

func (m *Model) setCreateError(msg string) {
	m.createError = msg
	if m.createForm != nil && m.createPlan == nil {
		m.createForm.SetError(msg)
	}
}

func (m *Model) loadCreateBranches() tea.Cmd {
	project, ok := m.selectedCreateProject()
	if !ok {
		return nil
	}
	key := projectKey(project)
	dir := project.Path
	return func() tea.Msg {
		branches, err := listCreateBranches(context.Background(), dir)
		if err != nil {
			branches = nil
		}
		current, _ := currentCreateBranch(context.Background(), dir)
		if current == "HEAD" {
			current = ""
		}
		return globalCreateBranchesMsg{ProjectKey: key, Branches: branches, Current: current}
	}
}

func (m *Model) applyCreateBranches(msg globalCreateBranchesMsg) {
	if m.createForm == nil || m.createForm.ProjectKey() != msg.ProjectKey {
		return
	}
	m.createForm.SetBranches(msg.Branches, msg.Current)
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
	md := m.activeCreateModal()
	if md == nil {
		return background
	}
	rendered := md.Render(width, height, m.createMouse)
	return ui.OverlayModal(background, rendered, width, height)
}

func (m *Model) closeCreateShell() {
	m.createOpen = false
	m.createBusy = false
	m.createError = ""
	m.createWarning = ""
	m.createForm = nil
	m.createModal = nil
	m.createModalWidth = 0
	m.createPlan = nil
	m.createRecord = nil
}

func (m *Model) CreatePaste(value string) bool {
	if !m.createOpen || m.createBusy || m.createPlan != nil || m.createForm == nil {
		return false
	}
	m.ensureCreateModal()
	md := m.createForm.Modal()
	if md == nil {
		return false
	}
	prev := md.FocusedID()
	md.SetFocus(workspacecreate.FieldName)
	for _, r := range value {
		_, _ = md.HandleKey(tea.KeyPressMsg{Code: r, Text: string(r)})
	}
	if prev != "" && prev != workspacecreate.FieldName {
		md.SetFocus(prev)
	}
	m.createForm.SyncAfterInput()
	m.setCreateError("")
	return true
}

func (m *Model) handleCreateShellKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	m.ensureCreateShellModal()
	if m.createBusy {
		return true, nil
	}
	if m.createPlan != nil {
		if m.createModal == nil {
			return true, nil
		}
		action, cmd := m.createModal.HandleKey(msg)
		return true, tea.Batch(cmd, m.applyCreateAction(action))
	}
	md := m.activeCreateModal()
	if md == nil {
		return true, nil
	}
	prevProject := ""
	if m.createForm != nil {
		prevProject = m.createForm.ProjectKey()
	}
	// The form owns the two-step flow: Esc on the picker step returns to the
	// kind list instead of closing, and Enter on a target-needing kind
	// advances to it. What escapes is an action for this switch.
	var action string
	var cmd tea.Cmd
	if m.createForm != nil {
		action, cmd = m.createForm.HandleKey(msg)
	} else {
		action, cmd = md.HandleKey(msg)
	}
	return true, tea.Batch(cmd, m.finishCreateInput(action, prevProject))
}

func (m *Model) handleCreateShellMouse(msg tea.MouseMsg) tea.Cmd {
	m.ensureCreateShellModal()
	if m.createBusy {
		return nil
	}
	if m.createPlan != nil {
		if m.createModal == nil {
			return nil
		}
		action := m.createModal.HandleMouse(msg, m.createMouse)
		return m.applyCreateAction(action)
	}
	md := m.activeCreateModal()
	if md == nil {
		return nil
	}
	prevProject := ""
	if m.createForm != nil {
		prevProject = m.createForm.ProjectKey()
	}
	action := md.HandleMouse(msg, m.createMouse)
	if action == workspacecreate.FieldKind {
		if click, ok := msg.(tea.MouseClickMsg); ok {
			// The form knows which shape its kind list is drawn in this
			// session and maps the click accordingly.
			for _, region := range m.createMouse.HitMap.Regions() {
				if region.ID != workspacecreate.FieldKind {
					continue
				}
				m.createForm.SetKindFromClick(region.Rect, click.X, click.Y)
				break
			}
		}
	}
	if action == workspacecreate.FieldSkip {
		_, _ = md.HandleKey(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	}
	if m.createForm != nil {
		action = m.createForm.TranslateMouseAction(action)
	}
	return m.finishCreateInput(action, prevProject)
}

func (m *Model) finishCreateInput(action, previousProject string) tea.Cmd {
	if m.createForm != nil {
		m.createForm.SyncAfterInput()
	}
	var reload tea.Cmd
	if m.createForm != nil && m.createForm.ProjectKey() != previousProject {
		reload = m.loadCreateBranches()
	}
	if action == "" {
		m.setCreateError("")
	}
	return tea.Batch(reload, m.applyCreateAction(action))
}

func (m *Model) applyCreateAction(action string) tea.Cmd {
	if workspacecreate.IsPlacementAction(action) {
		// On the picker step one click creates with that placement; from the
		// kind list of a target-needing kind it continues there instead.
		if m.createForm == nil {
			return nil
		}
		if m.createForm.ApplyPlacementActionStep(action) == workspacecreate.PlacementSubmitted {
			return m.applyCreateAction(workspacecreate.ActionCreate)
		}
		return nil
	}
	switch action {
	case "cancel", workspacecreate.ActionCancel, globalCreateCancelID:
		if m.createRecord != nil {
			// Once Git has mutated, escape/cancel means retain the usable
			// worktree; it must never silently abandon recovery state.
			return m.openCreatedWorktreeAnyway()
		}
		m.clearPendingCreated()
		m.closeCreateShell()
		return nil
	case workspacecreate.ActionCreate:
		if m.createForm == nil {
			return nil
		}
		if m.createForm.Step() == workspacecreate.StepTarget {
			return m.submitPaneTargetForm()
		}
		if m.createForm.Kind() == workspacecreate.KindWorktree {
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
		m.setCreateError("Choose a project")
		return nil
	}
	if m.createForm == nil {
		return nil
	}
	if err := m.createForm.Validate(); err != "" {
		m.setCreateError(err)
		return nil
	}
	setup := config.WorktreeSetupConfig{}
	dirPrefix := true
	if m.config != nil {
		setup = m.config.WorktreeSetupForProject(project.Path)
		dirPrefix = m.config.Plugins.Workspace.DirPrefix
	}
	name := strings.TrimSpace(m.createForm.Name())
	base := m.createForm.BaseBranch()
	agent := m.createForm.Agent()
	skip := m.createForm.SkipPerms()
	m.createBusy = true
	m.setCreateError("")
	m.createModal = nil
	_ = saveLastGlobalCreateProject(project.Path)
	m.createForm.PersistLastAgent()
	return func() tea.Msg {
		plan, err := resolveGlobalWorktree(context.Background(), project.Path, project.Path, name, base, dirPrefix, setup)
		if plan != nil {
			plan.RepoKey = projectKey(project)
			plan.OperationID = fmt.Sprintf("global-%d", time.Now().UnixNano())
			plan.AgentType = agent
			plan.SkipPerms = skip
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
		m.setCreateError(m.createError)
		return nil
	}
	return m.launchCreatedWorktree(project, m.createPlan, m.createRecord)
}

func (m *Model) launchCreatedWorktree(project Project, plan *workspaceops.WorktreePlan, record *workspaceops.WorktreeRecord) tea.Cmd {
	if plan == nil || record == nil {
		return nil
	}
	configured := map[string]string(nil)
	if m.config != nil {
		configured = m.config.Plugins.Workspace.AgentStart
	}
	startAgent := plan.AgentType != ""
	command := ""
	if startAgent {
		command = resolveGlobalAgentCmd(record.Path, plan.AgentType, configured, plan.SkipPerms)
	}
	spec := workspaceops.AgentLaunchSpec{
		SessionName: workspaceops.WorktreeSessionName(record.Path, record.Name), WorkDir: record.Path,
		AgentCommand: command, TaskID: plan.TaskID, Env: workspaceops.BuildEnvOverrides(plan.MainWorktree),
		StartAgent: startAgent,
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
		m.setCreateError("Choose a project")
		return nil
	}
	key := projectKey(project)
	display, session := workspaceops.ShellNames(project.Path, m.shellDefinitions(key))
	custom := ""
	if m.createForm != nil {
		custom = strings.TrimSpace(m.createForm.Name())
	}
	if custom != "" {
		var err error
		display, err = shellstate.NormalizeName(custom)
		if err != nil {
			m.setCreateError(err.Error())
			return nil
		}
	}
	cols, rows := max(20, m.width/2-4), max(5, m.height-4)
	agent := ""
	skip := false
	if m.createForm != nil {
		agent = m.createForm.Agent()
		skip = m.createForm.SkipPerms()
	}
	spec := workspaceops.ManagedShellSpec{
		ShellSpec:   workspaceops.ShellSpec{WorkDir: project.Path, SessionName: session, DisplayName: display, Cols: cols, Rows: rows},
		ProjectRoot: project.Path,
		AgentType:   agent,
		SkipPerms:   skip,
	}
	m.createBusy = true
	m.setCreateError("")
	m.createModal = nil
	m.pendingCreatedTmux = session
	m.pendingCreatedPath = ""
	_ = saveLastGlobalCreateProject(project.Path)
	if m.createForm != nil {
		m.createForm.PersistLastAgent()
	}
	return func() tea.Msg {
		_, err := createManagedShell(spec)
		if err == nil && agent != "" {
			configured := map[string]string(nil)
			if m.config != nil {
				configured = m.config.Plugins.Workspace.AgentStart
			}
			command := resolveGlobalAgentCmd(project.Path, agent, configured, skip)
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
	return m.refreshOneProject(project, false)
}

// refreshOneProject re-inventories exactly one project and folds the result
// back into the board. This is the cheap path — one Git worktree listing and
// one tmux inventory — as opposed to a full cycle's fan-out across every
// configured project, and it is what both a local mutation and a cross-instance
// manifest change use.
func (m *Model) refreshOneProject(project Project, background bool) tea.Cmd {
	return m.refreshOneProjectWithPanes(project, background, nil)
}

// refreshOneProjectWithPanes is refreshOneProject with the tmux inventory
// supplied. A nil panes slice means "collect a fresh one", which is what a
// mutation needs: a shell created a moment ago is not in any inventory taken
// before it. A caller holding panes from a just-completed cycle passes them
// instead, saving a subprocess spawn per project.
func (m *Model) refreshOneProjectWithPanes(project Project, background bool, panes []workspaceinventory.Pane) tea.Cmd {
	collector := m.collector.ForRefresh(maxCaptures, m.shellClaims)
	roots := append([]string(nil), m.roots...)
	// Snapshot the other projects' results here, on the update goroutine. The
	// command below runs on its own, and ranging over m.results from there would
	// race every write the next cycle makes.
	key := projectKey(project)
	dispatchedAt := time.Now()
	others := make([]workspaceinventory.ProjectResult, 0, len(m.results))
	for existingKey, existing := range m.results {
		if existingKey != key {
			others = append(others, existing)
		}
	}
	return func() tea.Msg {
		ctx := context.Background()
		inventory := collector.CollectProjectInventory(ctx, project.Name, project.Path)
		if inventory.Err != nil {
			return projectMutationRefreshMsg{Project: project, Result: inventory, Err: inventory.Err, Background: background, DispatchedAt: dispatchedAt}
		}
		claimsInputs := append(others, inventory)
		collector = collector.WithShellClaims(workspaceinventory.BuildShellClaims(claimsInputs))
		if panes == nil {
			collected, err := collector.ListPanes(ctx)
			if err != nil {
				return projectMutationRefreshMsg{Project: project, Result: inventory, Err: err, Background: background, DispatchedAt: dispatchedAt}
			}
			panes = collected
		}
		result := collector.RefreshProjectStatus(ctx, inventory, roots, panes)
		return projectMutationRefreshMsg{Project: project, Result: withProjectIdentity(result, project), Err: result.Err, Background: background, DispatchedAt: dispatchedAt}
	}
}

func (m *Model) applyProjectMutationRefresh(msg projectMutationRefreshMsg) tea.Cmd {
	if msg.Err != nil && msg.Background {
		// A background refresh that failed leaves the last good cards alone. The
		// next sweep tick retries, and the full cycle behind it still reports a
		// project that has genuinely gone away.
		m.tracef("background refresh project=%s failed: %v", projectKey(msg.Project), msg.Err)
		return nil
	}
	if msg.Err != nil {
		m.createOpen = true
		m.createBusy = false
		m.createModal = nil
		m.setCreateError(msg.Err.Error())
		return nil
	}
	key := projectKey(msg.Project)
	// A background result replaces the whole project, so one that has been
	// overtaken does not merely show stale data — it removes workspaces a newer
	// read had already found. That does not heal: the live-only poll re-observes
	// the membership it is given and never re-reads durable state, and the
	// manifest digest already matches, so the watcher will not fire again. The
	// project would stay wrong until its next sweep rotation, which is minutes
	// on a large set. Dropping the superseded result is the whole fix.
	//
	// Dated rather than generation-fenced on purpose: m.generation advances on
	// every poll, so fencing on it would discard the watcher refreshes this
	// feature exists to deliver.
	if msg.Background && msg.DispatchedAt.Before(m.inventoryStamp[key]) {
		m.tracef("background refresh project=%s superseded — dropping", key)
		return nil
	}
	m.results[key] = msg.Result
	m.markInventoryFresh(key)
	delete(m.projectErrors, key)
	// The live-only poll keys shell liveness off this map. Without a rebuild
	// here the next pass treats the shell we just created as unclaimed and
	// paints it dead (td-ecb0b8).
	m.syncShellClaims()
	m.syncBoard()
	return m.previewSync()
}

func (m *Model) clearPendingCreated() {
	m.pendingCreatedTmux = ""
	m.pendingCreatedPath = ""
}

// honorPendingCreated selects a still-pending created workspace once it is
// present in results and visible. Pending stays set until that happens.
func (m *Model) honorPendingCreated() bool {
	if m.pendingCreatedTmux == "" && m.pendingCreatedPath == "" {
		return false
	}
	for _, result := range m.results {
		for _, workspace := range result.Workspaces {
			createdShell := m.pendingCreatedTmux != "" && workspace.Kind == workspaceinventory.KindShell && workspace.TmuxName == m.pendingCreatedTmux
			createdWorktree := m.pendingCreatedPath != "" && workspace.Kind == workspaceinventory.KindWorktree && workspace.Path == m.pendingCreatedPath
			if !createdShell && !createdWorktree {
				continue
			}
			if m.workspaces.SelectID(workspace.ID) {
				m.clearPendingCreated()
				return true
			}
			return false
		}
	}
	return false
}

func (m *Model) createWheelAtBoundary(msg tea.MouseWheelMsg) bool {
	if !m.createOpen {
		return false
	}
	md := m.activeCreateModal()
	if md == nil {
		return false
	}
	return md.WheelAtBoundary(msg, m.createMouse)
}

func createProjectKeyFromAction(id string) string {
	return strings.TrimPrefix(id, globalCreateActionID+":")
}
