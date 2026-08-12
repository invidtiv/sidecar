package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
	app "github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"golang.org/x/term"
)

// Interactive mode constants
const (
	// pollingDecayFast is the polling interval during active typing.
	pollingDecayFast = 50 * time.Millisecond

	// pollingDecayMedium is the polling interval after brief inactivity.
	pollingDecayMedium = 200 * time.Millisecond

	// pollingDecaySlow is the polling interval after extended inactivity.
	pollingDecaySlow = 500 * time.Millisecond

	// inactivityMediumThreshold triggers medium polling.
	inactivityMediumThreshold = 2 * time.Second

	// inactivitySlowThreshold triggers slow polling.
	inactivitySlowThreshold = 10 * time.Second

	// superCopyKey is the platform copy chord, which every terminal surface
	// answers alongside the configured one.
	superCopyKey = tty.SuperCopyKey
)

// mouseFragmentTimeout bounds state kept while reassembling an SGR mouse report
// split across terminal input reads.
const mouseFragmentTimeout = 50 * time.Millisecond

// partialMouseSeqRegex is now provided by the tty package as tty.PartialMouseSeqRegex

// escapeTimerMsg is sent when the escape delay timer fires.
// If pendingEscape is still true, we forward the single Escape to tmux.
type escapeTimerMsg struct{}

// InteractiveSessionDeadMsg indicates the tmux session has ended.
// Sent when send-keys or capture fails with a session/pane not found error.
type InteractiveSessionDeadMsg struct{}

// terminalConfig is the one resolution of the user's terminal-interaction
// settings this plugin works from: chords, and whether a finished selection
// copies itself.
func (p *Plugin) terminalConfig() tty.Config {
	if p.ctx == nil {
		return app.TerminalConfig(nil)
	}
	return app.TerminalConfig(p.ctx.Config)
}

func (p *Plugin) getInteractiveExitKey() string { return p.terminalConfig().ExitKey }

func (p *Plugin) getInteractiveAttachKey() string { return p.terminalConfig().AttachKey }

func (p *Plugin) getInteractiveCopyKey() string { return p.terminalConfig().CopyKey }

func (p *Plugin) getInteractivePasteKey() string { return p.terminalConfig().PasteKey }

// isTerminalCopyChord reports whether a key press asks to copy the terminal
// selection.
func (p *Plugin) isTerminalCopyChord(msg tea.KeyPressMsg) bool {
	return p.terminalConfig().IsCopyChord(msg)
}

// sendInteractiveKeysCmd sends keys to tmux asynchronously (td-c2961e).
//
// The batch is queued at call time so it keeps its place relative to the
// keystrokes around it; only the wait happens in the returned Cmd's goroutine.
// Bubble Tea runs each Cmd concurrently, so ordering established inside the Cmd
// would be no ordering at all (td-8fcd2e). Call from Update.
// Returns InteractiveSessionDeadMsg if the session has ended.
func sendInteractiveKeysCmd(sessionName string, keys ...tty.KeySpec) tea.Cmd {
	return awaitInteractiveSend(tty.SendKeysOrdered(sessionName, keys...))
}

// awaitInteractiveSend turns a queued send's result channel into a tea.Cmd.
func awaitInteractiveSend(done <-chan error) tea.Cmd {
	return func() tea.Msg {
		if err := <-done; err != nil && tty.IsSessionDeadError(err) {
			return InteractiveSessionDeadMsg{}
		}
		return nil
	}
}

func (p *Plugin) updateMouseReportingMode(output string) {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return
	}
	p.interactiveState.MouseReportingEnabled = tty.DetectMouseReportingMode(output)
}

// updateBracketedPasteMode updates the BracketedPasteEnabled state from captured output.
// Should be called whenever new output is received for the interactive pane.
func (p *Plugin) updateBracketedPasteMode(output string) {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return
	}
	p.interactiveState.BracketedPasteEnabled = tty.DetectBracketedPasteMode(output)
}

// consumeSplitMouseFragment reassembles SGR mouse reports split across input
// reads. It only retains a fragment after seeing a structurally valid SGR
// prefix, so literal m, M, ;, and < remain ordinary typing.
func (p *Plugin) consumeSplitMouseFragment(text string) bool {
	now := time.Now()
	if p.mouseFragment != "" && now.Sub(p.mouseFragmentTime) >= mouseFragmentTimeout {
		p.mouseFragment = ""
	}

	if p.mouseFragment != "" {
		combined := p.mouseFragment + text
		if possible, complete := sgrMouseFragmentState(combined); possible {
			if complete {
				p.mouseFragment = ""
			} else {
				p.rememberMouseFragment(combined)
			}
			return true
		}
		p.mouseFragment = ""
	}

	// A separately delivered Escape is held by the double-Escape logic. Check
	// whether this text completes its role as the start of an SGR report before
	// treating it as a real keyboard Escape.
	if p.interactiveState != nil && p.interactiveState.EscapePressed &&
		time.Since(p.interactiveState.EscapeTime) < mouseFragmentTimeout {
		combined := "\x1b" + text
		if possible, complete := sgrMouseFragmentState(combined); possible {
			if !complete {
				p.rememberMouseFragment(combined)
			}
			return true
		}
	}

	if possible, complete := sgrMouseFragmentState(text); possible {
		// A lone "[" is valid user input and is handled only by the existing
		// Escape/mouse-proximity gates below.
		if text == "[" {
			return false
		}
		if !complete {
			p.rememberMouseFragment(text)
		}
		return true
	}
	return false
}

func (p *Plugin) rememberMouseFragment(fragment string) {
	p.mouseFragment = fragment
	p.mouseFragmentTime = time.Now()
}

