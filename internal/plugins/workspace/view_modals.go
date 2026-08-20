package workspace

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/worktreedelete"
)

// renderCreateModal renders the new worktree modal with dimmed background.
func (p *Plugin) renderCreateModal(width, height int) string {
	background := p.renderListView(width, height)
	if p.createPlan != nil || p.createBusyStep != "" {
		p.ensureCreateOperationModal()
		if p.createOperationModal == nil {
			return background
		}
		return ui.OverlayModal(background, p.createOperationModal.Render(width, height, p.mouseHandler), width, height)
	}

	p.ensureCreateModal()
	m := p.createFormModal()
	if m == nil {
		return background
	}

	modalContent := m.Render(width, height, p.mouseHandler)
	p.createForm.RestoreFocus()
	return ui.OverlayModal(background, modalContent, width, height)
}

// renderTaskLinkModal renders the task link modal for existing worktrees with dimmed background.
func (p *Plugin) renderTaskLinkModal(width, height int) string {
	background := p.renderListView(width, height)
	p.ensureTaskLinkModal()
	if p.taskLinkModal == nil {
		return background
	}
	return ui.OverlayModal(background, p.taskLinkModal.Render(width, height, p.mouseHandler), width, height)
}

func (p *Plugin) ensureTaskLinkModal() {
	if p.linkingWorktree == nil {
		return
	}
	modalW := min(70, max(1, p.width-4))
	if p.taskLinkModal != nil && p.taskLinkModalWidth == modalW {
		return
	}
	p.taskLinkModalWidth = modalW
	title := "Link Task to " + ansi.Truncate(p.linkingWorktree.Name, max(1, modalW-16), "…")
	p.taskLinkModal = modal.New(title, modal.WithWidth(modalW), modal.WithHints(false)).
		AddSection(p.taskPickerSection(taskLinkFieldID, taskLinkItemPrefix, "Search tasks:", 8)).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(modal.Btn(" Cancel ", createCancelID)))
	p.taskLinkModal.SetFocus(taskLinkFieldID)
}

// renderConfirmDeleteModal renders the delete confirmation modal. The modal is
// built by internal/worktreedelete, the one construction the global Workspaces
// browser raises too.
func (p *Plugin) renderConfirmDeleteModal(width, height int) string {
	background := p.renderListView(width, height)

	built := p.deleteConfirm.Modal(p.width)
	if built == nil {
		return background
	}
	return ui.OverlayModal(background, built.Render(width, height, p.mouseHandler), width, height)
}

// worktreeDeleteTarget projects a plugin worktree onto the shared
// confirmation's presentation-neutral target.
func worktreeDeleteTarget(wt *Worktree) worktreedelete.Target {
	if wt == nil {
		return worktreedelete.Target{}
	}
	return worktreedelete.Target{Name: wt.Name, Branch: wt.Branch, Path: wt.Path, IsMissing: wt.IsMissing}
}

const (
	deleteShellConfirmDeleteID = "delete-shell-confirm-delete"
	deleteShellConfirmCancelID = "delete-shell-confirm-cancel"
)

const (
	commitForMergeInputID  = "commit-for-merge-input"
	commitForMergeCommitID = "commit-for-merge-commit"
	commitForMergeCancelID = "commit-for-merge-cancel"
	commitForMergeActionID = "commit-for-merge-action"
)

// renderConfirmDeleteShellModal renders the shell delete confirmation modal.
func (p *Plugin) renderConfirmDeleteShellModal(width, height int) string {
	// Render the background (list view)
	background := p.renderListView(width, height)

	p.ensureConfirmDeleteShellModal()
	if p.deleteShellModal == nil {
		return background
	}

	modalContent := p.deleteShellModal.Render(width, height, p.mouseHandler)
	return ui.OverlayModal(background, modalContent, width, height)
}

// ensureConfirmDeleteShellModal builds/rebuilds the shell delete confirmation modal.
func (p *Plugin) ensureConfirmDeleteShellModal() {
	if p.deleteConfirmShell == nil {
		return
	}

	modalW := 50
	if modalW > p.width-4 {
		modalW = p.width - 4
	}
	if modalW < 20 {
		modalW = 20
	}

	if p.deleteShellModal != nil && p.deleteShellModalWidth == modalW {
		return
	}
	p.deleteShellModalWidth = modalW

	p.deleteShellModal = modal.New("Delete Shell?",
		modal.WithWidth(modalW),
		modal.WithVariant(modal.VariantDanger),
		modal.WithHints(false),
	).
		AddSection(p.deleteShellInfoSection()).
		AddSection(modal.Spacer()).
		AddSection(p.deleteShellWarningSection()).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Delete ", deleteShellConfirmDeleteID, modal.BtnDanger()),
			modal.Btn(" Cancel ", deleteShellConfirmCancelID),
		))
}

func (p *Plugin) deleteShellInfoSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.deleteConfirmShell == nil {
			return modal.RenderedSection{}
		}
		shell := p.deleteConfirmShell

		var sb strings.Builder
		fmt.Fprintf(&sb, "Name:    %s\n", lipgloss.NewStyle().Bold(true).Render(shell.Name))
		// Display names are not unique and every shell of a project shares its
		// directory, so the session name is the only thing that tells the user
		// which session this irreversible delete will kill.
		fmt.Fprintf(&sb, "Session: %s", dimText(shell.TmuxName))

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

