package workspace

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	// termPanelSessionPrefix is the tmux session naming prefix for terminal panels.
	termPanelSessionPrefix = "sidecar-tp-"

	// termPanelDefaultSize is the default split percentage for the terminal panel.
	termPanelDefaultSize = 50

	// termPanelMinSize is the minimum percentage the terminal panel can occupy.
	termPanelMinSize = 15

	// termPanelMaxSize is the maximum percentage the terminal panel can occupy.
	termPanelMaxSize = 85

	// Terminal panel adaptive poll intervals — mirrors agent polling.
	termPanelPollActive  = 200 * time.Millisecond
	termPanelPollIdle    = 2 * time.Second
	termPanelPollUnfocus = 500 * time.Millisecond
)

// TermPanelSessionCreatedMsg is sent when the terminal panel tmux session is created.
type TermPanelSessionCreatedMsg struct {
	SessionName string
	PaneID      string
	Err         error
}

// TermPanelCaptureMsg delivers captured output from the terminal panel's tmux session.
type TermPanelCaptureMsg struct {
	SessionName   string
	Generation    int
	Output        string
	Err           error
	HasCursor     bool
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	PaneHeight    int
	PaneWidth     int
	HistorySize   int
	CaptureBase   int
	HasHistory    bool
	// MouseReporting is tmux's #{mouse_any_flag} for the pane. Only meaningful
	// when HasCursor is set.
	MouseReporting bool
}

// termPanelPollMsg triggers the next poll cycle for the terminal panel.
type termPanelPollMsg struct {
	SessionName string
	Generation  int
}

// termPanelSessionName returns the tmux session name for the current worktree/shell's terminal panel.
func (p *Plugin) termPanelSessionName() string {
	if p.shellSelected {
		shell := p.getSelectedShell()
		if shell != nil {
			return termPanelSessionPrefix + sanitizeName(shell.TmuxName)
		}
		return ""
	}
	wt := p.selectedWorktree()
	if wt == nil {
		return ""
	}
	return termPanelSessionPrefix + sanitizeName(wt.Name)
}

// termPanelWorkDir returns the working directory for the terminal panel session.
func (p *Plugin) termPanelWorkDir() string {
	if p.shellSelected {
		return p.ctx.WorkDir
	}
	wt := p.selectedWorktree()
	if wt != nil {
		return wt.Path
	}
	return p.ctx.WorkDir
}

// toggleTermPanel is a simple on/off toggle for the terminal panel.
// When showing, it restores the last-used layout and focuses the terminal sub-pane.
// Creates the tmux session if it doesn't exist.
func (p *Plugin) toggleTermPanel() tea.Cmd {
	if p.termPanelVisible {
		// Hide: exit interactive mode if targeting terminal panel
		if p.interactiveState != nil && p.interactiveState.Active && p.interactiveState.TermPanel {
			p.exitInteractiveMode()
		}
		p.termPanelVisible = false
		p.termPanelFocused = false
		_ = state.SetTermPanelVisible(false)
		p.ctx.Logger.Debug("termPanel: hidden")
		return p.resizeSelectedPaneCmd()
	}

	// Show: restore last-used layout (persisted in state)
	p.termPanelVisible = true
	_ = state.SetTermPanelVisible(true)
	p.termPanelFocused = true // Focus the terminal sub-pane so the user can Enter to interact
	p.termPanelScroll = 0     // Reset scroll to show latest output
	p.activePane = PanePreview
	if state.GetTermPanelLayout() == "right" {
		p.termPanelLayout = TermPanelRight
	} else {
		p.termPanelLayout = TermPanelBottom
	}
	p.pollScheduler.Invalidate(termPanelPollKey())

	sessionName := p.termPanelSessionName()
	if sessionName == "" {
		p.ctx.Logger.Debug("termPanel: no session name (no worktree/shell selected)")
		p.termPanelVisible = false
		p.termPanelFocused = false
		return nil
	}

	p.ctx.Logger.Debug("termPanel: showing", "session", sessionName, "layout", p.termPanelLayout, "gen", p.pollScheduler.Current(termPanelPollKey()))

	// If we already have an active session for this, just show it
	if p.termPanelSession == sessionName && p.termPanelOutput != nil {
		p.ctx.Logger.Debug("termPanel: reusing existing session", "session", sessionName)
		return tea.Batch(
			p.resizeTermPanelPaneCmd(),
			p.resizeSelectedPaneCmd(),
			p.scheduleTermPanelPoll(0),
		)
	}

	// Switch to the new session (old session preserved for later reuse)
	if p.termPanelSession != "" && p.termPanelSession != sessionName {
		p.ctx.Logger.Debug("termPanel: switching session", "old", p.termPanelSession, "new", sessionName)
	}
	p.termPanelSession = sessionName
	if p.termPanelOutput == nil {
		p.termPanelOutput = tty.NewOutputBuffer(outputBufferCap)
	} else {
		p.termPanelOutput.Clear()
	}

	p.ctx.Logger.Debug("termPanel: creating/reusing session", "session", sessionName)
	return p.createTermPanelSession(sessionName)
}

