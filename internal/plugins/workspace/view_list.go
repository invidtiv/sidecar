package workspace

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentstatus"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// Modal style functions - return fresh styles using current theme colors.
func inputStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(styles.BorderNormal).
		Padding(0, 1)
}

func inputFocusedStyle() lipgloss.Style {
	return lipgloss.NewStyle().
		Border(lipgloss.NormalBorder()).
		BorderForeground(styles.Primary).
		Padding(0, 1)
}

// Panel dimension constants for consistent width calculations.
// These must stay in sync with styles.RenderGradientBorder.
const (
	panelBorderWidth  = 2                                    // Left + right border (1 each)
	panelPaddingWidth = 2                                    // Left + right padding (1 each)
	panelOverhead     = panelBorderWidth + panelPaddingWidth // Total overhead: 4
)

// View renders the plugin UI.
func (p *Plugin) View(width, height int) string {
	// Clear truncation cache if dimensions changed
	if p.width != width || p.height != height {
		p.truncateCache.Clear()
	}

	p.width = width
	p.height = height

	// CRITICAL: Clear hit regions at start of each render
	p.mouseHandler.Clear()

	switch p.viewMode {
	case ViewModeCreate:
		return p.renderCreateModal(width, height)
	case ViewModeKanban:
		return p.renderKanbanView(width, height)
	case ViewModeTaskLink:
		return p.renderTaskLinkModal(width, height)
	case ViewModeMerge:
		return p.renderMergeModal(width, height)
	case ViewModeAgentConfig:
		return p.renderAgentConfigModal(width, height)
	case ViewModeAgentChoice:
		return p.renderAgentChoiceModal(width, height)
	case ViewModeConfirmDelete:
		return p.renderConfirmDeleteModal(width, height)
	case ViewModeConfirmDeleteShell:
		return p.renderConfirmDeleteShellModal(width, height)
	case ViewModeCommitForMerge:
		return p.renderCommitForMergeModal(width, height)
	case ViewModePromptPicker:
		return p.renderPromptPickerModal(width, height)
	case ViewModeTypeSelector:
		return p.renderTypeSelectorModal(width, height)
	case ViewModeRenameShell:
		return p.renderRenameShellModal(width, height)
	case ViewModeFetchPR:
		return p.renderFetchPRModal(width, height)
	case ViewModeFilePicker:
		background := p.renderListView(width, height)
		return p.renderFilePickerModal(background)
	default:
		return p.renderListView(width, height)
	}
}

// registerPreviewTabRegions puts click targets over the Output/Diff/Task chips,
// taken from the same layout that drew them and registered only where they are
// drawn: a state with no tab row (shell, welcome guide, main worktree) gets no
// regions, and a chip the header dropped for want of columns gets none either.
//
// The row's width and its hint floor are read here rather than re-derived,
// because a second budget that merely ought to agree is how a dropped chip kept
// a region — one that sat on the interactive exit hint and exited interactive
// mode when clicked.
func (p *Plugin) registerPreviewTabRegions(split previewSplit) {
	if !p.previewTabsVisible() {
		return
	}
	// On the Output tab the chips are the terminal's own header row, laid out at
	// that surface's width and behind its hint floor; Diff and Task draw their
	// standalone tab row across the whole preview on the same first content row.
	row, width, hintFloor := p.previewContentY(), split.ContentWidth, 0
	if p.previewTab == PreviewTabOutput {
		if surface := p.terminalSurfaceGeometry(false); surface.OK {
			row, width = surface.HeaderY, surface.Width
		}
		if p.interactiveDescribes(false) {
			hintFloor = p.interactiveHintFloor()
		}
	}

	for i, placement := range layoutHeaderChips(p.previewTabChips(), width, hintFloor) {
		if !placement.Drawn {
			continue
		}
		p.mouseHandler.HitMap.AddRect(regionPreviewTab,
			split.ContentX+placement.Col, row, placement.Width, 1, i)
	}
}

