package workspace

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

const (
	// termPanelSessionPrefix is the tmux session naming prefix for terminal panels.
	termPanelSessionPrefix = "sidecar-tp-"

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

// toggleTermPanel is ctrl+t: on, a Shell leaf is opened beside the primary
// terminal at the remembered axis and ratio; off, that leaf is closed. The tmux
// session outlives the leaf either way, so toggling back shows the same shell
// with its scrollback intact.
//
// The leaf itself is syncShellLeaf's — which is also how a split the viewport
// cannot hold refuses: it turns the flag back off, and there is nothing here to
// keep in step with it.
func (p *Plugin) toggleTermPanel() tea.Cmd {
	if !terminalPanelEnabled() {
		return nil
	}
	if p.termPanelVisible {
		// Hide: exit interactive mode if targeting terminal panel
		if p.interactiveState != nil && p.interactiveState.Active && p.interactiveState.TermPanel {
			p.exitInteractiveMode()
		}
		// The shape the leaf was left in is what the next toggle opens at.
		p.rememberShellSplit()
		p.termPanelVisible = false
		p.termPanelFocused = false
		p.syncShellLeaf()
		p.saveSelectionState()
		return p.resizeSelectedPaneCmd()
	}

	p.termPanelVisible = true
	if !p.syncShellLeaf() {
		p.termPanelFocused = false
		return nil
	}
	p.termPanelFocused = true // Focus the terminal sub-pane so the user can Enter to interact
	p.termPanelScroll = 0     // Reset scroll to show latest output
	p.activePane = PanePreview
	sessionName := p.termPanelSessionName()
	if sessionName == "" {
		p.termPanelVisible = false
		p.termPanelFocused = false
		p.syncShellLeaf()
		return nil
	}
	p.saveSelectionState()

	// If we already have an active session for this, just show it
	if p.termPanelSession == sessionName && p.termPanelOutput != nil {
		return tea.Batch(
			p.resizeTermPanelPaneCmd(),
			p.resizeSelectedPaneCmd(),
		)
	}

	// Switch to the new session (old session preserved for later reuse)
	p.termPanelSession = sessionName
	p.releaseTerminalDocProjection(true)
	if p.termPanelOutput == nil {
		p.termPanelOutput = tty.NewOutputBuffer(outputBufferCap)
	} else {
		p.termPanelOutput.Clear()
	}

	return p.createTermPanelSession(sessionName)
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

// calculateTermPanelDimensions returns the width and height the terminal
// panel's tmux pane should be resized to. ok is false when the panel's leaf is
// not on screen, which means there is nothing to size.
func (p *Plugin) calculateTermPanelDimensions() (width, height int, ok bool) {
	// The box is the shell leaf's, from the pane tree; the leaf spends its own
	// first row on its header, so the terminal inside it is one row shorter.
	box, ok := p.terminalSlotBox(true)
	if !ok {
		return 0, 0, false
	}
	width, height = terminalSlotSize(box)
	return width, height, true
}

// calculateAgentPaneDimensions returns the width and height for the agent
// output area. The terminal leaf's box already accounts for any shell split
// beside it, so there is no second arithmetic here to keep in step with it.
func (p *Plugin) calculateAgentPaneDimensions() (width, height int) {
	box, ok := p.terminalSlotBox(false)
	if !ok {
		return p.calculatePreviewDimensions()
	}
	return terminalSlotSize(box)
}

// resizeTermPanelPaneCmd returns a command that resizes the terminal panel's
// tmux pane to match the current split dimensions.
func (p *Plugin) resizeTermPanelPaneCmd() tea.Cmd {
	if p.termPanelSession == "" || !p.termPanelVisible {
		return nil
	}
	ownership := p.currentTerminalOwnership()
	if ownership == 0 {
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
		return p.withTerminalOwnership(ownership, func() tea.Msg {
			workspaceResizeTmuxPane(target, w, h)
			return nil
		})
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
	p.releaseTerminalWindowPin(true)
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
	p.releaseTerminalWindowPin(true)
}
