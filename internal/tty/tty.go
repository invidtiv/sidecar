package tty

import (
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// Config holds configuration options for a tty Model.
type Config struct {
	// ExitKey is the keybinding to exit interactive mode (default: "ctrl+\\").
	ExitKey string

	// AttachKey is the keybinding to attach to the full tmux session (default: "ctrl+]").
	AttachKey string

	// CopyKey is the keybinding to copy selection (default: "alt+c").
	CopyKey string

	// PasteKey is the keybinding to paste clipboard (default: "alt+v").
	PasteKey string

	// ScrollbackLines is the number of scrollback lines to capture (default: 600).
	ScrollbackLines int
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		ExitKey:         "ctrl+\\",
		AttachKey:       "ctrl+]",
		CopyKey:         "alt+c",
		PasteKey:        "alt+v",
		ScrollbackLines: 600,
	}
}

// State tracks the interactive mode state for a tmux session.
type State struct {
	// Active indicates whether interactive mode is currently active.
	Active bool

	// TargetPane is the tmux pane ID (e.g., "%12") receiving input.
	TargetPane string

	// TargetSession is the tmux session name for the active pane.
	TargetSession string

	// LastKeyTime tracks when the last key was sent for polling decay.
	LastKeyTime time.Time

	// Escape handling state
	EscapePressed      bool
	EscapeTime         time.Time
	EscapeTimerPending bool

	// LastMouseEventTime tracks when the last tea.MouseMsg was received,
	// used to suppress split-CSI "[" that leaks from mouse sequences.
	LastMouseEventTime time.Time

	// Cursor state (updated asynchronously via CaptureResultMsg)
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	PaneHeight    int
	PaneWidth     int

	// Terminal mode state (updated from captured output)
	BracketedPasteEnabled bool
	MouseReportingEnabled bool

	// Visible buffer range for selection mapping
	VisibleStart     int
	VisibleEnd       int
	ContentRowOffset int

	// Resize debouncing
	LastResizeAt time.Time

	// Output buffer
	OutputBuf *OutputBuffer

	// Poll generation for invalidating stale polls
	PollGeneration int
}

// Model is an embeddable component that provides interactive tmux functionality.
// Plugins embed this Model and delegate Update/View when interactive mode is active.
type Model struct {
	Config Config
	State  *State

	ownerID       uint64
	runGeneration uint64

	// Width and Height are set by the containing plugin
	Width  int
	Height int

	// Callbacks for plugin integration
	OnExit   func() tea.Cmd // Called when user exits interactive mode
	OnAttach func() tea.Cmd // Called when user requests full tmux attach
}

// New creates a new tty Model with the given configuration.
// If config is nil, DefaultConfig() is used.
func New(config *Config) *Model {
	cfg := DefaultConfig()
	if config != nil {
		if config.ExitKey != "" {
			cfg.ExitKey = config.ExitKey
		}
		if config.AttachKey != "" {
			cfg.AttachKey = config.AttachKey
		}
		if config.CopyKey != "" {
			cfg.CopyKey = config.CopyKey
		}
		if config.PasteKey != "" {
			cfg.PasteKey = config.PasteKey
		}
		if config.ScrollbackLines > 0 {
			cfg.ScrollbackLines = config.ScrollbackLines
		}
	}
	return &Model{
		Config:  cfg,
		ownerID: nextModelID.Add(1),
	}
}

var nextModelID atomic.Uint64

// IsActive returns whether interactive mode is currently active.
func (m *Model) IsActive() bool {
	return m.State != nil && m.State.Active
}

// Enter enters interactive mode for the specified tmux session/pane.
// Returns a tea.Cmd to start polling for output.
func (m *Model) Enter(sessionName, paneID string) tea.Cmd {
	m.runGeneration++
	m.State = &State{
		Active:        true,
		TargetPane:    paneID,
		TargetSession: sessionName,
		LastKeyTime:   time.Now(),
		CursorVisible: true,
		OutputBuf:     NewOutputBuffer(m.Config.ScrollbackLines),
	}

	// Resize pane to match view dimensions
	target := paneID
	if target == "" {
		target = sessionName
	}
	if target != "" && m.Width > 0 && m.Height > 0 {
		ResizeTmuxPane(target, m.Width, m.Height)
	}

	// Return command to trigger initial poll
	return m.schedulePoll(0)
}