// sgrMouseFragmentState recognizes a complete SGR mouse report or any prefix
// that can become one. The grammar is ESC? "[" "<" digits ";" digits ";"
// digits ("M"|"m").
func sgrMouseFragmentState(s string) (possible, complete bool) {
	if s == "" {
		return false, false
	}
	i := 0
	if s[i] == '\x1b' {
		i++
		if i == len(s) {
			return true, false
		}
	}
	for _, want := range []byte{'[', '<'} {
		if i == len(s) {
			return true, false
		}
		if s[i] != want {
			return false, false
		}
		i++
	}
	for field := 0; field < 3; field++ {
		start := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if start == i {
			return i == len(s), false
		}
		if field < 2 {
			if i == len(s) {
				return true, false
			}
			if s[i] != ';' {
				return false, false
			}
			i++
		}
	}
	if i == len(s) {
		return true, false
	}
	if (s[i] == 'M' || s[i] == 'm') && i+1 == len(s) {
		return true, true
	}
	return false, false
}

// enterInteractiveMode enters interactive mode for the current selection.
// Returns a tea.Cmd if mode entry succeeded, nil otherwise.
// Requires tmux_interactive_input feature flag to be enabled.
func (p *Plugin) enterInteractiveMode() tea.Cmd {
	p.releaseTerminalDocProjection(false)
	// Check feature flag
	if !features.IsEnabled(features.TmuxInteractiveInput.Name) {
		return nil
	}

	// Determine target based on current selection
	var sessionName, paneID string

	if p.shellSelected {
		// Shell session
		if p.selectedShellIdx < 0 || p.selectedShellIdx >= len(p.shells) {
			return nil
		}
		shell := p.shells[p.selectedShellIdx]

		// td-f88fdd: Handle orphaned shells - recreate before entering interactive mode
		if shell.IsOrphaned {
			return p.recreateOrphanedShell(p.selectedShellIdx)
		}

		if shell.Agent == nil {
			return nil
		}
		sessionName = shell.TmuxName
		paneID = shell.Agent.TmuxPane
	} else {
		// Worktree
		wt := p.selectedWorktree()
		if wt == nil || wt.Agent == nil {
			return nil
		}
		sessionName = wt.Agent.TmuxSession
		paneID = wt.Agent.TmuxPane
	}

	// Resize tmux pane to match preview width (td-c7dd1e)
	// This ensures terminal content fits the visible area without being cut off
	target := paneID
	if target == "" {
		target = sessionName // Fall back to session name if pane ID not available
	}
	if target != "" {
		// When terminal panel is visible, agent pane only gets a portion
		var previewWidth, previewHeight int
		if p.termPanelVisible {
			previewWidth, previewHeight = p.calculateAgentPaneDimensions()
		} else {
			previewWidth, previewHeight = p.calculatePreviewDimensions()
		}
		previewWidth = p.terminalContentWidth(previewWidth)
		tty.SetWindowSizeManual(sessionName)
		// Entering interactive mode is an explicit local action; the user is
		// here, so this instance's geometry wins (td-ee222a).
		tty.ClaimGeometryLease(target)
		tty.ResizeTmuxPane(target, previewWidth, previewHeight)
		// Verify and retry once if resize didn't take effect
		if w, h, ok := tty.QueryPaneSize(target); ok && (w != previewWidth || h != previewHeight) {
			tty.ResizeTmuxPane(target, previewWidth, previewHeight)
		}
	}
	// Initialize interactive state
	p.interactiveState = &InteractiveState{
		Active:        true,
		TargetPane:    paneID,
		TargetSession: sessionName,
		LastKeyTime:   time.Now(),
		CursorVisible: true, // Assume visible until we get first cursor query result
		PaneOnEntry:   p.activePane,
	}
	// The embedded terminal owns input now, so make the preview the active pane.
	// nativeTerminalActive() gates both the native cursor and cell-motion mouse
	// reporting on it, and entering from the sidebar used to leave it behind —
	// interactive mode with no visible cursor at all (td-62b8ab).
	p.activePane = PanePreview
	p.selectionTermPanel = false
	p.clearTerminalSelection()

	p.viewMode = ViewModeInteractive

	// Invalidate existing poll timers to prevent duplicate poll chains (td-97327e).
	// Without this, entering interactive mode creates a second poll chain that runs
	// in parallel with the existing one, causing 200% CPU usage.
	if p.shellSelected {
		p.pollScheduler.Invalidate(shellPollKey(sessionName))
	} else {
		if wt := p.selectedWorktree(); wt != nil {
			p.pollScheduler.Invalidate(agentPollKey(wt.IdentityKey()))
		}
	}

	// Trigger immediate poll for fresh content (cursor position is captured atomically with output)
	cmds := []tea.Cmd{p.pollInteractivePane()}
	if !p.interactiveCopyPasteHintShown {
		p.interactiveCopyPasteHintShown = true
		cmds = append(cmds, func() tea.Msg {
			return app.ToastMsg{
				Message: fmt.Sprintf("Interactive copy/paste: %s or %s / %s (configurable)",
					p.getInteractiveCopyKey(), superCopyKey, p.getInteractivePasteKey()),
				Duration: 3 * time.Second,
			}
		})
	}
	return tea.Batch(cmds...)
}

// enterTermPanelInteractiveMode enters interactive mode targeting the terminal panel's tmux session.
func (p *Plugin) enterTermPanelInteractiveMode() tea.Cmd {
	p.releaseTerminalDocProjection(true)
	if !features.IsEnabled(features.TmuxInteractiveInput.Name) {
		return nil
	}
	if p.termPanelSession == "" || !p.termPanelVisible {
		return nil
	}
	// Resize terminal panel pane to match its split dimensions. A split too
	// small to draw has no panel to interact with.
	w, h, ok := p.calculateTermPanelDimensions()
	if !ok {
		return nil
	}

	sessionName := p.termPanelSession
	paneID := p.termPanelPaneID
	target := paneID
	if target == "" {
		target = sessionName
	}

	w = p.terminalContentWidth(w)
	tty.SetWindowSizeManual(sessionName)
	// Explicit local action: claim the terminal panel session outright rather
	// than render it at another machine's geometry (td-ee222a).
	tty.ClaimGeometryLease(target)
	tty.ResizeTmuxPane(target, w, h)
	if aw, ah, ok := tty.QueryPaneSize(target); ok && (aw != w || ah != h) {
		tty.ResizeTmuxPane(target, w, h)
	}

	p.termPanelScroll = 0 // Reset scroll so output aligns with cursor position
	p.termPanelDocFrozen = false
	p.interactiveState = &InteractiveState{
		Active:        true,
		TargetPane:    paneID,
		TargetSession: sessionName,
		TermPanel:     true,
		LastKeyTime:   time.Now(),
		CursorVisible: true,
		PaneOnEntry:   p.activePane,
	}
	p.activePane = PanePreview
	p.selectionTermPanel = true
	p.clearTerminalSelection()
	p.viewMode = ViewModeInteractive

	return p.pollInteractivePane()
}

