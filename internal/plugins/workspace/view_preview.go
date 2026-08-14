package workspace

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacediff"
)

// renderPreviewContent renders the preview pane content (no borders).
func (p *Plugin) renderPreviewContent(width, height int) string {
	if content, ok := p.renderDocumentSplit(width, height); ok {
		return content
	}
	return p.renderPreviewContentLegacy(width, height)
}

func (p *Plugin) renderPreviewContentLegacy(width, height int) string {
	var lines []string

	// Show welcome guide only when no worktree AND no shell is selected
	wt := p.selectedWorktree()
	if wt == nil && !p.selectingShell() {
		return p.truncateAllLines(p.renderWelcomeGuide(width, height), width)
	}

	// When shell is selected, show shell content directly without tabs
	// (Output/Diff/Task tabs are not relevant for the project shell). The shell's
	// name is the left region of the terminal's own header row instead.
	if p.selectingShell() {
		if p.termPanelVisible {
			return p.renderShellWithTermPanel(width, height)
		}
		return p.renderShellOutput(width, height)
	}

	// Main worktree: show informational view instead of normal tabs
	if wt.IsMain {
		return p.truncateAllLines(p.renderMainWorktreeView(width, height), width)
	}

	// The Output tab is a terminal surface, and a terminal surface owns its own
	// header row: the tab chips are its left region, so there is no standalone
	// tab row and the terminal starts on the row below the panel border.
	if p.previewTab == PreviewTabOutput {
		if p.termPanelVisible {
			// Terminal viewport renderers already expand tabs and truncate once.
			return p.renderOutputWithTermPanel(width, height)
		}
		return p.renderOutputContent(width, height)
	}

	// Diff and Task are not terminals, so they keep the standalone tab row and
	// the blank spacer under it.
	lines = append(lines, p.renderTabs(width))
	lines = append(lines, "")

	contentHeight := height - previewTabRows

	var content string
	switch p.previewTab {
	case PreviewTabDiff:
		content = p.renderDiffContent(width, contentHeight)
	case PreviewTabTask:
		content = p.renderTaskContent(width, contentHeight)
	}
	lines = append(lines, content)

	return p.truncateAllLines(strings.Join(lines, "\n"), width)
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

// previewTabChips renders the Output / Diff / Task pills as separate chips, so
// the header row can drop whole chips rather than clip one in half.
func (p *Plugin) previewTabChips() []string {
	return workspacediff.TabChips(workspacediff.Tab(p.previewTab))
}

// previewTabsVisible reports whether the preview is in a state that draws the
// Output/Diff/Task chips at all. The shell has no tabs, and neither the welcome
// guide nor the main-worktree view is a tab; anything else — including the
// Output tab's no-agent and orphaned states — puts them on its first row.
func (p *Plugin) previewTabsVisible() bool {
	if p.selectingShell() {
		return false
	}
	wt := p.selectedWorktree()
	return wt != nil && workspacediff.TabsVisible(false, wt.IsMain)
}

// renderTabs renders the standalone tab row the Diff and Task tabs still use.
// It goes through the same header layout the Output tab's chips do, so the tab
// hit regions describe this row too.
func (p *Plugin) renderTabs(width int) string {
	return p.terminalHeader(p.previewTabChips(), "", width, 0)
}

// paneFocusChip renders a sub-pane's identity chip, marked when that sub-pane
// holds focus. It is the left region of the surface's header row.
func (p *Plugin) paneFocusChip(label string, focused bool) string {
	if focused {
		return lipgloss.NewStyle().Foreground(styles.Primary).Bold(true).Render("▸ " + label)
	}
	return dimText(label)
}

func (p *Plugin) primaryTerminalFocused() bool {
	if p.activePane != PanePreview || p.termPanelFocused {
		return false
	}
	if _, doc := p.activeDocPane(); doc != nil {
		return p.paneFocus == terminalLeafID(p.paneRoot)
	}
	return p.termPanelVisible
}

func (p *Plugin) primaryTerminalFocusVisible() bool {
	_, doc := p.activeDocPane()
	return p.termPanelVisible || doc != nil
}

// terminalHeader renders a surface's single header row at the plugin's
// truncation settings. hintFloor is the columns the right region keeps at the
// chips' expense; zero leaves the chips first in line.
func (p *Plugin) terminalHeader(chips []string, hints string, width, hintFloor int) string {
	return terminalHeaderRow(chips, ui.ExpandTabs(hints, tabStopWidth), width, hintFloor,
		func(value string, max int) string {
			return p.truncateCache.Truncate(value, max, "")
		})
}

// interactiveExitHint is the header hint that must survive at any width: the
// mode label and the key that leaves it. Callers put it at the head of their
// hint string and pass its width as the header's hint floor, so a narrow pane
// drops tab chips rather than the way out of interactive mode.
func (p *Plugin) interactiveExitHint() string {
	interactiveStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color(styles.GetCurrentTheme().Colors.Warning)).
		Bold(true)
	return interactiveStyle.Render("INTERACTIVE") + " " + dimText(p.getInteractiveExitKey()+" exit")
}