// renderListView renders the main split-pane list view.
func (p *Plugin) renderListView(width, height int) string {
	// Pane height for panels (outer dimensions including borders)
	paneHeight := height
	if paneHeight < 4 {
		paneHeight = 4
	}

	// Inner content height (excluding borders and header lines)
	innerHeight := paneHeight - 2
	if innerHeight < 1 {
		innerHeight = 1
	}

	// The sidebar/divider/preview column arithmetic lives in one place so the
	// render path, the cursor path and the hit tests cannot drift (td-73fa86).
	split := p.previewSplitFor(width)

	// If sidebar is hidden, show only preview pane at full width
	if !p.sidebarVisible {
		// Register hit region for full-width preview (uses outer dimensions)
		p.mouseHandler.HitMap.AddRect(regionPreviewPane, 0, 0, split.PreviewWidth, paneHeight, nil)

		// Render content using calculated content width (consistent with panel overhead)
		previewContent := p.renderPreviewContent(split.ContentWidth, innerHeight)
		p.registerPreviewTabRegions(split)

		if p.previewFlashActive() {
			return styles.RenderPanelWithGradient(previewContent, split.PreviewWidth, paneHeight, styles.GetFlashGradient())
		}
		return styles.RenderPanel(previewContent, split.PreviewWidth, paneHeight, true)
	}

	sidebarW := split.SidebarWidth
	previewW := split.PreviewWidth
	sidebarContentW := split.SidebarContentWidth
	previewContentW := split.ContentWidth

	// Determine pane focus state
	sidebarActive := p.activePane == PaneSidebar
	previewActive := p.activePane == PanePreview

	// Register hit regions (order matters: last = highest priority)
	// 1. Pane regions (lowest priority - fallback for scroll)
	p.mouseHandler.HitMap.AddRect(regionSidebar, 0, 0, sidebarW, paneHeight, nil)
	p.mouseHandler.HitMap.AddRect(regionPreviewPane, split.PreviewX, 0, previewW, paneHeight, nil)

	// 2. Divider region (high priority - for drag)
	p.mouseHandler.HitMap.AddRect(regionPaneDivider, sidebarW, 0, dividerHitWidth, paneHeight, nil)

	// Render content for each pane using pre-calculated content widths
	sidebarContent := p.renderSidebarContent(sidebarContentW, innerHeight)
	previewContent := p.renderPreviewContent(previewContentW, innerHeight)

	// Preview tabs are registered after document bodies and their divider, so
	// the visible chips remain the highest-priority targets.
	p.registerPreviewTabRegions(split)

	flashActive := p.previewFlashActive()

	// Apply gradient border styles
	leftPane := styles.RenderPanel(sidebarContent, sidebarW, paneHeight, sidebarActive)

	var rightPane string
	if p.viewMode == ViewModeInteractive {
		// Use interactive gradient when in interactive mode (td-70aed9)
		rightPane = styles.RenderPanelWithGradient(previewContent, previewW, paneHeight, styles.GetInteractiveGradient())
	} else if flashActive && previewActive {
		rightPane = styles.RenderPanelWithGradient(previewContent, previewW, paneHeight, styles.GetFlashGradient())
	} else {
		rightPane = styles.RenderPanel(previewContent, previewW, paneHeight, previewActive)
	}

	// Render visible divider between panes
	divider := ui.RenderDivider(paneHeight)

	// Join horizontally
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, divider, rightPane)
}

