package workspace

import (
	"fmt"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
)

const (
	createNameFieldID       = "create-name"
	createBaseFieldID       = "create-base"
	createTaskFieldID       = "create-task"
	createAgentFieldID      = "create-agent"
	createSkipPermissionsID = "create-skip-permissions"
	createSubmitID          = "create-submit"
	createCancelID          = "create-cancel"
	createTaskNoneID        = "create-task-none"
	createConfirmID         = "create-confirm"
	createRetrySetupID      = "create-retry-setup"
	createOpenAnywayID      = "create-open-anyway"
	createDeleteCreatedID   = "create-delete-created"
	createCopyEnvID         = "create-copy-env"
	createRunHookID         = "create-run-hook"
	createDismissID         = "create-dismiss"
)

func (p *Plugin) ensureCreateOperationModal() {
	if p.createPlan == nil && p.createBusyStep == "" {
		return
	}
	modalW := 72
	if maxW := p.width - 4; modalW > maxW {
		modalW = maxW
	}
	if modalW < 20 {
		modalW = 20
	}
	if p.createOperationModal != nil && p.createOperationWidth == modalW {
		return
	}
	p.createOperationWidth = modalW

	if p.createBusyStep != "" {
		p.createOperationModal = modal.New("Creating Worktree", modal.WithWidth(modalW), modal.WithHints(false), modal.WithCloseOnBackdropClick(false)).
			AddSection(modal.Text("Current step: " + p.createBusyStep)).
			AddSection(modal.Text("This operation cannot be cancelled after submission."))
		return
	}
	if p.createDeleteResult != nil && p.createDeleteResult.WorktreeRemoved {
		lines := []string{"The worktree directory was removed."}
		if p.createDeleteResult.BranchRetained {
			lines = append(lines, "Branch retained: "+p.createPlan.Branch, "Its identity changed, so Sidecar did not delete it.", "Inspect or delete the branch manually when safe.")
		}
		if p.createDeleteResult.Err != nil {
			lines = append(lines, "", p.createDeleteResult.Err.Error())
		}
		p.createOperationModal = modal.New("Worktree Removed", modal.WithWidth(modalW), modal.WithVariant(modal.VariantWarning), modal.WithHints(false), modal.WithCloseOnBackdropClick(false)).
			AddSection(modal.Text(strings.Join(lines, "\n"))).AddSection(modal.Spacer()).
			AddSection(modal.Buttons(modal.Btn(" Dismiss ", createDismissID, modal.BtnPrimary())))
		return
	}
	if p.createSetupResult != nil {
		lines := []string{"The worktree was created and kept:", "Path: " + p.createPlan.Path, "HEAD: " + shortOID(p.createSetupResult.Worktree.HEADOID), "", "Setup warnings:"}
		for _, outcome := range p.createSetupResult.Warnings() {
			label := "warning"
			if outcome.Required {
				label = "required"
			}
			lines = append(lines, fmt.Sprintf("- %s (%s): %v", outcome.Action, label, outcome.Err))
		}
		p.createOperationModal = modal.New("Setup Needs Attention", modal.WithWidth(modalW), modal.WithVariant(modal.VariantWarning), modal.WithHints(false), modal.WithCloseOnBackdropClick(false)).
			AddSection(modal.Text(strings.Join(lines, "\n"))).
			AddSection(modal.Spacer()).
			AddSection(modal.Buttons(
				modal.Btn(" Retry Setup ", createRetrySetupID, modal.BtnPrimary()),
				modal.Btn(" Open Anyway ", createOpenAnywayID),
				modal.Btn(" Delete Newly Created ", createDeleteCreatedID, modal.BtnDanger()),
			))
		return
	}
	plan := p.createPlan
	lines := []string{
		"Source: " + plan.SourceRef,
		"Source OID: " + plan.SourceOID,
		"Source worktree: " + plan.SourceWorktree,
		"Destination: " + plan.Path,
		"Branch: " + plan.Branch,
		"Remote: " + plan.RemotePolicy,
	}
	if plan.TaskID == "" {
		lines = append(lines, "Task: none")
	} else {
		lines = append(lines, "Task: "+plan.TaskID+"  "+plan.TaskTitle)
	}
	p.createOperationModal = modal.New("Confirm Worktree Creation", modal.WithWidth(modalW), modal.WithPrimaryAction(createConfirmID), modal.WithHints(false)).
		AddSection(modal.Text(strings.Join(lines, "\n"))).
		AddSection(modal.Spacer())
	if len(plan.EnvFiles) > 0 {
		p.createOperationModal.AddSection(modal.Checkbox(createCopyEnvID, "Copy env files: "+strings.Join(plan.EnvFiles, ", "), &p.createCopyEnv))
	}
	if plan.RunHook {
		required := "optional"
		if plan.HookRequired {
			required = "required"
		}
		p.createOperationModal.AddSection(modal.Checkbox(createRunHookID, "Run "+plan.HookPath+" ("+required+")", &p.createRunHook))
	}
	p.createOperationModal.AddSection(modal.Spacer()).AddSection(modal.Buttons(
		modal.Btn(" Create ", createConfirmID, modal.BtnPrimary()),
		modal.Btn(" Back ", createCancelID),
	))
}

