package workspace

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
	app "github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"golang.org/x/term"
)

// Interactive mode constants
const (
	// doubleEscapeDelay is the max time between Escape presses for double-escape exit.
	// Single Escape is delayed by this amount to detect double-press.
	doubleEscapeDelay = 150 * time.Millisecond

	// pollingDecayFast is the polling interval during active typing.
	pollingDecayFast = 50 * time.Millisecond

	// pollingDecayMedium is the polling interval after brief inactivity.
	pollingDecayMedium = 200 * time.Millisecond

	// pollingDecaySlow is the polling interval after extended inactivity.
	pollingDecaySlow = 500 * time.Millisecond

	// keystrokeDebounce delays polling after keystrokes to batch rapid typing (td-8a0978).
	// Allows typing bursts to coalesce into fewer polls, reducing CPU usage.
	keystrokeDebounce = 20 * time.Millisecond

	// inactivityMediumThreshold triggers medium polling.
	inactivityMediumThreshold = 2 * time.Second

	// inactivitySlowThreshold triggers slow polling.
	inactivitySlowThreshold = 10 * time.Second

	// defaultExitKey is the default keybinding to exit interactive mode.
	defaultExitKey = "ctrl+\\"

	// defaultAttachKey is the default keybinding to attach from interactive mode (td-fd68d1).
	defaultAttachKey = "ctrl+]"

	// defaultCopyKey is the default keybinding to copy selection in interactive mode.
	defaultCopyKey = "alt+c"

	// defaultPasteKey is the default keybinding to paste clipboard in interactive mode.
	defaultPasteKey = "alt+v"
)

// =============================================================================
// Scroll tuning constants (td-3b15ee)
// Adjust these to balance scroll responsiveness vs escape sequence filtering.
// =============================================================================
const (
	// scrollDebounceInterval is the base debounce for scroll events (~60fps).
	// Lower = more responsive but more CPU. Higher = smoother but laggy.
	scrollDebounceInterval = 16 * time.Millisecond

	// scrollBurstDebounce is used during fast scrolling (burst mode).
	// Lower = more responsive. Higher = better filtering but feels sluggish.
	// 32ms ≈ 30fps, good balance of smooth scrolling and reduced event spam.
	scrollBurstDebounce = 12 * time.Millisecond

	// scrollBurstThreshold is scroll events needed to enter burst mode.
	// Lower = enter burst mode faster. Higher = more normal scrolling before burst kicks in.
	scrollBurstThreshold = 3

	// scrollBurstTimeout is how long after last scroll before burst mode ends.
	// Should be long enough for garbage events to clear. Too long = delayed typing response.
	scrollBurstTimeout = 500 * time.Millisecond

	// snapBackCooldown prevents snap-back to live output during active scrolling.
	// If user scrolled within this window, suspicious input won't trigger snap-back.
	snapBackCooldown = 100 * time.Millisecond

	// mouseFragmentTimeout bounds state kept while reassembling an SGR mouse
	// report split across terminal input reads.
	mouseFragmentTimeout = 50 * time.Millisecond
)

// partialMouseSeqRegex is now provided by the tty package as tty.PartialMouseSeqRegex

// escapeTimerMsg is sent when the escape delay timer fires.
// If pendingEscape is still true, we forward the single Escape to tmux.
type escapeTimerMsg struct{}

// InteractiveSessionDeadMsg indicates the tmux session has ended.
// Sent when send-keys or capture fails with a session/pane not found error.
type InteractiveSessionDeadMsg struct{}

// getInteractiveExitKey returns the configured exit keybinding for interactive mode.
// Falls back to defaultExitKey ("ctrl+\") if not configured.
func (p *Plugin) getInteractiveExitKey() string {
	if p.ctx != nil && p.ctx.Config != nil {
		if key := p.ctx.Config.Plugins.Workspace.InteractiveExitKey; key != "" {
			return key
		}
	}
	return defaultExitKey
}

// getInteractiveAttachKey returns the configured attach keybinding for interactive mode (td-fd68d1).
// Falls back to defaultAttachKey ("ctrl+]") if not configured.
func (p *Plugin) getInteractiveAttachKey() string {
	if p.ctx != nil && p.ctx.Config != nil {
		if key := p.ctx.Config.Plugins.Workspace.InteractiveAttachKey; key != "" {
			return key
		}
	}
	return defaultAttachKey
}

// getInteractiveCopyKey returns the configured copy keybinding for interactive mode.
// Falls back to defaultCopyKey ("alt+c") if not configured.
func (p *Plugin) getInteractiveCopyKey() string {
	if p.ctx != nil && p.ctx.Config != nil {
		if key := p.ctx.Config.Plugins.Workspace.InteractiveCopyKey; key != "" {
			return key
		}
	}
	return defaultCopyKey
}