// renderWorktreeItem renders a single worktree list item.
func (p *Plugin) renderWorktreeItem(wt *Worktree, selected bool, width int) string {
	// Keep selection visible even when preview pane is active (dimmed style)
	isSelected := selected
	isActiveFocus := selected && p.activePane == PaneSidebar

	// Status indicator - use special icon for main worktree
	var statusIcon string
	resolvedStatus := agentStatusPresentation(wt)
	activityIcon, activityText, activityStyle, hasActivity := p.animatedActivityPresentation(wt.Agent)
	if resolvedStatus.Health {
		hasActivity = false
	}
	if hasActivity {
		statusIcon = activityIcon
	} else if wt.IsMain {
		statusIcon = "◉" // Bullseye icon for main/primary worktree
	} else {
		statusIcon = wt.Status.Icon()
	}

	// Check for conflicts
	hasConflict := p.hasConflict(wt.IdentityKey(), p.conflicts)
	conflictIcon := ""
	if hasConflict {
		conflictIcon = " ⚠"
	}

	// Check for orphaned (session crashed)
	orphanedIcon := ""
	if wt.IsOrphaned {
		orphanedIcon = " ⚠"
	}

	// Check for PR
	hasPR := wt.PRURL != ""
	prIcon := ""
	if hasPR {
		prIcon = " PR"
	}

	// Name and time
	name := wt.Name
	// Strip repo prefix from non-main worktrees when configured
	if !wt.IsMain && p.ctx.Config != nil && p.ctx.Config.Plugins.Workspace.SidebarDisplay.HideRepoPrefix {
		repoName := filepath.Base(p.ctx.ProjectRoot)
		if repoName != "" && strings.HasPrefix(name, repoName+"-") {
			name = name[len(repoName)+1:]
		}
	}
	timeStr := formatRelativeTime(wt.UpdatedAt)

	// Calculate max name width to prevent wrapping
	// Line structure: " [icon] [name][prIcon][conflictIcon][orphanedIcon]  [time]"
	// Reserve: 4 (leading space + icon + space) + icons + time + 2 (min padding)
	iconWidth := 4 // " X " where X is status icon
	prWidth := 0
	if hasPR {
		prWidth = 3 // " PR"
	}
	conflictWidth := 0
	if hasConflict {
		conflictWidth = 2 // " ⚠"
	}
	orphanedWidth := 0
	if wt.IsOrphaned {
		orphanedWidth = 2 // " ⚠"
	}
	timeWidth := lipgloss.Width(timeStr)
	minPadding := 2
	maxNameWidth := width - iconWidth - prWidth - conflictWidth - orphanedWidth - timeWidth - minPadding
	if maxNameWidth < 8 {
		maxNameWidth = 8 // Minimum name width
	}
	// Truncate name if too long (use runes for proper Unicode handling)
	if lipgloss.Width(name) > maxNameWidth {
		name = ansi.Truncate(name, maxNameWidth, "…")
	}

	// Sidebar display settings
	var sdCfg config.SidebarDisplayConfig
	if p.ctx.Config != nil {
		sdCfg = p.ctx.Config.Plugins.Workspace.SidebarDisplay
	}

	// Stats if available
	statsStr := ""
	if !sdCfg.HideStats && wt.Stats != nil && (wt.Stats.Additions > 0 || wt.Stats.Deletions > 0) {
		statsStr = fmt.Sprintf("+%d -%d", wt.Stats.Additions, wt.Stats.Deletions)
	}

	// Build second line parts. Selected rows stay plain so ListItemSelected
	// can paint a uniform background; unselected rows use the themed agent chip.
	parts := p.worktreeStateLabels(wt)
	if wt.IsMain {
		// For root workspace, show branch name instead of agent
		parts = append(parts, wt.Branch)
	} else if !sdCfg.HideAgent {
		if wt.Agent != nil {
			parts = append(parts, styles.AgentLabel(string(wt.Agent.Type)))
		} else if wt.ChosenAgentType != "" && wt.ChosenAgentType != AgentNone {
			parts = append(parts, styles.AgentLabel(string(wt.ChosenAgentType)))
		} else {
			parts = append(parts, "—")
		}
	}
	if hasActivity {
		parts = append(parts, activityText)
	}
	if !sdCfg.HideTask && wt.TaskID != "" {
		parts = append(parts, wt.TaskID)
	}
	if statsStr != "" {
		parts = append(parts, statsStr)
	}
	if hasConflict {
		conflictFiles := p.getConflictingFiles(wt.IdentityKey(), p.conflicts)
		if len(conflictFiles) > 0 {
			parts = append(parts, fmt.Sprintf("⚠ %d overlapping dirty files", len(conflictFiles)))
		}
	}
	if wt.IsOrphaned {
		parts = append(parts, "⚠ session ended")
	}

	// When selected, use plain text to ensure consistent background
	if isSelected {
		// Build plain text lines
		line1 := fmt.Sprintf(" %s %s%s%s%s", statusIcon, name, prIcon, conflictIcon, orphanedIcon)
		line1Width := lipgloss.Width(line1)
		if line1Width < width-timeWidth-2 {
			line1 = line1 + strings.Repeat(" ", width-line1Width-timeWidth-1) + timeStr
		}
		line2 := "   " + strings.Join(parts, "  ")
		// Truncate line2 to prevent wrapping
		line2Width := lipgloss.Width(line2)
		if line2Width > width {
			if width > 1 {
				line2 = ansi.Truncate(line2, width, "…")
			}
			line2Width = width
		}
		// Pad line2 to full width for consistent background
		if line2Width < width {
			line2 = line2 + strings.Repeat(" ", width-line2Width)
		}
		content := line1 + "\n" + line2

		return workspacelist.ApplySelection(content, width, true, isActiveFocus)
	}

	// Not selected - use colored styles for visual interest
	var statusStyle lipgloss.Style
	if hasActivity {
		statusStyle = activityStyle
	} else if wt.IsMain {
		// Primary/cyan color for main worktree to stand out
		statusStyle = lipgloss.NewStyle().Foreground(styles.Primary)
	} else {
		switch wt.Status {
		case StatusActive:
			statusStyle = styles.StatusCompleted // Green
		case StatusWaiting:
			statusStyle = styles.StatusModified // Yellow/orange (warning)
		case StatusDone:
			statusStyle = styles.StatusCompleted // Green
		case StatusError:
			statusStyle = styles.StatusDeleted // Red
		default:
			statusStyle = styles.Muted // Gray for paused
		}
	}
	icon := statusStyle.Render(statusIcon)

	// Apply conflict style
	styledConflictIcon := ""
	if hasConflict {
		styledConflictIcon = styles.StatusModified.Render(" ⚠")
	}

	// Apply orphaned style (session ended)
	styledOrphanedIcon := ""
	if wt.IsOrphaned {
		styledOrphanedIcon = styles.StatusModified.Render(" ⚠")
	}

	// Apply PR style
	styledPRIcon := ""
	if hasPR {
		styledPRIcon = lipgloss.NewStyle().Foreground(styles.Secondary).Render(" PR")
	}

	// For non-selected, style parts individually — agent chip matches the
	// Agent Overview board (colour + raised fill via styles.RenderAgentChip).
	styledParts := make([]string, 0, len(parts)+4)
	for _, label := range p.worktreeStateLabels(wt) {
		styledParts = append(styledParts, dimText(label))
	}
	if wt.IsMain {
		// For root workspace, show branch name instead of agent
		styledParts = append(styledParts, wt.Branch)
	} else if !sdCfg.HideAgent {
		if wt.Agent != nil {
			styledParts = append(styledParts, styles.RenderAgentChip(string(wt.Agent.Type)))
		} else if wt.ChosenAgentType != "" && wt.ChosenAgentType != AgentNone {
			styledParts = append(styledParts, styles.RenderAgentChip(string(wt.ChosenAgentType)))
		} else {
			styledParts = append(styledParts, "—")
		}
	}
	if hasActivity {
		styledParts = append(styledParts, dimText(activityText))
	}
	if !sdCfg.HideTask && wt.TaskID != "" {
		styledParts = append(styledParts, wt.TaskID)
	}
	if statsStr != "" {
		styledParts = append(styledParts, statsStr)
	}
	if hasConflict {
		conflictFiles := p.getConflictingFiles(wt.IdentityKey(), p.conflicts)
		if len(conflictFiles) > 0 {
			styledParts = append(styledParts, styles.StatusModified.Render(fmt.Sprintf("⚠ %d dirty overlaps", len(conflictFiles))))
		}
	}
	if wt.IsOrphaned {
		styledParts = append(styledParts, styles.StatusModified.Render("⚠ session ended"))
	}

	// Build lines with styled elements
	line1 := fmt.Sprintf(" %s %s%s%s%s", icon, name, styledPRIcon, styledConflictIcon, styledOrphanedIcon)
	line1Width := ansi.StringWidth(line1)
	if line1Width < width-timeWidth-2 {
		line1 = line1 + strings.Repeat(" ", width-line1Width-timeWidth-1) + timeStr
	}
	line2 := "   " + strings.Join(styledParts, "  ")
	// Truncate line2 to prevent wrapping
	if ansi.StringWidth(line2) > width {
		line2 = ansi.Truncate(line2, width-1, "…")
	}

	content := line1 + "\n" + line2
	return styles.ListItemNormal.Width(width).Render(content)
}