func (p *Plugin) deleteShellWarningSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		warningStyle := lipgloss.NewStyle().Foreground(styles.Warning)

		var sb strings.Builder
		sb.WriteString(warningStyle.Render("This will:"))
		sb.WriteString("\n")
		sb.WriteString(dimText("  • Terminate the tmux session"))
		sb.WriteString("\n")
		sb.WriteString(dimText("  • Any running processes will be killed"))

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

const (
	renameShellInputID  = "rename-shell-input"
	renameShellRenameID = "rename-shell-rename"
	renameShellCancelID = "rename-shell-cancel"
	renameShellActionID = "rename-shell-action"
)

// ensureRenameShellModal builds/rebuilds the rename shell modal.
func (p *Plugin) ensureRenameShellModal() {
	if p.renameShellSession == nil {
		return
	}

	modalW := 50
	if modalW > p.width-4 {
		modalW = p.width - 4
	}
	if modalW < 20 {
		modalW = 20
	}

	// Only rebuild if modal doesn't exist or width changed
	if p.renameShellModal != nil && p.renameShellModalWidth == modalW {
		return
	}
	p.renameShellModalWidth = modalW

	p.renameShellModal = modal.New("Rename Shell",
		modal.WithWidth(modalW),
		modal.WithPrimaryAction(renameShellActionID),
		modal.WithHints(false),
	).
		AddSection(p.renameShellInfoSection()).
		AddSection(modal.Spacer()).
		AddSection(modal.InputWithLabel(renameShellInputID, "New Name:", &p.renameShellInput)).
		AddSection(modal.When(func() bool { return p.renameShellError != "" }, p.renameShellErrorSection())).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Rename ", renameShellRenameID),
			modal.Btn(" Cancel ", renameShellCancelID),
		))
}

// renameShellInfoSection renders the shell info section.
func (p *Plugin) renameShellInfoSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.renameShellSession == nil {
			return modal.RenderedSection{}
		}

		shell := p.renameShellSession
		var sb strings.Builder
		fmt.Fprintf(&sb, "Current: %s", lipgloss.NewStyle().Bold(true).Render(shell.Name))

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// renameShellErrorSection renders the error message section.
func (p *Plugin) renameShellErrorSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.renameShellError == "" {
			return modal.RenderedSection{}
		}

		errStyle := lipgloss.NewStyle().Foreground(styles.Error)
		content := errStyle.Render("Error: " + p.renameShellError)

		return modal.RenderedSection{Content: content}
	}, nil)
}

// renderRenameShellModal renders the rename shell modal.
func (p *Plugin) renderRenameShellModal(width, height int) string {
	background := p.renderListView(width, height)

	p.ensureRenameShellModal()
	if p.renameShellModal == nil {
		return background
	}

	modalContent := p.renameShellModal.Render(width, height, p.mouseHandler)
	return ui.OverlayModal(background, modalContent, width, height)
}

const (
	renameWorktreeInputID  = "rename-worktree-input"
	renameWorktreeRenameID = "rename-worktree-rename"
	renameWorktreeCancelID = "rename-worktree-cancel"
	renameWorktreeActionID = "rename-worktree-action"
)

func (p *Plugin) ensureRenameWorktreeModal() {
	if p.renameWorktree == nil {
		return
	}

	modalW := 50
	if modalW > p.width-4 {
		modalW = p.width - 4
	}
	if modalW < 20 {
		modalW = 20
	}

	if p.renameWorktreeModal != nil && p.renameWorktreeModalWidth == modalW {
		return
	}
	p.renameWorktreeModalWidth = modalW

	p.renameWorktreeModal = modal.New("Rename Worktree",
		modal.WithWidth(modalW),
		modal.WithPrimaryAction(renameWorktreeActionID),
		modal.WithHints(false),
	).
		AddSection(p.renameWorktreeInfoSection()).
		AddSection(modal.Spacer()).
		AddSection(modal.InputWithLabel(renameWorktreeInputID, "New Name:", &p.renameWorktreeInput)).
		AddSection(modal.When(func() bool { return p.renameWorktreeError != "" }, p.renameWorktreeErrorSection())).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Rename ", renameWorktreeRenameID),
			modal.Btn(" Cancel ", renameWorktreeCancelID),
		))
}

func (p *Plugin) renameWorktreeInfoSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.renameWorktree == nil {
			return modal.RenderedSection{}
		}

		var sb strings.Builder
		fmt.Fprintf(&sb, "Current: %s", lipgloss.NewStyle().Bold(true).Render(p.renameWorktree.Name))

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

func (p *Plugin) renameWorktreeErrorSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.renameWorktreeError == "" {
			return modal.RenderedSection{}
		}

		errStyle := lipgloss.NewStyle().Foreground(styles.Error)
		content := errStyle.Render("Error: " + p.renameWorktreeError)

		return modal.RenderedSection{Content: content}
	}, nil)
}

