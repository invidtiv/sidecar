package workspace

import (
	"fmt"

	"charm.land/bubbles/v2/textinput"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/state"
	ui "github.com/marcus/sidecar/internal/ui"
)

const (
	agentConfigAgentFieldID      = "agent-config-agent"
	agentConfigSkipPermissionsID = "agent-config-skip-permissions"
	agentConfigSubmitID          = "agent-config-submit"
	agentConfigCancelID          = "agent-config-cancel"
)

// openAgentConfigModal initializes and opens the agent config modal for a worktree.
func (p *Plugin) openAgentConfigModal(wt *Worktree, isRestart bool) {
	p.agentConfigWorktree = wt
	p.agentConfigIsRestart = isRestart
	// Keep current/stored agent visible even if hidden from the global allowlist.
	// List is fixed for the modal lifetime so key/mouse index sync cannot drop preferred.
	preferred := p.resolveWorktreeAgentType(wt)
	p.agentConfigAgentList = withPreferredAgent(p.selectableAgentTypes(), preferred)
	p.agentConfigAgentType, p.agentConfigAgentIdx = clampAgentSelection(p.agentConfigAgentList, preferred, -1)
	p.agentConfigAgentInput = textinput.New()
	p.agentConfigAgentInput.Placeholder = ""
	p.agentConfigAgentInput.Prompt = ""
	p.agentConfigAgentInput.CharLimit = 80
	p.prefillAgentConfigAgentInput()
	p.loadAgentConfigAutoApprove()
	p.agentConfigModal = nil
	p.agentConfigModalWidth = 0
	p.viewMode = ViewModeAgentConfig
}

// clearAgentConfigModal resets all agent config modal state.
func (p *Plugin) clearAgentConfigModal() {
	p.agentConfigWorktree = nil
	p.agentConfigIsRestart = false
	p.agentConfigAgentType = ""
	p.agentConfigAgentIdx = 0
	p.agentConfigAgentList = nil
	p.agentConfigSkipPerms = false
	p.agentConfigAgentInput = textinput.Model{}
	p.agentConfigModal = nil
	p.agentConfigModalWidth = 0
}

func (p *Plugin) shouldShowAgentConfigSkipPerms() bool {
	if p.agentConfigAgentType == AgentNone || p.agentConfigAgentType == "" {
		return false
	}
	flag, ok := SkipPermissionsFlags[p.agentConfigAgentType]
	return ok && flag != ""
}

func (p *Plugin) loadAgentConfigAutoApprove() {
	p.agentConfigSkipPerms = state.GetAgentAutoApprove(string(p.agentConfigAgentType))
}

func (p *Plugin) persistAgentConfigAutoApprove() {
	if p.agentConfigAgentType == "" {
		return
	}
	_ = state.SetAgentAutoApprove(string(p.agentConfigAgentType), p.agentConfigSkipPerms)
}

func (p *Plugin) prefillAgentConfigAgentInput() {
	label := AgentDisplayNames[p.agentConfigAgentType]
	if label == "" {
		label = string(p.agentConfigAgentType)
	}
	p.agentConfigAgentInput.SetValue(label)
}

func (p *Plugin) syncAgentConfigFromIdx() {
	prev := p.agentConfigAgentType
	if p.agentConfigAgentIdx >= 0 && p.agentConfigAgentIdx < len(p.agentConfigAgentList) {
		p.agentConfigAgentType = p.agentConfigAgentList[p.agentConfigAgentIdx]
	}
	if p.agentConfigAgentType != prev {
		p.prefillAgentConfigAgentInput()
	}
}

func (p *Plugin) agentConfigItems() []modal.DropdownItem {
	items := make([]modal.DropdownItem, len(p.agentConfigAgentList))
	for i, at := range p.agentConfigAgentList {
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

func (p *Plugin) ensureAgentConfigModal() {
	if p.agentConfigWorktree == nil {
		return
	}

	modalW := 60
	maxW := p.width - 4
	if maxW < 1 {
		maxW = 1
	}
	if modalW > maxW {
		modalW = maxW
	}

	if p.agentConfigModal != nil && p.agentConfigModalWidth == modalW {
		return
	}
	p.agentConfigModalWidth = modalW

	agentTypes := p.agentConfigAgentList
	if len(agentTypes) == 0 {
		preferred := AgentNone
		if p.agentConfigWorktree != nil {
			preferred = p.resolveWorktreeAgentType(p.agentConfigWorktree)
		}
		agentTypes = withPreferredAgent(p.selectableAgentTypes(), preferred)
		p.agentConfigAgentList = agentTypes
	}
	p.agentConfigAgentType, p.agentConfigAgentIdx = clampAgentSelection(agentTypes, p.agentConfigAgentType, p.agentConfigAgentIdx)
	p.prefillAgentConfigAgentInput()
	items := p.agentConfigItems()

	title := fmt.Sprintf("Start Agent: %s", p.agentConfigWorktree.Name)
	if p.agentConfigIsRestart {
		title = fmt.Sprintf("Restart Agent: %s", p.agentConfigWorktree.Name)
	}

	p.agentConfigModal = modal.New(title,
		modal.WithWidth(modalW),
		modal.WithPrimaryAction(agentConfigSubmitID),
		modal.WithHints(false),
	).
		AddSection(modal.Text("Agent")).
		AddSection(modal.Combo(agentConfigAgentFieldID, &p.agentConfigAgentInput, items, &p.agentConfigAgentIdx,
			modal.WithComboFilter(comboExactOrAllFilter(items)))).
		AddSection(modal.When(p.shouldShowAgentConfigSkipPerms, modal.Checkbox(agentConfigSkipPermissionsID, "Auto-approve all actions", &p.agentConfigSkipPerms))).
		AddSection(p.agentConfigSkipPermissionsHintSection()).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Start ", agentConfigSubmitID),
			modal.Btn(" Cancel ", agentConfigCancelID),
		))

	p.agentConfigModal.SetFocus(agentConfigAgentFieldID)
}

func (p *Plugin) agentConfigSkipPermissionsHintSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if p.agentConfigAgentType == AgentNone || p.agentConfigAgentType == "" {
			return modal.RenderedSection{}
		}
		if p.shouldShowAgentConfigSkipPerms() {
			flag := SkipPermissionsFlags[p.agentConfigAgentType]
			return modal.RenderedSection{Content: dimText(fmt.Sprintf("      (Adds %s)", flag))}
		}
		return modal.RenderedSection{Content: dimText("  Skip permissions not available for this agent")}
	}, nil)
}

func (p *Plugin) renderAgentConfigModal(width, height int) string {
	background := p.renderListView(width, height)

	p.ensureAgentConfigModal()
	if p.agentConfigModal == nil {
		return background
	}

	modalContent := p.agentConfigModal.Render(width, height, p.mouseHandler)
	return ui.OverlayModal(background, modalContent, width, height)
}
