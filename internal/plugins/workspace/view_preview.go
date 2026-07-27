package workspace

import (
	"fmt"
	"strings"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// renderPreviewContent renders the preview pane content (no borders).
func (p *Plugin) renderPreviewContent(width, height int) string {
	var lines []string

	// Show welcome guide only when no worktree AND no shell is selected
	wt := p.selectedWorktree()
	if wt == nil && !p.shellSelected {
		return p.truncateAllLines(p.renderWelcomeGuide(width, height), width)
	}

	// When shell is selected, show shell content directly without tabs
	// (Output/Diff/Task tabs are not relevant for the project shell)
	if p.shellSelected {
		var content string
		if p.termPanelVisible {
			content = p.renderShellWithTermPanel(width, height)
		} else {
			content = p.renderShellOutput(width, height)
		}
		content = p.prependFlashHint(content, width)
		return content
	}

	// Main worktree: show informational view instead of normal tabs
	if wt.IsMain {
		return p.truncateAllLines(p.renderMainWorktreeView(width, height), width)
	}

	// Tab header (only for worktrees, not shell)
	tabs := p.renderTabs(width)
	lines = append(lines, tabs)
	lines = append(lines, "") // Empty line after header

	contentHeight := height - 2 // header + empty line

	// Render content based on active tab
	var content string
	switch p.previewTab {
	case PreviewTabOutput:
		if p.termPanelVisible {
			content = p.renderOutputWithTermPanel(width, contentHeight)
		} else {
			content = p.renderOutputContent(width, contentHeight)
		}
	case PreviewTabDiff:
		content = p.renderDiffContent(width, contentHeight)
	case PreviewTabTask:
		content = p.renderTaskContent(width, contentHeight)
	}

	lines = append(lines, content)

	result := strings.Join(lines, "\n")
	result = p.prependFlashHint(result, width)
	if p.previewTab == PreviewTabOutput {
		// Terminal viewport renderers already expand tabs and truncate once.
		return result
	}
	return p.truncateAllLines(result, width)
}

// prependFlashHint adds an attach hint at the top of content when flash is active.
func (p *Plugin) prependFlashHint(content string, width int) string {
	if !p.flashPreviewTime.IsZero() && time.Since(p.flashPreviewTime) < flashDuration {
		hintStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(styles.GetCurrentTheme().Colors.Warning)).
			Bold(true)
		hint := p.truncateCache.Truncate(hintStyle.Render("Enter or double-click to attach"), width, "")
		return hint + "\n" + content
	}
	return content
}