// interactiveHintFloor is the columns interactiveExitHint needs to stay whole.
func (p *Plugin) interactiveHintFloor() int {
	return ansi.StringWidth(p.interactiveExitHint())
}

// terminalLiveEdgeKey is the chord this surface answers to put a scrolled-back
// window back on the live edge, in the state the pane is in.
//
// While the pane is live every unshifted key belongs to it, so the way back is
// the shifted chord the component's key hook maps. A watched pane never reaches
// that hook — its keys are the surface's own — and answers the plain
// jump-to-bottom key instead. Naming the shifted chord there would advertise a
// key nothing answers, which reads as a pane stuck in history.
func terminalLiveEdgeKey(interactive bool) string {
	if interactive {
		return tty.LiveEdgeKey
	}
	return watchedLiveEdgeKey
}

// watchedLiveEdgeKey is the jump-to-bottom key the preview answers while the
// pane is only being watched (keys.go, "G").
const watchedLiveEdgeKey = "G"

// renderCapturedTerminal draws one embedded terminal: its header row — identity
// chips left, hints right — and then the viewport, which runs from the next row
// to the bottom of the surface.
func (p *Plugin) renderCapturedTerminal(chips []string, hint string, buffer *tty.OutputBuffer, width, height int, termPanel bool, emptyText string) string {
	if projected := p.projectedTerminalBuffer(termPanel); projected != nil {
		buffer = projected
	}
	interactive := p.interactiveDescribes(termPanel)
	// While interactive the exit key leads the hints and is what the row must
	// keep; the chips give way for it instead of the other way round.
	hintFloor := 0
	if interactive {
		hintFloor = p.interactiveHintFloor()
	}

	// The attach flash lives in the right region rather than on a row of its
	// own, so flashing never costs the terminal a row. Hints are clipped from
	// the right, so it leads the region — except while interactive, where the
	// exit key holds that spot and the flash follows it.
	if p.previewFlashActive() {
		flashStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(styles.GetCurrentTheme().Colors.Warning)).
			Bold(true)
		flash := flashStyle.Render("Enter or double-click to attach")
		switch {
		case hint == "":
			hint = flash
		case interactive:
			hint += " " + flash
		default:
			hint = flash + " " + hint
		}
	}
	truncateEmpty := func(value string) string {
		return p.truncateCache.Truncate(ui.ExpandTabs(dimText(value), tabStopWidth), width, "")
	}
	height -= terminalHeaderRows // The header occupies the surface's first row.
	if height < 1 {
		return p.terminalHeader(chips, hint, width, hintFloor)
	}
	if buffer == nil || buffer.LineCount() == 0 {
		return p.terminalHeader(chips, hint, width, hintFloor) + "\n" + truncateEmpty(emptyText)
	}

	// The window itself — the buffer, the pane geometry it is fitted to, and
	// where it sits in scrollback — is the surface's one derivation, shared with
	// hit testing and the native cursor. Only decoration is added here.
	input := p.terminalWindowInput(termPanel, buffer, width, height)
	input.NativeCursor = interactive
	if p.selectionTermPanel == termPanel {
		input.Selection = &p.selection
	}
	input.SearchMatches = p.terminalSearchMatches(termPanel)
	input.LinkResolver = p.terminalLinkResolver(termPanel, buffer)
	result := renderTerminalViewport(input, p.truncateCache)
	if result.Content == "" {
		return p.terminalHeader(chips, hint, width, hintFloor) + "\n" + truncateEmpty(emptyText)
	}
	// What the header states about the drawn window — that it is off the live
	// edge and how to get back, that older lines exist above it, that the pane is
	// clipped — is the shared derivation's. The global browser states the same
	// facts from the same one. This header clips from the right rather than
	// dropping notes, so it takes them all.
	hint = tty.AppendStatus(hint, tty.WindowStatus(tty.WindowStatusInput{
		Layout:         result.Layout,
		AbsoluteBase:   input.AbsoluteBase,
		LoadingOlder:   input.LoadingOlder,
		MouseReporting: p.paneMouseReporting(termPanel),
		PaneLive:       interactive,
		PaneWidth:      input.PaneWidth,
		PaneHeight:     input.PaneHeight,
		LiveEdgeKey:    terminalLiveEdgeKey(interactive),
	}), 0, dimText)
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
	return p.terminalHeader(chips, hint, width, hintFloor) + "\n" + result.Content
}

