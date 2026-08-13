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

// updateBracketedPasteMode updates the BracketedPasteEnabled state from captured output.
// Should be called whenever new output is received for the interactive pane.
func (p *Plugin) updateBracketedPasteMode(output string) {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return
	}
	p.interactiveState.BracketedPasteEnabled = tty.DetectBracketedPasteMode(output)
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
	selectedShell := p.getSelectedShell()
	if selectedShell != nil {
		// Shell session
		// Only a top-level shell belongs to this worktree's recreation lifecycle.
		// Nested sibling shells are live projections and must never be recreated
		// from the current workDir.
		if p.shellSelected && selectedShell.IsOrphaned {
			return p.recreateOrphanedShell(p.selectedShellIdx)
		}

		if selectedShell.Agent == nil {
			return nil
		}
		sessionName = selectedShell.Agent.TmuxSession
		if sessionName == "" {
			sessionName = selectedShell.TmuxName
		}
		paneID = selectedShell.Agent.TmuxPane
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
	// The embedded terminal owns input, so the preview is the active pane:
	// nativeTerminalActive() gates both the native cursor and cell-motion mouse
	// reporting on it, and entering from the sidebar without this is interactive
	// mode with no visible cursor at all (td-62b8ab).
	p.activePane = PanePreview
	p.selectionTermPanel = false
	p.clearTerminalSelection()

	p.viewMode = ViewModeInteractive

	// Invalidate existing poll timers to prevent duplicate poll chains (td-97327e).
	// Without this, entering interactive mode creates a second poll chain that runs
	// in parallel with the existing one, causing 200% CPU usage.
	if selectedShell != nil {
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
	p.releaseTermPanelWindowPin()
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
// It takes no selection kind: every terminal surface reserves the same single
// header row, so a shell and a worktree are sized identically and a session can
// be sized before it is known which kind will render it (td-9b181e).
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
	if cmd, owned := p.resizeThroughTerminal(target, previewWidth, previewHeight); owned {
		return cmd
	}
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

	// The budget is the shared one, and waiting it out is not dropping the
	// resize: the pane is still drawn at the size it has not been given, so one
	// deferred assertion is armed for the whole window.
	if wait := tty.ResizeWait(p.interactiveState.LastResizeAt, time.Now()); wait > 0 {
		return tea.Batch(touch, p.deferInteractivePaneResize(wait))
	}
	p.interactiveState.LastResizeAt = time.Now()
	// A direct call, not the component's: this corrects a pane whose real size
	// drifted from what this instance asked for — another machine resized it —
	// and the component, believing it already asked for these dimensions, would
	// skip exactly the resize that fixes it. The size comes from the capture,
	// which observed it atomically with the output.
	return func() tea.Msg {
		tty.ResizeTmuxPane(target, previewWidth, previewHeight)
		return paneResizedMsg{}
	}
}

// deferInteractivePaneResize arms one assertion of the pane's geometry for the
// whole debounce window. The retry reads the geometry the surface holds when it
// fires, which is the newest by then, so a burst of sizes needs exactly one.
func (p *Plugin) deferInteractivePaneResize(wait time.Duration) tea.Cmd {
	if p.interactiveState == nil || p.interactiveState.ResizeRetryPending {
		return nil
	}
	p.interactiveState.ResizeRetryPending = true
	return tea.Tick(wait, func(time.Time) tea.Msg { return deferredPaneResizeMsg{} })
}

// resizeThroughTerminal hands a resize to the component that owns the pane. The
// component debounces, restarts its control subscription at the new size, and
// skips tmux entirely when the pane already has the size asked for — none of
// which a second transport beside it would know about.
//
// A pane with no live component still has to be resized directly: geometry is
// asserted for panes this surface merely draws, and for ones it is about to
// hand to a full-screen attach.
func (p *Plugin) resizeThroughTerminal(target string, width, height int) (tea.Cmd, bool) {
	model := p.terminalModelForTarget(target)
	if model == nil {
		return nil, false
	}
	return model.SetDimensions(width, height), true
}

// terminalModelForTarget is the live component drawing target, if this surface
// has one.
func (p *Plugin) terminalModelForTarget(target string) *tty.Model {
	if target == "" {
		return nil
	}
	for _, model := range []*tty.Model{p.primaryTerminal, p.panelTerminal} {
		if model != nil && model.IsActive() && model.GetTarget() == target {
			return model
		}
	}
	return nil
}

// terminalContentWidth returns the columns tmux can actually render into.
func (p *Plugin) terminalContentWidth(width int) int {
	return tty.ContentWidth(width)
}

// maybeResizeVisiblePane corrects a passively displayed pane whose size drifted
// from the viewport. Creation and layout events cannot cover this on their own:
// a pane observed by a poll may have been resized by another instance, or by a
// layout change that happened while it was off screen. Like the interactive
// corrector, it asserts geometry directly rather than through the component,
// whose own answer to "this is already the size I asked for" is what the drift
// contradicts.
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
	if p.selectingShell() {
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
	if shell := p.getSelectedShell(); shell != nil {
		if shell.Agent == nil {
			return shell.TmuxName
		}
		if shell.Agent.TmuxPane != "" {
			return shell.Agent.TmuxPane
		}
		if shell.Agent.TmuxSession != "" {
			return shell.Agent.TmuxSession
		}
		return shell.TmuxName
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
//
// Every way out goes through here, including the ones that never reach the
// component — a click landing off every terminal region, a filter opening, a
// selection change. The component keeps the escape window and any half-read
// mouse report, and ExitReleasesInput leaves it active behind this surface, so
// a mode left without releasing input still delivers the escape timer it
// scheduled to a pane the user has walked away from.
func (p *Plugin) exitInteractiveMode() {
	if terminal := p.activeInteractiveTerminal(); terminal != nil {
		terminal.ReleaseInput()
	}
	if p.interactiveState != nil {
		// Preserve focus on whichever sub-pane was interactive
		p.termPanelFocused = p.interactiveState.TermPanel
		// Hand the pane back to whoever had it before interactive mode claimed it.
		p.activePane = p.interactiveState.PaneOnEntry
		p.interactiveState.Active = false
	}
	p.interactiveState = nil
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

// handleInteractiveKeys hands a key to the pane's terminal component, which owns
// the whole live-key pipeline: the ways out, the double-escape window, mouse
// reports split across reads, and ordered delivery. What is left here is what
// belongs to the surface around the pane, and it is registered as the
// component's hooks rather than run ahead of it.
func (p *Plugin) handleInteractiveKeys(msg tea.KeyPressMsg) tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return p.leaveInteractiveMode()
	}
	terminal, open := p.interactiveTerminal()
	if terminal == nil {
		return nil
	}
	return tea.Batch(open, terminal.Update(msg))
}

// interactiveTerminal is the component behind the live pane, opened here if a
// mode-entry message has outrun the wrapper's reconciliation: the component is
// the only sender, so the mode cannot be live without it.
//
// Opening returns work — the control client's mailbox and the first capture —
// and a caller that drops it leaves the subprocess running with nobody draining
// it and no frame ever scheduled.
func (p *Plugin) interactiveTerminal() (*tty.Model, tea.Cmd) {
	terminal := p.activeInteractiveTerminal()
	if terminal == nil {
		if p.primaryTerminal == nil || p.panelTerminal == nil {
			p.resetTerminalModels()
		}
		terminal = p.activeInteractiveTerminal()
	}
	if terminal == nil {
		return nil, nil
	}
	if !terminal.IsActive() {
		return terminal, terminal.Open(tty.Target{
			Session: p.interactiveState.TargetSession,
			Pane:    p.interactiveState.TargetPane,
		})
	}
	return terminal, nil
}

// interactiveKey is the component's OnKey hook: the chords that act on the
// surface around the pane rather than on the pane itself.
func (p *Plugin) interactiveKey(msg tea.KeyPressMsg) (tea.Cmd, bool) {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil, false
	}
	if handled, cmd := p.handleTerminalSearchKey(msg, true); handled {
		return cmd, true
	}
	switch msg.String() {
	case "ctrl+t":
		cmd := p.toggleTermPanel()
		// If interactive mode survived the toggle (agent pane still active),
		// keep focus on agent pane and resize the interactive pane.
		if p.interactiveState != nil && p.interactiveState.Active && !p.interactiveState.TermPanel {
			p.termPanelFocused = false
			return tea.Batch(cmd, p.resizeInteractivePaneCmd()), true
		}
		return cmd, true
	case "alt+t":
		cmd := p.switchTermPanelLayout()
		if p.interactiveState != nil && p.interactiveState.Active {
			return tea.Batch(cmd, p.resizeInteractivePaneCmd()), true
		}
		return cmd, true
	}
	// Everything above is this surface's own — a search over its own buffer and
	// the panel the global browser does not draw. What is left is the set every
	// host answers, in the order the shared layer answers it.
	return p.terminalConfig().ResolveSurfaceChord(msg, tty.SurfaceChords{
		Copy: p.copyInteractiveSelectionCmd,
		SelectAll: func() tea.Cmd {
			p.selectAllTerminalOutput(p.interactiveState.TermPanel)
			return nil
		},
		Scrollback: func(key tea.KeyPressMsg) (tea.Cmd, bool) {
			handled, cmd := p.handleInteractiveScrollbackKey(key)
			return cmd, handled
		},
	})
}

// beforeInteractiveSend runs for a key on its way to the pane. Typing is owed a
// view of itself: a viewport left in scrollback would take the keystroke and
// show none of it.
func (p *Plugin) beforeInteractiveSend(msg tea.KeyPressMsg) {
	if p.interactiveState == nil {
		return
	}
	p.interactiveState.LastKeyTime = time.Now()
	if p.previewScroll == 0 && !p.previewFreeze.Active() {
		return
	}
	// A paste is the user's own act on the viewport, so it snaps back
	// unconditionally; ordinary keys defer to the shared rule, which ignores the
	// mouse-report bytes a flick leaks as text.
	if p.terminalConfig().IsPasteChord(msg) || p.shouldSnapBack(msg) {
		p.jumpPreviewWindow(0)
	}
}

// leaveInteractiveMode is the component's OnExit hook and the plugin's own way
// out. The window stays where the reader left it — the shared rule's answer,
// and the same one the global preview gives (td-2e3738). This surface used to
// snap it to the live edge, which was drift from the two window models rather
// than a decision: leaving the mode does not replace the buffer, so the rows on
// screen are the ones the reader was reading, and the window keeps following
// output from there as soon as it is back at zero.
func (p *Plugin) leaveInteractiveMode() tea.Cmd {
	termPanel := p.interactiveState != nil && p.interactiveState.TermPanel
	p.exitInteractiveMode()
	if termPanel {
		p.termPanelScroll = tty.LeaveLiveWindow(&p.termPanelFreeze, p.termPanelScroll, p.termPanelMaxScroll())
		p.termPanelFreezeDoc = false
	} else {
		p.previewScroll = tty.LeaveLiveWindow(&p.previewFreeze, p.previewScroll, p.previewWindowBound())
		p.previewFreezeDoc = false
	}
	return p.pollSelectedAgentNowIfVisible()
}

// noteSessionEnded is the component's OnSessionEnded hook: a pane that died
// under a keystroke or a forwarded click ends the mode by itself, and a mode
// that ends with no notice reads as a dropped keystroke.
func (p *Plugin) noteSessionEnded() tea.Cmd {
	if p.interactiveState == nil {
		return nil
	}
	cmd := p.leaveInteractiveMode()
	p.toastMessage = "Session ended"
	p.toastTime = time.Now()
	return cmd
}

// attachFromInteractive is the component's OnAttach hook: leave the embedded
// pane and hand the user the full tmux session (td-fd68d1).
func (p *Plugin) attachFromInteractive() tea.Cmd {
	isTermPanel := p.interactiveState != nil && p.interactiveState.TermPanel
	p.exitInteractiveMode()
	if isTermPanel && p.termPanelSession != "" {
		sessionName := p.termPanelSession
		return p.attachWithResize(sessionName, sessionName, "terminal", func(err error) tea.Msg {
			return TmuxAttachFinishedMsg{Err: err}
		})
	}
	if shell := p.getSelectedShell(); shell != nil {
		return p.ensureShellAndAttach(shell)
	} else {
		if wt := p.selectedWorktree(); wt != nil && wt.Agent != nil {
			p.attachedSession = wt.Name
			return p.AttachToSession(wt)
		}
	}
	return nil
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
	// A mode-entry message can precede the wrapper's reconciliation by one
	// update. Without a fallback for that window, ordinary keys reach the pane
	// through the component interactiveTerminal opens while modified ones —
	// shift+enter, ctrl+enter — are silently dropped.
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

// forwardScrollToTmux routes a wheel notch for the interactive pane.
//
// When the app running in the pane has enabled mouse tracking, the notch is its
// event: it is encoded as an SGR wheel report and sent to the pane, exactly as a
// real terminal emulator would. Full-screen apps like Claude Code draw their own
// scrollback inside the pane and keep tmux's history empty, so consuming the
// notch locally would slide the viewport across the app's live frame and leave
// the layout looking torn (the reported symptom).
//
// Otherwise the notch moves this surface's own window through the captured pane
// output. No tmux subprocesses needed — we scroll through the already-captured
// capture window (captureLineCount) of scrollback. Scroll up (delta < 0) steps
// back through scrollback, scroll down (delta > 0) moves toward live output.
func (p *Plugin) forwardScrollToTmux(action mouse.MouseAction, delta int) tea.Cmd {
	terminal := p.activeInteractiveTerminal()
	return tty.WheelHandler{
		Burst:          &p.wheel,
		MouseReporting: func() bool { return terminal != nil && terminal.PaneMouseReporting() },
		PaneCoords:     p.interactiveMouseCoords,
		// While the app owns the wheel it also owns what the pane shows, so the
		// viewport is pinned to the live frame.
		PinToLive: p.pinInteractiveViewportToLive,
		// The wheel is the user's most recent input, so it counts as activity for
		// this surface's own poll cadence as well as the component's: the cadence
		// decays to a slow tier on idle time, and a scroll that did not reset it
		// would be repainted at that tier.
		NoteActivity: func() { p.interactiveState.LastKeyTime = time.Now() },
		SendNotches: func(up bool, col, row, notches int) tea.Cmd {
			return terminal.SendWheelNotches(up, col, row, notches)
		},
		// Every notch the application has not claimed moves this surface's own
		// window, which is what makes the wheel work over a plain shell.
		ScrollLocal: p.scrollInteractiveViewportByWheel,
	}.Handle(tty.WheelGesture{
		Delta: delta, X: action.X, Y: action.Y,
		Shift: action.Shift, Alt: action.Alt, Now: p.now(),
	})
}

// pinInteractiveViewportToLive returns the interactive viewport to the live edge
// of the captured output, dropping any pending request for older history.
//
// A selection is anchored to buffer lines, so a jump this large leaves it
// highlighting rows the user never picked — the local scroll paths clear it for
// the same reason. Nothing is touched when the viewport is already live.
func (p *Plugin) pinInteractiveViewportToLive() {
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		if p.termPanelScroll != 0 || p.termPanelFreeze.Active() {
			p.clearTerminalSelection()
			// A jump chooses its own window, so the pin is dropped rather than thawed.
			p.releaseTermPanelWindowPin()
			p.termPanelScroll = 0
			p.cancelTerminalHistoryIntent(true)
		}
		return
	}
	if p.previewScroll == 0 && !p.previewFreeze.Active() {
		return
	}
	p.clearTerminalSelection()
	// A jump chooses its own window, so any pin is dropped rather than thawed.
	p.jumpPreviewWindow(0)
	p.cancelTerminalHistoryIntent(false)
}

// handleInteractiveScrollbackKey walks the window through scrollback while a
// pane is live. Which keys mean what is the shared layer's; applying the move to
// this surface — and reaching further back for history it has not loaded yet —
// is this one's.
func (p *Plugin) handleInteractiveScrollbackKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	// Every unshifted key is the pane's, and every key typed into a pane comes
	// through here. Resolve the layout only for the keys the shared rule claims,
	// or ordinary typing pays for a page size no one asked for.
	if !tty.IsScrollbackKey(msg) {
		return false, nil
	}
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

	p.clearTerminalSelectionOnScroll(termPanel)

	switch {
	case move.ToOldest:
		if termPanel {
			p.releaseTermPanelWindowPin()
			p.termPanelScroll = p.termPanelMaxScroll()
			return true, p.loadOlderTerminalHistory(true, historyLoadChunk)
		}
		p.jumpPreviewWindow(p.previewMaxScroll())
		return true, p.loadOlderTerminalHistory(false, historyLoadChunk)
	case move.ToLive:
		if termPanel {
			p.releaseTermPanelWindowPin()
			p.termPanelScroll = 0
			p.cancelTerminalHistoryIntent(true)
			return true, nil
		}
		p.jumpPreviewWindow(0)
		p.cancelTerminalHistoryIntent(false)
		return true, nil
	}
	// Both surfaces count their window in rows back from the live bottom, which
	// is the direction the shared move already counts in.
	return true, p.scrollInteractiveViewport(move.Rows)
}