// getInteractivePasteKey returns the configured paste keybinding for interactive mode.
// Falls back to defaultPasteKey ("alt+v") if not configured.
func (p *Plugin) getInteractivePasteKey() string {
	if p.ctx != nil && p.ctx.Config != nil {
		if key := p.ctx.Config.Plugins.Workspace.InteractivePasteKey; key != "" {
			return key
		}
	}
	return defaultPasteKey
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

// sendInteractivePasteInputCmd sends paste text to tmux asynchronously (td-c2961e).
// Used for multi-character terminal input (not clipboard paste which is already async).
// Shares the keystroke queue so pasted text cannot overtake surrounding keys.
func sendInteractivePasteInputCmd(sessionName, text string) tea.Cmd {
	return awaitInteractiveSend(tty.SendOrdered(sessionName, func() error {
		return tty.SendPasteInput(sessionName, text)
	}))
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

func (p *Plugin) pasteClipboardToTmuxCmd() tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}

	sessionName := p.interactiveState.TargetSession
	if sessionName == "" {
		return nil
	}

	return func() tea.Msg {
		text, err := clipboard.ReadAll()
		if err != nil {
			return InteractivePasteResultMsg{Err: err}
		}
		if text == "" {
			return InteractivePasteResultMsg{Empty: true}
		}

		// The clipboard read has to happen off the Update loop, so this enqueues
		// later than a keystroke Cmd would. Going through the queue anyway keeps
		// the paste from interleaving mid-write with concurrent keystrokes.
		err = <-tty.SendOrdered(sessionName, func() error {
			return tty.SendPasteInput(sessionName, text)
		})
		if err != nil {
			return InteractivePasteResultMsg{Err: err, SessionDead: tty.IsSessionDeadError(err)}
		}

		return InteractivePasteResultMsg{}
	}
}

func (p *Plugin) updateMouseReportingMode(output string) {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return
	}
	p.interactiveState.MouseReportingEnabled = tty.DetectMouseReportingMode(output)
}

// setPaneMouseReporting records tmux's #{mouse_any_flag} for the interactive
// pane. It is only called with metadata captured alongside the pane, so a
// capture that carried no cursor metadata leaves the last known value alone
// rather than falsely reporting the app released the mouse.
func (p *Plugin) setPaneMouseReporting(enabled bool) {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return
	}
	p.interactiveState.PaneMouseReporting = enabled
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
		previewWidth = p.terminalContentWidth(previewWidth, previewHeight, false)
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
	p.selection.Clear()

	p.viewMode = ViewModeInteractive

	// Invalidate existing poll timers to prevent duplicate poll chains (td-97327e).
	// Without this, entering interactive mode creates a second poll chain that runs
	// in parallel with the existing one, causing 200% CPU usage.
	if p.shellSelected {
		p.pollScheduler.Invalidate(shellPollKey(sessionName))
	} else {
		if wt := p.selectedWorktree(); wt != nil {
			p.pollScheduler.Invalidate(agentPollKey(wt.Name))
		}
	}

	// Trigger immediate poll for fresh content (cursor position is captured atomically with output)
	cmds := []tea.Cmd{p.pollInteractivePane()}
	if !p.interactiveCopyPasteHintShown {
		p.interactiveCopyPasteHintShown = true
		cmds = append(cmds, func() tea.Msg {
			return app.ToastMsg{
				Message:  fmt.Sprintf("Interactive copy/paste: %s / %s (configurable)", p.getInteractiveCopyKey(), p.getInteractivePasteKey()),
				Duration: 3 * time.Second,
			}
		})
	}
	return tea.Batch(cmds...)
}

// enterTermPanelInteractiveMode enters interactive mode targeting the terminal panel's tmux session.
func (p *Plugin) enterTermPanelInteractiveMode() tea.Cmd {
	if !features.IsEnabled(features.TmuxInteractiveInput.Name) {
		return nil
	}
	if p.termPanelSession == "" || !p.termPanelVisible {
		return nil
	}

	sessionName := p.termPanelSession
	paneID := p.termPanelPaneID
	target := paneID
	if target == "" {
		target = sessionName
	}

	// Resize terminal panel pane to match its split dimensions
	w, h := p.calculateTermPanelDimensions()
	w = p.terminalContentWidth(w, h, true)
	tty.SetWindowSizeManual(sessionName)
	// Explicit local action: claim the terminal panel session outright rather
	// than render it at another machine's geometry (td-ee222a).
	tty.ClaimGeometryLease(target)
	tty.ResizeTmuxPane(target, w, h)
	if aw, ah, ok := tty.QueryPaneSize(target); ok && (aw != w || ah != h) {
		tty.ResizeTmuxPane(target, w, h)
	}

	p.termPanelScroll = 0 // Reset scroll so output aligns with cursor position
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
	p.selection.Clear()
	p.viewMode = ViewModeInteractive

	// Invalidate the background poll chain so only the interactive poll loop runs.
	// The interactive chain captures the same pane and updates termPanelOutput,
	// so background polling is redundant during interactive mode.
	p.pollScheduler.Invalidate(termPanelPollKey())
	return p.pollInteractivePane()
}

