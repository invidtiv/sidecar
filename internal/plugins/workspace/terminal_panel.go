package workspace

import (
	"context"
	"fmt"
	"os/exec"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceops"
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

type termPanelSeed struct {
	session string
	run     string
	typeCmd string
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
		return p.pluginWorkDir()
	}
	wt := p.selectedWorktree()
	if wt != nil {
		return wt.Path
	}
	return p.pluginWorkDir()
}

// pluginWorkDir is the plugin's own working directory, or empty before a
// context is bound. The header asks for it every frame, including on a plugin
// a test drove straight into a render, so it answers rather than panics.
func (p *Plugin) pluginWorkDir() string {
	if p == nil || p.ctx == nil {
		return ""
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
		// Hide is a close that keeps the session and the user's typed name. It is
		// the same exit the ✕ takes, told apart by its mode, so the two paths
		// cannot drift into disagreeing about anything else.
		return p.closeShellLeaf(shellCloseHide)
	}

	// The split belongs to the workspace it is opened on, so it is claimed
	// before it exists: syncShellLeaf releases any split whose owner is not the
	// selection, and an unclaimed one would be released on the spot.
	p.claimShellLeafSurface()
	p.termPanelVisible = true
	if !p.syncShellLeaf() {
		p.termPanelFocused = false
		return nil
	}
	p.termPanelFocused = true // Focus the terminal sub-pane so the user can Enter to interact
	p.termPanelScroll = 0     // Reset scroll to show latest output
	p.activePane = PanePreview
	cmd := p.attachWorkspaceTerminalSplit()
	if p.termPanelSession == "" {
		p.abandonShellLeaf()
		p.syncShellLeaf()
		return nil
	}
	return cmd
}

// attachWorkspaceTerminalSplit points the visible Shell leaf at its tmux
// session: the durable selector is derived from the selection and assigned,
// a kept session's scrollback is reused rather than cleared, and any run/type
// seed queued for this session fires. toggleTermPanel (ctrl+t) and the layout
// spec's adopt path share it — a leaf that exists but has no session attached
// is exactly the state both must never persist. Returns nil without touching
// anything when no session can be derived; callers detect that the same way:
// termPanelSession stays empty.
func (p *Plugin) attachWorkspaceTerminalSplit() tea.Cmd {
	sessionName := p.termPanelSessionName()
	if sessionName == "" {
		return nil
	}

	// The session is the leaf's durable selector, and the save below is the
	// first one that will encode the leaf: assigning it after the save is how a
	// freshly created split was persisted with no session at all, and came back
	// from a relaunch attached to whatever the selection derived.
	reusing := p.termPanelSession == sessionName && p.termPanelOutput != nil
	p.termPanelSession = sessionName
	p.saveSelectionState()

	// If we already have an active session for this, just show it.
	// A --run/--type queued by sidecar create shell --split must still fire:
	// hiding the panel leaves this session in place, so the next --split
	// takes this branch and never emits TermPanelSessionCreatedMsg.
	if reusing {
		return tea.Batch(
			p.resizeTermPanelPaneCmd(),
			p.resizeSelectedPaneCmd(),
			p.applyPendingTermPanelSeed(sessionName),
		)
	}

	// Switch to the new session (old session preserved for later reuse)
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

func (p *Plugin) applyPendingTermPanelSeed(session string) tea.Cmd {
	seed := p.pendingTermPanelSeed
	if seed == nil || seed.session == "" || seed.session != session {
		return nil
	}
	p.pendingTermPanelSeed = nil
	run, typeCmd := seed.run, seed.typeCmd
	if run == "" && typeCmd == "" {
		return nil
	}
	return func() tea.Msg {
		ctx := context.Background()
		var err error
		if run != "" {
			err = workspaceops.StartAgentInShell(ctx, session, run)
		} else {
			err = workspaceops.TypeInShell(ctx, session, typeCmd)
		}
		if err != nil {
			return TermPanelSeedFailedMsg{Err: err}
		}
		return nil
	}
}

// TermPanelSeedFailedMsg is a --run/--type that could not be sent after the
// split session existed.
type TermPanelSeedFailedMsg struct {
	Err error
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
	return p.paneFocusChip(p.shellLeafTitle(), p.termPanelFocused)
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
	// The split's header carries the same ✕ every non-primary leaf has. Its
	// leaf id is what the region is registered with, so hover and the click
	// answer from one identity.
	closeLeafID := 0
	if leaf := p.shellLeaf(); leaf != nil {
		closeLeafID = leaf.ID
	}
	if p.termPanelOutput == nil {
		hintFloor := 0
		if p.interactiveDescribes(true) {
			hintFloor = p.interactiveHintFloor()
		}
		headerWidth := width
		reserve := ui.HeaderClose{CloseCol: -1}
		if closeLeafID != 0 {
			reserve = ui.ReserveHeaderClose(width)
			if reserve.CloseW > 0 {
				headerWidth = reserve.TabsWidth
			}
		}
		header := p.terminalHeader(chips, p.termPanelHints(), headerWidth, hintFloor)
		if reserve.CloseW > 0 {
			header = ui.ComposeHeaderClose(header, width, p.hoverPaneClose == closeLeafID)
		}
		if height <= terminalHeaderRows {
			return header
		}
		empty := p.truncateCache.Truncate(dimText("Starting terminal..."), width, "")
		return header + "\n" + empty
	}
	// The terminal panel has no action chips of its own; Diff and Task belong
	// to the surface's primary header.
	return p.renderCapturedTerminalWithClose(chips, nil, p.termPanelHints(), p.termPanelOutput, width, height, true, "Terminal ready", closeLeafID)
}

// refreshTermPanelForSelection points the terminal panel at the session its own
// workspace owns. A split does not follow the selection onto another workspace
// — that workspace's split is its own, restored from its own layout — so this
// only ever re-derives the session for the surface that owns the open leaf,
// which matters when a shell is renamed underneath it.
func (p *Plugin) refreshTermPanelForSelection() tea.Cmd {
	if !p.shellLeafOwnsSelection() {
		return nil
	}
	newSession := p.termPanelSessionName()
	if newSession == "" || newSession == p.termPanelSession {
		return nil
	}
	// Switch to new session (old session preserved for later reuse)
	p.termPanelSession = newSession
	p.forgetShellLeafName()
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

// killShellLeafSession ends the split's own tmux session. Only an explicit
// close does this: a hidden split is reattached by the next ctrl+t, but a closed
// one has no way back, and nothing else reaps sidecar-tp-* sessions.
func killShellLeafSession(session string) tea.Cmd {
	session = strings.TrimSpace(session)
	if !strings.HasPrefix(session, termPanelSessionPrefix) {
		return nil
	}
	return func() tea.Msg {
		_ = exec.Command("tmux", "kill-session", "-t", session).Run()
		return nil
	}
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