// renderWelcomeGuide renders a helpful guide when no worktree is selected.
func (p *Plugin) renderWelcomeGuide(width, height int) string {
	var lines []string

	// Section Style
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.Primary)
	warningStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.Warning)

	// Check if tmux is installed
	if !isTmuxInstalled() {
		lines = append(lines, warningStyle.Render("⚠ tmux Required"))
		lines = append(lines, "")
		lines = append(lines, dimText("Workspaces and shell sessions require tmux to be installed."))
		lines = append(lines, "")
		lines = append(lines, sectionStyle.Render("Install tmux:"))
		lines = append(lines, dimText("  "+getTmuxInstallInstructions()))
		lines = append(lines, "")
		lines = append(lines, dimText("After installing, restart sidecar to use this feature."))
		return strings.Join(lines, "\n")
	}

	// Git Worktree Explanation
	lines = append(lines, sectionStyle.Render("Git Worktrees: A Better Workflow"))
	lines = append(lines, dimText("  • Parallel Development: Work on multiple branches simultaneously"))
	lines = append(lines, dimText("    in separate directories."))
	lines = append(lines, dimText("  • No Context Switching: Keep your editor/server running while"))
	lines = append(lines, dimText("    reviewing a PR or fixing a bug."))
	lines = append(lines, dimText("  • Isolated Environments: Each worktree has its own clean state,"))
	lines = append(lines, dimText("    unaffected by other changes."))
	lines = append(lines, "")
	lines = append(lines, strings.Repeat("─", min(width-4, 60)))
	lines = append(lines, "")

	// Title
	title := lipgloss.NewStyle().Bold(true).Render("tmux Quick Reference")
	lines = append(lines, title)
	lines = append(lines, "")

	// Section: Attaching to agent sessions
	prefix := getTmuxPrefix()
	lines = append(lines, sectionStyle.Render("Agent Sessions"))
	lines = append(lines, dimText("  Enter      Attach to selected worktree session"))
	lines = append(lines, dimText(fmt.Sprintf("  %s d   Detach from session (return here)", prefix)))
	lines = append(lines, "")

	// Section: Navigation inside tmux
	lines = append(lines, sectionStyle.Render("Scrolling (in attached session)"))
	lines = append(lines, dimText(fmt.Sprintf("  %s [        Enter scroll mode", prefix)))
	lines = append(lines, dimText("  PgUp/PgDn       Scroll page (fn+↑/↓ on Mac)"))
	lines = append(lines, dimText("  ↑/↓             Scroll line by line"))
	lines = append(lines, dimText("  q               Exit scroll mode"))
	lines = append(lines, "")

	// Section: Interacting with editors
	lines = append(lines, sectionStyle.Render("Editor Navigation"))
	lines = append(lines, dimText("  When agent opens vim/nano:"))
	lines = append(lines, dimText("    :q!      Quit vim without saving"))
	lines = append(lines, dimText("    :wq      Save and quit vim"))
	lines = append(lines, dimText("    Ctrl-x   Exit nano"))
	lines = append(lines, "")

	// Section: Common tasks
	lines = append(lines, sectionStyle.Render("Tips"))
	lines = append(lines, dimText("  • Create a worktree with 'n' to start"))
	lines = append(lines, dimText("  • Agent output streams in the Output tab"))
	lines = append(lines, dimText("  • Attach to interact with the agent directly"))
	lines = append(lines, "")
	lines = append(lines, dimText("Customize tmux: ~/.tmux.conf (man tmux for options)"))

	return strings.Join(lines, "\n")
}

// truncateAllLines ensures every line in the content is truncated to maxWidth.
// Optimized to use strings.Builder for reduced allocations.
func (p *Plugin) truncateAllLines(content string, maxWidth int) string {
	if maxWidth <= 0 {
		return content
	}

	var sb strings.Builder
	sb.Grow(len(content)) // Pre-allocate approximate size

	start := 0
	for i := 0; i <= len(content); i++ {
		if i == len(content) || content[i] == '\n' {
			line := content[start:i]
			line = ui.ExpandTabs(line, tabStopWidth)
			if lipgloss.Width(line) > maxWidth {
				line = p.truncateCache.Truncate(line, maxWidth, "")
			}
			if start > 0 {
				sb.WriteByte('\n')
			}
			sb.WriteString(line)
			start = i + 1
		}
	}
	return sb.String()
}

// renderTabs renders the preview pane tab header.
func (p *Plugin) renderTabs(width int) string {
	tabs := []string{"Output", "Diff", "Task"}
	var rendered []string

	for i, tab := range tabs {
		if PreviewTab(i) == p.previewTab {
			rendered = append(rendered, styles.RenderPillWithStyle(tab, styles.BarChipActive, nil))
		} else {
			rendered = append(rendered, styles.RenderPillWithStyle(tab, styles.BarChip, nil))
		}
	}

	return p.truncateCache.Truncate(strings.Join(rendered, " "), width, "")
}

