package workspace

import (
	"fmt"
	"image/color"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/styles"
)

const activityAnimationInterval = 175 * time.Millisecond

// Working breathes evenly. Blocked uses two brighter beats followed by a
// longer rest so it attracts attention without making the whole row blink.
var (
	workingPulse = []float64{1.00, 0.96, 0.87, 0.75, 0.62, 0.50, 0.42, 0.38, 0.42, 0.50, 0.62, 0.75, 0.87, 0.96}
	blockedPulse = []float64{1.00, 0.78, 0.52, 0.88, 1.00, 0.68, 0.43, 0.38, 0.38, 0.38, 0.38, 0.38, 0.43, 0.52, 0.68, 0.84}
)

type activityAnimationTickMsg struct {
	generation uint64
}

func (p *Plugin) animatedActivityPresentation(agent *Agent) (icon, text string, style lipgloss.Style, ok bool) {
	icon, text, style, ok = activityPresentation(agent)
	if !ok || agent == nil {
		return icon, text, style, ok
	}

	switch agent.Activity.DisplayState() {
	case string(agentactivity.StateWorking):
		level := pulseLevel(workingPulse, p.activityAnimationFrame)
		return pulseCircle(level), text, pulseStyle(styles.Success, level, false), true
	case string(agentactivity.StateBlocked):
		level := pulseLevel(blockedPulse, p.activityAnimationFrame)
		return pulseDiamond(level), text, pulseStyle(styles.Warning, level, true), true
	default:
		return icon, text, style, ok
	}
}

func pulseLevel(levels []float64, frame int) float64 {
	if len(levels) == 0 {
		return 1
	}
	if frame < 0 {
		frame = -frame
	}
	return levels[frame%len(levels)]
}

func pulseCircle(level float64) string {
	switch {
	case level >= 0.84:
		return "●"
	case level >= 0.56:
		return "•"
	default:
		return "∙"
	}
}

func pulseDiamond(level float64) string {
	if level >= 0.72 {
		return "◆"
	}
	return "◇"
}

func pulseStyle(base color.Color, level float64, bold bool) lipgloss.Style {
	// Blend toward the theme's muted text rather than a hard-coded background;
	// this keeps every frame legible in both light and dark themes.
	fg := blendColor(styles.TextMuted, base, level)
	return lipgloss.NewStyle().Foreground(fg).Bold(bold)
}

func blendColor(low, high color.Color, amount float64) color.Color {
	if amount < 0 {
		amount = 0
	} else if amount > 1 {
		amount = 1
	}
	lr, lg, lb, _ := low.RGBA()
	hr, hg, hb, _ := high.RGBA()
	blend := func(a, b uint32) uint8 {
		return uint8((float64(a>>8)*(1-amount) + float64(b>>8)*amount) + 0.5)
	}
	return lipgloss.Color(fmt.Sprintf("#%02X%02X%02X", blend(lr, hr), blend(lg, hg), blend(lb, hb)))
}

func (p *Plugin) activityAnimationNeeded() bool {
	if !p.focused || !p.applicationFocused || (p.viewMode != ViewModeList && p.viewMode != ViewModeKanban) {
		return false
	}
	needsAnimation := func(agent *Agent) bool {
		if agent == nil || !supportsAgentActivity(agent.Type) {
			return false
		}
		state := agent.Activity.DisplayState()
		return state == string(agentactivity.StateWorking) || state == string(agentactivity.StateBlocked)
	}
	worktreeNeedsAnimation := func(wt *Worktree) bool {
		return wt != nil && !wt.IsMissing && !wt.IsOrphaned && needsAnimation(wt.Agent)
	}
	shellNeedsAnimation := func(shell *ShellSession) bool {
		return shell != nil && !shell.IsOrphaned && needsAnimation(shell.Agent)
	}

	effectiveView := p.viewMode
	if effectiveView == ViewModeKanban && kanbanUsesListFallback(p.width) {
		effectiveView = ViewModeList
	}

	switch effectiveView {
	case ViewModeList:
		if !p.sidebarVisible {
			return false
		}
		// The current list renderer shows every shell, then a scrollable slice
		// of worktrees. Match that projection so hidden worktrees cannot keep
		// the animation clock alive.
		for _, shell := range p.shells {
			if shellNeedsAnimation(shell) {
				return true
			}
		}
		start := p.scrollOffset
		if start < 0 {
			start = 0
		}
		end := start + p.visibleCount
		if end > len(p.worktrees) {
			end = len(p.worktrees)
		}
		for i := start; i < end; i++ {
			if worktreeNeedsAnimation(p.worktrees[i]) {
				return true
			}
		}
	case ViewModeKanban:
		limit := kanbanVisibleCardCount(p.height)
		for i := 0; i < len(p.shells) && i < limit; i++ {
			if shellNeedsAnimation(p.shells[i]) {
				return true
			}
		}
		columns := p.getKanbanColumns()
		for _, lane := range kanbanLaneOrder {
			items := columns[lane]
			for i := 0; i < len(items) && i < limit; i++ {
				if worktreeNeedsAnimation(items[i]) {
					return true
				}
			}
		}
	}
	return false
}

func (p *Plugin) startActivityAnimation() tea.Cmd {
	if p.activityAnimationScheduled || !p.activityAnimationNeeded() {
		return nil
	}
	p.activityAnimationScheduled = true
	generation := p.activityAnimationGeneration
	return tea.Tick(activityAnimationInterval, func(time.Time) tea.Msg {
		return activityAnimationTickMsg{generation: generation}
	})
}

func appendActivityAnimationCmd(cmds []tea.Cmd, cmd tea.Cmd) []tea.Cmd {
	if cmd != nil {
		return append(cmds, cmd)
	}
	return cmds
}