// calculatePreviewDimensions returns the content width and height for the preview pane.
// Used to resize tmux panes to match the visible area.
// IMPORTANT: This must stay in sync with renderListView() width calculations.
func (p *Plugin) calculatePreviewDimensions() (width, height int) {
	return p.previewDimensionsFor(p.shellSelected)
}

// previewDimensionsFor computes preview dimensions for a given selection kind
// rather than the current one. Sizing a tmux session at creation time needs the
// dimensions the pane will be rendered into once it is selected, which is not
// necessarily what is selected right now.
func (p *Plugin) previewDimensionsFor(shellSelected bool) (width, height int) {
	if p.width <= 0 || p.height <= 0 {
		if w, h, err := term.GetSize(int(os.Stdout.Fd())); err == nil && w > 0 && h > 0 {
			return w - panelOverhead, h - panelBorderWidth - 1
		}
		return 80, 24 // Safe defaults
	}

	// Calculate preview pane width based on sidebar visibility
	// Uses panelOverhead constant to ensure consistency with render path
	if !p.sidebarVisible {
		// Full width minus panel overhead (borders + padding)
		width = p.width - panelOverhead
	} else {
		// Account for sidebar and divider (same calculation as renderListView)
		available := p.width - dividerWidth
		sidebarW := (available * p.sidebarWidth) / 100
		if sidebarW < 15 {
			sidebarW = 15
		}
		if sidebarW > available-40 {
			sidebarW = available - 40
		}
		previewW := available - sidebarW
		if previewW < 40 {
			previewW = 40
		}
		// Subtract panel overhead for content width
		width = previewW - panelOverhead
	}

	// Calculate height: total height minus borders (2) and UI elements
	// - panelBorderWidth for top/bottom panel borders
	// - 1 for hint line
	// - 2 for tabs header (worktrees only)
	paneHeight := p.height - panelBorderWidth
	if shellSelected {
		// Shell: no tabs, just hint
		height = paneHeight - 1
	} else {
		// Worktree: tabs header + hint
		height = paneHeight - 3
	}

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
		previewWidth, previewHeight = p.calculateTermPanelDimensions()
	} else if p.termPanelVisible {
		previewWidth, previewHeight = p.calculateAgentPaneDimensions()
	} else {
		previewWidth, previewHeight = p.calculatePreviewDimensions()
	}
	previewWidth = p.terminalContentWidth(previewWidth, previewHeight, isTermPanel)
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
		previewWidth, previewHeight = p.calculateTermPanelDimensions()
	} else if p.termPanelVisible {
		previewWidth, previewHeight = p.calculateAgentPaneDimensions()
	} else {
		previewWidth, previewHeight = p.calculatePreviewDimensions()
	}
	previewWidth = p.terminalContentWidth(previewWidth, previewHeight, isTermPanel)
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
// The terminal scrollbar is viewport chrome, so when it is visible tmux must
// wrap one column earlier instead of rendering a final column that Sidecar then
// clips off (td-e8bdcf).
func (p *Plugin) terminalContentWidth(width, height int, termPanel bool) int {
	if width <= 1 || height <= 0 {
		return width
	}
	buffer := p.terminalOutputBuffer(termPanel)
	_, total, _ := p.terminalHistorySummary(termPanel, buffer)
	if total > height {
		return width - 1
	}
	return width
}

// maybeResizeVisiblePaneForScrollbar reacts when output crosses the scrollbar
// threshold in passive mode. Creation and layout events cannot cover this: a
// fresh pane often starts without scrollback and grows a scrollbar later.
func (p *Plugin) maybeResizeVisiblePaneForScrollbar(target string, paneWidth, paneHeight int, termPanel bool) tea.Cmd {
	if target == "" || paneWidth <= 0 || paneHeight <= 0 {
		return nil
	}
	var width, height int
	if termPanel {
		width, height = p.calculateTermPanelDimensions()
	} else if p.termPanelVisible {
		width, height = p.calculateAgentPaneDimensions()
	} else {
		width, height = p.calculatePreviewDimensions()
	}
	width = p.terminalContentWidth(width, height, termPanel)
	if paneWidth == width && paneHeight == height {
		return nil
	}
	return func() tea.Msg {
		tty.ResizeTmuxPane(target, width, height)
		return paneResizedMsg{}
	}
}