func (p *Plugin) renderCapturedTerminal(hint string, buffer *tty.OutputBuffer, width, height int, termPanel bool, emptyText string) string {
	truncateHint := func(value string) string {
		return p.truncateCache.Truncate(ui.ExpandTabs(value, tabStopWidth), width, "")
	}
	truncateEmpty := func(value string) string {
		return p.truncateCache.Truncate(ui.ExpandTabs(dimText(value), tabStopWidth), width, "")
	}
	height-- // Reserve one line for the hint.
	if height < 1 {
		return truncateHint(hint)
	}
	if buffer == nil || buffer.LineCount() == 0 {
		return truncateHint(hint) + "\n" + truncateEmpty(emptyText)
	}

	interactive := p.viewMode == ViewModeInteractive &&
		p.interactiveState != nil &&
		p.interactiveState.Active &&
		p.interactiveState.TermPanel == termPanel
	var cursorRow, cursorCol, paneHeight, paneWidth int
	var cursorVisible bool
	if interactive {
		cursorRow, cursorCol, paneHeight, paneWidth, cursorVisible, _ = p.getCursorPosition()
		if p.interactiveState.MouseReportingEnabled {
			hint += " " + dimText("app mouse • ⇧drag select")
		}
	}

	follow := p.autoScrollOutput
	offset := p.previewOffset
	offsetFromBottom := false
	trimTrailing := p.autoScrollOutput && !interactive
	if termPanel {
		if p.selectionTermPanel && p.selection.Anchor.Valid() {
			follow = false
			offset = p.termPanelSelectionOffset
		} else {
			follow = p.termPanelScroll == 0
			offset = p.termPanelScroll
			offsetFromBottom = true
		}
		trimTrailing = !interactive
	}
	absoluteBase, totalItems, loadingOlder := p.terminalHistorySummary(termPanel, buffer)
	var selection *ui.SelectionState
	if p.selectionTermPanel == termPanel {
		selection = &p.selection
	}

	result := renderTerminalViewport(terminalViewportInput{
		Buffer:           buffer,
		Width:            width,
		Height:           height,
		Offset:           offset,
		OffsetFromBottom: offsetFromBottom,
		Follow:           follow,
		TrimTrailing:     trimTrailing,
		Interactive:      interactive,
		Selection:        selection,
		CursorRow:        cursorRow,
		CursorCol:        cursorCol,
		CursorVisible:    cursorVisible,
		PaneHeight:       paneHeight,
		PaneWidth:        paneWidth,
		NativeCursor:     interactive,
		AbsoluteBase:     absoluteBase,
		TotalItems:       totalItems,
		LoadingOlder:     loadingOlder,
		SearchMatches:    p.terminalSearchMatches(termPanel),
	}, p.truncateCache)
	if result.Content == "" {
		return truncateHint(hint) + "\n" + truncateEmpty(emptyText)
	}
	if linesBack := result.Layout.MaxOffset - result.Layout.Start; linesBack > 0 {
		hint += " " + dimText(fmt.Sprintf("▲ %d lines back • ⇧End live", linesBack))
	}
	if loadingOlder {
		hint += " " + dimText("loading older history…")
	} else if result.Layout.Start == 0 && absoluteBase > 0 {
		hint += " " + dimText(fmt.Sprintf("▲ %d older lines available", absoluteBase))
	}
	if p.terminalSearch.TermPanel == termPanel && p.terminalSearch.SourceKey != "" {
		if p.terminalSearch.InputActive {
			hint += " " + dimText("/"+p.terminalSearch.Query+"▌")
		} else if p.terminalSearch.Query != "" {
			if len(p.terminalSearch.Matches) == 0 {
				hint += " " + dimText("no matches")
			} else {
				hint += " " + dimText(fmt.Sprintf("%d/%d matches • n/N", p.terminalSearch.Current+1, len(p.terminalSearch.Matches)))
			}
		}
	}
	return truncateHint(hint) + "\n" + result.Content
}