// switchTermPanelLayout toggles the terminal panel between bottom and right layouts.
// Only works when the terminal panel is visible.
func (p *Plugin) switchTermPanelLayout() tea.Cmd {
	if !p.termPanelVisible {
		return nil
	}

	if p.termPanelLayout == TermPanelBottom {
		p.termPanelLayout = TermPanelRight
		_ = state.SetTermPanelLayout("right")
	} else {
		p.termPanelLayout = TermPanelBottom
		_ = state.SetTermPanelLayout("bottom")
	}
	p.ctx.Logger.Debug("termPanel: switched layout", "layout", p.termPanelLayout)
	return tea.Batch(p.resizeTermPanelPaneCmd(), p.resizeSelectedPaneCmd())
}

// createTermPanelSession creates or reuses a tmux session for the terminal panel.
func (p *Plugin) createTermPanelSession(sessionName string) tea.Cmd {
	workDir := p.termPanelWorkDir()

	return func() tea.Msg {
		// Check if session already exists
		if sessionExists(sessionName) {
			paneID := getPaneID(sessionName)
			return TermPanelSessionCreatedMsg{SessionName: sessionName, PaneID: paneID}
		}

		if !isTmuxInstalled() {
			return TermPanelSessionCreatedMsg{
				SessionName: sessionName,
				Err:         fmt.Errorf("tmux not installed"),
			}
		}

		// Create new detached session
		args := []string{
			"new-session",
			"-d",
			"-s", sessionName,
			"-c", workDir,
		}
		if err := tty.NewSession(args...); err != nil {
			return TermPanelSessionCreatedMsg{
				SessionName: sessionName,
				Err:         fmt.Errorf("create terminal panel session: %w", err),
			}
		}

		paneID := getPaneID(sessionName)
		return TermPanelSessionCreatedMsg{SessionName: sessionName, PaneID: paneID}
	}
}

// scheduleTermPanelPoll schedules the next poll for the terminal panel output.
func (p *Plugin) scheduleTermPanelPoll(delay time.Duration) tea.Cmd {
	if p.terminalControlUsing(workspaceControlPanel) {
		return nil
	}
	sessionName := p.termPanelSession
	if sessionName == "" {
		return nil
	}
	return p.pollScheduler.Schedule(termPanelPollKey(), delay, func(gen int) tea.Msg {
		return termPanelPollMsg{SessionName: sessionName, Generation: gen}
	})
}

