package workspace

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/workspacecreate"
)

const (
	createNameFieldID       = workspacecreate.FieldName
	createBaseFieldID       = workspacecreate.FieldBase
	createAgentFieldID      = workspacecreate.FieldAgent
	createSkipPermissionsID = workspacecreate.FieldSkip
	createSubmitID          = workspacecreate.ActionCreate
	createCancelID          = workspacecreate.ActionCancel
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
	if p.createForm == nil {
		return
	}
	modalW := 70
	maxW := p.width - 4
	if maxW < 1 {
		maxW = 1
	}
	if modalW > maxW {
		modalW = maxW
	}
	p.createForm.Build(modalW)
	if p.createError != "" {
		p.createForm.SetError(p.createError)
	}
}

func (p *Plugin) createFormModal() *modal.Modal {
	if p.createForm == nil {
		return nil
	}
	return p.createForm.Modal()
}

func (p *Plugin) setCreateError(msg string) {
	p.createError = msg
	if p.createForm != nil {
		p.createForm.SetError(msg)
	}
}

func (p *Plugin) createFormValues() (name, base string, agent AgentType, skip bool) {
	if p.createForm == nil {
		return "", "", AgentNone, false
	}
	return p.createForm.Name(), p.createForm.BaseBranch(), AgentType(p.createForm.Agent()), p.createForm.SkipPerms()
}

func (p *Plugin) persistCreateLastAgent() {
	if p.createForm != nil {
		p.createForm.PersistLastAgent()
	}
}

func (p *Plugin) setCreateKindFromClick(x int) {
	if p.createForm == nil || p.mouseHandler == nil {
		return
	}
	for _, region := range p.mouseHandler.HitMap.Regions() {
		if region.ID != workspacecreate.FieldKind {
			continue
		}
		p.createForm.SetKindFromClickX(x, region.Rect.X, region.Rect.W)
		return
	}
}

// createFormPlacementAction records a placement button click and creates
// immediately: one click is the whole gesture, no second confirmation.
func (p *Plugin) createFormPlacementAction(action string) tea.Cmd {
	if p.createForm == nil || !p.createForm.ApplyPlacementAction(action) {
		return nil
	}
	return p.submitCreateForm()
}

func (p *Plugin) submitCreateForm() tea.Cmd {
	if p.createForm == nil {
		return nil
	}
	if p.createForm.Kind() == workspacecreate.KindTerminalSplit {
		name, placement := p.createForm.TerminalName(), p.createForm.PlacementSplit()
		p.viewMode = ViewModeList
		p.clearCreateModal()
		return p.createTerminalSplit(name, placement)
	}
	if p.createForm.Kind() == workspacecreate.KindShell {
		p.createForm.PersistLastAgent()
		name, _, agent, skip := p.createFormValues()
		p.viewMode = ViewModeList
		cmd := p.createShell(shellCreateOpts{CustomName: name, AgentType: agent, SkipPerms: skip})
		p.clearCreateModal()
		return cmd
	}
	return p.validateAndCreateWorktree()
}

func (p *Plugin) syncCreateFormAfterInput() {
	if p.createForm != nil {
		p.createForm.SyncAfterInput()
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