// calculatePreviewDimensions returns the content width and height for the preview pane.
// Used to resize tmux panes to match the visible area.
// IMPORTANT: This must stay in sync with renderListView() width calculations.
// It no longer takes a selection kind: every terminal surface now reserves the
// same single header row, so a shell and a worktree are sized identically and a
// session can be sized before it is known which kind will render it (td-9b181e).
func (p *Plugin) calculatePreviewDimensions() (width, height int) {
	leaf, ok := p.terminalLeafBox()
	if !ok {
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
			return w - panelOverhead, h - panelBorderWidth - terminalHeaderRows
		}
		return 80, 24 // Safe defaults
	}

	// The pane-tree leaf includes its header, while tmux receives only the
	// terminal viewport below it.
	width = leaf.W
	height = leaf.H - terminalHeaderRows

	if width < 20 {
		width = 20
	}
	if height < 5 {
		height = 5
	}

	return width, height
}

// resizeInteractivePaneCmd resizes the active interactive tmux pane to match the UI.
// This is used after window/sidebar resizing to keep cursor position aligned.
func (p *Plugin) resizeInteractivePaneCmd() tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}

	target := p.interactiveState.TargetPane
	if target == "" {
		target = p.interactiveState.TargetSession
	}

	return p.resizeTmuxTargetCmd(target)
}

// resizeTmuxTargetCmd returns a tea.Cmd that resizes a tmux target to preview dimensions.
// Skips resize if current size already matches. Retries once if verify fails.
// Returns paneResizedMsg when the size actually changed, triggering a fresh poll
// so captured content reflects the new width/wrapping.
func (p *Plugin) resizeTmuxTargetCmd(target string) tea.Cmd {
	if target == "" {
		return nil
	}

	// Determine dimensions: terminal panel target gets terminal panel dims,
	// agent target gets split-aware dims, or full dims if no panel.
	var previewWidth, previewHeight int
	isTermPanel := p.termPanelVisible && (target == p.termPanelPaneID || target == p.termPanelSession)
	if isTermPanel {
		var drawn bool
		previewWidth, previewHeight, drawn = p.calculateTermPanelDimensions()
		if !drawn {
			// No panel is drawn at this size, so there is no geometry to assert.
			return nil
		}
	} else if p.termPanelVisible {
		previewWidth, previewHeight = p.calculateAgentPaneDimensions()
	} else {
		previewWidth, previewHeight = p.calculatePreviewDimensions()
	}
	previewWidth = p.terminalContentWidth(previewWidth)
	return func() tea.Msg {
		if actualWidth, actualHeight, ok := tty.QueryPaneSize(target); ok {
			if actualWidth == previewWidth && actualHeight == previewHeight {
				// Nothing to assert, but we are still the instance driving this
				// pane's geometry — keep the lease from going stale under us.
				tty.TouchGeometryLease(target)
				return nil
			}
		}
		tty.ResizeTmuxPane(target, previewWidth, previewHeight)
		if actualWidth, actualHeight, ok := tty.QueryPaneSize(target); ok {
			if actualWidth != previewWidth || actualHeight != previewHeight {
				tty.ResizeTmuxPane(target, previewWidth, previewHeight)
			}
		}
		return paneResizedMsg{}
	}
}

func (p *Plugin) maybeResizeInteractivePane(paneWidth, paneHeight int) tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}
	if paneWidth <= 0 || paneHeight <= 0 {
		return nil
	}

	var previewWidth, previewHeight int
	isTermPanel := p.interactiveState.TermPanel
	if isTermPanel && p.termPanelVisible {
		var drawn bool
		previewWidth, previewHeight, drawn = p.calculateTermPanelDimensions()
		if !drawn {
			return nil
		}
	} else if p.termPanelVisible {
		previewWidth, previewHeight = p.calculateAgentPaneDimensions()
	} else {
		previewWidth, previewHeight = p.calculatePreviewDimensions()
	}
	previewWidth = p.terminalContentWidth(previewWidth)
	target := p.interactiveState.TargetPane
	if target == "" {
		target = p.interactiveState.TargetSession
	}
	if target == "" {
		return nil
	}
	// This poll runs whether or not a resize follows, and it is the poll — not the
	// occasional resize — that marks this instance as the one driving the pane's
	// geometry. Ticking the lease here keeps a settled owner from looking
	// abandoned to another machine, which would hand ownership back and forth
	// every staleness budget (td-ee222a).
	touch := func() tea.Msg {
		tty.TouchGeometryLease(target)
		return nil
	}
	if paneWidth == previewWidth && paneHeight == previewHeight {
		return touch
	}

	if !p.interactiveState.LastResizeAt.IsZero() && time.Since(p.interactiveState.LastResizeAt) < 500*time.Millisecond {
		return touch
	}
	p.interactiveState.LastResizeAt = time.Now()
	// The capture already returned the actual pane size. Trust that atomic
	// observation instead of spawning two more display-message queries around
	// the resize.
	return func() tea.Msg {
		tty.ResizeTmuxPane(target, previewWidth, previewHeight)
		return paneResizedMsg{}
	}
}

// terminalContentWidth returns the columns tmux can actually render into.
func (p *Plugin) terminalContentWidth(width int) int {
	return tty.ContentWidth(width)
}