// Scope returns the identity of the current Model activation. Commands created
// for this model should include this scope in their result messages.
func (m *Model) Scope() MessageScope {
	return MessageScope{
		Owner:      m.ownerID,
		Target:     m.GetTarget(),
		Generation: m.runGeneration,
	}
}

func (m *Model) owns(scope MessageScope) bool {
	current := m.Scope()
	return m.IsActive() &&
		scope.Owner == current.Owner &&
		scope.Target == current.Target &&
		scope.Generation == current.Generation
}

// Exit exits interactive mode.
func (m *Model) Exit() {
	if m.State != nil {
		m.State.Active = false
	}
	m.State = nil
}

// Update handles messages in interactive mode.
// Returns the updated model and any commands to execute.
// Plugins should call this when they receive messages and interactive mode is active.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	if !m.IsActive() {
		return nil
	}

	switch msg := msg.(type) {
	case tea.KeyPressMsg:
		return m.handleKey(msg)

	case tea.PasteMsg:
		// v2: bracketed paste arrives as a dedicated message. Forward the
		// pasted content to tmux as literal paste text.
		return m.handlePaste(msg.Content)

	case tea.MouseMsg:
		return m.handleMouse(msg)

	case EscapeTimerMsg:
		if !m.owns(msg.Scope) {
			return nil
		}
		return m.handleEscapeTimer()

	case CaptureResultMsg:
		if !m.owns(msg.Scope) {
			return nil
		}
		return m.handleCaptureResult(msg)

	case PollTickMsg:
		if !m.owns(msg.Scope) {
			return nil
		}
		return m.handlePollTick(msg)

	case PaneResizedMsg:
		if !m.owns(msg.Scope) {
			return nil
		}
		return m.schedulePoll(0)

	case SessionDeadMsg:
		if !m.owns(msg.Scope) {
			return nil
		}
		m.Exit()
		if m.OnExit != nil {
			return m.OnExit()
		}
		return nil

	case PasteResultMsg:
		if !m.owns(msg.Scope) {
			return nil
		}
		if msg.SessionDead {
			m.Exit()
			if m.OnExit != nil {
				return m.OnExit()
			}
		}
		return nil
	}

	return nil
}

// View renders the interactive terminal content. Cursor ownership is exposed
// separately through Cursor so the containing plugin can place Bubble Tea's
// native cursor after applying its pane offset.
// Plugins should call this to render the terminal when interactive mode is active.
func (m *Model) View() string {
	if !m.IsActive() || m.State.OutputBuf == nil {
		return ""
	}

	fit := m.paneFit()
	lineCount := m.State.OutputBuf.LineCount()
	// The buffer's tail is the pane, so pane row 0 is lineCount-PaneHeight and
	// the visible window starts RowOffset rows into it — which is the pane's
	// tail unless the cursor pulls the window up (td-73fa86).
	start := lineCount - fit.Height
	if m.State.PaneHeight > 0 {
		start = lineCount - m.State.PaneHeight + fit.RowOffset
	}
	if start < 0 || fit.Height <= 0 {
		start = 0
	}
	end := lineCount
	if fit.Height > 0 && start+fit.Height < end {
		end = start + fit.Height
	}
	lines := m.State.OutputBuf.LinesRange(start, end)
	if fit.Width <= 0 {
		return strings.Join(lines, "\n")
	}
	// Clip to the pane's real geometry: a wider pane would otherwise emit lines
	// past the viewport and wrap over the surrounding layout (td-73fa86).
	clipped := make([]string, len(lines))
	for i, line := range lines {
		if fit.ColOffset > 0 {
			line = ansi.TruncateLeft(line, fit.ColOffset, "")
		}
		clipped[i] = ansi.Truncate(line, fit.Width, "")
	}
	return strings.Join(clipped, "\n")
}