func (p *Plugin) renderRenameWorktreeModal(width, height int) string {
	background := p.renderListView(width, height)

	p.ensureRenameWorktreeModal()
	if p.renameWorktreeModal == nil {
		return background
	}

	modalContent := p.renameWorktreeModal.Render(width, height, p.mouseHandler)
	return ui.OverlayModal(background, modalContent, width, height)
}

func (p *Plugin) agentChoiceItems() []modal.ListItem {
	items := make([]modal.ListItem, 0, 2)
	if fullTmuxAttachEnabled() {
		items = append(items, modal.ListItem{ID: "agent-choice-attach", Label: "Attach to session"})
	}
	return append(items, modal.ListItem{ID: "agent-choice-restart", Label: "Restart agent"})
}

// ensureAgentChoiceModal builds/rebuilds the agent choice modal.
func (p *Plugin) ensureAgentChoiceModal() {
	if p.agentChoiceWorktree == nil {
		return
	}

	modalW := 50
	if p.width > 0 && modalW > p.width-4 {
		modalW = p.width - 4
	}
	if modalW < 20 {
		modalW = 20
	}

	// Only rebuild if modal doesn't exist or width changed
	if p.agentChoiceModal != nil && p.agentChoiceModalWidth == modalW {
		return
	}
	p.agentChoiceModalWidth = modalW

	items := p.agentChoiceItems()

	title := fmt.Sprintf("Agent Running: %s", p.agentChoiceWorktree.Name)

	p.agentChoiceModal = modal.New(title,
		modal.WithWidth(modalW),
		modal.WithPrimaryAction(agentChoiceActionID),
		modal.WithHints(false),
	).
		AddSection(modal.Text("An agent is already running on this worktree.\nWhat would you like to do?")).
		AddSection(modal.Spacer()).
		AddSection(modal.List(agentChoiceListID, items, &p.agentChoiceIdx, modal.WithMaxVisible(2))).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Confirm ", agentChoiceConfirmID),
			modal.Btn(" Cancel ", agentChoiceCancelID),
		))
}

// renderAgentChoiceModal renders the agent action choice modal.
func (p *Plugin) renderAgentChoiceModal(width, height int) string {
	background := p.renderListView(width, height)

	p.ensureAgentChoiceModal()
	if p.agentChoiceModal == nil {
		return background
	}

	modalContent := p.agentChoiceModal.Render(width, height, p.mouseHandler)
	return ui.OverlayModal(background, modalContent, width, height)
}

// clearAgentChoiceModal clears agent choice modal state.
func (p *Plugin) clearAgentChoiceModal() {
	p.agentChoiceWorktree = nil
	p.agentChoiceIdx = 0
	p.agentChoiceModal = nil
	p.agentChoiceModalWidth = 0
}