// maybeResizeVisiblePane corrects a passively displayed pane whose size drifted
// from the viewport. Creation and layout events cannot cover this on their own:
// a pane observed by a poll may have been resized by another instance, or by a
// layout change that happened while it was off screen.
func (p *Plugin) maybeResizeVisiblePane(target string, paneWidth, paneHeight int, termPanel bool) tea.Cmd {
	if target == "" || paneWidth <= 0 || paneHeight <= 0 {
		return nil
	}
	var width, height int
	if termPanel {
		var drawn bool
		width, height, drawn = p.calculateTermPanelDimensions()
		if !drawn {
			return nil
		}
	} else if p.termPanelVisible {
		width, height = p.calculateAgentPaneDimensions()
	} else {
		width, height = p.calculatePreviewDimensions()
	}
	width = p.terminalContentWidth(width)
	if paneWidth == width && paneHeight == height {
		return nil
	}
	return func() tea.Msg {
		tty.ResizeTmuxPane(target, width, height)
		return paneResizedMsg{}
	}
}

func (p *Plugin) liveTerminalOutputBuffer(termPanel bool) *tty.OutputBuffer {
	if termPanel {
		return p.termPanelOutput
	}
	if p.shellSelected {
		if shell := p.getSelectedShell(); shell != nil && shell.Agent != nil {
			return shell.Agent.OutputBuf
		}
		return nil
	}
	if wt := p.selectedWorktree(); wt != nil && wt.Agent != nil {
		return wt.Agent.OutputBuf
	}
	return nil
}

func (p *Plugin) terminalOutputBuffer(termPanel bool) *tty.OutputBuffer {
	if projected := p.projectedTerminalBuffer(termPanel); projected != nil {
		return projected
	}
	return p.liveTerminalOutputBuffer(termPanel)
}

// resizeSelectedPaneCmd resizes the currently selected tmux pane to match the
// preview dimensions. Called in non-interactive mode so that capture-pane output
// is already wrapped at the correct width.
func (p *Plugin) resizeSelectedPaneCmd() tea.Cmd {
	if !features.IsEnabled(features.TmuxInteractiveInput.Name) {
		return nil
	}
	return p.resizeTmuxTargetCmd(p.previewResizeTarget())
}

// resizeForAttachCmd resizes the tmux pane to the full terminal size before
// attaching, so the user gets the full available space without dot borders.
func (p *Plugin) resizeForAttachCmd(target string) tea.Cmd {
	if target == "" {
		return nil
	}
	return func() tea.Msg {
		w, h, err := term.GetSize(int(os.Stdout.Fd()))
		if err != nil || w <= 0 || h <= 0 {
			// Fallback to plugin dimensions
			w, h = p.width, p.height
		}
		if w <= 0 || h <= 0 {
			return nil
		}
		// Attaching is proof the user is at this machine, so it outranks another
		// instance's geometry lease. The hold, not the claim, is what makes it
		// stick: the TUI is suspended for the whole attach, so nothing here ticks
		// the lease and a peer would otherwise reclaim the session the user is
		// sitting in a few seconds in (td-ee222a). attachWithResize releases it.
		tty.HoldGeometryLease(target)
		tty.ResizeTmuxPane(target, w, h)
		return nil
	}
}

// attachWithResize resizes the tmux pane to full terminal, waits briefly for
// tmux to process, then attaches. Centralizes resize-before-attach logic.
func (p *Plugin) attachWithResize(target, sessionName, displayName string, onComplete func(error) tea.Msg) tea.Cmd {
	c := exec.Command("tmux", "attach-session", "-t", sessionName)
	termState, _ := term.GetState(int(os.Stdout.Fd()))
	wrappedOnComplete := func(err error) tea.Msg {
		// The event loop is running again, so the geometry loop takes the lease
		// back over from the background refresher resizeForAttachCmd started.
		tty.ReleaseGeometryHold(target)
		if termState != nil {
			_ = term.Restore(int(os.Stdout.Fd()), termState)
		}
		return onComplete(err)
	}
	return tea.Sequence(
		p.resizeForAttachCmd(target),
		tea.Tick(50*time.Millisecond, func(time.Time) tea.Msg { return nil }),
		tea.Printf("\nAttaching to %s. Press %s d to return to sidecar.\n", displayName, getTmuxPrefix()),
		tea.ExecProcess(c, wrappedOnComplete),
	)
}

// previewResizeTarget returns the tmux target for the currently selected pane.
func (p *Plugin) previewResizeTarget() string {
	if p.shellSelected {
		shell := p.getSelectedShell()
		if shell == nil || shell.Agent == nil {
			return ""
		}
		if shell.Agent.TmuxPane != "" {
			return shell.Agent.TmuxPane
		}
		return shell.Agent.TmuxSession
	}

	wt := p.selectedWorktree()
	if wt == nil || wt.Agent == nil {
		return ""
	}
	if wt.Agent.TmuxPane != "" {
		return wt.Agent.TmuxPane
	}
	return wt.Agent.TmuxSession
}

// exitInteractiveMode exits interactive mode and returns to list view.
func (p *Plugin) exitInteractiveMode() {
	if p.interactiveState != nil {
		// Preserve focus on whichever sub-pane was interactive
		p.termPanelFocused = p.interactiveState.TermPanel
		// Hand the pane back to whoever had it before interactive mode claimed it.
		p.activePane = p.interactiveState.PaneOnEntry
		p.interactiveState.Active = false
	}
	p.interactiveState = nil
	p.mouseFragment = ""
	p.wheel.Reset()
	p.clearTerminalSelection()
	p.viewMode = ViewModeList
}

// handleInteractivePaste forwards bracketed-paste content (delivered as a
// tea.PasteMsg in v2) to the interactive tmux session.
func (p *Plugin) handleInteractivePaste(content string) tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active || content == "" {
		return nil
	}
	terminal := p.activeInteractiveTerminal()
	if terminal == nil || !terminal.IsActive() {
		return nil
	}
	return terminal.Update(tea.PasteMsg{Content: content})
}