// handleTermPanelPoll captures output from the terminal panel's tmux session.
// Uses the global capture cache/coordinator to avoid redundant subprocess calls.
// When interactive mode targets the terminal panel, also captures cursor position.
func (p *Plugin) handleTermPanelPoll(sessionName string, generation int) tea.Cmd {
	if p.terminalControlUsing(workspaceControlPanel) {
		return nil
	}
	captureCursor := p.viewMode == ViewModeInteractive && p.interactiveState != nil && p.interactiveState.Active && p.interactiveState.TermPanel
	if captureCursor {
		if remaining, scrolling := p.interactiveScrollDelay(); scrolling {
			return p.scheduleTermPanelPoll(remaining)
		}
	}
	target := p.termPanelPaneID
	if target == "" {
		target = sessionName
	}
	return func() tea.Msg {
		// Polling this pane is what makes this instance the one driving its
		// geometry. Without a tick here the panel's only lease events are resize
		// commands, so a peer's abandoned lease would take one panel toggle per
		// tick of the staleness budget to reclaim (td-ee222a).
		tty.TouchGeometryLease(target)

		var output string
		var cursor capturedCursor
		var capture capturedPaneMetadata
		var err error
		if captureCursor {
			// Interactive mode: bypass cache for fresh capture, same as agent pane.
			// The global cache has 300ms TTL which causes stale reads during typing.
			output, cursor, err = capturePaneDirectWithJoinAndCursor(sessionName, target, false)
			capture = cursor.capturedPaneMetadata
		} else {
			// The visible panel uses a direct metadata capture so deep history
			// can be addressed without loading it on the polling hot path.
			output, capture, err = capturePaneDirectWithJoinMetadata(sessionName, false)
		}
		msg := TermPanelCaptureMsg{
			SessionName: sessionName,
			Generation:  generation,
			Output:      output,
			Err:         err,
			HistorySize: capture.HistorySize,
			CaptureBase: capture.CaptureBase,
			HasHistory:  capture.Valid,
			PaneWidth:   capture.PaneWidth,
			PaneHeight:  capture.PaneHeight,
		}
		if cursor.Valid {
			msg.HasCursor = true
			msg.CursorRow = cursor.Row
			msg.CursorCol = cursor.Col
			msg.CursorVisible = cursor.Visible
			msg.MouseReporting = cursor.MouseReporting
		}
		return msg
	}
}

// termPanelEffectiveSize returns the effective split size percentage.
func (p *Plugin) termPanelEffectiveSize() int {
	size := p.termPanelSize
	if size <= 0 {
		size = termPanelDefaultSize
	}
	if size < termPanelMinSize {
		size = termPanelMinSize
	}
	if size > termPanelMaxSize {
		size = termPanelMaxSize
	}
	return size
}

// calculateTermPanelDimensions returns the width and height that the terminal
// panel's tmux pane should be resized to, based on the current layout and split.
func (p *Plugin) calculateTermPanelDimensions() (width, height int) {
	previewWidth, previewHeight := p.calculatePreviewDimensions()
	size := p.termPanelEffectiveSize()

	if p.termPanelLayout == TermPanelRight {
		termWidth := previewWidth * size / 100
		if termWidth < 10 {
			termWidth = 10
		}
		return termWidth, previewHeight
	}
	// Bottom layout
	// previewHeight excludes the main pane's hint. The split renderer divides
	// the container that includes both child hints, then each child renderer
	// reserves its own hint row before terminal content is drawn.
	containerHeight := previewHeight + 1
	termBoxHeight := containerHeight * size / 100
	if termBoxHeight < 3 {
		termBoxHeight = 3
	}
	return previewWidth, max(termBoxHeight-1, 1)
}

// calculateAgentPaneDimensions returns the width and height for the agent
// output area when the terminal panel is visible. When hidden, returns full
// preview dimensions.
func (p *Plugin) calculateAgentPaneDimensions() (width, height int) {
	previewWidth, previewHeight := p.calculatePreviewDimensions()
	if !p.termPanelVisible {
		return previewWidth, previewHeight
	}
	size := p.termPanelEffectiveSize()

	if p.termPanelLayout == TermPanelRight {
		termWidth := previewWidth * size / 100
		if termWidth < 10 {
			termWidth = 10
		}
		outputWidth := previewWidth - termWidth - 1 // -1 for divider
		if outputWidth < 10 {
			outputWidth = 10
		}
		// If both minimums exceed available width, fall back to full preview
		if outputWidth+termWidth+1 > previewWidth {
			return previewWidth, previewHeight
		}
		return outputWidth, previewHeight
	}
	// Bottom layout
	containerHeight := previewHeight + 1
	termBoxHeight := containerHeight * size / 100
	if termBoxHeight < 3 {
		termBoxHeight = 3
	}
	outputBoxHeight := containerHeight - termBoxHeight - 1 // -1 for divider
	if outputBoxHeight < 3 {
		outputBoxHeight = 3
	}
	// If both minimums exceed available height, fall back to full preview
	if outputBoxHeight+termBoxHeight+1 > containerHeight {
		return previewWidth, previewHeight
	}
	return previewWidth, max(outputBoxHeight-1, 1)
}