// paneFit projects the pane's observed geometry onto the viewport the embedding
// plugin gave this model. The pane can be any size — another sidecar instance
// may be driving the same tmux session — so the requested size is only a
// request (td-73fa86).
func (m *Model) paneFit() PaneFit {
	return FitPane(PaneFitInput{
		ViewWidth:  m.Width,
		ViewHeight: m.Height,
		PaneWidth:  m.State.PaneWidth,
		PaneHeight: m.State.PaneHeight,
		CursorCol:  m.State.CursorCol,
		CursorRow:  m.State.CursorRow,
		HasCursor:  m.State.CursorVisible && m.State.CursorCol >= 0 && m.State.CursorRow >= 0,
	})
}

// PaneCoords maps 1-indexed coordinates within the rendered content area to the
// 1-indexed pane coordinates tmux's mouse protocol expects. A clipped pane is
// drawn scrolled, so a click lands ColOffset columns and RowOffset rows in
// (td-73fa86).
func (m *Model) PaneCoords(col, row int) (int, int, bool) {
	if !m.IsActive() {
		return 0, 0, false
	}
	return m.paneFit().PaneCoords(col-1, row-1, m.State.PaneWidth, m.State.PaneHeight)
}

// SizeIndicator describes a pane that is larger than the viewport it is drawn
// into, e.g. "200x50, showing 120x40". It returns "" when the whole pane is
// visible, so callers can render it unconditionally.
func (m *Model) SizeIndicator() string {
	if !m.IsActive() {
		return ""
	}
	fit := m.paneFit()
	return PaneSizeIndicator(m.State.PaneWidth, m.State.PaneHeight, fit.Width, fit.Height)
}

// Cursor returns the native cursor position relative to View().
func (m *Model) Cursor() *tea.Cursor {
	if !m.IsActive() || m.State.OutputBuf == nil || !m.State.CursorVisible ||
		m.Width <= 0 || m.Height <= 0 || m.State.CursorRow < 0 || m.State.CursorCol < 0 {
		return nil
	}
	fit := m.paneFit()
	if fit.Width <= 0 || fit.Height <= 0 {
		return nil
	}
	// View renders pane rows fit.RowOffset..+fit.Height, so a pane row lands
	// RowOffset rows higher on screen.
	row := m.State.CursorRow - fit.RowOffset
	if row < 0 || row >= fit.Height {
		return nil
	}
	col := min(max(m.State.CursorCol-fit.ColOffset, 0), fit.Width-1)
	cursor := tea.NewCursor(col, row)
	cursor.Shape = tea.CursorBlock
	cursor.Blink = true
	return cursor
}

// PreferredMouseMode reports the mode suitable for an active embedded
// terminal. Containers still own coordinate translation and mouse forwarding.
func (m *Model) PreferredMouseMode() tea.MouseMode {
	if m.IsActive() {
		return tea.MouseModeCellMotion
	}
	return tea.MouseModeNone
}

// GetTarget returns the current tmux target (pane ID or session name).
func (m *Model) GetTarget() string {
	if !m.IsActive() {
		return ""
	}
	if m.State.TargetPane != "" {
		return m.State.TargetPane
	}
	return m.State.TargetSession
}