// handleInteractiveKeys processes key input in interactive mode.
// Returns a tea.Cmd for any async operations needed.
func (p *Plugin) handleInteractiveKeys(msg tea.KeyPressMsg) tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
		p.exitInteractiveMode()
		p.previewOffset = 0
		p.autoScrollOutput = true
		return p.pollSelectedAgentNowIfVisible()
	}
	if handled, cmd := p.handleTerminalSearchKey(msg, true); handled {
		return cmd
	}
	// Check for exit keys

	// Primary exit: Configurable key (default: Ctrl+\)
	if msg.String() == p.getInteractiveExitKey() {
		p.exitInteractiveMode()
		// Reset scroll to bottom so we see the current terminal state,
		// not stale scrollback from before interactive mode.
		p.previewOffset = 0
		p.autoScrollOutput = true
		// Trigger an immediate poll to capture fresh tmux pane content.
		return p.pollSelectedAgentNowIfVisible()
	}

	// Terminal panel toggle: intercept before forwarding to tmux
	if msg.String() == "ctrl+t" {
		cmd := p.toggleTermPanel()
		// If interactive mode survived the toggle (agent pane still active),
		// keep focus on agent pane and resize the interactive pane.
		if p.interactiveState != nil && p.interactiveState.Active && !p.interactiveState.TermPanel {
			p.termPanelFocused = false
			return tea.Batch(cmd, p.resizeInteractivePaneCmd())
		}
		return cmd
	}
	if msg.String() == "alt+t" {
		cmd := p.switchTermPanelLayout()
		if p.interactiveState != nil && p.interactiveState.Active {
			return tea.Batch(cmd, p.resizeInteractivePaneCmd())
		}
		return cmd
	}

	if handled, cmd := p.handleInteractiveScrollbackKey(msg); handled {
		return cmd
	}

	// Attach shortcut: exit interactive and attach to full session (td-fd68d1)
	if msg.String() == p.getInteractiveAttachKey() {
		isTermPanel := p.interactiveState != nil && p.interactiveState.TermPanel
		p.exitInteractiveMode()
		// Terminal panel: attach to its tmux session
		if isTermPanel && p.termPanelSession != "" {
			sessionName := p.termPanelSession
			return p.attachWithResize(sessionName, sessionName, "terminal", func(err error) tea.Msg {
				return TmuxAttachFinishedMsg{Err: err}
			})
		}
		// Attach to the appropriate agent/shell session
		if p.shellSelected {
			if idx := p.selectedShellIdx; idx >= 0 && idx < len(p.shells) {
				return p.ensureShellAndAttachByIndex(idx)
			}
		} else {
			if wt := p.selectedWorktree(); wt != nil && wt.Agent != nil {
				p.attachedSession = wt.Name
				return p.AttachToSession(wt)
			}
		}
		return nil
	}

	// Reassembling an SGR mouse report split across input reads is this surface's
	// own: it holds the partial fragment between reads, which the shared gate —
	// deciding one key at a time — cannot.
	if len(msg.Text) > 0 && p.consumeSplitMouseFragment(msg.Text) {
		p.interactiveState.EscapePressed = false
		return nil
	}

	switch tty.GateKey(tty.KeyGateInput{
		Msg:           msg,
		EscapePressed: p.interactiveState.EscapePressed,
		EscapeAt:      p.interactiveState.EscapeTime,
		LastMouseAt:   p.lastMouseEventTime,
		Now:           time.Now(),
	}) {
	case tty.KeyGateExitDoubleEscape:
		p.interactiveState.EscapePressed = false
		p.interactiveState.EscapeTimerPending = false // Cancel pending timer
		p.exitInteractiveMode()
		p.previewOffset = 0
		p.autoScrollOutput = true
		return p.pollSelectedAgentNowIfVisible()

	case tty.KeyGateHoldEscape:
		p.interactiveState.EscapePressed = true
		p.interactiveState.EscapeTime = time.Now()
		// Timer leak prevention (td-83dc22): only schedule a timer if one isn't
		// already pending.
		if p.interactiveState.EscapeTimerPending {
			return nil
		}
		p.interactiveState.EscapeTimerPending = true
		return tea.Tick(tty.DoubleEscapeDelay, func(t time.Time) tea.Msg {
			return escapeTimerMsg{}
		})

	case tty.KeyGateDrop:
		if msg.Text == "[" {
			// The bracket opened a report whose remainder is still to come, so hold
			// it for the reassembly above rather than dropping it outright.
			p.rememberMouseFragment("[")
		}
		p.interactiveState.EscapePressed = false
		return nil
	}

	// Non-escape key: check if we have a pending Escape to forward first
	pendingEscape := false
	if p.interactiveState.EscapePressed {
		p.interactiveState.EscapePressed = false
		// Timer leak prevention (td-83dc22): pending timer will be ignored when it fires
		// since EscapePressed is now false (no need to cancel, it's harmless)
		pendingEscape = true
	}

	if p.isTerminalCopyChord(msg) {
		return p.copyInteractiveSelectionCmd()
	}
	if msg.String() == "ctrl+a" && p.interactiveState != nil {
		p.selectAllTerminalOutput(p.interactiveState.TermPanel)
		return nil
	}

	if p.terminalConfig().IsPasteChord(msg) {
		p.interactiveState.LastKeyTime = time.Now()
		if !p.autoScrollOutput {
			p.autoScrollOutput = true
			p.scrollToBottom()
		}
		if terminal := p.activeInteractiveTerminal(); terminal != nil {
			return terminal.Update(msg)
		}
		return nil
	}

	// Update last key time for polling decay
	p.interactiveState.LastKeyTime = time.Now()

	// Snap back to live view if scrolled up, so user can see what they're typing
	// td-e2ce50: Multiple guards against bounce during fast scrolling:
	// 1. Don't snap back if we recently scrolled (time-based protection)
	// 2. Don't snap back for mouse sequence fragments
	// 3. Only snap back for actual user typing (single printable chars or specific keys)
	if !p.autoScrollOutput && p.shouldSnapBack(msg) {
		p.autoScrollOutput = true
		p.scrollToBottom()
	}

	terminal := p.activeInteractiveTerminal()
	if terminal == nil || !terminal.IsActive() || terminal.State == nil {
		// A mode-entry message can precede the wrapper's reconciliation by one
		// update. Preserve that narrow provisional input path; once present, the
		// shared component is the only sender.
		target := p.interactiveState.TargetPane
		if target == "" {
			target = p.interactiveState.TargetSession
		}
		key, literal := tty.MapKeyToTmux(msg)
		if key == "" && !pendingEscape {
			return nil
		}
		var keys []tty.KeySpec
		if pendingEscape {
			keys = append(keys, tty.KeySpec{Value: "Escape"})
		}
		if key != "" {
			keys = append(keys, tty.KeySpec{Value: key, Literal: literal})
		}
		return sendInteractiveKeysCmd(target, keys...)
	}
	// Preserve workspace's double-Escape UX while handing ordered input and
	// fallback scheduling to the shared terminal component.
	if pendingEscape {
		terminal.State.EscapePressed = true
		terminal.State.EscapeTime = p.interactiveState.EscapeTime
	}
	return terminal.Update(msg)
}