// ensureMergeModal builds/rebuilds the merge workflow modal.
func (p *Plugin) ensureMergeModal() {
	if p.mergeState == nil {
		return
	}

	modalW := 70
	if p.width > 0 && modalW > p.width-4 {
		modalW = p.width - 4
	}
	if modalW < 30 {
		modalW = 30
	}

	// Only rebuild if modal doesn't exist, width changed, or step changed
	if p.mergeModal != nil && p.mergeModalWidth == modalW && p.mergeModalStep == p.mergeState.Step {
		return
	}
	p.mergeModalWidth = modalW
	p.mergeModalStep = p.mergeState.Step

	title := fmt.Sprintf("Merge Workflow: %s", p.mergeState.Worktree.Name)

	// Determine primary action based on current step
	var primaryAction string
	switch p.mergeState.Step {
	case MergeStepTargetBranch:
		primaryAction = mergeTargetActionID
	case MergeStepMergeMethod:
		primaryAction = mergeMethodActionID
	case MergeStepPostMergeConfirmation:
		primaryAction = mergeCleanUpButtonID
	}

	// Build modal based on current step
	opts := []modal.Option{
		modal.WithWidth(modalW),
		modal.WithHints(false),
		modal.WithCloseOnBackdropClick(false),
	}
	if primaryAction != "" {
		opts = append(opts, modal.WithPrimaryAction(primaryAction))
	}
	m := modal.New(title, opts...)

	// Keep the active action visible on compact terminals; the full progress
	// list would otherwise consume most of a 60x24 viewport.
	if p.height > 0 && p.height < 30 {
		m.AddSection(modal.Text(dimText("Step: " + p.mergeState.Step.String())))
	} else {
		m.AddSection(p.mergeProgressSection())
	}
	m.AddSection(modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		return modal.RenderedSection{Content: strings.Repeat("─", min(contentWidth, 60))}
	}, nil))
	m.AddSection(modal.Spacer())

	// Add step-specific sections
	switch p.mergeState.Step {
	case MergeStepReviewDiff:
		m.AddSection(p.mergeReviewDiffSection())
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Text(dimText("Press Enter to continue, Esc to cancel")))

	case MergeStepTargetBranch:
		m.AddSection(modal.Text(lipgloss.NewStyle().Bold(true).Render("Target Branch:")))
		m.AddSection(modal.Spacer())
		if len(p.mergeState.TargetBranches) > 0 {
			items := make([]modal.ListItem, len(p.mergeState.TargetBranches))
			for i, b := range p.mergeState.TargetBranches {
				label := b
				if i == 0 {
					label = b + " (default)"
				}
				items[i] = modal.ListItem{ID: "branch-" + b, Label: label}
			}
			maxVis := len(items)
			if maxVis > 8 {
				maxVis = 8
			}
			m.AddSection(modal.List(mergeTargetListID, items, &p.mergeState.TargetBranchOption, modal.WithMaxVisible(maxVis)))
		} else {
			m.AddSection(modal.Text("Loading branches..."))
		}
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Text(dimText("↑/↓: select   Enter: continue   Esc: cancel")))

	case MergeStepMergeMethod:
		m.AddSection(modal.Text(lipgloss.NewStyle().Bold(true).Render("Choose Merge Method:")))
		m.AddSection(modal.Spacer())
		items := []modal.ListItem{
			{ID: "merge-pr", Label: "Create Pull Request (Recommended)"},
			{ID: "merge-direct", Label: "Direct Merge"},
		}
		m.AddSection(modal.List(mergeMethodListID, items, &p.mergeState.MergeMethodOption, modal.WithMaxVisible(2)))
		m.AddSection(modal.Spacer())
		m.AddSection(p.mergeMethodHintsSection())
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Text(dimText("↑/↓: select   Enter: continue   Esc: cancel")))

	case MergeStepDirectMerge:
		if op := p.mergeState.DirectOperation; op != nil {
			m.AddSection(modal.Text(lipgloss.NewStyle().Foreground(styles.Success).Render("Preflight passed")))
			m.AddSection(modal.Spacer())
			m.AddSection(modal.Text(fmt.Sprintf("Source: %s at %s", op.SourceBranch, shortOID(op.SourceOID))))
			m.AddSection(modal.Text(fmt.Sprintf("Target: %s at %s", op.TargetBranch, shortOID(op.TargetOID))))
			m.AddSection(modal.Text(fmt.Sprintf("Checkout: %s", op.TargetPath)))
			m.AddSection(modal.Text(fmt.Sprintf("Remote: %s", op.Remote)))
			m.AddSection(modal.Spacer())
			m.AddSection(modal.Text(dimText("Fast-forwarding target, merging the reviewed source OID, then pushing...")))
		} else {
			m.AddSection(modal.Text("Running direct-merge preflight..."))
			m.AddSection(modal.Spacer())
			m.AddSection(modal.Text(dimText(fmt.Sprintf("Resolving a safe checkout for '%s'...", p.mergeState.TargetBranch))))
		}

	case MergeStepPush:
		m.AddSection(modal.Text("Pushing branch to remote..."))

	case MergeStepGeneratePR:
		if p.mergeState.PRGenerationActive {
			dots := strings.Repeat(".", p.mergeState.PRGenerationDots)
			m.AddSection(modal.Text("Preparing editable PR draft" + dots))
		} else {
			agentName := AgentDisplayNames[p.mergeState.Worktree.ChosenAgentType]
			if agentName == "" {
				agentName = "Selected agent"
			}
			m.AddSection(modal.Text("Choose how to prepare the editable title and body."))
			m.AddSection(modal.Text(fmt.Sprintf("Remote: %s   Base: %s:%s", p.mergeState.PushRemote, p.mergeState.PR.Repository, p.mergeState.TargetBranch)))
			head := p.mergeState.PR.HeadRef
			if p.mergeState.PR.HeadOwner != "" {
				head = p.mergeState.PR.HeadOwner + ":" + head
			}
			m.AddSection(modal.Text("Head: " + head + " at " + shortOID(p.mergeState.ReviewedOID)))
			m.AddSection(modal.Spacer())
			m.AddSection(modal.Text("Commit summary stays local and is deterministic."))
			m.AddSection(modal.Text(fmt.Sprintf("%s may send a capped code diff to its configured external provider.", agentName)))
			m.AddSection(modal.Spacer())
			m.AddSection(modal.Buttons(
				modal.Btn(" Commit Summary ", mergeFallbackDraftID, modal.BtnPrimary()),
				modal.Btn(" Use Agent ", mergeAgentDraftID),
				modal.Btn(" Cancel ", "cancel"),
			))
		}

	case MergeStepEditPR:
		m.AddSection(modal.Text("Review and edit everything GitHub will receive."))
		m.AddSection(modal.Text(fmt.Sprintf("%s → %s:%s", p.mergeState.PR.HeadRef, p.mergeState.PR.Repository, p.mergeState.TargetBranch)))
		m.AddSection(modal.Spacer())
		m.AddSection(modal.InputWithLabel("merge-pr-title", "Title", &p.mergeState.PRTitleInput))
		m.AddSection(modal.TextareaWithLabel("merge-pr-body", "Body", &p.mergeState.PRBodyInput, 8))
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Buttons(modal.Btn(" Create PR ", mergeCreatePRID, modal.BtnPrimary()), modal.Btn(" Cancel ", "cancel")))

	case MergeStepCreatePR:
		m.AddSection(modal.Text("Creating pull request..."))

	case MergeStepWaitingMerge:
		m.AddSection(p.mergeWaitingSection())
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Buttons(modal.Btn(" Check Now ", "check-pr"), modal.Btn(" Stop Watching ", mergeStopWatchingID)))
		m.AddSection(modal.Text(dimText("o: open PR   y: copy URL   Esc: exit")))

	case MergeStepPostMergeConfirmation:
		m.AddSection(p.mergePostMergeHeaderSection())
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Text(lipgloss.NewStyle().Bold(true).Render("Cleanup Options")))
		m.AddSection(modal.Text(dimText("Select what to clean up:")))
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Checkbox(mergeConfirmWorktreeID, "Delete local worktree", &p.mergeState.DeleteLocalWorktree))
		m.AddSection(modal.Text(dimText("  Removes " + p.mergeState.Worktree.Path)))
		m.AddSection(modal.Checkbox(mergeConfirmBranchID, "Delete local branch", &p.mergeState.DeleteLocalBranch))
		m.AddSection(modal.Text(dimText("  Removes '" + p.mergeState.Worktree.Branch + "' locally")))
		if p.mergeState.ForceDeleteRequired {
			m.AddSection(modal.Checkbox(mergeForceBranchID, "Force-delete local branch", &p.mergeState.ForceDeleteLocalBranch))
			m.AddSection(modal.Text(dimText("  Required after squash/rebase: Git cannot prove the reviewed head is an ancestor.")))
		}
		m.AddSection(modal.Checkbox(mergeConfirmRemoteID, "Delete remote branch", &p.mergeState.DeleteRemoteBranch))
		m.AddSection(modal.Text(dimText("  Removes from GitHub (often auto-deleted)")))
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
			return modal.RenderedSection{Content: strings.Repeat("─", min(contentWidth, 60))}
		}, nil))
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Text(lipgloss.NewStyle().Bold(true).Render("Sync Local Branch")))
		m.AddSection(modal.Checkbox(mergeConfirmPullID, fmt.Sprintf("Update local '%s' from remote", p.mergeState.TargetBranch), &p.mergeState.PullAfterMerge))
		if p.mergeState.CurrentBranch != "" {
			m.AddSection(modal.Text(dimText(fmt.Sprintf("  Current branch: %s", p.mergeState.CurrentBranch))))
		} else {
			m.AddSection(modal.Text(dimText(fmt.Sprintf("  Updates local %s to include merged PR", p.mergeState.TargetBranch))))
		}
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Buttons(
			modal.Btn(" Clean Up ", mergeCleanUpButtonID),
			modal.Btn(" Skip All ", mergeSkipButtonID),
		))
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Text(dimText("↑/↓: navigate  space: toggle  enter: confirm  esc: cancel")))

	case MergeStepCleanup:
		m.AddSection(modal.Text("Cleaning up worktree and branch..."))

	case MergeStepDone:
		m.AddSection(p.mergeDoneSection())

	case MergeStepError:
		// Override with danger-variant modal for prominent error display
		m = modal.New(p.mergeState.ErrorTitle,
			modal.WithWidth(modalW),
			modal.WithVariant(modal.VariantDanger),
			modal.WithHints(false),
			modal.WithCloseOnBackdropClick(false),
		)
		m.AddSection(p.mergeProgressSection())
		m.AddSection(modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
			return modal.RenderedSection{Content: strings.Repeat("─", min(contentWidth, 60))}
		}, nil))
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Text(lipgloss.NewStyle().Foreground(styles.Error).Bold(true).Render("Error Output:")))
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Text(p.mergeState.ErrorDetail))
		m.AddSection(modal.Spacer())
		buttons := []modal.ButtonDef{modal.Btn(" Dismiss ", "dismiss")}
		if op := p.mergeState.DirectOperation; op != nil {
			switch op.Recovery {
			case DirectMergeRecoveryConflict:
				buttons = []modal.ButtonDef{
					modal.Btn(" Continue ", "continue"),
					modal.Btn(" Abort ", "abort", modal.BtnDanger()),
					modal.Btn(" Dismiss ", "dismiss"),
				}
			case DirectMergeRecoveryPushFailure:
				buttons = []modal.ButtonDef{
					modal.Btn(" Retry Push ", "retry-push"),
					modal.Btn(" Dismiss ", "dismiss"),
				}
			}
		}
		m.AddSection(modal.Buttons(buttons...))
		m.AddSection(modal.Spacer())
		m.AddSection(modal.Text(dimText("Use the recovery action above, or y: copy error")))
	}

	p.mergeModal = m
}

