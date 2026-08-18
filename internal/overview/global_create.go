package overview

import (
	"context"
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"github.com/marcus/sidecar/internal/workspaceops"
)

const (
	globalCreateProjectID = "global-create-project"
	globalCreateKindID    = "global-create-kind"
	globalCreateAgentID   = "global-create-agent"
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
	// Background marks a refresh nobody asked for — a manifest watcher signal or
	// a sweep tick rather than a create or delete this surface just performed.
	// Its failures must stay silent: raising the create modal's error on a
	// project the user never touched would be an alert about nothing.
	Background bool
	// DispatchedAt dates the durable state this result was built from, so a
	// background refresh that has been overtaken can be recognised and dropped.
	DispatchedAt time.Time
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
	m.createProjectInput = textinput.New()
	m.createProjectInput.Prompt = ""
	m.createProjectInput.CharLimit = 80
	m.createAgentInput = textinput.New()
	m.createAgentInput.Prompt = ""
	m.createAgentInput.CharLimit = 80
	m.createAgentType = m.defaultCreateAgent()
	m.rematchCreateAgentIndex()
	m.prefillCreateProjectInput()
	m.prefillCreateAgentInput()
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
	prevFocus := ""
	existed := m.createModal != nil
	if existed {
		prevFocus = m.createModal.FocusedID()
	}
	if m.createModal != nil && m.createModalWidth == modalW {
		return
	}
	m.buildCreateModal(modalW, prevFocus)
	if existed && prevFocus != "" && m.createModal != nil && m.createPlan == nil {
		w, h := m.createRenderSize()
		_ = m.createModal.Render(w, h, m.createMouse)
		m.createModal.SetFocus(prevFocus)
	}
}

func (m *Model) createRenderSize() (int, int) {
	w, h := m.width, m.height
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	return w, h
}

func (m *Model) rebuildCreateChooser() {
	if !m.createOpen || m.createPlan != nil {
		return
	}
	prevFocus := ""
	if m.createModal != nil {
		prevFocus = m.createModal.FocusedID()
	}
	m.createModal = nil
	m.createModalWidth = 0
	m.buildCreateModal(m.createModalContentWidth(), prevFocus)
	if m.createModal == nil {
		return
	}
	w, h := m.createRenderSize()
	_ = m.createModal.Render(w, h, m.createMouse)
	if prevFocus != "" {
		m.createModal.SetFocus(prevFocus)
	}
}