// handleUnknownSequence forwards unrecognized CSI sequences to tmux in
// interactive mode. Bubble Tea does not parse CSI u (kitty keyboard protocol)
// or modifyOtherKeys sequences, so modified keys like shift+enter arrive as
// unknownCSISequenceMsg. Normalization and delivery belong to the shared
// terminal component, which already owns ordered input for this pane.
func (p *Plugin) handleUnknownSequence(msg tea.Msg) tea.Cmd {
	if p.viewMode != ViewModeInteractive {
		return nil
	}
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}
	if terminal := p.activeInteractiveTerminal(); terminal != nil && terminal.IsActive() {
		return terminal.SendUnknownSequence(msg)
	}
	// The same provisional window handleInteractiveKeys keeps: a mode-entry
	// message can precede the wrapper's reconciliation by one update. Without a
	// fallback here, ordinary keys reach the pane during that window while
	// modified ones — shift+enter, ctrl+enter — are silently dropped.
	csiu := tty.NormalizeToCSIu(tty.ExtractUnknownCSIBytes(msg))
	if csiu == "" {
		return nil
	}
	target := p.interactiveState.TargetPane
	if target == "" {
		target = p.interactiveState.TargetSession
	}
	return sendInteractiveKeysCmd(target, tty.KeySpec{Value: csiu, Literal: true})
}

// handleEscapeTimer processes the escape delay timer firing.
// If a single Escape is still pending (no second Escape arrived), forward it to tmux.
func (p *Plugin) handleEscapeTimer() tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}

	// Timer leak prevention (td-83dc22): clear the pending flag since timer has fired
	p.interactiveState.EscapeTimerPending = false

	if !p.interactiveState.EscapePressed {
		// Escape was already handled (double-press or another key arrived)
		return nil
	}

	// Timer fired with pending Escape: forward the single Escape to tmux async (td-c2961e)
	p.interactiveState.EscapePressed = false

	// Update last key time and let the shared terminal own ordered delivery and
	// provisional fallback scheduling.
	p.interactiveState.LastKeyTime = time.Now()
	terminal := p.activeInteractiveTerminal()
	if terminal == nil || terminal.State == nil {
		return nil
	}
	terminal.State.EscapePressed = true
	terminal.State.EscapeTimerPending = true
	return terminal.Update(tty.EscapeTimerMsg{Scope: terminal.Scope()})
}

// forwardScrollToTmux routes a wheel notch for the interactive pane.
//
// When the app running in the pane has enabled mouse tracking, the notch is its
// event: it is encoded as an SGR wheel report and sent to the pane, exactly as a
// real terminal emulator would. Full-screen apps like Claude Code draw their own
// scrollback inside the pane and keep tmux's history empty, so consuming the
// notch locally would slide the viewport across the app's live frame and leave
// the layout looking torn (the reported symptom).
//
// Otherwise the notch scrolls the captured pane output using previewOffset. No
// tmux subprocesses needed — we scroll through the already-captured capture
// window (captureLineCount) of scrollback. Scroll up (delta < 0) pauses
// auto-scroll, scroll down (delta > 0)
// moves toward live output.
func (p *Plugin) forwardScrollToTmux(action mouse.MouseAction, delta int) tea.Cmd {
	delta, flush := p.wheel.Add(delta, time.Now())
	if !flush {
		return nil
	}

	if cmd, forwarded := p.forwardWheelToPane(action, delta); forwarded {
		return cmd
	}

	// When interactive mode targets the terminal panel, scroll terminal panel output
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		p.clearTerminalSelection()
		p.termPanelScroll -= delta
		if p.termPanelScroll < 0 {
			p.termPanelScroll = 0
		}
		if maxScroll := p.termPanelMaxScroll(); p.termPanelScroll > maxScroll {
			p.termPanelScroll = maxScroll
		}
		if delta > 0 && p.termPanelScroll == 0 {
			p.cancelTerminalHistoryIntent(true)
		}
		if delta < 0 && p.termPanelScroll == p.termPanelMaxScroll() {
			return p.loadOlderTerminalHistory(true, -delta)
		}
		return nil
	}

	maxOffset := p.getMaxScrollOffset()
	if delta < 0 {
		// Scroll up: move toward top of content
		if p.autoScrollOutput && maxOffset >= p.previewOffset {
			p.previewOffset = maxOffset
		}
		p.previewOffset += delta
		if p.previewOffset < 0 {
			p.previewOffset = 0
		}
		p.autoScrollOutput = false
		if p.previewOffset == 0 {
			return p.loadOlderTerminalHistory(false, -delta)
		}
	} else {
		// Scroll down: move toward bottom of content
		p.previewOffset += delta
		if p.previewOffset > maxOffset {
			p.previewOffset = maxOffset
		}
		if p.previewOffset >= maxOffset {
			p.autoScrollOutput = true
			p.cancelTerminalHistoryIntent(false)
		}
	}
	return nil
}