// handleKey processes key input in interactive mode.
func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if !m.IsActive() {
		return nil
	}

	// Check for exit key
	if msg.String() == m.Config.ExitKey {
		m.Exit()
		if m.OnExit != nil {
			return m.OnExit()
		}
		return nil
	}

	// Check for attach key
	if msg.String() == m.Config.AttachKey {
		m.Exit()
		if m.OnAttach != nil {
			return m.OnAttach()
		}
		return nil
	}

	// Double-escape exit handling
	if msg.Code == tea.KeyEscape {
		if m.State.EscapePressed {
			// Second Escape within window: exit
			m.State.EscapePressed = false
			m.State.EscapeTimerPending = false
			m.Exit()
			if m.OnExit != nil {
				return m.OnExit()
			}
			return nil
		}
		// First Escape: mark pending and start delay timer
		m.State.EscapePressed = true
		m.State.EscapeTime = time.Now()
		if !m.State.EscapeTimerPending {
			m.State.EscapeTimerPending = true
			scope := m.Scope()
			return tea.Tick(DoubleEscapeDelay, func(t time.Time) tea.Msg {
				return EscapeTimerMsg{Scope: scope}
			})
		}
		return nil
	}

	// Filter partial SGR mouse sequences (td-e2ce50: use lenient check for truncated sequences)
	// Catches even very short fragments like "[<" that occur when terminal splits mouse events.
	// Multi-char fragments like "[<35;10;20M" are caught by LooksLikeMouseFragment.
	if len(msg.Text) > 0 {
		if LooksLikeMouseFragment(msg.Text) {
			m.State.EscapePressed = false
			return nil // Drop mouse sequence fragments
		}
	}

	// Suppress bare "[" that leaks from split SGR mouse sequences.
	// See the detailed comment in workspace/interactive.go handleInteractiveKeys
	// for the full explanation. Two gates:
	//   1. ESC gate: EscapePressed && <5ms since ESC — the ESC was delivered as
	//      a separate keypress and "[" is its CSI continuation.
	//   2. Mouse gate: <10ms since last tea.MouseMsg — Bubble Tea consumed the
	//      ESC internally but "[" leaked as a rune. Successfully-parsed mouse
	//      events and the leaked "[" come from the same terminal output burst.
	if msg.Text == "[" {
		escGate := m.State.EscapePressed &&
			time.Since(m.State.EscapeTime) < 5*time.Millisecond
		mouseGate := time.Since(m.State.LastMouseEventTime) < 10*time.Millisecond
		if escGate || mouseGate {
			m.State.EscapePressed = false
			return nil
		}
	}

	// Handle pending escape before processing new key
	var cmds []tea.Cmd
	pendingEscape := false
	if m.State.EscapePressed {
		m.State.EscapePressed = false
		pendingEscape = true
	}

	// Paste key
	if msg.String() == m.Config.PasteKey {
		m.State.LastKeyTime = time.Now()
		return PasteClipboardToTmuxCmd(m.Scope(), m.State.TargetSession, m.State.BracketedPasteEnabled)
	}

	// Update last key time
	m.State.LastKeyTime = time.Now()

	sessionName := m.State.TargetSession

	// Check for paste input
	if IsPasteInput(msg) {
		text := msg.Text
		bracketed := m.State.BracketedPasteEnabled
		scope := m.Scope()
		if pendingEscape {
			cmds = append(cmds, func() tea.Msg {
				if err := SendKeyToTmux(sessionName, "Escape"); err != nil && IsSessionDeadError(err) {
					return SessionDeadMsg{Scope: scope}
				}
				var err error
				if bracketed {
					err = SendBracketedPasteToTmux(sessionName, text)
				} else {
					err = SendPasteToTmux(sessionName, text)
				}
				if err != nil && IsSessionDeadError(err) {
					return SessionDeadMsg{Scope: scope}
				}
				return nil
			})
		} else {
			cmds = append(cmds, SendPasteInputCmd(scope, sessionName, text, bracketed))
		}
		cmds = append(cmds, m.schedulePoll(KeystrokeDebounce))
		return tea.Batch(cmds...)
	}

	// Map key to tmux format and send
	key, useLiteral := MapKeyToTmux(msg)
	if key == "" {
		if pendingEscape {
			cmds = append(cmds, SendKeysCmd(m.Scope(), sessionName, KeySpec{"Escape", false}))
			cmds = append(cmds, m.schedulePoll(KeystrokeDebounce))
		}
		return tea.Batch(cmds...)
	}

	// Send keys
	if pendingEscape {
		cmds = append(cmds, SendKeysCmd(m.Scope(), sessionName,
			KeySpec{"Escape", false},
			KeySpec{key, useLiteral},
		))
	} else {
		cmds = append(cmds, SendKeysCmd(m.Scope(), sessionName, KeySpec{key, useLiteral}))
	}

	cmds = append(cmds, m.schedulePoll(KeystrokeDebounce))
	return tea.Batch(cmds...)
}