func (p *Plugin) termPanelMaxScroll() int {
	if p.termPanelOutput == nil {
		return 0
	}
	_, height := p.calculateTermPanelDimensions()
	lineCount := p.termPanelOutput.LineCount()
	if p.viewMode != ViewModeInteractive || p.interactiveState == nil || !p.interactiveState.Active || !p.interactiveState.TermPanel {
		lineCount = p.termPanelOutput.LastNonEmptyLine() + 1
	}
	return max(lineCount-height, 0)
}

// resizeTermPanelPaneCmd returns a command that resizes the terminal panel's
// tmux pane to match the current split dimensions.
func (p *Plugin) resizeTermPanelPaneCmd() tea.Cmd {
	if p.termPanelSession == "" || !p.termPanelVisible {
		return nil
	}
	target := p.termPanelPaneID
	if target == "" {
		target = p.termPanelSession
	}
	w, h := p.calculateTermPanelDimensions()
	w = p.terminalContentWidth(w, h, true)
	return func() tea.Msg {
		tty.ResizeTmuxPane(target, w, h)
		return nil
	}
}

// termPanelHintLine renders the focus-aware label for the terminal panel sub-pane.
func (p *Plugin) termPanelHintLine() string {
	if p.viewMode == ViewModeInteractive && p.interactiveState != nil && p.interactiveState.Active && p.interactiveState.TermPanel {
		interactiveStyle := lipgloss.NewStyle().
			Foreground(lipgloss.Color(styles.GetCurrentTheme().Colors.Warning)).
			Bold(true)
		return interactiveStyle.Render("INTERACTIVE") + " " + dimText(p.getInteractiveExitKey()+" exit")
	}
	if p.termPanelFocused {
		focusStyle := lipgloss.NewStyle().
			Foreground(styles.Primary).
			Bold(true)
		return focusStyle.Render("▸ Terminal") + " " + dimText("enter interactive")
	}
	return dimText("Terminal")
}

// renderTermPanelOutput renders the terminal panel's captured output.
func (p *Plugin) renderTermPanelOutput(width, height int) string {
	hint := p.termPanelHintLine()
	if p.termPanelOutput == nil {
		hint = p.truncateCache.Truncate(ui.ExpandTabs(hint, tabStopWidth), width, "")
		empty := p.truncateCache.Truncate(dimText("Starting terminal..."), width, "")
		if height <= 1 {
			return hint
		}
		return hint + "\n" + empty
	}
	return p.renderCapturedTerminal(hint, p.termPanelOutput, width, height, true, "Terminal ready")
}

// renderTermPanelDividerH renders a horizontal divider (for bottom layout).
func (p *Plugin) renderTermPanelDividerH(width int) string {
	dividerStyle := lipgloss.NewStyle().Foreground(styles.BorderNormal)
	return dividerStyle.Render(strings.Repeat("─", width))
}