func (p *Plugin) worktreeStateLabels(wt *Worktree) []string {
	if wt == nil {
		return nil
	}
	labels := make([]string, 0, 8)
	switch {
	case wt.IsMain:
		labels = append(labels, "main")
	case wt.IsBare:
		labels = append(labels, "bare")
	case wt.IsDetached || wt.Branch == "(detached)":
		labels = append(labels, "detached")
	default:
		labels = append(labels, "branch "+wt.Branch)
	}
	if wt.IsLocked {
		labels = append(labels, "locked · actions unavailable")
	}
	if wt.IsMissing {
		labels = append(labels, "folder missing · actions unavailable")
	} else if wt.IsPrunable {
		labels = append(labels, "prunable · actions unavailable")
	}
	if p.activeLifecycleOperationID != "" && ((p.mergeState != nil && p.mergeState.Worktree != nil && p.mergeState.Worktree.IdentityKey() == wt.IdentityKey()) || (p.createPlan != nil && p.createPlan.Path == wt.Path)) {
		labels = append(labels, "operation in progress")
	}
	if wt.SetupWarning != "" {
		labels = append(labels, "setup warning: "+wt.SetupWarning)
	}
	if wt.PRState != "" {
		labels = append(labels, "PR "+wt.PRState)
	} else if wt.PRURL != "" {
		labels = append(labels, "PR unavailable")
	}
	if wt.Changes != nil {
		switch wt.Changes.State {
		case LoadStateError:
			labels = append(labels, "diff error")
		case LoadStateTruncated:
			labels = append(labels, "diff truncated")
		default:
			if wt.Changes.Truncated {
				labels = append(labels, "diff truncated")
			}
		}
	}
	return labels
}