func (p *Plugin) terminalOutputBuffer(termPanel bool) *tty.OutputBuffer {
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
	p.pendingScrollDelta = 0
	p.scrollBurstCount = 0
	p.selection.Clear()
	p.viewMode = ViewModeList
}

// handleInteractivePaste forwards bracketed-paste content (delivered as a
// tea.PasteMsg in v2) to the interactive tmux session.
func (p *Plugin) handleInteractivePaste(content string) tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active || content == "" {
		return nil
	}
	sessionName := p.interactiveState.TargetSession
	return tea.Batch(
		sendInteractivePasteInputCmd(sessionName, content),
		p.pollInteractivePane(),
	)
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

	// Secondary exit: Double-Escape with 150ms delay
	// Per spec: first Escape is delayed to detect double-press
	if msg.Code == tea.KeyEscape {
		if p.interactiveState.EscapePressed {
			// Second Escape within window: exit interactive mode
			p.interactiveState.EscapePressed = false
			p.interactiveState.EscapeTimerPending = false // Cancel pending timer
			p.exitInteractiveMode()
			p.previewOffset = 0
			p.autoScrollOutput = true
			return p.pollSelectedAgentNowIfVisible()
		}
		// First Escape: mark pending and start delay timer
		// Do NOT forward to tmux yet - wait for timer or next key
		p.interactiveState.EscapePressed = true
		p.interactiveState.EscapeTime = time.Now()
		// Timer leak prevention (td-83dc22): only schedule timer if one isn't already pending
		if !p.interactiveState.EscapeTimerPending {
			p.interactiveState.EscapeTimerPending = true
			return tea.Tick(doubleEscapeDelay, func(t time.Time) tea.Msg {
				return escapeTimerMsg{}
			})
		}
		return nil
	}

	// Filter partial SGR mouse sequences that leaked through Bubble Tea's
	// input parser due to split-read timing (ESC arrived separately) (td-791865).
	// Must be checked BEFORE forwarding pending escape, since the ESC was part
	// of the mouse sequence, not a real user keypress.
	// td-e2ce50: Use lenient check to catch truncated/split sequences during fast scrolling.
	// Multi-char fragments like "[<35;10;20M" are caught by LooksLikeMouseFragment.
	if len(msg.Text) > 0 {
		if p.consumeSplitMouseFragment(msg.Text) {
			p.interactiveState.EscapePressed = false
			return nil
		}
		if tty.LooksLikeMouseFragment(msg.Text) {
			// Cancel the pending escape — it was the leading byte of this mouse event
			p.interactiveState.EscapePressed = false
			return nil // Drop mouse sequence fragments
		}
	}

	// Suppress bare "[" that leaks from split SGR mouse sequences.
	//
	// With tea.WithMouseAllMotion(), the terminal sends an SGR mouse sequence
	// (ESC [ < params M/m) for every mouse movement. Bubble Tea's input reader
	// can split these sequences across read boundaries:
	//
	//   Read 1: ESC        → delivered as tea.KeyEscape (or consumed internally)
	//   Read 2: [          → delivered as tea.KeyRunes{'['}  ← the leak
	//   Read 3: <35;10;20M → delivered as tea.KeyRunes or parsed as mouse
	//
	// The ESC-time-gate catches case where ESC was delivered as a keypress
	// (setting EscapePressed). But sometimes Bubble Tea's parser consumes the
	// ESC internally while still emitting "[" as a leftover rune — EscapePressed
	// is never set, so the ESC gate doesn't fire.
	//
	// The mouse-proximity gate catches this: if ANY mouse event was delivered
	// within the last 10ms, a bare "[" is almost certainly a CSI fragment, not
	// a real keypress. Real "[" typing doesn't coincide with mouse activity at
	// sub-10ms granularity. This works because successfully-parsed mouse events
	// (tea.MouseMsg) and the leaked "[" originate from the same burst of terminal
	// output — they arrive within microseconds of each other.
	if msg.Text == "[" {
		escGate := p.interactiveState.EscapePressed &&
			time.Since(p.interactiveState.EscapeTime) < 5*time.Millisecond
		mouseGate := time.Since(p.lastMouseEventTime) < 10*time.Millisecond
		if escGate || mouseGate {
			p.rememberMouseFragment("[")
			p.interactiveState.EscapePressed = false
			return nil
		}
	}

	// Non-escape key: check if we have a pending Escape to forward first
	var cmds []tea.Cmd
	pendingEscape := false
	if p.interactiveState.EscapePressed {
		p.interactiveState.EscapePressed = false
		// Timer leak prevention (td-83dc22): pending timer will be ignored when it fires
		// since EscapePressed is now false (no need to cancel, it's harmless)
		pendingEscape = true
	}

	if msg.String() == p.getInteractiveCopyKey() {
		return p.copyInteractiveSelectionCmd()
	}
	if msg.String() == "ctrl+a" && p.interactiveState != nil {
		p.selectAllTerminalOutput(p.interactiveState.TermPanel)
		return nil
	}

	if msg.String() == p.getInteractivePasteKey() {
		p.interactiveState.LastKeyTime = time.Now()
		if !p.autoScrollOutput {
			p.autoScrollOutput = true
			p.scrollToBottom()
		}
		cmds = append(cmds, p.pasteClipboardToTmuxCmd())
		return tea.Batch(cmds...)
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

	sessionName := p.interactiveState.TargetSession

	// Check for paste (multi-character input with newlines or long text)
	if tty.IsPasteInput(msg) {
		text := msg.Text
		// Send paste async (td-c2961e): escape + paste in order if pending
		if pendingEscape {
			cmds = append(cmds, awaitInteractiveSend(tty.SendOrdered(sessionName, func() error {
				if err := tty.SendKeyToTmux(sessionName, "Escape"); err != nil {
					return err
				}
				return tty.SendPasteToTmux(sessionName, text)
			})))
		} else {
			cmds = append(cmds, sendInteractivePasteInputCmd(sessionName, text))
		}
		cmds = append(cmds, p.pollInteractivePane())
		return tea.Batch(cmds...)
	}

	// Map key to tmux format and send
	key, useLiteral := tty.MapKeyToTmux(msg)
	if key == "" {
		// Still send pending escape if nothing else to send
		if pendingEscape {
			cmds = append(cmds, sendInteractiveKeysCmd(sessionName, tty.KeySpec{Value: "Escape"}))
			cmds = append(cmds, p.scheduleDebouncedPoll(keystrokeDebounce))
		}
		return tea.Batch(cmds...)
	}

	// Send keys async (td-c2961e): pending escape + key in order within single goroutine
	if pendingEscape {
		cmds = append(cmds, sendInteractiveKeysCmd(sessionName,
			tty.KeySpec{Value: "Escape"},
			tty.KeySpec{Value: key, Literal: useLiteral},
		))
	} else {
		cmds = append(cmds, sendInteractiveKeysCmd(sessionName, tty.KeySpec{Value: key, Literal: useLiteral}))
	}

	// Schedule debounced poll to batch rapid keystrokes (td-8a0978)
	cmds = append(cmds, p.scheduleDebouncedPoll(keystrokeDebounce))
	return tea.Batch(cmds...)
}