// mergeProgressSection renders the progress indicators for all steps.
func (p *Plugin) mergeProgressSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.mergeState == nil {
			return modal.RenderedSection{}
		}

		var sb strings.Builder
		// Determine which steps to show based on merge method
		var steps []MergeWorkflowStep
		if p.mergeState.UseDirectMerge {
			steps = []MergeWorkflowStep{
				MergeStepReviewDiff,
				MergeStepTargetBranch,
				MergeStepMergeMethod,
				MergeStepDirectMerge,
				MergeStepPostMergeConfirmation,
				MergeStepCleanup,
			}
		} else {
			steps = []MergeWorkflowStep{
				MergeStepReviewDiff,
				MergeStepTargetBranch,
				MergeStepMergeMethod,
				MergeStepPush,
				MergeStepGeneratePR,
				MergeStepEditPR,
				MergeStepCreatePR,
				MergeStepWaitingMerge,
				MergeStepPostMergeConfirmation,
				MergeStepCleanup,
			}
		}

		for i, step := range steps {
			status := p.mergeState.StepStatus[step]
			icon := "○" // pending
			color := styles.TextMuted

			switch status {
			case "running":
				icon = "●"
				color = styles.Warning
			case "done":
				icon = "✓"
				color = styles.Success
			case "error":
				icon = "✗"
				color = styles.Error
			case "skipped":
				icon = "○"
				color = styles.TextMuted
			}

			stepName := step.String()
			if step == p.mergeState.Step {
				stepName = lipgloss.NewStyle().Bold(true).Render(stepName)
			}

			stepLine := fmt.Sprintf("  %s %s",
				lipgloss.NewStyle().Foreground(color).Render(icon),
				stepName,
			)
			if i > 0 {
				sb.WriteString("\n")
			}
			sb.WriteString(stepLine)
		}

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// mergeReviewDiffSection renders the diff summary for review.
func (p *Plugin) mergeReviewDiffSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.mergeState == nil {
			return modal.RenderedSection{}
		}

		var sb strings.Builder
		sb.WriteString(lipgloss.NewStyle().Bold(true).Render("Files Changed:"))
		sb.WriteString("\n\n")

		if p.mergeState.StepStatus[MergeStepReviewDiff] == "running" {
			sb.WriteString(dimText("Loading..."))
		} else if p.mergeState.DiffSummary != "" {
			summaryLines := strings.Split(p.mergeState.DiffSummary, "\n")
			maxLines := 15
			if len(summaryLines) > maxLines {
				summaryLines = summaryLines[:maxLines]
				summaryLines = append(summaryLines, fmt.Sprintf("... (%d more files)", len(strings.Split(p.mergeState.DiffSummary, "\n"))-maxLines))
			}
			for _, line := range summaryLines {
				sb.WriteString(p.colorStatLine(line, contentWidth))
				sb.WriteString("\n")
			}
		} else {
			sb.WriteString(dimText("No files changed"))
		}

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// mergeMethodHintsSection renders hints for the merge method options.
func (p *Plugin) mergeMethodHintsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.mergeState == nil {
			return modal.RenderedSection{}
		}

		var sb strings.Builder

		if p.mergeState.MergeMethodOption == 0 {
			sb.WriteString(dimText("Push the reviewed commit to the resolved remote and create a GitHub PR"))
		} else {
			sb.WriteString(dimText(fmt.Sprintf("Merge directly to '%s' without PR", p.mergeState.TargetBranch)))
			sb.WriteString("\n")
			sb.WriteString(lipgloss.NewStyle().Foreground(styles.Warning).Render("Warning: Bypasses code review"))
		}

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// mergeWaitingSection renders the waiting for merge step content.
func (p *Plugin) mergeWaitingSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.mergeState == nil {
			return modal.RenderedSection{}
		}

		var sb strings.Builder
		if p.mergeState.ExistingPR {
			sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(styles.Warning).Render("Using Existing Pull Request"))
		} else {
			sb.WriteString(lipgloss.NewStyle().Bold(true).Render("Pull Request Created"))
		}
		sb.WriteString("\n\n")
		if p.mergeState.PRPollKind != "" {
			fmt.Fprintf(&sb, "Status: %s\n", p.mergeState.PRPollKind)
			if p.mergeState.PRWatchStopped {
				sb.WriteString(dimText("Watching stopped; the PR URL is preserved.") + "\n")
			}
		}

		var focusables []modal.FocusableInfo
		urlLineY := 2 // header (line 0), blank (line 1), URL (line 2)

		if p.mergeState.PRURL != "" {
			styledURL := styles.Link.Render(p.mergeState.PRURL)
			clickableURL := ansi.SetHyperlink(p.mergeState.PRURL) + styledURL + ansi.ResetHyperlink()
			fmt.Fprintf(&sb, "URL: %s", clickableURL)
			sb.WriteString("\n")

			focusables = append(focusables, modal.FocusableInfo{
				ID:      mergePRURLID,
				OffsetX: 5, // after "URL: "
				OffsetY: urlLineY,
				Width:   ansi.StringWidth(p.mergeState.PRURL),
				Height:  1,
			})
		}

		sb.WriteString("\n")
		if !p.mergeState.PRWatchStopped {
			sb.WriteString("Watching pull request by repository and number...")
		}
		sb.WriteString("\n\n")
		sb.WriteString(strings.Repeat("─", min(contentWidth, 60)))
		sb.WriteString("\n\n")

		sb.WriteString(lipgloss.NewStyle().Bold(true).Render("After merge:"))
		sb.WriteString("\n\n")

		// Radio options
		if p.mergeState.DeleteAfterMerge {
			sb.WriteString(lipgloss.NewStyle().Foreground(styles.Primary).Render(" ● Delete worktree after merge"))
		} else {
			sb.WriteString(dimText(" ○ Delete worktree after merge"))
		}
		sb.WriteString("\n")
		if !p.mergeState.DeleteAfterMerge {
			sb.WriteString(lipgloss.NewStyle().Foreground(styles.Primary).Render(" ● Keep worktree"))
		} else {
			sb.WriteString(dimText(" ○ Keep worktree"))
		}
		sb.WriteString("\n\n")
		sb.WriteString(dimText(" (This takes effect only once the PR is merged)"))

		return modal.RenderedSection{Content: sb.String(), Focusables: focusables}
	}, nil)
}