// renderShellEntryForSession renders a shell entry for a specific shell session.
func (p *Plugin) renderShellEntryForSession(shell *ShellSession, selected bool, width int) string {
	isActiveFocus := selected && p.activePane == PaneSidebar

	// Determine icon based on session state and agent status
	var statusIcon string
	var statusStyle lipgloss.Style

	// td-f88fdd: Handle orphaned shells (manifest entry exists but tmux session is gone)
	resolvedStatus := shellAgentStatusPresentation(shell)
	activityIcon, activityText, activityStyle, hasActivity := p.animatedActivityPresentation(shell.Agent)
	if resolvedStatus.Health {
		statusIcon = "◌" // Empty circle for orphaned
		statusStyle = styles.Muted
	} else if hasActivity {
		statusIcon = activityIcon
		statusStyle = activityStyle
	} else if shell.Agent != nil {
		// Live session with no semantic activity: process is up but not busy.
		// ◎ + green = "live" (quieter than working's filled ● / activity glyphs).
		// Reserve "running" for a future real workload signal (e.g. dev servers).
		statusIcon = "◎"
		statusStyle = styles.StatusCompleted // Green
	} else {
		statusIcon = "○"
		statusStyle = styles.Muted
	}

	// Use shell display name
	displayName := shell.Name

	// Second line: agent chip (overview style) + status. Plain text when
	// selected so the full-row selection background stays uniform; themed
	// chip when not selected.
	statusText := shellStatusLine(shell, resolvedStatus, hasActivity, activityText, selected)

	// Calculate layout
	maxNameWidth := width - 4 - 2 // icon + padding
	nameRunes := []rune(displayName)
	if len(nameRunes) > maxNameWidth {
		displayName = string(nameRunes[:maxNameWidth-1]) + "…"
	}

	// Build lines
	if selected {
		// Selected style
		line1 := fmt.Sprintf(" %s %s", statusIcon, displayName)
		line1Width := lipgloss.Width(line1)
		if line1Width < width {
			line1 = line1 + strings.Repeat(" ", width-line1Width)
		}
		line2 := "   " + statusText
		line2Width := lipgloss.Width(line2)
		if line2Width > width {
			line2Runes := []rune(line2)
			if width > 1 {
				line2 = string(line2Runes[:width-1]) + "…"
			}
			line2Width = width
		}
		if line2Width < width {
			line2 = line2 + strings.Repeat(" ", width-line2Width)
		}
		content := line1 + "\n" + line2

		return workspacelist.ApplySelection(content, width, true, isActiveFocus)
	}

	// Not selected - use styled icon
	icon := statusStyle.Render(statusIcon)
	line1 := fmt.Sprintf(" %s %s", icon, displayName)
	line2 := "   " + statusText
	// Truncate line2 to prevent wrapping
	if ansi.StringWidth(line2) > width {
		line2 = ansi.Truncate(line2, width-1, "…")
	}
	content := line1 + "\n" + line2
	return styles.ListItemNormal.Width(width).Render(content)
}

