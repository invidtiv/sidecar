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
	// Pre-styled content (agent chip) must not be re-wrapped in Muted /
	// CardSelected or the chip colours are wiped. finishKanbanCardLine
	// pads and applies selection background only.
	preStyled := false
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
			preStyled = true
		}
		name := shell.Name
		maxNameLen := width - 3
		if runes := []rune(name); len(runes) > maxNameLen {
			name = string(runes[:maxNameLen-3]) + "..."
		}
		content = fmt.Sprintf(" %s %s", statusIcon, name)
	case 1:
		if resolvedStatus.Health {
			content = "  shell · offline"
		} else if _, text, _, ok := activityPresentation(shell.Agent); ok && shell.Agent != nil {
			content = "  " + kanbanAgentStatus(string(shell.Agent.Type), text, isSelected)
			preStyled = !isSelected
		} else if shell.Agent != nil {
			provider := string(shell.Agent.Type)
			if provider != "" && provider != string(AgentNone) {
				content = "  " + kanbanAgentStatus(provider, "running", isSelected)
				preStyled = !isSelected
			} else {
				content = "  shell · running"
			}
		} else {
			content = "  shell · no session"
		}
	}
	return finishKanbanCardLine(content, width, isSelected, preStyled, lineIdx > 0)
}

// renderKanbanCardLine renders a single line of a kanban card.
// lineIdx: 0=name, 1=agent, 2=task, 3=stats
func (p *Plugin) renderKanbanCardLine(wt *Worktree, lineIdx, width int, isSelected bool) string {
	var content string
	preStyled := false
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
			preStyled = true
		}
		name := wt.Name
		maxNameLen := width - 3
		if runes := []rune(name); len(runes) > maxNameLen {
			name = string(runes[:maxNameLen-3]) + "..."
		}
		content = fmt.Sprintf(" %s %s", icon, name)
	case 1:
		if presentation.health {
			content = "  " + presentation.statusText
		} else {
			provider := ""
			if wt.Agent != nil {
				provider = string(wt.Agent.Type)
			} else if wt.ChosenAgentType != "" && wt.ChosenAgentType != AgentNone {
				provider = string(wt.ChosenAgentType)
			}
			if provider != "" {
				content = "  " + kanbanAgentStatus(provider, presentation.statusText, isSelected)
				preStyled = !isSelected
			} else if presentation.statusText != "" {
				content = "  " + presentation.statusText
			}
		}
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
	return finishKanbanCardLine(content, width, isSelected, preStyled, lineIdx > 0)
}

// kanbanAgentStatus composes the agent chip + optional status text for a
// kanban card line. Unselected rows use the themed chip (colour + raised
// fill); selected rows use plain AgentLabel so CardSelected can paint the row.
func kanbanAgentStatus(provider, status string, selected bool) string {
	var agent string
	if selected {
		agent = styles.AgentLabel(provider)
	} else {
		agent = styles.RenderAgentChip(provider)
	}
	if agent == "" {
		return status
	}
	if status == "" {
		return agent
	}
	if selected {
		return agent + " · " + status
	}
	return agent + styles.Muted.Render(" · "+status)
}

// finishKanbanCardLine pads a card line to width and applies selection or
// muted styling. When preStyled is true the content already carries ANSI
// colour (agent chips, activity icons) so we must not re-wrap it in a
// Foreground style that would wipe those colours — only pad, and for
// selection apply a background-only style.
func finishKanbanCardLine(content string, width int, selected, preStyled, mute bool) string {
	if lipgloss.Width(content) > width {
		content = ansi.Truncate(content, width, "")
	}
	if contentWidth := lipgloss.Width(content); contentWidth < width {
		content += strings.Repeat(" ", width-contentWidth)
	}
	if selected {
		if preStyled {
			// Background only — preserve chip / activity foregrounds.
			return lipgloss.NewStyle().
				Background(styles.CardSelected.GetBackground()).
				Width(width).
				Render(content)
		}
		return styles.CardSelected.Width(width).Render(content)
	}
	if preStyled {
		return content
	}
	if mute {
		return styles.Muted.Width(width).Render(content)
	}
	return lipgloss.NewStyle().Width(width).Render(content)
}