// handleUnknownSequence forwards unrecognized CSI sequences to tmux in
// interactive mode. BubbleTea v1 doesn't parse CSI u (kitty keyboard protocol)
// or modifyOtherKeys sequences, so modified keys like shift+enter arrive as
// unknownCSISequenceMsg. We normalize them to CSI u format and forward to tmux.
func (p *Plugin) handleUnknownSequence(msg tea.Msg) tea.Cmd {
	if p.viewMode != ViewModeInteractive {
		return nil
	}
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}

	raw := tty.ExtractUnknownCSIBytes(msg)
	if raw == nil {
		return nil
	}

	csiu := tty.NormalizeToCSIu(raw)
	if csiu == "" {
		return nil
	}

	sessionName := p.interactiveState.TargetSession
	return sendInteractiveKeysCmd(sessionName, tty.KeySpec{Value: csiu, Literal: true})
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

	// Update last key time and poll immediately for better responsiveness (td-babfd9)
	p.interactiveState.LastKeyTime = time.Now()
	return tea.Batch(
		sendInteractiveKeysCmd(p.interactiveState.TargetSession, tty.KeySpec{Value: "Escape"}),
		p.pollInteractivePaneImmediate(),
	)
}

// maxWheelNotchesPerFlush caps how many wheel reports one debounced burst can
// send. A fast trackpad flick can coalesce a large delta, and every notch is a
// separate `tmux send-keys`; past a point the app has scrolled as far as the
// gesture meant anyway.
const maxWheelNotchesPerFlush = 10

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
	now := time.Now()

	// Detect and handle scroll bursts (fast trackpad scrolling)
	timeSinceLastScroll := now.Sub(p.lastScrollTime)
	if timeSinceLastScroll < scrollBurstTimeout {
		p.scrollBurstCount++
	} else {
		// Burst ended, reset
		p.scrollBurstCount = 1
		p.scrollBurstStarted = now
	}

	// During burst mode, use more aggressive debouncing
	debounceInterval := scrollDebounceInterval
	if p.scrollBurstCount > scrollBurstThreshold {
		debounceInterval = scrollBurstDebounce
	}

	p.pendingScrollDelta += delta
	if timeSinceLastScroll < debounceInterval {
		return nil
	}
	p.lastScrollTime = now
	delta = p.pendingScrollDelta
	p.pendingScrollDelta = 0

	if cmd, forwarded := p.forwardWheelToPane(action, delta); forwarded {
		return cmd
	}

	// When interactive mode targets the terminal panel, scroll terminal panel output
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		p.selection.Clear()
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