func createIndexedID(prefix string, idx int) string {
	return fmt.Sprintf("%s%d", prefix, idx)
}

func parseIndexedID(prefix, id string) (int, bool) {
	if !strings.HasPrefix(id, prefix) {
		return 0, false
	}
	idx, err := strconv.Atoi(strings.TrimPrefix(id, prefix))
	if err != nil {
		return 0, false
	}
	return idx, true
}

func (p *Plugin) ensureCreateModal() {
	modalW := 70
	maxW := p.width - 4
	if maxW < 1 {
		maxW = 1
	}
	if modalW > maxW {
		modalW = maxW
	}

	branchN, taskN := len(p.branchAll), len(p.taskSearchAll)
	if p.createModal != nil && p.createModalWidth == modalW && p.createModalBranchN == branchN && p.createModalTaskN == taskN {
		return
	}

	prevFocus := ""
	if p.createModal != nil {
		prevFocus = p.createModal.FocusedID()
	}

	p.createModalWidth = modalW
	p.createModalBranchN = branchN
	p.createModalTaskN = taskN
	p.syncCreateAgentFromIdx()
	p.prefillCreateAgentInput()

	branchItems := p.createBranchItems()
	taskItems := p.createTaskItems()
	agentItems := p.createAgentItems()

	p.createModal = modal.New("Create New Worktree",
		modal.WithWidth(modalW),
		modal.WithPrimaryAction(createSubmitID),
		modal.WithHints(false),
	).
		AddSection(modal.InputWithLabel(createNameFieldID, "Name", &p.createNameInput, modal.WithSubmitOnEnter(true))).
		AddSection(p.createSlugHintSection()).
		AddSection(modal.Text("Base Branch")).
		AddSection(modal.Combo(createBaseFieldID, &p.createBaseBranchInput, branchItems, &p.createBaseIdx,
			modal.WithComboFilter(comboExactOrAllFilter(branchItems)))).
		AddSection(modal.Text("Link Task")).
		AddSection(modal.Combo(createTaskFieldID, &p.taskSearchInput, taskItems, &p.createTaskIdx)).
		AddSection(modal.Text("Agent")).
		AddSection(modal.Combo(createAgentFieldID, &p.createAgentInput, agentItems, &p.createAgentIdx,
			modal.WithComboFilter(comboExactOrAllFilter(agentItems)))).
		AddSection(modal.When(p.shouldShowSkipPermissions, modal.Checkbox(createSkipPermissionsID, "Auto-approve all actions", &p.createSkipPermissions))).
		AddSection(p.createSkipPermissionsHintSection()).
		AddSection(p.createErrorSection()).
		AddSection(modal.Buttons(
			modal.Btn(" Create ", createSubmitID),
			modal.Btn(" Cancel ", createCancelID),
		))

	if prevFocus != "" {
		p.createModal.SetFocus(prevFocus)
	}
}

func (p *Plugin) createBranchItems() []modal.DropdownItem {
	items := make([]modal.DropdownItem, len(p.branchAll))
	for i, branch := range p.branchAll {
		items[i] = modal.DropdownItem{ID: branch, Label: branch, Value: branch}
	}
	return items
}

func (p *Plugin) createTaskItems() []modal.DropdownItem {
	items := make([]modal.DropdownItem, 0, len(p.taskSearchAll)+1)
	items = append(items, modal.DropdownItem{ID: createTaskNoneID, Label: "(none)", Value: ""})
	for _, task := range p.taskSearchAll {
		items = append(items, modal.DropdownItem{
			ID:    task.ID,
			Label: task.ID + "  " + task.Title,
			Value: task.ID,
			Desc:  task.Title,
			Data:  task,
		})
	}
	return items
}

func (p *Plugin) createAgentItems() []modal.DropdownItem {
	types := p.selectableAgentTypes()
	items := make([]modal.DropdownItem, len(types))
	for i, at := range types {
		label := AgentDisplayNames[at]
		if label == "" {
			label = string(at)
		}
		items[i] = modal.DropdownItem{
			ID:    string(at),
			Label: label,
			Value: label,
			Data:  at,
		}
	}
	return items
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

func (p *Plugin) createSlugHintSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		display := strings.TrimSpace(p.createNameInput.Value())
		slug := SlugifyWorktreeName(display)
		if slug == "" || slug == display {
			return modal.RenderedSection{}
		}
		return modal.RenderedSection{Content: dimText("git: " + slug)}
	}, nil)
}

func (p *Plugin) createSkipPermissionsHintSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.createAgentType == AgentNone {
			return modal.RenderedSection{}
		}
		if p.shouldShowSkipPermissions() {
			flag := SkipPermissionsFlags[p.createAgentType]
			return modal.RenderedSection{Content: dimText(fmt.Sprintf("      (Adds %s)", flag))}
		}
		return modal.RenderedSection{Content: dimText("  Skip permissions not available for this agent")}
	}, nil)
}

func (p *Plugin) createErrorSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.createError == "" {
			return modal.RenderedSection{}
		}
		errStyle := lipgloss.NewStyle().Foreground(styles.Error)
		return modal.RenderedSection{Content: errStyle.Render("Error: " + p.createError)}
	}, nil)
}

func (p *Plugin) prefillCreateBaseBranch() {
	if strings.TrimSpace(p.createBaseBranchInput.Value()) != "" {
		p.syncCreateBaseIdx()
		return
	}
	if p.ctx == nil || p.ctx.WorkDir == "" {
		return
	}
	current, err := getCurrentBranch(p.ctx.WorkDir)
	if err != nil || current == "" || current == "HEAD" {
		return
	}
	p.createBaseBranchInput.SetValue(current)
	p.syncCreateBaseIdx()
}

func (p *Plugin) syncCreateBaseIdx() {
	val := p.createBaseBranchInput.Value()
	for i, branch := range p.branchAll {
		if branch == val {
			p.createBaseIdx = i
			return
		}
	}
}

func (p *Plugin) prefillCreateAgentInput() {
	label := AgentDisplayNames[p.createAgentType]
	if label == "" {
		label = string(p.createAgentType)
	}
	if p.createAgentInput.Value() != label {
		p.createAgentInput.SetValue(label)
	}
}

func (p *Plugin) syncCreateAgentFromIdx() {
	agents := p.selectableAgentTypes()
	prev := p.createAgentType
	p.createAgentType, p.createAgentIdx = clampAgentSelection(agents, p.createAgentType, p.createAgentIdx)
	if p.createAgentType != prev {
		p.loadCreateAutoApprove()
		p.prefillCreateAgentInput()
	}
}

func (p *Plugin) loadCreateAutoApprove() {
	p.createSkipPermissions = state.GetAgentAutoApprove(string(p.createAgentType))
}

func (p *Plugin) persistCreateAutoApprove() {
	if p.createAgentType == "" {
		return
	}
	_ = state.SetAgentAutoApprove(string(p.createAgentType), p.createSkipPermissions)
}

func (p *Plugin) syncCreateTaskFromCombo() {
	val := strings.TrimSpace(p.taskSearchInput.Value())
	if val == "" {
		p.createTaskID = ""
		p.createTaskTitle = ""
		return
	}
	items := p.createTaskItems()
	if p.createTaskIdx >= 0 && p.createTaskIdx < len(items) {
		item := items[p.createTaskIdx]
		if t, ok := item.Data.(Task); ok && (val == item.Value || val == item.Label || val == t.ID) {
			p.createTaskID = t.ID
			p.createTaskTitle = t.Title
			return
		}
	}
	for i, t := range p.taskSearchAll {
		if t.ID == val {
			p.createTaskID = t.ID
			p.createTaskTitle = t.Title
			p.createTaskIdx = i + 1
			return
		}
	}
	p.createTaskID = ""
	p.createTaskTitle = ""
}

func (p *Plugin) rematchCreateTaskIdx() {
	if p.createTaskID == "" {
		p.createTaskIdx = 0
		return
	}
	for i, t := range p.taskSearchAll {
		if t.ID == p.createTaskID {
			p.createTaskIdx = i + 1
			p.taskSearchInput.SetValue(t.ID)
			return
		}
	}
}

func (p *Plugin) clearCreateTaskSelection() {
	p.createTaskID = ""
	p.createTaskTitle = ""
	p.createTaskIdx = 0
	p.taskSearchInput.SetValue("")
}

func (p *Plugin) applyCreateModalAfterInput(prevAgent AgentType, prevSkip bool) {
	p.syncCreateAgentFromIdx()
	p.syncCreateTaskFromCombo()
	if p.createAgentType != prevAgent {
		p.loadCreateAutoApprove()
		return
	}
	if p.createSkipPermissions != prevSkip {
		p.persistCreateAutoApprove()
	}
}