// renderOutputContent renders agent output.
func (p *Plugin) renderOutputContent(width, height int) string {
	wt := p.selectedWorktree()
	if wt == nil {
		return p.truncateAllLines(dimText("No worktree selected"), width)
	}

	// Check for orphaned worktree (agent file exists but tmux session gone)
	if wt.IsOrphaned && wt.Agent == nil {
		return p.truncateAllLines(p.renderOrphanedMessage(wt.ChosenAgentType), width)
	}

	if wt.Agent == nil {
		return p.truncateAllLines(dimText("No agent running\nPress 's' to start an agent"), width)
	}

	// Hint depends on mode - interactive mode shows exit hints
	var hint string
	if p.viewMode == ViewModeInteractive && p.interactiveState != nil && p.interactiveState.Active && !p.interactiveState.TermPanel {
		// Interactive mode targeting this agent pane - show exit hint with highlight
		interactiveStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(styles.GetCurrentTheme().Colors.Warning)).
			Bold(true)
		hint = interactiveStyle.Render("INTERACTIVE") + " " + dimText(p.getInteractiveExitKey()+" exit • "+p.getInteractiveAttachKey()+" attach")
	} else if p.termPanelVisible && !p.termPanelFocused && p.activePane == PanePreview {
		// Terminal panel visible and agent sub-pane is focused
		focusStyle := lipgloss.NewStyle().
			Foreground(styles.Primary).
			Bold(true)
		hint = focusStyle.Render("▸ Agent") + " " + dimText("enter interactive")
	} else if p.termPanelVisible && p.termPanelFocused && p.activePane == PanePreview {
		// Terminal panel visible but terminal sub-pane is focused
		hint = dimText("Agent")
	} else {
		// Only show "E for interactive" hint if feature flag is enabled
		detach := getTmuxDetachHint()
		if features.IsEnabled(features.TmuxInteractiveInput.Name) {
			hint = dimText(fmt.Sprintf("t to attach • E for interactive • %s to detach", detach))
		} else {
			hint = dimText(fmt.Sprintf("t to attach • %s to detach", detach))
		}
	}
	return p.renderCapturedTerminal(hint, wt.Agent.OutputBuf, width, height, false, "No output yet")
}

// renderOrphanedMessage renders the recovery prompt for orphaned worktrees.
func (p *Plugin) renderOrphanedMessage(agentType AgentType) string {
	var lines []string

	// Warning header
	warningStyle := lipgloss.NewStyle().
		Bold(true).
		Foreground(lipgloss.Color(styles.GetCurrentTheme().Colors.Warning))

	lines = append(lines, warningStyle.Render("Session Ended"))
	lines = append(lines, "")
	lines = append(lines, dimText("The tmux session has ended, but your worktree and work are still intact."))
	lines = append(lines, "")

	// Show previously running agent
	agentName := AgentDisplayNames[agentType]
	if agentName == "" {
		agentName = string(agentType)
	}
	lines = append(lines, dimText(fmt.Sprintf("Previously running: %s", agentName)))
	lines = append(lines, "")

	// Action prompt
	actionStyle := lipgloss.NewStyle().
		Foreground(styles.Primary)
	lines = append(lines, actionStyle.Render("Press Enter to start a new session"))

	return strings.Join(lines, "\n")
}

// renderShellOutput renders the selected shell's output.
func (p *Plugin) renderShellOutput(width, height int) string {
	// Get the selected shell
	shell := p.getSelectedShell()
	if shell == nil || shell.Agent == nil {
		return p.truncateAllLines(p.renderShellPrimer(width, height), width)
	}

	// Hint depends on mode - interactive mode shows exit hints
	var hint string
	if p.viewMode == ViewModeInteractive && p.interactiveState != nil && p.interactiveState.Active && !p.interactiveState.TermPanel {
		// Interactive mode targeting this shell pane
		interactiveStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(styles.GetCurrentTheme().Colors.Warning)).
			Bold(true)
		hint = interactiveStyle.Render("INTERACTIVE") + " " + dimText(p.getInteractiveExitKey()+" exit")
	} else if p.termPanelVisible && !p.termPanelFocused && p.activePane == PanePreview {
		focusStyle := lipgloss.NewStyle().
			Foreground(styles.Primary).
			Bold(true)
		hint = focusStyle.Render("▸ Shell") + " " + dimText("enter interactive")
	} else if p.termPanelVisible && p.termPanelFocused && p.activePane == PanePreview {
		hint = dimText("Shell")
	} else {
		// Only show "E for interactive" hint if feature flag is enabled
		detach := getTmuxDetachHint()
		if features.IsEnabled(features.TmuxInteractiveInput.Name) {
			hint = dimText(fmt.Sprintf("t to attach • E for interactive • %s to detach", detach))
		} else {
			hint = dimText(fmt.Sprintf("t to attach • %s to detach", detach))
		}
	}
	return p.renderCapturedTerminal(hint, shell.Agent.OutputBuf, width, height, false, "No output yet")
}