// handlePaste forwards bracketed-paste content (delivered as a tea.PasteMsg in
// v2) to the tmux session, honoring the target app's bracketed paste mode.
func (m *Model) handlePaste(content string) tea.Cmd {
	if !m.IsActive() || content == "" {
		return nil
	}
	m.State.LastKeyTime = time.Now()
	cmds := []tea.Cmd{
		SendPasteInputCmd(m.Scope(), m.State.TargetSession, content, m.State.BracketedPasteEnabled),
		m.schedulePoll(KeystrokeDebounce),
	}
	return tea.Batch(cmds...)
}

// handleMouse processes mouse input in interactive mode.
func (m *Model) handleMouse(msg tea.MouseMsg) tea.Cmd {
	// Record every mouse event (including motion) for split-CSI suppression.
	// See the "[" gate comment in handleKey.
	m.State.LastMouseEventTime = time.Now()

	if !m.IsActive() || !m.State.MouseReportingEnabled {
		return nil
	}

	// Only handle left-button click (press) events. In v2 a press is a
	// MouseClickMsg; release/wheel/motion are distinct types we ignore here.
	click, ok := msg.(tea.MouseClickMsg)
	if !ok {
		return nil
	}
	mouse := click.Mouse()
	if mouse.Button != tea.MouseLeft {
		return nil
	}

	// Convert to pane-relative coordinates
	col := mouse.X + 1
	row := mouse.Y + 1

	sessionName := m.State.TargetSession
	scope := m.Scope()
	return func() tea.Msg {
		if err := SendSGRMouse(sessionName, 0, col, row, false); err != nil {
			if IsSessionDeadError(err) {
				return SessionDeadMsg{Scope: scope}
			}
			return nil
		}
		if err := SendSGRMouse(sessionName, 0, col, row, true); err != nil {
			if IsSessionDeadError(err) {
				return SessionDeadMsg{Scope: scope}
			}
		}
		return nil
	}
}

// handleEscapeTimer processes the escape delay timer firing.
func (m *Model) handleEscapeTimer() tea.Cmd {
	if !m.IsActive() {
		return nil
	}

	m.State.EscapeTimerPending = false

	if !m.State.EscapePressed {
		return nil
	}

	// Timer fired with pending Escape: forward it to tmux
	m.State.EscapePressed = false
	m.State.LastKeyTime = time.Now()

	return tea.Batch(
		SendKeysCmd(m.Scope(), m.State.TargetSession, KeySpec{"Escape", false}),
		m.schedulePoll(0),
	)
}

// handleCaptureResult processes captured output from tmux.
func (m *Model) handleCaptureResult(msg CaptureResultMsg) tea.Cmd {
	if !m.IsActive() || m.State.OutputBuf == nil {
		return nil
	}
	if msg.PollGeneration != m.State.PollGeneration {
		return nil
	}

	if msg.Err != nil {
		if IsSessionDeadError(msg.Err) {
			m.Exit()
			if m.OnExit != nil {
				return m.OnExit()
			}
		}
		return nil
	}

	// Update output buffer
	changed := m.State.OutputBuf.Update(msg.Output)

	// Update cursor state
	m.State.CursorRow = msg.CursorRow
	m.State.CursorCol = msg.CursorCol
	m.State.CursorVisible = msg.CursorVisible
	m.State.PaneHeight = msg.PaneHeight
	m.State.PaneWidth = msg.PaneWidth

	// Update terminal mode state
	if changed {
		m.State.BracketedPasteEnabled = DetectBracketedPasteMode(msg.Output)
		m.State.MouseReportingEnabled = DetectMouseReportingMode(msg.Output)
	}

	// Schedule next poll with adaptive interval
	return m.schedulePoll(CalculatePollingInterval(m.State.LastKeyTime))
}