// renderTermPanelDividerV renders a vertical divider (for right layout).
func (p *Plugin) renderTermPanelDividerV(height int) string {
	dividerStyle := lipgloss.NewStyle().Foreground(styles.BorderNormal)
	var sb strings.Builder
	for i := 0; i < height; i++ {
		sb.WriteString(dividerStyle.Render("│"))
		if i < height-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// renderOutputWithTermPanel renders the output content split with the terminal panel.
func (p *Plugin) renderOutputWithTermPanel(width, height int) string {
	size := p.termPanelEffectiveSize()
	// Calculate the absolute X offset where preview content starts.
	// This is needed to register hit regions at the correct screen position.
	previewContentX := panelOverhead / 2
	if p.sidebarVisible {
		available := p.width - dividerWidth
		sidebarW := (available * p.sidebarWidth) / 100
		if sidebarW < 15 {
			sidebarW = 15
		}
		if sidebarW > available-40 {
			sidebarW = available - 40
		}
		previewContentX = sidebarW + dividerWidth + panelOverhead/2
	}

	// Absolute Y offset for content within preview: panel border (1) + tab header (1) + blank line (1) = 3.
	// For shells (no tabs), the content starts at panel border (1).
	previewContentY := 1
	if !p.shellSelected {
		previewContentY = 3
	}

	if p.termPanelLayout == TermPanelRight {
		// Right layout: output | divider | terminal
		termWidth := width * size / 100
		if termWidth < 10 {
			termWidth = 10
		}
		outputWidth := width - termWidth - 1 // -1 for divider
		if outputWidth < 10 {
			outputWidth = 10
		}
		// Guard: if total exceeds width, fall back to output-only
		if outputWidth+termWidth+1 > width {
			return p.renderOutputContent(width, height)
		}

		// Register hit regions for the vertical divider and terminal panel content
		absX := previewContentX + outputWidth
		p.mouseHandler.HitMap.AddRect(regionTermPanelDivider, absX, previewContentY, dividerHitWidth, height, nil)
		p.mouseHandler.HitMap.AddRect(regionTermPanelContent, absX+1, previewContentY, termWidth, height, nil)

		outputPane := p.renderOutputContent(outputWidth, height)
		termPane := p.renderTermPanelOutput(termWidth, height)
		divider := p.renderTermPanelDividerV(height)

		outputPane = padToHeight(outputPane, height, outputWidth)
		termPane = padToHeight(termPane, height, termWidth)

		// Ensure every line of the output pane is exactly outputWidth printable
		// characters so JoinHorizontal doesn't shift the divider/terminal pane.
		outputPane = enforceLineWidths(outputPane, outputWidth)

		return lipgloss.JoinHorizontal(lipgloss.Top, outputPane, divider, termPane)
	}

	// Bottom layout: output / divider / terminal
	termHeight := height * size / 100
	if termHeight < 3 {
		termHeight = 3
	}
	outputHeight := height - termHeight - 1 // -1 for divider
	if outputHeight < 3 {
		outputHeight = 3
	}
	// Guard: if total exceeds height, fall back to output-only
	if outputHeight+termHeight+1 > height {
		return p.renderOutputContent(width, height)
	}

	// Register hit regions for the horizontal divider and terminal panel content
	absY := previewContentY + outputHeight
	p.mouseHandler.HitMap.AddRect(regionTermPanelDivider, previewContentX, absY, width, dividerHitWidth, nil)
	p.mouseHandler.HitMap.AddRect(regionTermPanelContent, previewContentX, absY+1, width, termHeight, nil)

	outputPane := padToHeight(p.renderOutputContent(width, outputHeight), outputHeight, width)
	divider := p.renderTermPanelDividerH(width)
	termPane := p.renderTermPanelOutput(width, termHeight)

	return outputPane + "\n" + divider + "\n" + termPane
}

// renderShellWithTermPanel renders the shell output split with the terminal panel.
func (p *Plugin) renderShellWithTermPanel(width, height int) string {
	size := p.termPanelEffectiveSize()

	previewContentX := panelOverhead / 2
	if p.sidebarVisible {
		available := p.width - dividerWidth
		sidebarW := (available * p.sidebarWidth) / 100
		if sidebarW < 15 {
			sidebarW = 15
		}
		if sidebarW > available-40 {
			sidebarW = available - 40
		}
		previewContentX = sidebarW + dividerWidth + panelOverhead/2
	}
	previewContentY := 1 // Shell has no tabs, only panel border

	if p.termPanelLayout == TermPanelRight {
		termWidth := width * size / 100
		if termWidth < 10 {
			termWidth = 10
		}
		outputWidth := width - termWidth - 1
		if outputWidth < 10 {
			outputWidth = 10
		}
		// Guard: if total exceeds width, fall back to shell-only
		if outputWidth+termWidth+1 > width {
			return p.renderShellOutput(width, height)
		}

		absX := previewContentX + outputWidth
		p.mouseHandler.HitMap.AddRect(regionTermPanelDivider, absX, previewContentY, dividerHitWidth, height, nil)
		p.mouseHandler.HitMap.AddRect(regionTermPanelContent, absX+1, previewContentY, termWidth, height, nil)

		shellPane := p.renderShellOutput(outputWidth, height)
		termPane := p.renderTermPanelOutput(termWidth, height)
		divider := p.renderTermPanelDividerV(height)

		shellPane = padToHeight(shellPane, height, outputWidth)
		termPane = padToHeight(termPane, height, termWidth)

		shellPane = enforceLineWidths(shellPane, outputWidth)

		return lipgloss.JoinHorizontal(lipgloss.Top, shellPane, divider, termPane)
	}

	// Bottom layout
	termHeight := height * size / 100
	if termHeight < 3 {
		termHeight = 3
	}
	outputHeight := height - termHeight - 1
	if outputHeight < 3 {
		outputHeight = 3
	}
	// Guard: if total exceeds height, fall back to shell-only
	if outputHeight+termHeight+1 > height {
		return p.renderShellOutput(width, height)
	}

	absY := previewContentY + outputHeight
	p.mouseHandler.HitMap.AddRect(regionTermPanelDivider, previewContentX, absY, width, dividerHitWidth, nil)
	p.mouseHandler.HitMap.AddRect(regionTermPanelContent, previewContentX, absY+1, width, termHeight, nil)

	shellPane := padToHeight(p.renderShellOutput(width, outputHeight), outputHeight, width)
	divider := p.renderTermPanelDividerH(width)
	termPane := p.renderTermPanelOutput(width, termHeight)

	return shellPane + "\n" + divider + "\n" + termPane
}

// refreshTermPanelForSelection switches the terminal panel to the newly selected worktree/shell.
// Returns a tea.Cmd if a new session needs to be created/polled.
func (p *Plugin) refreshTermPanelForSelection() tea.Cmd {
	if !p.termPanelVisible {
		return nil
	}
	newSession := p.termPanelSessionName()
	if newSession == "" || newSession == p.termPanelSession {
		return nil
	}
	// Switch to new session (old session preserved for later reuse)
	p.pollScheduler.Invalidate(termPanelPollKey())
	p.termPanelSession = newSession
	p.termPanelPaneID = ""
	p.termPanelScroll = 0
	if p.termPanelOutput == nil {
		p.termPanelOutput = tty.NewOutputBuffer(outputBufferCap)
	} else {
		p.termPanelOutput.Clear()
	}
	return p.createTermPanelSession(newSession)
}

// cleanupTermPanelSession resets terminal panel state without killing the tmux session.
// Sessions are preserved so they can be reattached on next launch (like agent sessions).
func (p *Plugin) cleanupTermPanelSession() {
	p.termPanelSession = ""
	p.termPanelPaneID = ""
	p.termPanelOutput = nil
}

// enforceLineWidths ensures every line in content is exactly targetWidth
// printable characters wide (accounting for ANSI escape sequences).
// Lines shorter than targetWidth are padded with spaces; lines longer are
// truncated. This prevents lipgloss.JoinHorizontal from shifting columns
// when the left pane has variable-width lines.
func enforceLineWidths(content string, targetWidth int) string {
	lines := strings.Split(content, "\n")
	for i, line := range lines {
		w := ansi.StringWidth(line)
		if w < targetWidth {
			lines[i] = line + strings.Repeat(" ", targetWidth-w)
		} else if w > targetWidth {
			lines[i] = ansi.Truncate(line, targetWidth, "")
		}
	}
	return strings.Join(lines, "\n")
}