func padLinesToHeight(lines []string, target int) []string {
	if target <= 0 || len(lines) >= target {
		return lines
	}
	for len(lines) < target {
		lines = append(lines, "")
	}
	return lines
}

// renderShellPrimer renders a helpful guide when no shell session exists.
func (p *Plugin) renderShellPrimer(width, height int) string {
	var lines []string

	// Section style
	sectionStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.Primary)
	warningStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.Warning)

	// Check if tmux is installed
	if !isTmuxInstalled() {
		lines = append(lines, warningStyle.Render("⚠ tmux Required"))
		lines = append(lines, "")
		lines = append(lines, dimText("The project shell requires tmux to be installed."))
		lines = append(lines, "")
		lines = append(lines, sectionStyle.Render("Install tmux:"))
		lines = append(lines, dimText("  "+getTmuxInstallInstructions()))
		lines = append(lines, "")
		lines = append(lines, dimText("After installing, restart sidecar to use this feature."))
		return strings.Join(lines, "\n")
	}

	// Title
	lines = append(lines, sectionStyle.Render("Project Shell"))
	lines = append(lines, "")

	// Description
	lines = append(lines, dimText("A tmux session in your project directory for running"))
	lines = append(lines, dimText("builds, dev servers, or quick terminal tasks."))
	lines = append(lines, "")

	// Quick start
	prefix := getTmuxPrefix()
	lines = append(lines, sectionStyle.Render("Quick Start"))
	lines = append(lines, dimText("  Enter         Create and attach to shell"))
	lines = append(lines, dimText("  K             Kill shell session"))
	lines = append(lines, dimText(fmt.Sprintf("  %s d      Detach (return to sidecar)", prefix)))
	lines = append(lines, "")
	lines = append(lines, strings.Repeat("─", min(width-4, 50)))
	lines = append(lines, "")

	// Shell vs Worktrees explanation
	lines = append(lines, sectionStyle.Render("Shell vs Worktrees"))
	lines = append(lines, "")
	lines = append(lines, dimText("Shell: A single terminal in your project root."))
	lines = append(lines, dimText("  Use for dev servers, builds, quick commands."))
	lines = append(lines, "")
	lines = append(lines, dimText("Workspaces: Separate git working directories, each"))
	lines = append(lines, dimText("  with its own branch. Use for parallel development"))
	lines = append(lines, dimText("  or running AI agents on isolated tasks."))
	lines = append(lines, "")

	// How to create worktree
	lines = append(lines, sectionStyle.Render("Create a Worktree"))
	lines = append(lines, dimText("  Press 'n' or click New in the sidebar"))

	return strings.Join(lines, "\n")
}

// renderMainWorktreeView renders a helpful view when the main worktree is selected.
func (p *Plugin) renderMainWorktreeView(width, height int) string {
	var lines []string

	titleStyle := lipgloss.NewStyle().Bold(true).Foreground(styles.Primary)
	hintStyle := lipgloss.NewStyle().Foreground(styles.Success)

	lines = append(lines, "")
	lines = append(lines, titleStyle.Render("Main Worktree"))
	lines = append(lines, "")
	lines = append(lines, dimText("This is your primary working directory."))
	lines = append(lines, dimText("Workspaces branch off from here as isolated environments."))
	lines = append(lines, "")
	lines = append(lines, strings.Repeat("─", min(width-4, 50)))
	lines = append(lines, "")
	lines = append(lines, hintStyle.Render("Press 'n' to create a new workspace"))
	lines = append(lines, "")
	lines = append(lines, dimText("Each workspace gets its own directory, branch, and"))
	lines = append(lines, dimText("optional AI agent. Work on multiple features in"))
	lines = append(lines, dimText("parallel without switching branches."))
	lines = append(lines, "")
	lines = append(lines, hintStyle.Render("Press 'ctrl+n' for a new shell"))
	lines = append(lines, "")
	lines = append(lines, dimText("Shells are plain terminals in this directory, for"))
	lines = append(lines, dimText("quick tasks that don't need their own workspace."))

	return strings.Join(lines, "\n")
}