// scrollTerminalWindowByWheel places one coalesced wheel notch on a terminal
// surface — the panel or the primary one — and is the whole of what a local
// notch does to that surface, so the passive panel, interactive mode and the
// watched preview all answer the same way (td-c3649a).
//
// The order inside it is load-bearing. Thawing comes first, before the
// selection is answered: while a window is pinned to an absolute start the
// surface reads a document projection rather than its live buffer, and that
// snapshot carries no absolute coordinates, so a selection asked about it is
// dropped for a reason that stops being true one statement later. Thawing first
// also hands a gesture's pin back as a distance from the live bottom, where
// clearing the selection ahead of it releases that pin outright and leaves the
// placement below resuming from a stale offset.
func (p *Plugin) scrollTerminalWindowByWheel(termPanel bool, rows int) tea.Cmd {
	if termPanel {
		p.thawTermPanelWindow()
		p.clearTerminalSelectionOnScroll(true)
		p.scrollTermPanelWindowRows(rows)
		if rows > 0 && p.termPanelScroll == 0 {
			p.cancelTerminalHistoryIntent(true)
		}
		if rows < 0 && p.termPanelScroll == p.termPanelMaxScroll() {
			return p.loadOlderTerminalHistory(true, -rows)
		}
		return nil
	}

	p.releaseTerminalDocProjection(false)
	p.thawPreviewWindow()
	p.clearTerminalSelectionOnScroll(false)
	p.scrollPreviewWindowRows(rows)
	if rows > 0 && p.previewScroll == 0 {
		p.cancelTerminalHistoryIntent(false)
	}
	if rows < 0 && p.previewScroll == p.previewMaxScroll() {
		return p.loadOlderTerminalHistory(false, -rows)
	}
	return nil
}

