package workspace

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	boardkanban "github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/styles"
)

// renderKanbanView adapts workspace cards to the shared board component. The
// component owns layout, height constraints, scroll, and hit geometry; this
// plugin retains workspace-specific text and actions.
func (p *Plugin) renderKanbanView(width, height int) string {
	p.syncKanbanComponent()
	if p.kanban.Compact(width, minKanbanColumnWidth) {
		return p.renderListView(width, height)
	}

	result := p.kanban.Render(boardkanban.RenderOptions{
		Width: width, Height: height,
		Header: "Workspaces", HeaderRight: "List|[Kanban]",
		MinColumnWidth: minKanbanColumnWidth, CardHeight: kanbanCardHeight,
		RenderCard: func(card boardkanban.Card, line, cardWidth int, selected, _ bool) string {
			if strings.HasPrefix(card.ID, "shell:") {
				if shell := p.kanbanShellByID(card.ID); shell != nil {
					return p.renderKanbanShellCardLine(shell, line, cardWidth, selected)
				}
			} else if wt := p.kanbanWorktreeByID(card.ID); wt != nil {
				return p.renderKanbanCardLine(wt, line, cardWidth, selected)
			}
			return strings.Repeat(" ", cardWidth)
		},
	})

	toggleWidth := len("List|[Kanban]")
	toggleX := width - 2 - toggleWidth
	p.mouseHandler.HitMap.AddRect(regionViewToggle, toggleX, 1, len("List"), 1, 0)
	p.mouseHandler.HitMap.AddRect(regionViewToggle, toggleX+len("List|"), 1, len("[Kanban]"), 1, 1)
	for _, region := range result.Regions {
		switch region.Kind {
		case boardkanban.RegionColumn:
			p.mouseHandler.HitMap.AddRect(regionKanbanColumn, region.X, region.Y, region.W, region.H, region)
		case boardkanban.RegionCard:
			p.mouseHandler.HitMap.AddRect(regionKanbanCard, region.X, region.Y, region.W, region.H, region)
		}
	}
	return result.View
}

func (p *Plugin) kanbanShellByID(id string) *ShellSession {
	for _, shell := range p.shells {
		key := shell.TmuxName
		if key == "" {
			key = shell.Name
		}
		if id == "shell:"+key {
			return shell
		}
	}
	return nil
}

func (p *Plugin) kanbanWorktreeByID(id string) *Worktree {
	for _, wt := range p.worktrees {
		if id == "worktree:"+wt.IdentityKey() {
			return wt
		}
	}
	return nil
}

// renderKanbanShellCardLine renders a single line of a shell kanban card.
// lineIdx: 0=name, 1=status, 2-3=empty
func (p *Plugin) renderKanbanShellCardLine(shell *ShellSession, lineIdx, width int, isSelected bool) string {
	var content string
	resolvedStatus := shellAgentStatusPresentation(shell)
	switch lineIdx {
	case 0:
		statusIcon := "○"
		var statusStyle lipgloss.Style
		hasAnimatedActivity := false
		if resolvedStatus.Health {
			statusIcon = "◌"
		} else if icon, _, style, ok := p.animatedActivityPresentation(shell.Agent); ok {
			statusIcon, statusStyle, hasAnimatedActivity = icon, style, true
		} else if shell.Agent != nil {
			statusIcon = "●"
		}
		if hasAnimatedActivity && !isSelected {
			statusIcon = statusStyle.Render(statusIcon)
		}
		name := shell.Name
		maxNameLen := width - 3
		if runes := []rune(name); len(runes) > maxNameLen {
			name = string(runes[:maxNameLen-3]) + "..."
		}
		content = fmt.Sprintf(" %s %s", statusIcon, name)
	case 1:
		statusText := "  shell · no session"
		if resolvedStatus.Health {
			statusText = "  shell · offline"
		} else if _, text, _, ok := activityPresentation(shell.Agent); ok {
			agentName := shellAgentAbbreviations[shell.Agent.Type]
			if agentName == "" && shell.Agent != nil {
				agentName = string(shell.Agent.Type)
			}
			statusText = fmt.Sprintf("  %s · %s", agentName, text)
		} else if shell.Agent != nil {
			statusText = "  shell · running"
		}
		content = statusText
	}
	if lipgloss.Width(content) > width {
		content = ansi.Truncate(content, width, "")
	}
	if contentWidth := lipgloss.Width(content); contentWidth < width {
		content += strings.Repeat(" ", width-contentWidth)
	}
	if isSelected {
		return styles.ListItemSelected.Width(width).Render(content)
	}
	if lineIdx > 0 {
		return styles.Muted.Width(width).Render(content)
	}
	return lipgloss.NewStyle().Width(width).Render(content)
}

// renderKanbanCardLine renders a single line of a kanban card.
// lineIdx: 0=name, 1=agent, 2=task, 3=stats
func (p *Plugin) renderKanbanCardLine(wt *Worktree, lineIdx, width int, isSelected bool) string {
	var content string
	presentation := kanbanPresentationForWorktree(wt)
	var activityStyle lipgloss.Style
	hasAnimatedActivity := false
	if !presentation.health {
		if icon, _, style, ok := p.animatedActivityPresentation(wt.Agent); ok {
			presentation.icon, activityStyle, hasAnimatedActivity = icon, style, true
		}
	}
	switch lineIdx {
	case 0:
		icon := presentation.icon
		if hasAnimatedActivity && !isSelected {
			icon = activityStyle.Render(icon)
		}
		name := wt.Name
		maxNameLen := width - 3
		if runes := []rune(name); len(runes) > maxNameLen {
			name = string(runes[:maxNameLen-3]) + "..."
		}
		content = fmt.Sprintf(" %s %s", icon, name)
	case 1:
		agentStr := ""
		if wt.Agent != nil {
			agentStr = "  " + string(wt.Agent.Type)
		} else if wt.ChosenAgentType != "" && wt.ChosenAgentType != AgentNone {
			agentStr = "  " + string(wt.ChosenAgentType)
		}
		if presentation.health {
			agentStr = "  " + presentation.statusText
		} else if presentation.statusText != "" {
			agentStr += " · " + presentation.statusText
		}
		content = agentStr
	case 2:
		if wt.TaskID != "" {
			taskStr := wt.TaskID
			maxLen := width - 2
			if runes := []rune(taskStr); len(runes) > maxLen {
				taskStr = string(runes[:maxLen-3]) + "..."
			}
			content = "  " + taskStr
		}
	case 3:
		if wt.Stats != nil && (wt.Stats.Additions > 0 || wt.Stats.Deletions > 0) {
			content = fmt.Sprintf("  +%d -%d", wt.Stats.Additions, wt.Stats.Deletions)
		}
	}
	if contentWidth := lipgloss.Width(content); contentWidth < width {
		content += strings.Repeat(" ", width-contentWidth)
	}
	if isSelected {
		return styles.ListItemSelected.Width(width).Render(content)
	}
	if lineIdx > 0 {
		return styles.Muted.Width(width).Render(content)
	}
	return lipgloss.NewStyle().Width(width).Render(content)
}