// forwardWheelToPane sends delta as SGR wheel reports when the app running in
// the interactive pane has asked for mouse events. It reports forwarded=false
// whenever the notch belongs to the local viewport instead — no mouse tracking,
// no interactive pane, or a pointer position that does not map into the pane —
// so the caller falls through to its scrollback handling unchanged.
func (p *Plugin) forwardWheelToPane(action mouse.MouseAction, delta int) (tea.Cmd, bool) {
	state := p.interactiveState
	if delta == 0 || state == nil || !state.Active || !state.PaneMouseReporting {
		return nil, false
	}
	// Alt is the "give me the terminal, not the app" modifier for the wheel.
	// Shift is checked too for symmetry with click handling, but shift+wheel
	// never reaches here — mouse.HandleMouse maps it to horizontal scroll.
	if action.Shift || action.Alt {
		return nil, false
	}
	sessionName := state.TargetSession
	if sessionName == "" {
		return nil, false
	}
	col, row, ok := p.interactiveMouseCoords(action.X, action.Y)
	if !ok {
		return nil, false
	}

	// While the app owns the wheel it also owns what the pane shows, so the
	// viewport is pinned to the live frame. Without this a viewport left
	// scrolled back — by alt+wheel, or by plain wheel from before the app
	// enabled tracking — would sit frozen over stale rows while the app
	// repainted below it.
	p.pinInteractiveViewportToLive()

	// The wheel is the user's most recent input, so it counts as activity: the
	// poll cadence decays to its slow tier on idle time, and a scroll that did
	// not reset it would be repainted at that tier.
	state.LastKeyTime = time.Now()

	// Delta is a line count — mouse.HandleMouse expands one notch into
	// WheelScrollLines — but the pane wants notches, and the app applies its own
	// lines-per-notch on top. Forwarding the line count made every notch scroll
	// roughly WheelScrollLines times too far.
	up := delta < 0
	notches := min(wheelNotchesForDelta(delta), maxWheelNotchesPerFlush)

	// Queued from the Update loop so wheel reports keep their order relative to
	// keystrokes for the same pane rather than racing them.
	cmd := awaitInteractiveSend(tty.SendOrdered(sessionName, func() error {
		return tty.SendSGRWheel(sessionName, up, col, row, notches)
	}))
	return tea.Batch(cmd, p.pollInteractivePaneImmediate()), true
}

// wheelNotchesForDelta converts a scroll delta in lines back into whole wheel
// notches, never rounding a real scroll down to nothing.
func wheelNotchesForDelta(delta int) int {
	lines := max(delta, -delta)
	return max(lines/mouse.WheelScrollLines, 1)
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
			p.selection.Clear()
			p.termPanelScroll = 0
			p.cancelTerminalHistoryIntent(true)
		}
		return
	}
	maxOffset := p.getMaxScrollOffset()
	if p.autoScrollOutput && p.previewOffset >= maxOffset {
		return
	}
	p.selection.Clear()
	p.previewOffset = maxOffset
	p.autoScrollOutput = true
	p.cancelTerminalHistoryIntent(false)
}

func (p *Plugin) handleInteractiveScrollbackKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if !msg.Mod.Contains(tea.ModShift) {
		return false, nil
	}

	pageSize := p.getPreviewVisibleHeight()
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		_, pageSize = p.calculateTermPanelDimensions()
	}
	pageSize = max(pageSize-1, 1)

	switch msg.Code {
	case tea.KeyUp:
		return true, p.scrollInteractiveViewport(-1)
	case tea.KeyDown:
		return true, p.scrollInteractiveViewport(1)
	case tea.KeyPgUp:
		return true, p.scrollInteractiveViewport(-pageSize)
	case tea.KeyPgDown:
		return true, p.scrollInteractiveViewport(pageSize)
	case tea.KeyHome:
		if p.interactiveState != nil && p.interactiveState.TermPanel {
			p.selection.Clear()
			p.termPanelScroll = p.termPanelMaxScroll()
			return true, p.loadOlderTerminalHistory(true, historyLoadChunk)
		} else {
			p.previewOffset = 0
			p.autoScrollOutput = false
			return true, p.loadOlderTerminalHistory(false, historyLoadChunk)
		}
	case tea.KeyEnd:
		if p.interactiveState != nil && p.interactiveState.TermPanel {
			p.selection.Clear()
			p.termPanelScroll = 0
			p.cancelTerminalHistoryIntent(true)
		} else {
			p.previewOffset = p.getMaxScrollOffset()
			p.autoScrollOutput = true
			p.cancelTerminalHistoryIntent(false)
		}
	default:
		return false, nil
	}
	return true, nil
}