// shellStatusLine builds the agent + status second line for a shell entry.
// When selected is true the agent is plain AgentLabel text (selection paints
// the row). When false it uses the themed RenderAgentChip fill + colour.
func shellStatusLine(shell *ShellSession, resolvedStatus agentstatus.Presentation, hasActivity bool, activityText string, selected bool) string {
	agentChip := func(provider AgentType) string {
		if provider == AgentNone || provider == "" {
			return ""
		}
		if selected {
			// Icon + name only — ListItemSelected supplies the row fill.
			label := styles.AgentLabel(string(provider))
			if label == "" {
				return string(provider)
			}
			return label
		}
		if chip := styles.RenderAgentChip(string(provider)); chip != "" {
			return chip
		}
		return string(provider)
	}
	suffix := func(chip, status string) string {
		if chip == "" {
			if selected {
				return "shell · " + status
			}
			return dimText("shell · " + status)
		}
		if selected {
			return chip + " · " + status
		}
		return chip + dimText(" · "+status)
	}

	if resolvedStatus.Health {
		if shell.ChosenAgent != AgentNone && shell.ChosenAgent != "" {
			return suffix(agentChip(shell.ChosenAgent), "offline")
		}
		return suffix("", "offline")
	}
	if shell.Agent != nil {
		// Prefer the live type when Identify has a real provider. "shell" is a
		// demotion / discovery miss — fall back to ChosenAgent so a Cursor
		// session that launched as `agent`/`node` does not permanently read as
		// "shell · live" before the next screen-backed poll upgrades it.
		provider := shell.Agent.Type
		if provider == AgentShell || provider == AgentNone || provider == "" {
			provider = shell.ChosenAgent
		}
		chip := agentChip(provider)
		if chip == "" {
			// Live session with no identifiable agent type.
			if hasActivity {
				return suffix("", activityText)
			}
			return suffix("", "live")
		}
		if hasActivity {
			return suffix(chip, activityText)
		}
		// Live but quiet: "live", not "running" — see the ◎ icon rationale above.
		return suffix(chip, "live")
	}
	if shell.ChosenAgent != AgentNone && shell.ChosenAgent != "" {
		return suffix(agentChip(shell.ChosenAgent), "stopped")
	}
	return suffix("", "no session")
}

func activityPresentation(agent *Agent) (icon, text string, style lipgloss.Style, ok bool) {
	if agent == nil || !supportsAgentActivity(agent.Type) {
		return "", "", lipgloss.Style{}, false
	}
	p := agentstatus.Resolve(agentstatus.Input{ProviderSupported: true, Activity: agent.Activity, CapturedAt: agent.ActivityCapturedAt, Now: agent.ActivityCapturedAt, DoneTTL: agentstatus.DefaultDoneTTL})
	switch p.Lane {
	case agentstatus.LaneWorking:
		return p.Icon, p.Label, styles.StatusCompleted, true
	case agentstatus.LaneBlocked:
		return p.Icon, p.Label, styles.StatusModified, true
	case agentstatus.LaneDone:
		return p.Icon, p.Label, styles.StatusCompleted, true
	case agentstatus.LaneIdle:
		return p.Icon, p.Label, styles.Muted, true
	default:
		return p.Icon, p.Label, styles.Muted, true
	}
}