// renderTaskContent renders linked task info.
func (p *Plugin) renderTaskContent(width, height int) string {
	wt := p.selectedWorktree()
	if wt == nil {
		return dimText("No worktree selected")
	}

	if wt.TaskID == "" {
		return dimText("No linked task\nPress 't' to link a task")
	}

	// Check if we're loading or don't have cached details for this task
	if p.taskLoading || p.cachedTask == nil || p.cachedTaskID != wt.TaskID {
		return dimText(fmt.Sprintf("Loading task %s...", wt.TaskID))
	}

	task := p.cachedTask
	var lines []string

	// Mode indicator
	modeHint := dimText("[m] raw")
	if p.taskMarkdownMode {
		modeHint = dimText("[m] rendered")
	}

	// Header
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render(fmt.Sprintf("Task: %s", task.ID))+"  "+modeHint)

	// Status and priority
	statusLine := fmt.Sprintf("Status: %s", task.Status)
	if task.Priority != "" {
		statusLine += fmt.Sprintf("  Priority: %s", task.Priority)
	}
	if task.Type != "" {
		statusLine += fmt.Sprintf("  Type: %s", task.Type)
	}
	lines = append(lines, statusLine)
	lines = append(lines, strings.Repeat("─", min(width-4, 60)))
	lines = append(lines, "")

	// Title
	lines = append(lines, lipgloss.NewStyle().Bold(true).Render(task.Title))
	lines = append(lines, "")

	// Markdown rendering for description and acceptance
	if p.taskMarkdownMode && p.markdownRenderer != nil {
		// Build markdown content
		var mdContent strings.Builder
		if task.Description != "" {
			mdContent.WriteString(task.Description)
			mdContent.WriteString("\n\n")
		}
		if task.Acceptance != "" {
			mdContent.WriteString("## Acceptance Criteria\n\n")
			mdContent.WriteString(task.Acceptance)
		}

		// Check if we need to re-render (width changed or cache empty)
		if p.taskMarkdownWidth != width || len(p.taskMarkdownRendered) == 0 {
			p.taskMarkdownRendered = p.markdownRenderer.RenderContent(mdContent.String(), width-4)
			p.taskMarkdownWidth = width
		}

		// Append rendered lines
		lines = append(lines, p.taskMarkdownRendered...)
	} else {
		// Plain text fallback
		if task.Description != "" {
			wrapped := wrapText(task.Description, width-4)
			lines = append(lines, wrapped)
			lines = append(lines, "")
		}

		if task.Acceptance != "" {
			lines = append(lines, lipgloss.NewStyle().Bold(true).Render("Acceptance Criteria:"))
			wrapped := wrapText(task.Acceptance, width-4)
			lines = append(lines, wrapped)
			lines = append(lines, "")
		}
	}

	// Timestamps (dimmed)
	lines = append(lines, "")
	if task.CreatedAt != "" {
		lines = append(lines, dimText(fmt.Sprintf("Created: %s", task.CreatedAt)))
	}
	if task.UpdatedAt != "" {
		lines = append(lines, dimText(fmt.Sprintf("Updated: %s", task.UpdatedAt)))
	}

	// Track total line count for scroll clamping
	p.taskRenderedLineCount = len(lines)

	// Apply scroll offset (unified: previewOffset = line from top)
	if p.previewOffset > 0 && p.previewOffset < len(lines) {
		lines = lines[p.previewOffset:]
	} else if p.previewOffset >= len(lines) {
		// Clamp: offset past content, show last page
		if len(lines) > height {
			lines = lines[len(lines)-height:]
		}
	}

	// Trim to visible height
	if len(lines) > height {
		lines = lines[:height]
	}

	return strings.Join(lines, "\n")
}