// mergePostMergeHeaderSection renders the header for post-merge confirmation.
func (p *Plugin) mergePostMergeHeaderSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		mergeMethod := "PR Merged"
		if p.mergeState != nil && p.mergeState.UseDirectMerge {
			mergeMethod = "Direct Merge Complete"
		}
		header := lipgloss.NewStyle().Bold(true).Foreground(styles.Success).Render(mergeMethod + "!")
		separator := strings.Repeat("─", min(contentWidth, 60))
		return modal.RenderedSection{Content: header + "\n\n" + separator}
	}, nil)
}

// mergeDoneSection renders the completion summary.
func (p *Plugin) mergeDoneSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.mergeState == nil {
			return modal.RenderedSection{}
		}

		var sb strings.Builder
		sb.WriteString(lipgloss.NewStyle().Bold(true).Foreground(styles.Success).Render("Merge workflow complete!"))
		sb.WriteString("\n\n")

		if p.mergeState.CleanupResults != nil {
			results := p.mergeState.CleanupResults
			sb.WriteString("Summary:\n")

			successStyle := lipgloss.NewStyle().Foreground(styles.Success)
			if results.LocalWorktreeDeleted {
				sb.WriteString(successStyle.Render("  ✓ Local worktree deleted"))
				sb.WriteString("\n")
			}
			if results.LocalBranchDeleted {
				sb.WriteString(successStyle.Render("  ✓ Local branch deleted"))
				sb.WriteString("\n")
			}
			if results.RemoteBranchDeleted {
				sb.WriteString(successStyle.Render("  ✓ Remote branch deleted"))
				sb.WriteString("\n")
			}
			if results.PullAttempted {
				if results.PullSuccess {
					message := results.PullMessage
					if message == "" {
						message = "Pulled latest changes"
					}
					sb.WriteString(successStyle.Render("  ✓ " + message))
					sb.WriteString("\n")
				} else if results.PullError != nil {
					warnStyle := lipgloss.NewStyle().Foreground(styles.Warning)
					errorStyle := lipgloss.NewStyle().Foreground(styles.Error)

					sb.WriteString(warnStyle.Render("  ⚠ Pull failed: "))
					sb.WriteString(errorStyle.Render(results.PullErrorSummary))
					sb.WriteString("\n")

					if results.ShowErrorDetails {
						sb.WriteString("\n")
						sb.WriteString(dimText("  Details:"))
						sb.WriteString("\n")
						allDetailLines := strings.Split(results.PullErrorFull, "\n")
						maxDetailLines := 10
						detailLines := allDetailLines
						if len(allDetailLines) > maxDetailLines {
							detailLines = allDetailLines[:maxDetailLines]
						}
						for _, line := range detailLines {
							if line = strings.TrimSpace(line); line != "" {
								sb.WriteString(dimText("    " + line))
								sb.WriteString("\n")
							}
						}
						if len(allDetailLines) > maxDetailLines {
							sb.WriteString(dimText(fmt.Sprintf("    ... (%d more lines)", len(allDetailLines)-maxDetailLines)))
							sb.WriteString("\n")
						}
						sb.WriteString("\n")
						sb.WriteString(dimText("  Press 'd' to hide details"))
					} else {
						sb.WriteString(dimText("  Press 'd' for full error details"))
					}
					sb.WriteString("\n")

					if results.BranchDiverged {
						sb.WriteString("\n")
						sb.WriteString(strings.Repeat("─", min(contentWidth, 60)))
						sb.WriteString("\n\n")
						sb.WriteString(lipgloss.NewStyle().Bold(true).Render("Resolution Options"))
						sb.WriteString("\n")
						sb.WriteString(dimText(fmt.Sprintf("  Your local '%s' has diverged from remote.", results.BaseBranch)))
						sb.WriteString("\n\n")
						sb.WriteString(dimText("    [r] Rebase local onto remote"))
						sb.WriteString("\n")
						sb.WriteString(dimText("        Replay your local commits on top of remote changes"))
						sb.WriteString("\n\n")
						sb.WriteString(dimText("    [m] Merge remote into local"))
						sb.WriteString("\n")
						sb.WriteString(dimText("        Creates a merge commit combining both histories"))
						sb.WriteString("\n")
					}
				}
			}

			if len(results.Errors) > 0 {
				sb.WriteString("\n")
				sb.WriteString(lipgloss.NewStyle().Foreground(styles.Warning).Render("Warnings:"))
				sb.WriteString("\n")
				for _, err := range results.Errors {
					sb.WriteString(dimText("  • " + err))
					sb.WriteString("\n")
				}
			}
		} else {
			sb.WriteString("No cleanup performed. Worktree and branches remain.")
		}

		sb.WriteString("\n\n")
		if p.mergeState.CleanupResults != nil && p.mergeState.CleanupResults.BranchDiverged {
			sb.WriteString(dimText("r: rebase  m: merge  d: details  Enter: close"))
		} else if p.mergeState.CleanupResults != nil && p.mergeState.CleanupResults.PullError != nil {
			sb.WriteString(dimText("d: details  Enter: close"))
		} else {
			sb.WriteString(dimText("Press Enter to close"))
		}

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// clearMergeModal clears the merge modal state.
func (p *Plugin) clearMergeModal() {
	p.mergeModal = nil
	p.mergeModalWidth = 0
	p.mergeModalStep = 0
}

