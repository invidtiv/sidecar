package tty

import (
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// DefaultScrollbackLines is the number of scrollback lines captured from a pane
// when no explicit scrollback is requested. The live capture stays small; older
// ranges are fetched lazily as the user scrolls past the loaded boundary.
const DefaultScrollbackLines = 600

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

	// SelectAllKey is the keybinding to select every line of output
	// (default: "ctrl+a"). The empty-copy notice names it, so a surface that
	// hard-codes the chord can tell the user to press a key it no longer binds.
	SelectAllKey string

	// CopyOnSelect copies a finished selection without a copy chord, the way
	// xterm does.
	CopyOnSelect bool

	// ScrollbackLines is the number of scrollback lines to capture
	// (default: DefaultScrollbackLines).
	ScrollbackLines int
}

// DefaultConfig returns the default configuration.
func DefaultConfig() Config {
	return Config{
		ExitKey:         "ctrl+\\",
		AttachKey:       "ctrl+]",
		CopyKey:         "alt+c",
		PasteKey:        "alt+v",
		SelectAllKey:    "ctrl+a",
		ScrollbackLines: DefaultScrollbackLines,
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

	// Resize debouncing
	LastResizeAt time.Time

	// Output buffer
	OutputBuf *OutputBuffer

	// Poll generation for invalidating stale polls
	PollGeneration int
}

// HistoryInfo is the transport-neutral absolute history state exposed to an
// embedding viewport. It contains coordinates only; control frames and the
// screen-model implementation remain private to Model.
type HistoryInfo struct {
	HistorySize int
	CaptureBase int
	LoadedStart int
	LoadedEnd   int
	HasHistory  bool
}

// Model is an embeddable component that provides interactive tmux functionality.
// Plugins embed this Model and delegate Update/View when interactive mode is active.
type Model struct {
	Config Config
	State  *State

	ownerID                   uint64
	runGeneration             uint64
	scopeTarget               string
	control                   terminalControlSource
	subscription              terminalControlSubscription
	mailbox                   *terminalMailbox
	mailboxDone               chan struct{}
	controlGen                uint64
	modelLive                 bool
	visible                   bool
	focused                   bool
	input                     terminalInputSender
	capture                   terminalCaptureSource
	history                   HistoryInfo
	recoveryPending           bool
	fallbackEstablished       bool
	consecutiveRecoveryBlanks int

	// Width and Height are set by the containing plugin
	Width  int
	Height int

	// Callbacks for plugin integration
	OnExit   func() tea.Cmd // Called when user exits interactive mode
	OnAttach func() tea.Cmd // Called when user requests full tmux attach

	// OnSessionEnded is called instead of OnExit when the terminal ends because
	// the pane died rather than because the user left it. A host that says
	// nothing about that difference shows a mode ending by itself, which reads
	// as a dropped keystroke. Nil falls back to OnExit.
	OnSessionEnded func() tea.Cmd

	// OnKey is the host's first look at a live key press: the chords that act on
	// the surface around the pane rather than on the pane itself — its own
	// panel toggles, its scrollback, its selection. Returning true stops the key
	// before any of it becomes input, and before the double-escape window sees
	// it. A host that answers a key here must not also answer it outside the
	// component, or the pane receives it twice.
	OnKey func(tea.KeyPressMsg) (tea.Cmd, bool)

	// BeforeSend runs for a key that is about to reach the pane, and only for
	// those: a held escape, a dropped mouse fragment and anything OnKey claimed
	// never arrive here. It is where a host pins its viewport to the live edge
	// and records that the user is typing.
	BeforeSend func(tea.KeyPressMsg)

	// ExitAction is what leaving the mode does to the terminal behind it.
	ExitAction ExitAction

	fragment MouseFragment

	// resizeRetryPending records that a deferred assertion is already armed, so
	// a burst of sizes arriving inside one debounce window schedules one retry
	// rather than one per size. It must be cleared on every path that can drop
	// the retry message, or the model believes a retry it will never receive is
	// still coming and swallows every resize after it.
	resizeRetryPending bool

	// nowFn is the model's clock for the resize debounce. Tests drive a burst
	// through the window without wall-clock time passing inside it; nil is
	// time.Now.
	nowFn func() time.Time
}

// ExitAction separates ending input ownership from closing the terminal.
type ExitAction uint8

const (
	// ExitClosesTerminal ends the mode and the terminal together. A host that
	// only shows the pane while the user is typing into it has nothing left to
	// draw afterwards.
	ExitClosesTerminal ExitAction = iota

	// ExitReleasesInput ends input ownership alone: the buffer, its loaded
	// scrollback and its feed survive. A host that keeps drawing the pane after
	// the user leaves it requires this — closing here drops the scrollback the
	// user just read, and the host's next reconciliation reopens the pane with
	// an empty buffer on the same update, which reads as the output vanishing.
	ExitReleasesInput
)

// Hooks are the host's answers to everything the component decides for itself:
// the chords the surface around the pane owns, the moment a key is about to
// reach it, and the ways the mode can end. They are set together because a host
// that wires some of them re-implements the rest outside the component, which is
// how two surfaces embedding the same terminal come to answer the same key
// differently.
type Hooks struct {
	OnKey          func(tea.KeyPressMsg) (tea.Cmd, bool)
	BeforeSend     func(tea.KeyPressMsg)
	OnExit         func() tea.Cmd
	OnAttach       func() tea.Cmd
	OnSessionEnded func() tea.Cmd
	ExitAction     ExitAction
}

// SetHooks adopts a host's whole contract with the component. ExitAction is
// carried with the callbacks rather than defaulted, so a host states what
// leaving the mode does to the terminal behind it.
func (m *Model) SetHooks(h Hooks) {
	m.OnKey = h.OnKey
	m.BeforeSend = h.BeforeSend
	m.OnExit = h.OnExit
	m.OnAttach = h.OnAttach
	m.OnSessionEnded = h.OnSessionEnded
	m.ExitAction = h.ExitAction
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
		if config.SelectAllKey != "" {
			cfg.SelectAllKey = config.SelectAllKey
		}
		if config.ScrollbackLines > 0 {
			cfg.ScrollbackLines = config.ScrollbackLines
		}
	}
	return &Model{
		Config:  cfg,
		ownerID: nextModelID.Add(1),
		control: sharedTerminalControl,
		visible: true,
		focused: true,
		input:   defaultTerminalInputSender{},
		capture: defaultTerminalCaptureSource{},
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
	m.Exit()
	m.runGeneration++
	m.State = &State{
		Active:        true,
		TargetPane:    paneID,
		TargetSession: sessionName,
		LastKeyTime:   time.Now(),
		CursorVisible: true,
		OutputBuf:     NewOutputBuffer(m.Config.ScrollbackLines),
	}
	m.visible = true
	m.modelLive = false
	m.resizeRetryPending = false
	m.recoveryPending = false
	m.fallbackEstablished = false
	m.consecutiveRecoveryBlanks = 0
	m.history = HistoryInfo{}
	m.scopeTarget = paneID
	if m.scopeTarget == "" {
		m.scopeTarget = sessionName
	}
	m.mailbox = &terminalMailbox{events: make(chan terminalControlEvent, terminalMailboxCapacity)}
	m.mailboxDone = make(chan struct{})

	// Resize pane to match view dimensions
	target := paneID
	if target == "" {
		target = sessionName
	}
	if target != "" && m.Width > 0 && m.Height > 0 {
		ResizeTmuxPane(target, m.Width, m.Height)
	}

	cmds := []tea.Cmd{m.schedulePoll(0), m.listenControl()}
	if paneID != "" {
		m.startControl()
	} else {
		cmds = append(cmds, resolvePaneCmd(m.Scope(), sessionName))
	}
	return tea.Batch(cmds...)
}

// Target identifies the tmux session and pane displayed by a Model.
type Target struct {
	Session string
	Pane    string
}

// Open activates the terminal for target. Enter remains as the compatibility
// spelling for existing embedders.
func (m *Model) Open(target Target) tea.Cmd { return m.Enter(target.Session, target.Pane) }

// Scope returns the identity of the current Model activation. Commands created
// for this model should include this scope in their result messages.
func (m *Model) Scope() MessageScope {
	return MessageScope{
		Owner:      m.ownerID,
		Target:     m.scopeTarget,
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
	if m.subscription != nil {
		m.subscription.Close()
		m.subscription = nil
	}
	if m.mailboxDone != nil {
		close(m.mailboxDone)
		m.mailboxDone = nil
	}
	m.controlGen++
	m.modelLive = false
	m.recoveryPending = false
	m.fallbackEstablished = false
	m.consecutiveRecoveryBlanks = 0
	if m.State != nil {
		m.State.Active = false
	}
	m.State = nil
}

// Close releases the current target and rejects all queued deliveries.
func (m *Model) Close() { m.Exit() }

// endDeadSession closes a terminal whose pane is gone. There is no transport
// left to keep, so this closes whatever the host's ExitAction says about the
// ways out the user chooses.
func (m *Model) endDeadSession() tea.Cmd {
	m.Exit()
	if m.OnSessionEnded != nil {
		return m.OnSessionEnded()
	}
	if m.OnExit != nil {
		return m.OnExit()
	}
	return nil
}

// ReleaseInput ends the component's ownership of the keyboard for a host that
// took the mode away from outside — a click landing off every terminal region,
// a focus change. Every way out must close the double-escape window and drop a
// half-read mouse report, or a timer scheduled by the last escape still reaches
// a model the host believes it has left.
func (m *Model) ReleaseInput() { m.releaseInput() }

// releaseInput ends the component's ownership of the keyboard: the double-escape
// window closes, a half-read mouse report is dropped, and nothing typed reaches
// the pane until a host hands the keyboard back. Whether the terminal behind it
// closes with the mode is [Model.ExitAction]'s answer, not this one's.
func (m *Model) releaseInput() {
	m.fragment.Reset()
	if m.State != nil {
		m.State.EscapePressed = false
		m.State.EscapeTimerPending = false
	}
	if m.ExitAction == ExitReleasesInput {
		return
	}
	m.Exit()
}

// Update handles messages in interactive mode.
// Returns the updated model and any commands to execute.
// Plugins should call this when they receive messages and interactive mode is active.
func (m *Model) Update(msg tea.Msg) tea.Cmd {
	if !m.IsActive() {
		// An inactive model answers nothing, so a retry aimed at it is dropped
		// here. The flag goes with it: it describes a message still in flight.
		m.resizeRetryPending = false
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

	case deferredResizeMsg:
		// Cleared before the ownership guard: a retry this model will not act on
		// is a retry it no longer has, and leaving the flag set here wedges every
		// later resize behind a message that already came and went.
		m.resizeRetryPending = false
		if !m.owns(msg.Scope) {
			return nil
		}
		return m.assertDimensions()

	case PaneResizedMsg:
		if !m.owns(msg.Scope) {
			return nil
		}
		if m.modelLive {
			return nil
		}
		return m.schedulePoll(0)

	case paneResolvedMsg:
		if !m.owns(msg.Scope) || msg.Err != nil || msg.Pane == "" {
			return nil
		}
		m.State.TargetPane = msg.Pane
		m.startControl()
		return nil

	case terminalControlMsg:
		return m.handleControlDelivery(msg)

	case terminalControlRetryMsg:
		if !m.owns(msg.Scope) || msg.Gen != m.controlGen || !m.visible || m.subscription != nil {
			return nil
		}
		m.startControl()
		return nil

	case SessionDeadMsg:
		if !m.owns(msg.Scope) {
			return nil
		}
		return m.endDeadSession()

	case PasteResultMsg:
		if !m.owns(msg.Scope) {
			return nil
		}
		if msg.SessionDead {
			return m.endDeadSession()
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
	lineCount, paneTop, known := m.State.OutputBuf.PaneWindow()
	// The visible window starts RowOffset rows into the pane — which is the
	// pane's tail unless the cursor pulls the window up (td-73fa86). paneTop
	// never goes negative, so View and Cursor stay anchored to the same row even
	// when tmux captured fewer lines than the pane is tall.
	start := lineCount - fit.Height
	if known || m.State.PaneHeight > 0 {
		start = m.paneTop(lineCount, paneTop, known) + fit.RowOffset
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

// paneTop is the buffer line holding pane row 0. It prefers the split the
// producer published with the content, and falls back to the buffer's tail for
// a buffer that never received one. The fallback is only ever an approximation:
// a capture whose final grid rows were blank leaves the tail short, and the
// pane then appears to start a row too early (td-d29821). Clamping at 0 keeps
// the mapping from pane row to buffer line one-to-one in that case instead of
// letting View clamp while Cursor does not (td-73fa86).
func (m *Model) paneTop(lineCount, paneTop int, known bool) int {
	if known {
		return paneTop
	}
	return max(lineCount-m.State.PaneHeight, 0)
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
	return PlaceCursor(col, row)
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

// inputTarget is the single routing decision for embedded terminal input.
// Rendering, capture, and input must all address the explicit pane when one is
// known. Full-session attach remains a separate, deliberately session-scoped
// capability owned by the embedding callback.
func (m *Model) inputTarget() string { return m.GetTarget() }

// History returns a read-only snapshot of absolute history metadata and the
// currently loaded half-open range. HasHistory is false for legacy/provisional
// captures that do not carry trustworthy absolute coordinates.
func (m *Model) History() HistoryInfo {
	info := m.history
	if !m.IsActive() || m.State.OutputBuf == nil {
		return HistoryInfo{}
	}
	if start, end, ok := m.State.OutputBuf.AbsoluteRange(); ok {
		info.LoadedStart, info.LoadedEnd = start, end
	} else {
		info.LoadedStart, info.LoadedEnd = 0, 0
	}
	return info
}

// LinesAbsoluteRange returns loaded terminal rows in [start, end).
func (m *Model) LinesAbsoluteRange(start, end int) []string {
	if !m.IsActive() || m.State.OutputBuf == nil {
		return nil
	}
	return m.State.OutputBuf.LinesAbsoluteRange(start, end)
}

// PrependHistory merges an older overlapping capture into the loaded absolute
// buffer. Transport and scheduling remain owned by Model's embedding journey.
func (m *Model) PrependHistory(content string, baseLine int) bool {
	if !m.IsActive() || m.State.OutputBuf == nil || !m.history.HasHistory {
		return false
	}
	return m.State.OutputBuf.PrependSnapshot(content, baseLine)
}

// handleKey processes key input in interactive mode.
func (m *Model) handleKey(msg tea.KeyPressMsg) tea.Cmd {
	if !m.IsActive() {
		return nil
	}

	// The host's own acts come first, including ahead of the ways out: a host
	// that is typing into its own field — a search box over the pane — owns
	// every key while it is, exit chord included.
	if m.OnKey != nil {
		if cmd, handled := m.OnKey(msg); handled {
			return cmd
		}
	}

	// Check for exit key
	if msg.String() == m.Config.ExitKey {
		m.releaseInput()
		if m.OnExit != nil {
			return m.OnExit()
		}
		return nil
	}

	// Check for attach key. An empty AttachKey is a host with no attach path:
	// the chord belongs to the pane like any other key.
	if m.Config.AttachKey != "" && msg.String() == m.Config.AttachKey {
		m.releaseInput()
		if m.OnAttach != nil {
			return m.OnAttach()
		}
		return nil
	}

	now := time.Now()
	// Reassembling a report split across reads reads state the one-key gate
	// cannot: the fragment held from the previous read.
	if len(msg.Text) > 0 && m.fragment.Consume(msg.Text, m.State.EscapePressed, m.State.EscapeTime, now) {
		m.State.EscapePressed = false
		return nil
	}

	switch GateKey(KeyGateInput{
		Msg:           msg,
		EscapePressed: m.State.EscapePressed,
		EscapeAt:      m.State.EscapeTime,
		LastMouseAt:   m.State.LastMouseEventTime,
		Now:           now,
	}) {
	case KeyGateExitDoubleEscape:
		m.releaseInput()
		if m.OnExit != nil {
			return m.OnExit()
		}
		return nil

	case KeyGateHoldEscape:
		// Hold the escape rather than forwarding it: the second half of a double
		// escape may still be on its way, and the timer forwards it if it is not.
		m.State.EscapePressed = true
		m.State.EscapeTime = now
		if m.State.EscapeTimerPending {
			return nil
		}
		m.State.EscapeTimerPending = true
		scope := m.Scope()
		return tea.Tick(DoubleEscapeDelay, func(t time.Time) tea.Msg {
			return EscapeTimerMsg{Scope: scope}
		})

	case KeyGateDrop:
		if msg.Text == "[" {
			// The bracket opened a report whose remainder is still to come, so it
			// is held for the reassembly above rather than dropped outright.
			m.fragment.Hold("[", now)
		}
		m.State.EscapePressed = false
		return nil
	}

	// Handle pending escape before processing new key
	var cmds []tea.Cmd
	pendingEscape := false
	if m.State.EscapePressed {
		m.State.EscapePressed = false
		pendingEscape = true
	}

	if m.BeforeSend != nil {
		m.BeforeSend(msg)
	}

	// Paste key
	if msg.String() == m.Config.PasteKey {
		m.State.LastKeyTime = time.Now()
		return m.input.PasteClipboard(m.Scope(), m.inputTarget())
	}

	// Update last key time
	m.State.LastKeyTime = time.Now()

	target := m.inputTarget()

	// Check for paste input
	if IsPasteInput(msg) {
		text := msg.Text
		scope := m.Scope()
		if pendingEscape {
			cmds = append(cmds, m.input.SendEscapePaste(scope, target, text))
		} else {
			cmds = append(cmds, m.input.SendPaste(scope, target, text))
		}
		cmds = append(cmds, m.schedulePoll(KeystrokeDebounce))
		return tea.Batch(cmds...)
	}

	// Map key to tmux format and send
	key, useLiteral := MapKeyToTmux(msg)
	if key == "" {
		if pendingEscape {
			cmds = append(cmds, m.input.SendKeys(m.Scope(), target, KeySpec{"Escape", false}))
			cmds = append(cmds, m.schedulePoll(KeystrokeDebounce))
		}
		return tea.Batch(cmds...)
	}

	// Send keys
	if pendingEscape {
		cmds = append(cmds, m.input.SendKeys(m.Scope(), target,
			KeySpec{"Escape", false},
			KeySpec{key, useLiteral},
		))
	} else {
		cmds = append(cmds, m.input.SendKeys(m.Scope(), target, KeySpec{key, useLiteral}))
	}

	cmds = append(cmds, m.schedulePoll(KeystrokeDebounce))
	return tea.Batch(cmds...)
}

// SendUnknownSequence forwards an unparsed CSI sequence — kitty CSI u or
// modifyOtherKeys, which Bubble Tea does not turn into a KeyPressMsg — to the
// pane as the key it actually is, so modified keys like shift+enter reach the
// application running there.
//
// It is a method rather than a case in Update because a host that already
// forwards these itself would otherwise send each of them twice; the caller
// decides which of the two paths owns the sequence.
func (m *Model) SendUnknownSequence(msg tea.Msg) tea.Cmd {
	if !m.IsActive() {
		return nil
	}
	raw := ExtractUnknownCSIBytes(msg)
	if raw == nil {
		return nil
	}
	csiu := NormalizeToCSIu(raw)
	if csiu == "" {
		return nil
	}
	m.State.LastKeyTime = time.Now()
	return tea.Batch(
		m.input.SendKeys(m.Scope(), m.inputTarget(), KeySpec{Value: csiu, Literal: true}),
		m.schedulePoll(KeystrokeDebounce),
	)
}

// handlePaste forwards bracketed-paste content (delivered as a tea.PasteMsg in
// v2) to the tmux session; tmux applies the target app's bracketed paste mode.
func (m *Model) handlePaste(content string) tea.Cmd {
	if !m.IsActive() || content == "" {
		return nil
	}
	m.State.LastKeyTime = time.Now()
	cmds := []tea.Cmd{
		m.input.SendPaste(m.Scope(), m.inputTarget(), content),
		m.schedulePoll(KeystrokeDebounce),
	}
	return tea.Batch(cmds...)
}

// handleMouse records mouse activity in interactive mode.
//
// It deliberately forwards nothing. A pointer event only becomes the pane's
// after a host has hit-tested it, routed it through PaneCoords, and decided the
// gesture was not a local selection; a second path here would send raw viewport
// coordinates to a clipped pane and forward drags the host had already claimed.
func (m *Model) handleMouse(tea.MouseMsg) tea.Cmd {
	m.NoteMouseActivity()
	return nil
}

// NoteMouseActivity records that a mouse event reached this host, which is what
// the bare-"[" gate in handleKey reads. Hosts that route pointer events
// themselves must call it, or a split SGR sequence leaks into the pane as a
// literal bracket.
func (m *Model) NoteMouseActivity() {
	if m.State == nil {
		return
	}
	m.State.LastMouseEventTime = time.Now()
}

// LastMouseActivity is when a mouse event last reached this terminal. A host
// that runs the shared key gate itself reads it from here rather than keeping a
// clock of its own, or the two would disagree about the same bracket.
func (m *Model) LastMouseActivity() time.Time {
	if m.State == nil {
		return time.Time{}
	}
	return m.State.LastMouseEventTime
}

// Buffer is the captured output the terminal is drawing, so a host can render
// its own window over it — scrolled back, with a selection highlighted — instead
// of only the live tail View draws.
func (m *Model) Buffer() *OutputBuffer {
	if !m.IsActive() {
		return nil
	}
	return m.State.OutputBuf
}

// PaneMouseReporting reports that the application running in the pane has asked
// for mouse events. It is the whole of "does this notch or click belong to the
// app or to the surface showing it".
func (m *Model) PaneMouseReporting() bool {
	return m.IsActive() && m.State.MouseReportingEnabled
}

// PaneSize is the pane's observed grid, which can differ from the viewport it is
// drawn in: another client may be driving the same session.
func (m *Model) PaneSize() (width, height int) {
	if !m.IsActive() {
		return 0, 0
	}
	return m.State.PaneWidth, m.State.PaneHeight
}

// CursorState is the pane's own cursor, in pane coordinates.
func (m *Model) CursorState() (row, col int, visible bool) {
	if !m.IsActive() {
		return 0, 0, false
	}
	return m.State.CursorRow, m.State.CursorCol, m.State.CursorVisible
}

// SendClick forwards a press and release at 1-indexed pane coordinates to the
// application running in the pane, and polls straight away so the frame it draws
// in response is not held back by the idle cadence.
func (m *Model) SendClick(col, row int) tea.Cmd {
	if !m.IsActive() {
		return nil
	}
	m.State.LastKeyTime = time.Now()
	return tea.Batch(
		m.input.SendMouse(m.Scope(), m.inputTarget(), col, row),
		m.schedulePoll(0),
	)
}

// SendWheelNotches forwards whole wheel notches at 1-indexed pane coordinates.
//
// The wheel is the user's most recent input, so it counts as activity: the poll
// cadence decays to its slow tier on idle time, and a scroll that did not reset
// it would be repainted at that tier.
func (m *Model) SendWheelNotches(up bool, col, row, notches int) tea.Cmd {
	if !m.IsActive() || notches <= 0 {
		return nil
	}
	m.State.LastKeyTime = time.Now()
	return tea.Batch(
		m.input.SendWheel(m.Scope(), m.inputTarget(), up, col, row, notches),
		m.schedulePoll(0),
	)
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
		m.input.SendKeys(m.Scope(), m.inputTarget(), KeySpec{"Escape", false}),
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
			if cmd := m.endDeadSession(); cmd != nil {
				return cmd
			}
		}
		// A transient capture failure must not strand a dead-control recovery.
		// Keep fallback polling alive and allow a clean control retry; the next
		// successful capture remains provisional until its replacement seed.
		if m.recoveryPending {
			m.recoveryPending = false
			return tea.Batch(
				m.schedulePoll(CalculatePollingInterval(m.State.LastKeyTime)),
				m.retryControl(),
			)
		}
		return nil
	}
	if m.preserveRecoveryBlank(msg.Output) {
		// tmux may briefly expose an empty alternate grid while a replacement
		// client changes geometry. Preserve the last known-good model frame and
		// retry capture before accepting blank as real terminal state.
		return m.schedulePoll(PollingDecayFast)
	}

	// Update output buffer. The poll carries no absolute coordinates, but its
	// capture and its pane height were observed together, so it can still state
	// where the live grid starts. CapturePaneOutput never passes -J, so the
	// capture's rows are the grid's rows.
	changed := m.State.OutputBuf.ApplySnapshot(CaptureSnapshot(CaptureInput{
		Output:     msg.Output,
		PaneHeight: msg.PaneHeight,
	}))
	m.history = HistoryInfo{}
	if m.recoveryPending {
		m.fallbackEstablished = true
	}

	// Update cursor state
	m.State.CursorRow = msg.CursorRow
	m.State.CursorCol = msg.CursorCol
	m.State.CursorVisible = msg.CursorVisible
	m.State.PaneHeight = msg.PaneHeight
	m.State.PaneWidth = msg.PaneWidth

	// Update terminal mode state. Mouse tracking is taken from the flag the
	// capture carried, and taken on every poll: an application turns tracking on
	// and off without redrawing a cell, so a mode read only when the screen
	// changed goes stale exactly when it matters.
	if changed {
		m.State.BracketedPasteEnabled = DetectBracketedPasteMode(msg.Output)
	}
	m.State.MouseReportingEnabled = msg.MouseReporting

	// Control-death recovery is deliberately sequenced after this accepted
	// capture. This guarantees the fallback screen becomes visible before a
	// replacement seed can invalidate its poll generation.
	nextPoll := m.schedulePoll(CalculatePollingInterval(m.State.LastKeyTime))
	if m.recoveryPending {
		m.recoveryPending = false
		return tea.Batch(nextPoll, m.retryControl())
	}
	return nextPoll
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
		output, state, err := m.capture.Capture(target, m.Config.ScrollbackLines)
		if err != nil {
			return CaptureResultMsg{
				Scope:          scope,
				PollGeneration: pollGeneration,
				Target:         target,
				Err:            err,
			}
		}

		return CaptureResultMsg{
			Scope:          scope,
			PollGeneration: pollGeneration,
			Target:         target,
			Output:         output,
			CursorRow:      state.CursorRow,
			CursorCol:      state.CursorCol,
			CursorVisible:  state.CursorVisible,
			PaneHeight:     state.PaneHeight,
			PaneWidth:      state.PaneWidth,
			MouseReporting: state.MouseReporting,
		}
	}
}

// schedulePoll schedules a poll with the given delay.
func (m *Model) schedulePoll(delay time.Duration) tea.Cmd {
	if !m.IsActive() || !m.visible || m.modelLive {
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

// ResizeDebounce bounds how often a resize is asserted against tmux while a
// layout is still moving — a window drag delivers one size per frame.
const ResizeDebounce = 500 * time.Millisecond

// ResizeWait is how long a host must hold off asserting geometry, given when it
// last did. Zero means assert now. Every surface that resizes a pane asks here:
// a second literal budget beside this one is how two surfaces come to answer the
// same layout change at different rates.
//
// Waiting is not dropping. A caller that gets a positive wait still owes the
// pane the newest geometry and must schedule exactly one deferred assertion for
// it — one, however many sizes arrive inside the window, or a burst becomes a
// chain of resizes spaced a debounce apart, which is what the budget exists to
// prevent.
func ResizeWait(last, now time.Time) time.Duration {
	if last.IsZero() {
		return 0
	}
	if wait := ResizeDebounce - now.Sub(last); wait > 0 {
		return wait
	}
	return 0
}

// now reads the model's clock.
func (m *Model) now() time.Time {
	if m.nowFn != nil {
		return m.nowFn()
	}
	return time.Now()
}

// SetDimensions updates the view dimensions for resize handling.
func (m *Model) SetDimensions(width, height int) tea.Cmd {
	if width == m.Width && height == m.Height {
		return nil
	}

	m.Width = width
	m.Height = height
	return m.assertDimensions()
}

// assertDimensions gives the pane the size the model holds, or defers doing
// so until the debounce window closes.
//
// The debounce bounds how often tmux is asked; it must not decide whether the
// pane is ever given the size. Two layout changes inside one window — ctrl+t
// then alt+t, a window resize followed by a panel toggle — would otherwise leave
// the pane at the first one's geometry with nothing left to correct it, because
// the model already believes it asked for the second.
func (m *Model) assertDimensions() tea.Cmd {
	if !m.IsActive() {
		return nil
	}
	if m.subscription != nil {
		return m.restartControlForResize()
	}

	target := m.GetTarget()
	scope := m.Scope()
	if target == "" {
		return nil
	}

	if wait := ResizeWait(m.State.LastResizeAt, m.now()); wait > 0 {
		// One retry stands for the whole burst: it reads the geometry the model
		// holds when it fires, which is the newest by then. Arming a second would
		// chain a resize per size the window passed through.
		if m.resizeRetryPending {
			return nil
		}
		m.resizeRetryPending = true
		return tea.Tick(wait, func(time.Time) tea.Msg {
			return deferredResizeMsg{Scope: scope}
		})
	}
	// Recorded here, where the resize is actually issued: a deferred call that
	// consumed the budget would push its own retry out of reach.
	m.State.LastResizeAt = m.now()
	width, height := m.Width, m.Height

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
	if m.subscription != nil {
		return m.restartControlForResize()
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