// scrollInteractiveViewportByWheel moves the interactive window by a coalesced
// notch. A notch counts rendered rows down the screen where the scrollback keys
// count rows back through scrollback; that is the only difference between them.
// Where the window lands — including the clamp to what the surface has measured
// — is the shared rule's, and it is the same answer for every local wheel path.
func (p *Plugin) scrollInteractiveViewportByWheel(delta int) tea.Cmd {
	return p.scrollTerminalWindowByWheel(
		p.interactiveState != nil && p.interactiveState.TermPanel, delta)
}

// scrollInteractiveViewport moves whichever pane interactive mode is pointed at
// delta rows back through scrollback, negative towards the live edge, and
// reaches for older history when the window runs out of loaded buffer. It is
// the scrollback keys' placement: a key move lands inside what the surface has
// measured, and it counts in the direction the shared move reports.
func (p *Plugin) scrollInteractiveViewport(delta int) tea.Cmd {
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		p.clearTerminalSelectionOnScroll(true)
		p.scrollTermPanelWindow(delta)
		if delta < 0 && p.termPanelScroll == 0 {
			p.cancelTerminalHistoryIntent(true)
		}
		if delta > 0 && p.termPanelScroll == p.termPanelMaxScroll() {
			return p.loadOlderTerminalHistory(true, delta)
		}
		return nil
	}

	p.scrollPreviewWindow(delta)
	if delta < 0 && p.previewScroll == 0 {
		p.cancelTerminalHistoryIntent(false)
	}
	if delta > 0 && p.previewScroll == p.previewMaxScroll() {
		return p.loadOlderTerminalHistory(false, delta)
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
	if !p.selectingShell() && p.previewTab != PreviewTabOutput {
		return 0, 0, false
	}

	// Origin and size of the surface interactive mode is targeting, taken from
	// the one derivation the render path draws with. Re-deriving the terminal
	// panel's offset from calculatePreviewDimensions instead lands a row off in
	// the bottom layout — the split divides the container height — for every
	// window height where the two floors disagree, and a click is forwarded to
	// the wrong tmux row.
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
	if shell := p.getSelectedShell(); shell != nil {
		return p.scheduleShellPollByName(shell.TmuxName, interval)
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
	if shell := p.getSelectedShell(); shell != nil {
		return p.scheduleShellPollByName(shell.TmuxName, delay)
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
	return p.wheel.Remaining(p.now())
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
	return tty.ShouldSnapBack(msg, p.now().Sub(p.wheel.LastAt()))
}