// forwardWheelToPane sends delta as SGR wheel reports when the notch belongs to
// the app running in the interactive pane. It reports forwarded=false whenever
// the notch belongs to the local viewport instead, so the caller falls through
// to its scrollback handling unchanged.
func (p *Plugin) forwardWheelToPane(action mouse.MouseAction, delta int) (tea.Cmd, bool) {
	terminal := p.activeInteractiveTerminal()
	reporting := terminal != nil && terminal.PaneMouseReporting()
	var col, row int
	inPane := false
	if reporting {
		col, row, inPane = p.interactiveMouseCoords(action.X, action.Y)
	}
	route, notches := tty.RouteWheel(tty.WheelInput{
		Delta:          delta,
		Shift:          action.Shift,
		Alt:            action.Alt,
		MouseReporting: reporting,
		InPane:         inPane,
	})
	if route != tty.WheelPane {
		return nil, false
	}

	// While the app owns the wheel it also owns what the pane shows, so the
	// viewport is pinned to the live frame. Without this a viewport left
	// scrolled back — by alt+wheel, or by plain wheel from before the app
	// enabled tracking — would sit frozen over stale rows while the app
	// repainted below it.
	p.pinInteractiveViewportToLive()

	// The wheel is the user's most recent input, so it counts as activity for
	// this surface's own poll cadence as well as the component's: the cadence
	// decays to a slow tier on idle time, and a scroll that did not reset it
	// would be repainted at that tier.
	p.interactiveState.LastKeyTime = time.Now()

	return tea.Batch(
		terminal.SendWheelNotches(delta < 0, col, row, notches),
		p.pollInteractivePaneImmediate(),
	), true
}

func wheelNotchesForDelta(delta int) int {
	return tty.WheelNotches(delta)
}

// pinInteractiveViewportToLive returns the interactive viewport to the live edge
// of the captured output, dropping any pending request for older history.
//
// A selection is anchored to buffer lines, so a jump this large leaves it
// highlighting rows the user never picked — the local scroll paths clear it for
// the same reason. Nothing is touched when the viewport is already live.
func (p *Plugin) pinInteractiveViewportToLive() {
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		if p.termPanelScroll != 0 {
			p.clearTerminalSelection()
			p.termPanelScroll = 0
			p.cancelTerminalHistoryIntent(true)
		}
		return
	}
	maxOffset := p.getMaxScrollOffset()
	if p.autoScrollOutput && p.previewOffset >= maxOffset {
		return
	}
	p.clearTerminalSelection()
	p.previewOffset = maxOffset
	p.autoScrollOutput = true
	p.cancelTerminalHistoryIntent(false)
}

// handleInteractiveScrollbackKey walks the window through scrollback while a
// pane is live. Which keys mean what is the shared layer's; applying the move to
// this surface — and reaching further back for history it has not loaded yet —
// is this one's.
func (p *Plugin) handleInteractiveScrollbackKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	pageSize := p.getPreviewVisibleHeight()
	termPanel := p.interactiveState != nil && p.interactiveState.TermPanel
	if termPanel {
		if _, panelHeight, ok := p.calculateTermPanelDimensions(); ok {
			pageSize = panelHeight
		}
	}
	move, ok := tty.MapScrollbackKey(msg, pageSize)
	if !ok {
		return false, nil
	}

	switch {
	case move.ToOldest:
		if termPanel {
			p.clearTerminalSelection()
			p.termPanelScroll = p.termPanelMaxScroll()
			return true, p.loadOlderTerminalHistory(true, historyLoadChunk)
		}
		p.previewOffset = 0
		p.autoScrollOutput = false
		return true, p.loadOlderTerminalHistory(false, historyLoadChunk)
	case move.ToLive:
		if termPanel {
			p.clearTerminalSelection()
			p.termPanelScroll = 0
			p.cancelTerminalHistoryIntent(true)
			return true, nil
		}
		p.previewOffset = p.getMaxScrollOffset()
		p.autoScrollOutput = true
		p.cancelTerminalHistoryIntent(false)
		return true, nil
	}
	// This surface browses by an absolute top offset, so older output is a
	// smaller offset: the shared move counts rows backwards.
	return true, p.scrollInteractiveViewport(-move.Rows)
}

func (p *Plugin) scrollInteractiveViewport(delta int) tea.Cmd {
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		p.clearTerminalSelection()
		p.termPanelScroll -= delta
		p.termPanelScroll = min(max(p.termPanelScroll, 0), p.termPanelMaxScroll())
		if delta > 0 && p.termPanelScroll == 0 {
			p.cancelTerminalHistoryIntent(true)
		}
		if delta < 0 && p.termPanelScroll == p.termPanelMaxScroll() {
			return p.loadOlderTerminalHistory(true, -delta)
		}
		return nil
	}

	maxOffset := p.getMaxScrollOffset()
	if delta < 0 && p.autoScrollOutput && maxOffset >= p.previewOffset {
		p.previewOffset = maxOffset
	}
	p.previewOffset = min(max(p.previewOffset+delta, 0), maxOffset)
	p.autoScrollOutput = p.previewOffset >= maxOffset
	if delta > 0 && p.autoScrollOutput {
		p.cancelTerminalHistoryIntent(false)
	}
	if delta < 0 && p.previewOffset == 0 {
		return p.loadOlderTerminalHistory(false, -delta)
	}
	return nil
}

// forwardClickToTmux hands a click to the application running in the interactive
// pane. It goes out through the same component the keystrokes do, so a click and
// the keys around it keep their order rather than racing as separate commands,
// and whether the click belongs to the application at all is the component's one
// answer — the same one the wheel asks.
func (p *Plugin) forwardClickToTmux(x, y int) tea.Cmd {
	terminal := p.activeInteractiveTerminal()
	if terminal == nil || !terminal.PaneMouseReporting() {
		return nil
	}
	col, row, ok := p.interactiveMouseCoords(x, y)
	if !ok {
		return nil
	}
	return terminal.SendClick(col, row)
}