// renderMergeModal renders the merge workflow modal with dimmed background.
func (p *Plugin) renderMergeModal(width, height int) string {
	background := p.renderListView(width, height)

	p.ensureMergeModal()
	if p.mergeModal == nil {
		return background
	}

	modalContent := p.mergeModal.Render(width, height, p.mouseHandler)
	return ui.OverlayModal(background, modalContent, width, height)
}

// ensureCommitForMergeModal builds/rebuilds the commit-for-merge modal.
func (p *Plugin) ensureCommitForMergeModal() {
	if p.mergeCommitState == nil {
		return
	}

	modalW := 60
	if p.width > 0 && modalW > p.width-4 {
		modalW = p.width - 4
	}
	if modalW < 30 {
		modalW = 30
	}

	// Only rebuild if modal doesn't exist or width changed
	if p.commitForMergeModal != nil && p.commitForMergeModalWidth == modalW {
		return
	}
	p.commitForMergeModalWidth = modalW

	p.commitForMergeModal = modal.New("Uncommitted Changes",
		modal.WithWidth(modalW),
		modal.WithVariant(modal.VariantWarning),
		modal.WithPrimaryAction(commitForMergeActionID),
		modal.WithHints(false),
	).
		AddSection(p.commitForMergeInfoSection()).
		AddSection(modal.Spacer()).
		AddSection(p.commitForMergeChangesSection()).
		AddSection(modal.Spacer()).
		AddSection(modal.Text(dimText("You must commit these changes before creating a PR."))).
		AddSection(modal.Text(dimText("All changes will be staged and committed."))).
		AddSection(modal.Spacer()).
		AddSection(modal.InputWithLabel(commitForMergeInputID, "Commit message:", &p.mergeCommitMessageInput)).
		AddSection(modal.When(func() bool { return p.mergeCommitState != nil && p.mergeCommitState.Error != "" }, p.commitForMergeErrorSection())).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Commit ", commitForMergeCommitID),
			modal.Btn(" Cancel ", commitForMergeCancelID),
		))
}