func (p *Plugin) scrollInteractiveViewport(delta int) tea.Cmd {
	if p.interactiveState != nil && p.interactiveState.TermPanel {
		p.selection.Clear()
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

// forwardClickToTmux sends a mouse click to the tmux pane.
// Currently a no-op as full mouse support requires knowing the terminal's mouse mode.
// This is provided for future extension.
func (p *Plugin) forwardClickToTmux(x, y int) tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}
	if !p.interactiveState.MouseReportingEnabled {
		return nil
	}
	interaction := p.interactiveState
	sessionName := interaction.TargetSession
	col, row, ok := p.interactiveMouseCoords(x, y)
	if !ok {
		return nil
	}

	return func() tea.Msg {
		if err := tty.SendSGRMouse(sessionName, 0, col, row, false); err != nil {
			return interactiveClickSentMsg{SessionName: sessionName, Interaction: interaction, Err: err}
		}
		if err := tty.SendSGRMouse(sessionName, 0, col, row, true); err != nil {
			return interactiveClickSentMsg{SessionName: sessionName, Interaction: interaction, Err: err}
		}
		return interactiveClickSentMsg{SessionName: sessionName, Interaction: interaction}
	}
}

func (p *Plugin) interactiveMouseCoords(x, y int) (col, row int, ok bool) {
	if p.width <= 0 || p.height <= 0 {
		return 0, 0, false
	}
	if !p.shellSelected && p.previewTab != PreviewTabOutput {
		return 0, 0, false
	}

	previewX := 0
	if p.sidebarVisible {
		available := p.width - dividerWidth
		sidebarW := (available * p.sidebarWidth) / 100
		if sidebarW < 15 {
			sidebarW = 15
		}
		if sidebarW > available-40 {
			sidebarW = available - 40
		}
		previewX = sidebarW + dividerWidth
	}

	contentX := previewX + panelOverhead/2
	contentY := 1
	if !p.shellSelected {
		contentY += 2
	}
	if !p.flashPreviewTime.IsZero() && time.Since(p.flashPreviewTime) < flashDuration {
		contentY++
	}
	contentY++ // hint line

	// When interactive mode targets the terminal panel, adjust content origin
	// to account for the terminal panel's position within the preview area.
	targetingTermPanel := p.interactiveState != nil && p.interactiveState.Active && p.interactiveState.TermPanel && p.termPanelVisible
	if targetingTermPanel {
		previewWidth, previewHeight := p.calculatePreviewDimensions()
		size := p.termPanelEffectiveSize()
		if p.termPanelLayout == TermPanelRight {
			termWidth := previewWidth * size / 100
			if termWidth < 10 {
				termWidth = 10
			}
			outputWidth := previewWidth - termWidth - 1
			if outputWidth < 10 {
				outputWidth = 10
			}
			contentX += outputWidth + 1 // skip agent output + divider
		} else {
			termHeight := previewHeight * size / 100
			if termHeight < 3 {
				termHeight = 3
			}
			outputHeight := previewHeight - termHeight - 1
			if outputHeight < 3 {
				outputHeight = 3
			}
			contentY += outputHeight + 1 // skip agent output + divider
		}
		contentY++ // terminal panel hint/label line
	}

	relX := x - contentX
	relY := y - contentY
	if relX < 0 || relY < 0 {
		return 0, 0, false
	}

	// The pane's real geometry decides what is on screen where, so hit testing
	// reads the layout the render path produced rather than re-deriving one: a
	// wider pane is drawn horizontally scrolled, a taller one starts partway
	// down, and the scrollbar takes a column off both (td-73fa86).
	viewWidth, viewHeight := p.calculatePreviewDimensions()
	if targetingTermPanel {
		viewWidth, viewHeight = p.calculateTermPanelDimensions()
	}
	paneWidth, paneHeight := viewWidth, viewHeight
	if geometry := p.paneGeometryFor(targetingTermPanel); geometry.known() {
		paneWidth, paneHeight = geometry.Width, geometry.Height
	}
	if p.interactiveState != nil &&
		p.interactiveState.PaneWidth > 0 && p.interactiveState.PaneHeight > 0 {
		paneWidth, paneHeight = p.interactiveState.PaneWidth, p.interactiveState.PaneHeight
	}

	layout := p.terminalSelectionViewportLayout()
	if layout.DisplayWidth <= 0 || layout.DisplayHeight <= 0 {
		return 0, 0, false
	}
	if relX >= layout.DisplayWidth || relY >= layout.DisplayHeight {
		return 0, 0, false
	}

	col = min(relX+layout.Fit.ColOffset+1, paneWidth)
	// Vertical placement comes from the buffer window, not the fit: the
	// workspace viewport scrolls history as well as the live pane.
	row = min(max(layout.paneRowAt(relY)+1, 1), paneHeight)

	return col, row, true
}

