package workspace

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/x/ansi"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/tty"
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

	// termPanelMinBoxCols / termPanelMinBoxRows are the floors each child of the
	// split gets before the split is abandoned as too small to draw.
	termPanelMinBoxCols = 10
	termPanelMinBoxRows = 3
)

// TermPanelSessionCreatedMsg is sent when the terminal panel tmux session is created.
type TermPanelSessionCreatedMsg struct {
	SessionName string
	PaneID      string
	Err         error
}

// termPanelSessionName returns the tmux session name for the current worktree/shell's terminal panel.
func (p *Plugin) termPanelSessionName() string {
	if p.selectingShell() {
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
	return termPanelSessionPrefix + worktreeSessionSuffix(wt)
}

// termPanelWorkDir returns the working directory for the terminal panel session.
func (p *Plugin) termPanelWorkDir() string {
	if shell := p.getSelectedShell(); shell != nil {
		if shell.WorkDir != "" {
			return shell.WorkDir
		}
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
	if !terminalPanelEnabled() {
		return nil
	}
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
	sessionName := p.termPanelSessionName()
	if sessionName == "" {
		p.ctx.Logger.Debug("termPanel: no session name (no worktree/shell selected)")
		p.termPanelVisible = false
		p.termPanelFocused = false
		return nil
	}

	p.ctx.Logger.Debug("termPanel: showing", "session", sessionName, "layout", p.termPanelLayout)

	// If we already have an active session for this, just show it
	if p.termPanelSession == sessionName && p.termPanelOutput != nil {
		p.ctx.Logger.Debug("termPanel: reusing existing session", "session", sessionName)
		return tea.Batch(
			p.resizeTermPanelPaneCmd(),
			p.resizeSelectedPaneCmd(),
		)
	}

	// Switch to the new session (old session preserved for later reuse)
	if p.termPanelSession != "" && p.termPanelSession != sessionName {
		p.ctx.Logger.Debug("termPanel: switching session", "old", p.termPanelSession, "new", sessionName)
	}
	p.termPanelSession = sessionName
	p.releaseTerminalDocProjection(true)
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
	if !terminalPanelEnabled() || !p.termPanelVisible {
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

// termPanelSplitBoxes reports the split the current layout divides the preview
// into: columns in the right layout, rows in the bottom one. It is the single
// place the split is measured — the sizers, the renderers and
// terminalSurfaceGeometry all take it from here, against the same preview
// dimensions — because two callers deriving it from differently floored inputs
// could reach different fits verdicts, and one of them would then size or place
// a panel the other never drew.
//
// fits false means there is no terminal panel on screen at all: the renderers
// fall back to an output-only layout, and nothing may be sized or located.
func (p *Plugin) termPanelSplitBoxes() (outputBox, termBox int, fits bool) {
	previewWidth, previewHeight := p.calculatePreviewDimensions()
	size := p.termPanelEffectiveSize()
	if p.termPanelLayout == TermPanelRight {
		return termPanelRightBoxes(previewWidth, size)
	}
	// The renderer splits the whole preview content area, which is previewHeight
	// plus the one header row previewHeight has already taken off; see
	// termPanelContainerHeight.
	return termPanelBottomBoxes(termPanelContainerHeight(previewHeight), size)
}

// calculateTermPanelDimensions returns the width and height that the terminal
// panel's tmux pane should be resized to, based on the current layout and
// split. ok is false when the split does not fit, which means no panel is drawn
// and there is nothing to size.
func (p *Plugin) calculateTermPanelDimensions() (width, height int, ok bool) {
	previewWidth, previewHeight := p.calculatePreviewDimensions()
	_, termBox, fits := p.termPanelSplitBoxes()
	if !fits {
		return 0, 0, false
	}
	if p.termPanelLayout == TermPanelRight {
		return termBox, previewHeight, true
	}
	// Each child box spends its own first row on its header, so the terminal
	// inside it is one row shorter than the box.
	return previewWidth, max(termBox-terminalHeaderRows, 1), true
}

// termPanelContainerHeight converts the primary terminal's viewport height into
// the height of the container the bottom split divides: the preview content area
// below the panel border, header row included.
func termPanelContainerHeight(previewHeight int) int {
	return previewHeight + terminalHeaderRows
}

// termPanelBottomBoxes is the bottom split's box arithmetic, shared by the
// sizers and the renderers so a child's tmux pane cannot be sized against a
// different split than the one drawn. The box heights include each child's own
// header row. fits is false when the two floors together overflow the
// container, which is every caller's cue to fall back to a full-height,
// output-only layout.
func termPanelBottomBoxes(containerHeight, size int) (outputBox, termBox int, fits bool) {
	termBox = containerHeight * size / 100
	if termBox < termPanelMinBoxRows {
		termBox = termPanelMinBoxRows
	}
	outputBox = containerHeight - termBox - termPanelDividerRows
	if outputBox < termPanelMinBoxRows {
		outputBox = termPanelMinBoxRows
	}
	return outputBox, termBox, outputBox+termBox+termPanelDividerRows <= containerHeight
}

// termPanelRightBoxes is the right split's column arithmetic, and exists for the
// same reason termPanelBottomBoxes does: it was hand-rolled identically in the
// two sizers and both renderers, which is where the next drift would have
// started.
func termPanelRightBoxes(containerWidth, size int) (outputBox, termBox int, fits bool) {
	termBox = containerWidth * size / 100
	if termBox < termPanelMinBoxCols {
		termBox = termPanelMinBoxCols
	}
	outputBox = containerWidth - termBox - termPanelDividerCols
	if outputBox < termPanelMinBoxCols {
		outputBox = termPanelMinBoxCols
	}
	return outputBox, termBox, outputBox+termBox+termPanelDividerCols <= containerWidth
}

// calculateAgentPaneDimensions returns the width and height for the agent
// output area when the terminal panel is visible. When hidden, returns full
// preview dimensions.
func (p *Plugin) calculateAgentPaneDimensions() (width, height int) {
	previewWidth, previewHeight := p.calculatePreviewDimensions()
	if !p.termPanelVisible {
		return previewWidth, previewHeight
	}
	outputBox, _, fits := p.termPanelSplitBoxes()
	// If both minimums exceed the available space, the renderers draw no panel
	// and this surface gets the whole preview.
	if !fits {
		return previewWidth, previewHeight
	}
	if p.termPanelLayout == TermPanelRight {
		return outputBox, previewHeight
	}
	// The box spends its first row on the header, like every other surface.
	return previewWidth, max(outputBox-terminalHeaderRows, 1)
}

// termPanelMaxScroll is how far back the panel's window can sit, in rows from
// its live edge. It is the bound of the window the render path draws: the panel
// used to hand-roll the trim off its own dimensions instead, which is a second
// derivation of one window and disagrees with the drawn one wherever a
// letterboxed or clipped pane does (td-bbbbfe).
func (p *Plugin) termPanelMaxScroll() int {
	// No panel drawn means no window to bound. The bound's own input falls back
	// to the preview's size when the panel surface has no geometry, which would
	// answer for a surface that is not on screen.
	if p.termPanelOutput == nil {
		return 0
	}
	if _, _, ok := p.calculateTermPanelDimensions(); !ok {
		return 0
	}
	return p.terminalWindowBound(true)
}

// thawTermPanelWindow hands a panel window pinned to an absolute start — by a
// pointer gesture or by document activation — back to the distance-from-bottom
// model without moving the rows on screen. Where it resumes following from is
// the shared rule's. Closing a document deliberately does not call this: the
// first explicit panel navigation owns the transition.
func (p *Plugin) thawTermPanelWindow() {
	p.releaseTerminalDocProjection(true)
	if offset, thawed := p.termPanelFreeze.ThawFrom(p.termPanelMaxScroll()); thawed {
		p.termPanelScroll = offset
	}
	p.termPanelFreezeDoc = false
}

// scrollTermPanelWindow moves the panel's window delta rows back through
// scrollback, negative towards the live edge. Scrolling is an explicit
// navigation of this surface, so it thaws first, and where the window lands is
// the shared rule's — the primary surface and the global preview place theirs
// by the same one.
func (p *Plugin) scrollTermPanelWindow(delta int) {
	p.thawTermPanelWindow()
	p.termPanelScroll = tty.ScrollWindow(&p.termPanelFreeze, p.termPanelScroll, delta, p.termPanelMaxScroll())
}

// scrollTermPanelWindowRows is scrollTermPanelWindow for a caller counting
// rendered rows down the screen — a wheel notch — which is the opposite
// direction. Reconciling the two is the shared rule's, not this call site's.
func (p *Plugin) scrollTermPanelWindowRows(rows int) {
	p.thawTermPanelWindow()
	p.termPanelScroll = tty.ScrollWindowRows(&p.termPanelFreeze, p.termPanelScroll, rows, p.termPanelMaxScroll())
}

// pinTermPanelWindow holds the panel window at an absolute start and records who
// is holding it. The two owners are not released by the same events: a pointer
// gesture's pin lives exactly as long as the selection reading those rows, while
// a document activation's outlives that selection, because the document is meant
// to keep showing the context it was opened from. Whether this pin takes at all
// is the shared freeze's rule — a second freeze inside one gesture keeps the
// first, and its owner with it.
func (p *Plugin) pinTermPanelWindow(start int, doc bool) {
	if p.termPanelFreeze.Active() {
		return
	}
	p.termPanelFreeze.Freeze(start)
	p.termPanelFreezeDoc = doc
}

// releaseTermPanelWindowPin drops the pin whoever placed it, for a jump that
// chooses its own window rather than resuming from the pinned one.
func (p *Plugin) releaseTermPanelWindowPin() {
	p.termPanelFreeze.Release()
	p.termPanelFreezeDoc = false
}

// releaseTermPanelGesturePin drops a pin a pointer gesture placed, once the
// selection it was reading is gone — the gesture's half of the freeze/thaw
// obligation. A pin left behind by a selection that no longer exists holds the
// panel off the live edge with nothing on screen to explain why, which reads as
// a pane that went quiet. A document's pin is not this one's to drop.
func (p *Plugin) releaseTermPanelGesturePin() {
	if p.termPanelFreezeDoc {
		return
	}
	p.releaseTermPanelWindowPin()
}

// thawTermPanelGesturePin is the panel's half of the freeze a pointer gesture
// owes at its end, and the same answer the primary surface gives in
// thawPreviewGesturePin: the rows the gesture left on screen stay there, held
// as a distance from the live bottom, so a pin taken at the live edge resumes
// following from offset 0 while a gesture that walked the window back through
// scrollback keeps where it walked to. Releasing instead resumes from whatever
// offset the surface held before the gesture froze it, which snaps the window
// back with nothing on screen to explain the jump. A document's pin is not the
// gesture's to end: it outlives the selection the click made. That guard is
// also what makes thawTermPanelWindow's doc-projection release harmless here —
// a panel projection always implies a doc-owned pin, which has already
// returned above.
func (p *Plugin) thawTermPanelGesturePin() {
	if p.termPanelFreezeDoc {
		return
	}
	p.thawTermPanelWindow()
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
	w, h, ok := p.calculateTermPanelDimensions()
	if !ok {
		// No panel is drawn at this size, so there is no pane geometry to assert.
		return nil
	}
	w = p.terminalContentWidth(w)
	if cmd, owned := p.resizeThroughTerminal(target, w, h); owned {
		return cmd
	}
	return func() tea.Msg {
		tty.ResizeTmuxPane(target, w, h)
		return nil
	}
}

// termPanelChip is the terminal panel's identity chip, the left region of its
// header row.
func (p *Plugin) termPanelChip() string {
	return p.paneFocusChip("Terminal", p.termPanelFocused)
}

// termPanelHints is the right region of the terminal panel's header row.
func (p *Plugin) termPanelHints() string {
	if p.interactiveDescribes(true) {
		return p.interactiveExitHint()
	}
	if p.termPanelFocused {
		return dimText("enter interactive")
	}
	return ""
}

// renderTermPanelOutput renders the terminal panel's captured output.
func (p *Plugin) renderTermPanelOutput(width, height int) string {
	chips := []string{p.termPanelChip()}
	if p.termPanelOutput == nil {
		hintFloor := 0
		if p.interactiveDescribes(true) {
			hintFloor = p.interactiveHintFloor()
		}
		header := p.terminalHeader(chips, p.termPanelHints(), width, hintFloor)
		if height <= terminalHeaderRows {
			return header
		}
		empty := p.truncateCache.Truncate(dimText("Starting terminal..."), width, "")
		return header + "\n" + empty
	}
	// The terminal panel has no action chips of its own; Diff and Task belong
	// to the surface's primary header.
	return p.renderCapturedTerminal(chips, nil, p.termPanelHints(), p.termPanelOutput, width, height, true, "Terminal ready")
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
	// The split comes from termPanelSplitBoxes rather than from the handed-in
	// width/height, so the boxes drawn here are the boxes the sizers resized the
	// tmux panes to and the ones terminalSurfaceGeometry places.
	outputBox, termBox, fits := p.termPanelSplitBoxes()
	// Absolute origin where preview content starts, so hit regions land on the
	// screen positions the render actually produces. The flash hint used to be
	// prepended as a row after these regions registered, leaving them a row high
	// for its duration; it now lives in the header's right region and shifts
	// nothing.
	terminalBox, _ := p.terminalLeafBox()
	previewContentX := terminalBox.X
	previewContentY := terminalBox.Y

	if p.termPanelLayout == TermPanelRight {
		// Right layout: output | divider | terminal.
		// Guard: if total exceeds width, fall back to output-only
		if !fits {
			return p.renderOutputContent(width, height)
		}
		outputWidth, termWidth := outputBox, termBox

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

	// Bottom layout: output / divider / terminal.
	// Guard: if total exceeds height, fall back to output-only
	if !fits {
		return p.renderOutputContent(width, height)
	}
	outputHeight, termHeight := outputBox, termBox

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
	outputBox, termBox, fits := p.termPanelSplitBoxes()

	terminalBox, _ := p.terminalLeafBox()
	previewContentX := terminalBox.X
	previewContentY := terminalBox.Y

	if p.termPanelLayout == TermPanelRight {
		// Guard: if total exceeds width, fall back to shell-only
		if !fits {
			return p.renderShellOutput(width, height)
		}
		outputWidth, termWidth := outputBox, termBox

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
	// Guard: if total exceeds height, fall back to shell-only
	if !fits {
		return p.renderShellOutput(width, height)
	}
	outputHeight, termHeight := outputBox, termBox

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
	p.termPanelSession = newSession
	p.releaseTerminalDocProjection(true)
	p.termPanelPaneID = ""
	p.termPanelScroll = 0
	p.releaseTermPanelWindowPin()
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
	p.releaseTerminalDocProjection(true)
	p.termPanelSession = ""
	p.termPanelPaneID = ""
	p.termPanelOutput = nil
	p.releaseTermPanelWindowPin()
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