func (p *Plugin) interactiveMouseCoords(x, y int) (col, row int, ok bool) {
	if p.width <= 0 || p.height <= 0 {
		return 0, 0, false
	}
	if !p.shellSelected && p.previewTab != PreviewTabOutput {
		return 0, 0, false
	}

	// Origin and size of the surface interactive mode is targeting. This used to
	// re-derive the terminal panel's offset from calculatePreviewDimensions while
	// the split itself divides the container height, so in the bottom layout it
	// landed a row off — and a click was forwarded to the wrong tmux row — for
	// every window height where the two floors disagreed. There is now one
	// derivation, and it is the one the render path draws with.
	targetingTermPanel := p.interactiveState != nil && p.interactiveState.Active &&
		p.interactiveState.TermPanel && p.termPanelVisible
	surface := p.terminalSurfaceGeometry(targetingTermPanel)
	if !surface.OK {
		return 0, 0, false
	}

	// The pane's real geometry decides what is on screen where, so hit testing
	// reads the layout the render path produced rather than re-deriving one: a
	// wider pane is drawn horizontally scrolled, a taller one starts partway
	// down, and the scrollbar takes a column off both (td-73fa86).
	paneWidth, paneHeight := p.resolvedPaneGeometry(targetingTermPanel, p.interactiveDescribes(targetingTermPanel))
	if paneWidth <= 0 || paneHeight <= 0 {
		paneWidth, paneHeight = surface.Width, surface.Height
	}

	return tty.PaneCoordsAt(p.terminalSelectionViewportLayout(),
		x-surface.X, y-surface.Y, paneWidth, paneHeight)
}

// pollInteractivePane schedules a poll for interactive mode with adaptive timing.
func (p *Plugin) pollInteractivePane() tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}
	if terminal := p.activeInteractiveTerminal(); terminal != nil && terminal.IsActive() {
		return nil
	}

	// Determine polling interval based on activity
	interval := pollingDecayFast
	inactivity := time.Since(p.interactiveState.LastKeyTime)

	if inactivity > inactivitySlowThreshold {
		interval = pollingDecaySlow
	} else if inactivity > inactivityMediumThreshold {
		interval = pollingDecayMedium
	}
	if remaining, scrolling := p.interactiveScrollDelay(); scrolling {
		interval = remaining
	}

	if p.interactiveState.TermPanel {
		return nil
	}

	// Use existing shell or worktree polling mechanism
	// Worktrees use scheduleInteractivePoll to skip stagger (td-8856c9)
	if p.shellSelected && p.selectedShellIdx >= 0 && p.selectedShellIdx < len(p.shells) {
		return p.scheduleShellPollByName(p.shells[p.selectedShellIdx].TmuxName, interval)
	}
	if wt := p.selectedWorktree(); wt != nil {
		return p.scheduleInteractivePoll(wt.Name, interval)
	}
	return nil
}

// pollInteractivePaneImmediate schedules an immediate poll for interactive mode (td-babfd9).
// Used after keystrokes to minimize latency - captures output immediately rather than
// waiting for the next poll cycle.
func (p *Plugin) pollInteractivePaneImmediate() tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}
	if terminal := p.activeInteractiveTerminal(); terminal != nil && terminal.IsActive() {
		return nil
	}

	delay := time.Duration(0)
	if remaining, scrolling := p.interactiveScrollDelay(); scrolling {
		delay = remaining
	}

	if p.interactiveState.TermPanel {
		return nil
	}

	// Schedule with 0ms delay for immediate capture (td-8856c9: no stagger for worktrees)
	if p.shellSelected && p.selectedShellIdx >= 0 && p.selectedShellIdx < len(p.shells) {
		return p.scheduleShellPollByName(p.shells[p.selectedShellIdx].TmuxName, delay)
	}
	if wt := p.selectedWorktree(); wt != nil {
		return p.scheduleInteractivePoll(wt.Name, delay)
	}
	return nil
}

// interactiveScrollDelay reports how long a poll should be deferred while the
// user is mid-flick. Callers reschedule instead of returning nil so the poll
// chain always has a continuation.
//
// The deferral only makes sense when the flick moves sidecar's own viewport,
// which needs no capture to repaint. When the app running in the pane owns the
// wheel, the flick changed the pane itself, and holding the capture back for the
// rest of the burst window is exactly the wrong thing — it is the difference
// between scrolling that tracks the wheel and scrolling that lurches.
func (p *Plugin) interactiveScrollDelay() (time.Duration, bool) {
	if terminal := p.activeInteractiveTerminal(); terminal != nil && terminal.PaneMouseReporting() {
		return 0, false
	}
	return p.wheel.Remaining(time.Now())
}

// getCursorPosition returns the cached cursor position for rendering (td-648af4).
// This NEVER spawns subprocesses - it only returns cached state updated by
// queryCursorPositionCmd() which runs asynchronously during polling.
// Returns the cursor row, column (0-indexed), pane height, and whether the cursor is visible.
func (p *Plugin) getCursorPosition() (row, col, paneHeight, paneWidth int, visible bool, err error) {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return 0, 0, 0, 0, false, nil
	}

	// Return cached values - never spawn subprocess from View()
	return p.interactiveState.CursorRow, p.interactiveState.CursorCol, p.interactiveState.PaneHeight, p.interactiveState.PaneWidth, p.interactiveState.CursorVisible, nil
}

func shouldOverlayCursor(interactive, cursorVisible, atLiveEdge bool) bool {
	return interactive && cursorVisible && atLiveEdge
}

// shouldSnapBack reports whether a key is real typing, which a viewport parked
// in scrollback owes a jump to the live edge.
func (p *Plugin) shouldSnapBack(msg tea.KeyPressMsg) bool {
	return tty.ShouldSnapBack(msg, time.Since(p.wheel.LastAt()))
}