// commitForMergeInfoSection renders the workspace info section.
func (p *Plugin) commitForMergeInfoSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.mergeCommitState == nil || p.mergeCommitState.Worktree == nil {
			return modal.RenderedSection{}
		}

		wt := p.mergeCommitState.Worktree
		var sb strings.Builder
		fmt.Fprintf(&sb, "Workspace: %s\n", lipgloss.NewStyle().Bold(true).Render(wt.Name))
		fmt.Fprintf(&sb, "Branch:    %s", wt.Branch)

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// commitForMergeChangesSection renders the change counts section.
func (p *Plugin) commitForMergeChangesSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.mergeCommitState == nil {
			return modal.RenderedSection{}
		}

		var sb strings.Builder
		sb.WriteString(lipgloss.NewStyle().Bold(true).Render("Changes to commit:"))
		if p.mergeCommitState.StagedCount > 0 {
			fmt.Fprintf(&sb, "\n  • %d staged file(s)", p.mergeCommitState.StagedCount)
		}
		if p.mergeCommitState.ModifiedCount > 0 {
			fmt.Fprintf(&sb, "\n  • %d modified file(s)", p.mergeCommitState.ModifiedCount)
		}
		if p.mergeCommitState.UntrackedCount > 0 {
			fmt.Fprintf(&sb, "\n  • %d untracked file(s)", p.mergeCommitState.UntrackedCount)
		}

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// commitForMergeErrorSection renders the error message section.
func (p *Plugin) commitForMergeErrorSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.mergeCommitState == nil || p.mergeCommitState.Error == "" {
			return modal.RenderedSection{}
		}

		errStyle := lipgloss.NewStyle().Foreground(styles.Error)
		content := errStyle.Render("Error: " + p.mergeCommitState.Error)

		return modal.RenderedSection{Content: content}
	}, nil)
}

// clearCommitForMergeModal clears commit-for-merge modal state.
func (p *Plugin) clearCommitForMergeModal() {
	p.commitForMergeModal = nil
	p.commitForMergeModalWidth = 0
}

// renderCommitForMergeModal renders the commit-before-merge modal.
func (p *Plugin) renderCommitForMergeModal(width, height int) string {
	background := p.renderListView(width, height)

	p.ensureCommitForMergeModal()
	if p.commitForMergeModal == nil {
		return background
	}

	modalContent := p.commitForMergeModal.Render(width, height, p.mouseHandler)
	return ui.OverlayModal(background, modalContent, width, height)
}