// renderOutputContent renders agent output.
func (p *Plugin) renderOutputContent(width, height int) string {
	// The tab chips are this surface's left region. When the terminal panel is
	// up they are followed by the agent sub-pane's own identity chip, so both
	// children of the split name themselves and neither can be truncated away —
	// the chips are also the row's only hit region (regionPreviewTab).
	chips := p.previewTabChips()

	// The states below have no terminal to draw, but they are still the Output
	// tab, so they still owe the header row the tabs live on: without it a
	// freshly created worktree shows no tabs at all while their hit regions stay
	// live underneath the message.
	notice := func(body string) string {
		return p.terminalHeader(chips, "", width, 0) + "\n" + p.truncateAllLines(body, width)
	}

	wt := p.selectedWorktree()
	if wt == nil {
		return notice(dimText("No worktree selected"))
	}

	// Check for orphaned worktree (agent file exists but tmux session gone)
	if wt.IsOrphaned && wt.Agent == nil {
		return notice(p.renderOrphanedMessage(wt.ChosenAgentType))
	}

	if wt.Agent == nil {
		return notice(dimText("No agent running\nPress 's' to start an agent"))
	}

	// Hint depends on mode - interactive mode shows exit hints
	var hint string
	switch {
	case p.interactiveDescribes(false):
		// Interactive mode targeting this agent pane - show exit hint with
		// highlight. The exit key leads, so the header's hint floor keeps it.
		hint = p.interactiveExitHint()
		if key := p.getInteractiveAttachKey(); key != "" {
			hint += dimText(" • " + key + " attach")
		}
	case p.primaryTerminalFocusVisible() && p.activePane == PanePreview:
		// Split with the terminal panel: say which child has focus.
		agentFocused := p.primaryTerminalFocused()
		chips = append(chips, p.paneFocusChip("Agent", agentFocused))
		if agentFocused {
			hint = dimText("enter interactive")
		}
	default:
		hint = p.watchedAttachHint()
	}
	return p.renderCapturedTerminal(chips, hint, wt.Agent.OutputBuf, width, height, false, "No output yet")
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

	// A shell has no tabs, so its left region is its own name — the thing the
	// sidebar selected — carrying the same focus marking the split children use.
	name := shell.Name
	if name == "" {
		name = "Shell"
	}
	shellFocused := p.primaryTerminalFocused()
	chips := []string{p.paneFocusChip(name, shellFocused)}

	// Hint depends on mode - interactive mode shows exit hints
	var hint string
	switch {
	case p.interactiveDescribes(false):
		// Interactive mode targeting this shell pane
		hint = p.interactiveExitHint()
	case p.termPanelVisible && p.activePane == PanePreview:
		if shellFocused {
			hint = dimText("enter interactive")
		}
	default:
		hint = p.watchedAttachHint()
	}
	return p.renderCapturedTerminal(chips, hint, shell.Agent.OutputBuf, width, height, false, "No output yet")
}

// watchedAttachHint is the watched-pane attach/detach copy. Empty when full
// attach is off — do not replace it with an explanation.
func (p *Plugin) watchedAttachHint() string {
	if !fullTmuxAttachEnabled() {
		return ""
	}
	detach := getTmuxDetachHint()
	if features.IsEnabled(features.TmuxInteractiveInput.Name) {
		return dimText(fmt.Sprintf("t to attach • E for interactive • %s to detach", detach))
	}
	return dimText(fmt.Sprintf("t to attach • %s to detach", detach))
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

// padLinesToWidth right-pads each line to target display columns, ignoring ANSI
// sequences. Lines already at or beyond the target are left alone.
func padLinesToWidth(lines []string, target int) []string {
	if target <= 0 {
		return lines
	}
	for i, line := range lines {
		if gap := target - ansi.StringWidth(line); gap > 0 {
			lines[i] = line + strings.Repeat(" ", gap)
		}
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
	lines = append(lines, dimText("  Enter         Create and type in the shell"))
	lines = append(lines, dimText("  D             Delete shell session"))
	if fullTmuxAttachEnabled() {
		lines = append(lines, dimText("  t             Attach to full tmux session"))
		lines = append(lines, dimText(fmt.Sprintf("  %s d      Detach (return to sidecar)", prefix)))
	}
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
		view, _ := workspacediff.RenderTask(workspacediff.TaskView{}, workspacediff.TaskRenderOpts{
			EmptyHint: "Press 't' to link a task", Width: width, Height: height,
		})
		return view
	}

	// Check if we're loading or don't have cached details for this task
	if p.taskLoading || p.cachedTask == nil || p.cachedTaskID != wt.TaskID {
		return dimText(fmt.Sprintf("Loading task %s...", wt.TaskID))
	}

	task := p.cachedTask
	var lines []string

	mode := "Raw"
	if p.taskMarkdownMode {
		mode = "Rendered"
	}
	modeHint := dimText("[m] " + mode)

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