// pollInteractivePane schedules a poll for interactive mode with adaptive timing.
func (p *Plugin) pollInteractivePane() tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
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

	// When interactive mode targets the terminal panel, use terminal panel polling
	if p.interactiveState.TermPanel {
		return p.scheduleTermPanelPoll(interval)
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

// scheduleDebouncedPoll schedules a poll with debounce delay to batch rapid keystrokes (td-8a0978).
// Uses generation tracking to cancel stale timers, reducing subprocess spam during typing.
func (p *Plugin) scheduleDebouncedPoll(delay time.Duration) tea.Cmd {
	if p.interactiveState == nil || !p.interactiveState.Active {
		return nil
	}

	// When interactive mode targets the terminal panel, use terminal panel polling.
	// Increment generation to invalidate stale timers from previous keystrokes,
	// preventing poll chain accumulation during rapid typing.
	if p.interactiveState.TermPanel {
		p.pollScheduler.Invalidate(termPanelPollKey())
		return p.scheduleTermPanelPoll(delay)
	}

	// Use shell or worktree polling mechanism based on current selection.
	// Shells and agents use separate scheduler keys, so restarting one poll
	// chain cannot invalidate another pane with the same display name.
	if p.shellSelected && p.selectedShellIdx >= 0 && p.selectedShellIdx < len(p.shells) {
		shellName := p.shells[p.selectedShellIdx].TmuxName
		if shellName != "" {
			p.pollScheduler.Invalidate(shellPollKey(shellName))
			return p.scheduleShellPollByName(shellName, delay)
		}
	} else if wt := p.selectedWorktree(); wt != nil {
		p.pollScheduler.Invalidate(agentPollKey(wt.Name))
		return p.scheduleInteractivePoll(wt.Name, delay)
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

	delay := time.Duration(0)
	if remaining, scrolling := p.interactiveScrollDelay(); scrolling {
		delay = remaining
	}

	// When interactive mode targets the terminal panel, use terminal panel polling
	if p.interactiveState.TermPanel {
		return p.scheduleTermPanelPoll(delay)
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
	if p.interactiveState != nil && p.interactiveState.PaneMouseReporting {
		return 0, false
	}
	if p.scrollBurstCount <= 0 {
		return 0, false
	}
	elapsed := time.Since(p.lastScrollTime)
	if elapsed >= scrollBurstTimeout {
		return 0, false
	}
	return scrollBurstTimeout - elapsed, true
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

// shouldSnapBack determines if we should snap back to live view for a given key (td-e2ce50).
// Returns false during active scrolling or for input that looks like mouse sequence fragments.
// This prevents bounce-scroll caused by split mouse events triggering snap-back.
func (p *Plugin) shouldSnapBack(msg tea.KeyPressMsg) bool {
	// Guard 1: Don't snap back during active scrolling (time-based protection)
	// If user scrolled recently, suspicious input is likely mouse garbage
	if time.Since(p.lastScrollTime) < snapBackCooldown {
		return false
	}

	// Guard 2: Don't snap back for anything that looks like mouse sequence data
	if len(msg.Text) > 0 {
		// Check for any mouse-like fragments
		if tty.LooksLikeMouseFragment(msg.Text) {
			return false
		}
		// Multi-character input (not single keypress) is suspicious during scrolling
		// Could be paste (which we handle separately) or split mouse sequence
		if len([]rune(msg.Text)) > 1 {
			return false
		}
	}

	// Guard 3: Don't snap back for Escape - it might be start of a mouse sequence
	// Real escape is handled by the double-escape exit logic
	if msg.Code == tea.KeyEscape {
		return false
	}

	// Snap back for actual user typing:
	// - Single printable characters
	// - Navigation/editing keys
	// Single character that's not suspicious
	if len(msg.Text) > 0 {
		return len([]rune(msg.Text)) == 1
	}
	switch msg.Code {
	case tea.KeyEnter, tea.KeyTab, tea.KeyBackspace, tea.KeyDelete,
		tea.KeyUp, tea.KeyDown, tea.KeyLeft, tea.KeyRight,
		tea.KeyHome, tea.KeyEnd, tea.KeyPgUp, tea.KeyPgDown:
		return true
	default:
		// Other special keys (ctrl+x, etc.) - snap back
		return true
	}
}