// handlePollTick handles a poll tick message.
func (m *Model) handlePollTick(msg PollTickMsg) tea.Cmd {
	if !m.IsActive() {
		return nil
	}

	// Check generation to skip stale polls
	if msg.Generation != m.State.PollGeneration {
		return nil
	}

	target := m.GetTarget()
	if target == "" {
		return nil
	}

	// Capture output and cursor position atomically
	scope := msg.Scope
	pollGeneration := msg.Generation
	return func() tea.Msg {
		output, err := CapturePaneOutput(target, m.Config.ScrollbackLines)
		if err != nil {
			return CaptureResultMsg{
				Scope:          scope,
				PollGeneration: pollGeneration,
				Target:         target,
				Err:            err,
			}
		}

		row, col, paneHeight, paneWidth, visible, _ := QueryCursorPositionSync(target)

		return CaptureResultMsg{
			Scope:          scope,
			PollGeneration: pollGeneration,
			Target:         target,
			Output:         output,
			CursorRow:      row,
			CursorCol:      col,
			CursorVisible:  visible,
			PaneHeight:     paneHeight,
			PaneWidth:      paneWidth,
		}
	}
}

// schedulePoll schedules a poll with the given delay.
func (m *Model) schedulePoll(delay time.Duration) tea.Cmd {
	if !m.IsActive() {
		return nil
	}

	m.State.PollGeneration++
	gen := m.State.PollGeneration
	target := m.GetTarget()
	scope := m.Scope()

	if delay <= 0 {
		return func() tea.Msg {
			return PollTickMsg{Scope: scope, Target: target, Generation: gen}
		}
	}

	return tea.Tick(delay, func(t time.Time) tea.Msg {
		return PollTickMsg{Scope: scope, Target: target, Generation: gen}
	})
}

// SetDimensions updates the view dimensions for resize handling.
func (m *Model) SetDimensions(width, height int) tea.Cmd {
	if width == m.Width && height == m.Height {
		return nil
	}

	m.Width = width
	m.Height = height

	if !m.IsActive() {
		return nil
	}

	// Debounce resize
	if !m.State.LastResizeAt.IsZero() && time.Since(m.State.LastResizeAt) < 500*time.Millisecond {
		return nil
	}
	m.State.LastResizeAt = time.Now()

	target := m.GetTarget()
	scope := m.Scope()
	if target == "" {
		return nil
	}

	return func() tea.Msg {
		// Check if resize is needed
		actualWidth, actualHeight, ok := QueryPaneSize(target)
		if ok && actualWidth == width && actualHeight == height {
			return nil
		}
		ResizeTmuxPane(target, width, height)
		return PaneResizedMsg{Scope: scope}
	}
}

// ResizeAndPollImmediate updates dimensions and triggers an immediate resize and poll.
// Unlike SetDimensions, this bypasses debouncing for use with WindowSizeMsg.
// The resize and poll are batched so the view updates immediately after resize.
func (m *Model) ResizeAndPollImmediate(width, height int) tea.Cmd {
	if width == m.Width && height == m.Height {
		return nil
	}

	m.Width = width
	m.Height = height

	if !m.IsActive() {
		return nil
	}

	target := m.GetTarget()
	scope := m.Scope()
	if target == "" {
		return nil
	}

	// Resize command
	resizeCmd := func() tea.Msg {
		actualWidth, actualHeight, ok := QueryPaneSize(target)
		if ok && actualWidth == width && actualHeight == height {
			return nil
		}
		ResizeTmuxPane(target, width, height)
		return PaneResizedMsg{Scope: scope}
	}

	// Immediate poll command
	m.State.PollGeneration++
	gen := m.State.PollGeneration
	pollCmd := func() tea.Msg {
		return PollTickMsg{Scope: scope, Target: target, Generation: gen}
	}

	return tea.Batch(resizeCmd, pollCmd)
}