func (m *Model) buildCreateModal(modalW int, prevFocus string) {
	m.createModalWidth = modalW
	if m.createPlan != nil {
		m.ensureCreatePlanModal(modalW)
		return
	}
	if prevFocus != globalCreateProjectID {
		m.prefillCreateProjectInput()
	}
	if prevFocus != globalCreateAgentID {
		m.prefillCreateAgentInput()
	}
	projectItems := m.createProjectItems()
	agentItems := m.createAgentItems()
	sections := []modal.Section{
		createKindToggle(globalCreateKindID, &m.createKindIndex),
		modal.Spacer(),
		modal.Text("Project"),
		modal.Combo(globalCreateProjectID, &m.createProjectInput, projectItems, &m.createProjectIndex,
			modal.WithComboFilter(comboExactOrAllFilter(projectItems))),
		modal.Text("Agent"),
		modal.Combo(globalCreateAgentID, &m.createAgentInput, agentItems, &m.createAgentIndex,
			modal.WithComboFilter(comboExactOrAllFilter(agentItems))),
		modal.InputWithLabel(globalCreateNameID, "Name", &m.createNameInput),
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

func (m *Model) createProjectItems() []modal.DropdownItem {
	items := make([]modal.DropdownItem, 0, len(m.projects))
	for _, project := range m.projects {
		key := projectKey(project)
		items = append(items, modal.DropdownItem{ID: "project:" + key, Label: project.Name, Value: project.Name, Data: key})
	}
	return items
}

func (m *Model) createAgentTypes() []string {
	configured := []string(nil)
	if m.config != nil {
		configured = m.config.Plugins.Workspace.Agents
	}
	return resolveCreateAgents(configured, m.createKindIndex != globalCreateWorktree)
}

func (m *Model) createAgentItems() []modal.DropdownItem {
	types := m.createAgentTypes()
	items := make([]modal.DropdownItem, len(types))
	for i, at := range types {
		label := createAgentLabel(at)
		items[i] = modal.DropdownItem{ID: "agent:" + at, Label: label, Value: label, Data: at}
	}
	return items
}

func (m *Model) defaultCreateAgent() string {
	agents := m.createAgentTypes()
	if last := strings.TrimSpace(loadLastCreateAgent()); last != "" && indexOfCreateAgent(agents, last) >= 0 {
		return last
	}
	cfgDefault := ""
	if m.config != nil {
		cfgDefault = strings.TrimSpace(m.config.Plugins.Workspace.DefaultAgentType)
	}
	if cfgDefault != "" && indexOfCreateAgent(agents, cfgDefault) >= 0 {
		return cfgDefault
	}
	if m.createKindIndex == globalCreateWorktree {
		for _, at := range agents {
			if at != "" {
				return at
			}
		}
	}
	return ""
}

func (m *Model) rematchCreateAgentIndex() {
	agents := m.createAgentTypes()
	idx := indexOfCreateAgent(agents, m.createAgentType)
	if idx < 0 {
		m.createAgentType = m.defaultCreateAgent()
		idx = indexOfCreateAgent(agents, m.createAgentType)
	}
	if idx < 0 {
		idx = 0
		if len(agents) > 0 {
			m.createAgentType = agents[0]
		}
	}
	m.createAgentIndex = idx
}

func (m *Model) syncCreateAgentFromIdx() {
	agents := m.createAgentTypes()
	if m.createAgentIndex >= 0 && m.createAgentIndex < len(agents) {
		m.createAgentType = agents[m.createAgentIndex]
	}
}

func (m *Model) selectedCreateAgent() string {
	m.syncCreateAgentFromIdx()
	return strings.TrimSpace(m.createAgentType)
}

func (m *Model) prefillCreateProjectInput() {
	label := ""
	if project, ok := m.selectedCreateProject(); ok {
		label = project.Name
	}
	if m.createProjectInput.Value() != label {
		m.createProjectInput.SetValue(label)
	}
}

func (m *Model) prefillCreateAgentInput() {
	label := createAgentLabel(m.createAgentType)
	if m.createAgentInput.Value() != label {
		m.createAgentInput.SetValue(label)
	}
}

func comboExactOrAllFilter(items []modal.DropdownItem) modal.ComboFilterFunc {
	return func(query string, item modal.DropdownItem) bool {
		if query == "" || comboQueryMatchesItemExactly(query, items) {
			return true
		}
		q := strings.ToLower(query)
		if strings.Contains(strings.ToLower(item.Label), q) {
			return true
		}
		if item.Value != "" && strings.Contains(strings.ToLower(item.Value), q) {
			return true
		}
		if item.Desc != "" && strings.Contains(strings.ToLower(item.Desc), q) {
			return true
		}
		return false
	}
}

func comboQueryMatchesItemExactly(query string, items []modal.DropdownItem) bool {
	for _, it := range items {
		if query == it.Value || query == it.Label {
			return true
		}
	}
	return false
}

func createKindToggle(id string, selected *int) modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		sel := 0
		if selected != nil {
			sel = *selected
		}
		focused := focusID == id
		shellStyle, treeStyle := styles.Button, styles.Button
		if focused {
			if sel == globalCreateWorktree {
				treeStyle = styles.ButtonFocused
			} else {
				shellStyle = styles.ButtonFocused
			}
		} else if sel == globalCreateWorktree {
			treeStyle = styles.ButtonHover
		} else {
			shellStyle = styles.ButtonHover
		}
		shell := shellStyle.Render(" Shell ")
		sep := styles.Muted.Render(" | ")
		tree := treeStyle.Render(" Worktree ")
		content := lipgloss.JoinHorizontal(lipgloss.Top, shell, sep, tree)
		if ansi.StringWidth(content) > contentWidth && contentWidth > 0 {
			content = ansi.Truncate(content, contentWidth, "…")
		}
		return modal.RenderedSection{
			Content: content,
			Focusables: []modal.FocusableInfo{{
				ID: id, OffsetX: 0, OffsetY: 0,
				Width:  ansi.StringWidth(content),
				Height: 1,
			}},
		}
	}, func(msg tea.Msg, focusID string) (string, tea.Cmd) {
		if focusID != id || selected == nil {
			return "", nil
		}
		key, ok := msg.(tea.KeyPressMsg)
		if !ok {
			return "", nil
		}
		switch key.String() {
		case "left", "h", "k":
			*selected = globalCreateShell
		case "right", "l", "j":
			*selected = globalCreateWorktree
		}
		return "", nil
	})
}

func (m *Model) setCreateKindFromClick(x int) {
	if m.createMouse == nil {
		return
	}
	for _, region := range m.createMouse.HitMap.Regions() {
		if region.ID != globalCreateKindID {
			continue
		}
		if x >= region.Rect.X+region.Rect.W/2 {
			m.createKindIndex = globalCreateWorktree
		} else {
			m.createKindIndex = globalCreateShell
		}
		return
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
	if action == globalCreateKindID {
		if click, ok := msg.(tea.MouseClickMsg); ok {
			m.setCreateKindFromClick(click.X)
		}
	}
	return m.applyCreateAction(action, before, beforeKind)
}

func (m *Model) applyCreateAction(action string, previousProject, previousKind int) tea.Cmd {
	// Kind reorder moves None between the ends of the agent list. Syncing the
	// old index against the new order would rewrite createAgentType (None→claude,
	// claude→codex). Rematch the chosen type onto the new order instead.
	if m.createKindIndex != previousKind {
		m.rematchCreateAgentIndex()
	} else {
		m.syncCreateAgentFromIdx()
	}
	if m.createProjectIndex != previousProject || m.createKindIndex != previousKind {
		if project, ok := m.selectedCreateProject(); ok {
			m.createProjectKey = projectKey(project)
		}
		m.updateCreatePlaceholder()
		m.rebuildCreateChooser()
	}
	switch action {
	case "cancel", globalCreateCancelID:
		if m.createRecord != nil {
			// Once Git has mutated, escape/cancel means retain the usable
			// worktree; it must never silently abandon recovery state.
			return m.openCreatedWorktreeAnyway()
		}
		m.clearPendingCreated()
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
	if m.config != nil {
		setup = m.config.WorktreeSetupForProject(project.Path)
		dirPrefix = m.config.Plugins.Workspace.DirPrefix
	}
	agent := m.selectedCreateAgent()
	m.createBusy = true
	m.createError = ""
	m.createModal = nil
	_ = saveLastGlobalCreateProject(project.Path)
	_ = saveLastCreateAgent(agent)
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
	configured := map[string]string(nil)
	if m.config != nil {
		configured = m.config.Plugins.Workspace.AgentStart
	}
	startAgent := plan.AgentType != ""
	command := ""
	if startAgent {
		command = workspaceops.ResolveAgentCommand(record.Path, plan.AgentType, configured, plan.SkipPerms)
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
	agent := m.selectedCreateAgent()
	spec := workspaceops.ManagedShellSpec{ShellSpec: workspaceops.ShellSpec{WorkDir: project.Path, SessionName: session, DisplayName: display, Cols: cols, Rows: rows}, ProjectRoot: project.Path, AgentType: agent}
	m.createBusy = true
	m.createError = ""
	m.createModal = nil
	m.pendingCreatedTmux = session
	m.pendingCreatedPath = ""
	_ = saveLastGlobalCreateProject(project.Path)
	_ = saveLastCreateAgent(agent)
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
		m.createError = msg.Err.Error()
		m.createOpen = true
		m.createBusy = false
		m.createModal = nil
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
	if !m.createOpen || m.createModal == nil {
		return false
	}
	return m.createModal.WheelAtBoundary(msg, m.createMouse)
}

func createProjectKeyFromAction(id string) string {
	return strings.TrimPrefix(id, globalCreateActionID+":")
}
