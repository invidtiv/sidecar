package tty

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/clip"
	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

// The callback side of ControlManager is intentionally hidden here. The
// manager's ordered actor must never wait for Bubble Tea, and Bubble Tea must
// be the only goroutine mutating Model state.
const terminalMailboxCapacity = 32

type terminalMailbox struct {
	events      chan terminalControlEvent
	overflowGen atomic.Uint64
}

// modelInteractionState retains model fields that tty.State does not expose as
// public surface policy. Keeping the full state makes the delivery boundary's
// no-op decision match the control actor's presentation identity instead of
// collapsing distinct cursor, alternate-screen, or mouse-mode transitions.
type modelInteractionState struct {
	cursorStyle screenmodel.CursorStyle
	altScreen   bool
	mouse       screenmodel.MouseState
}

type terminalControlSubscription interface {
	SetVisible(bool)
	SetFocused(bool)
	Resize(int, int)
	Close()
}

type terminalInputSender interface {
	SendKeys(MessageScope, string, ...KeySpec) tea.Cmd
	SendPaste(MessageScope, string, string) tea.Cmd
	SendEscapePaste(MessageScope, string, string) tea.Cmd
	PasteClipboard(MessageScope, string) tea.Cmd
	SendMouse(MessageScope, string, int, int) tea.Cmd
	SendWheel(MessageScope, string, bool, int, int, int) tea.Cmd
}

type terminalCaptureSource interface {
	Capture(target string, scrollback int) (output string, state PaneState, err error)
}

type defaultTerminalCaptureSource struct{}

// terminalRecoveryBlankLimit is one source-neutral consecutive-observation
// budget shared by fallback captures, control snapshots, and replacement model
// seeds. Eight observations cover the short attach/geometry redraw window while
// bounding pure fast-capture retention at about 400ms and pure 250ms seed-retry
// retention at about 2s. Any accepted nonblank candidate resets the budget.
const terminalRecoveryBlankLimit = 8

func (defaultTerminalCaptureSource) Capture(target string, scrollback int) (string, PaneState, error) {
	return CapturePaneWithState(target, scrollback)
}

type defaultTerminalInputSender struct{ model *Model }

// These seams keep the ordered actor and activation lease production-real in
// concurrency tests while replacing only the final tmux/clipboard effect.
var (
	terminalSendKeys      = SendKeys
	terminalSendPaste     = SendPasteInput
	terminalSendKey       = SendKeyToTmux
	terminalSendPasteRaw  = SendPasteToTmux
	terminalSendMouse     = SendSGRMouse
	terminalSendWheel     = SendSGRWheel
	terminalReadClipboard = clip.ReadAll
)

func (s defaultTerminalInputSender) SendKeys(scope MessageScope, target string, keys ...KeySpec) tea.Cmd {
	return awaitOrderedSend(scope, SendOrdered(target, func() error {
		return s.model.withActivationError(scope, func() error { return terminalSendKeys(target, keys...) })
	}))
}

func (s defaultTerminalInputSender) SendPaste(scope MessageScope, target, text string) tea.Cmd {
	return awaitOrderedSend(scope, SendOrdered(target, func() error {
		return s.model.withActivationError(scope, func() error { return terminalSendPaste(target, text) })
	}))
}

func (s defaultTerminalInputSender) SendEscapePaste(scope MessageScope, target, text string) tea.Cmd {
	return awaitOrderedSend(scope, SendOrdered(target, func() error {
		return s.model.withActivationError(scope, func() error {
			if err := terminalSendKey(target, "Escape"); err != nil {
				return err
			}
			return terminalSendPasteRaw(target, text)
		})
	}))
}

func (s defaultTerminalInputSender) PasteClipboard(scope MessageScope, target string) tea.Cmd {
	var result PasteResultMsg
	done := SendOrdered(target, func() error {
		return s.model.withActivationError(scope, func() error {
			result.Scope = scope
			text, err := terminalReadClipboard()
			if err != nil || text == "" {
				// Over SSH the system clipboard belongs to a machine the
				// reader cannot see; what this session copied, it still has.
				if recent, ok := clip.LastCopied(); ok && recent != "" {
					text, err = recent, nil
				}
			}
			if err != nil {
				result.Err = err
				return nil
			}
			if text == "" {
				result.Empty = true
				return nil
			}
			if err := terminalSendPasteRaw(target, text); err != nil {
				result.Err = err
				result.SessionDead = IsSessionDeadError(err)
			}
			return nil
		})
	})
	return func() tea.Msg {
		<-done
		if result.Scope.Owner == 0 {
			return nil
		}
		return result
	}
}

// Pointer reports are queued at call time, like keystrokes, so a click or a
// notch keeps its place relative to the keys around it. Bubble Tea runs each Cmd
// concurrently, so ordering established inside the returned Cmd would be no
// ordering at all (td-8fcd2e).
func (s defaultTerminalInputSender) SendMouse(scope MessageScope, target string, col, row int) tea.Cmd {
	return awaitOrderedSend(scope, SendOrdered(target, func() error {
		return s.model.withActivationError(scope, func() error {
			if err := terminalSendMouse(target, 0, col, row, false); err != nil {
				return err
			}
			return terminalSendMouse(target, 0, col, row, true)
		})
	}))
}

func (s defaultTerminalInputSender) SendWheel(scope MessageScope, target string, up bool, col, row, notches int) tea.Cmd {
	return awaitOrderedSend(scope, SendOrdered(target, func() error {
		return s.model.withActivationError(scope, func() error {
			return terminalSendWheel(target, up, col, row, notches)
		})
	}))
}

func awaitOrderedSend(scope MessageScope, done <-chan error) tea.Cmd {
	return func() tea.Msg {
		if err := <-done; err != nil && IsSessionDeadError(err) {
			return SessionDeadMsg{Scope: scope}
		}
		return nil
	}
}

type terminalControlSource interface {
	Subscribe(ControlRequest) (terminalControlSubscription, error)
}

type controlManagerSource struct{ manager *ControlManager }

func (s controlManagerSource) Subscribe(r ControlRequest) (terminalControlSubscription, error) {
	return s.manager.Subscribe(r)
}

var sharedTerminalControl terminalControlSource = controlManagerSource{manager: NewControlManager()}

// inertControlSource is the transport a model gets under `go test` unless a
// test asks for a real one.
//
// The shared source spawns `tmux -C attach-session` as a child process, and
// nothing in Enter's contract tells a caller that. A unit test that only wanted
// to assert key routing — `m := New(nil); m.Enter("session", "%1")` — was
// therefore starting a real tmux client per test, against whatever server
// TMUX_TMPDIR resolved to. The client outlives the test binary, so `go test`
// left a growing population of orphans behind on the developer's own server
// (td-4d99ae). Subscribing to nothing is the honest default for a unit test:
// the transport is exactly the part it is not exercising.
//
// Tests that do exercise the transport build their own ControlManager over an
// explicit socket (newProcessControlChannelForSocket) and call Subscribe on it
// directly, so they never reach this.
type inertControlSource struct{}

// ErrInertControlTransport is what an inert transport reports. A test that
// meant to exercise control mode and sees this in a fallback has not found a
// broken transport — it has found that it never opted in. Callers that care can
// tell the two apart with errors.Is rather than passing vacuously.
var ErrInertControlTransport = errors.New(
	"tmux control: inert transport under test; call tty.UseRealControlTransport to opt in")

func (inertControlSource) Subscribe(ControlRequest) (terminalControlSubscription, error) {
	return nil, ErrInertControlTransport
}

// realControlUnderTest lets a test opt back in to the real transport.
var realControlUnderTest atomic.Bool

// UseRealControlTransport restores the real tmux control transport for tests
// that genuinely exercise it — the live-terminal tests that drive a real pane
// through the seed-and-stream path and assert against tmux's own answers.
//
// Those tests must already have isolated tmux (see internal/testenv), because
// this is the switch that lets a test spawn real `tmux -C attach-session`
// children. It is deliberately explicit: opting in is a statement that the test
// owns a private server and will let its panes be torn down with it.
func UseRealControlTransport() (restore func()) {
	previous := realControlUnderTest.Swap(true)
	return func() { realControlUnderTest.Store(previous) }
}

// defaultControlSource picks the transport a new Model starts with.
//
// Under `go test` the default is inert, because the overwhelming majority of
// tests that construct a Model are asserting on key routing or rendering and
// never wanted a tmux child process at all — see inertControlSource.
func defaultControlSource() terminalControlSource {
	if testing.Testing() && !realControlUnderTest.Load() {
		return inertControlSource{}
	}
	return sharedTerminalControl
}

type terminalControlEventKind uint8

const (
	terminalFrameEvent terminalControlEventKind = iota + 1
	terminalSnapshotEvent
	terminalInvalidEvent
	terminalFallbackEvent
)

type terminalControlEvent struct {
	kind     terminalControlEventKind
	frame    ModelFrame
	snapshot ControlSnapshot
	invalid  ModelInvalidation
	err      error
	gen      uint64
}

type terminalControlMsg struct {
	Scope       MessageScope
	Event       terminalControlEvent
	OverflowGen uint64
}

type paneResolvedMsg struct {
	Scope MessageScope
	Pane  string
	Err   error
}

type terminalControlRetryMsg struct {
	Scope MessageScope
	Gen   uint64
}

var terminalResolvePane = func(target string) (string, error) {
	out, err := exec.Command("tmux", "display-message", "-p", "-t", target, "#{pane_id}").Output()
	pane := strings.TrimSpace(string(out))
	if err == nil && !controlPanePattern.MatchString(pane) {
		err = fmt.Errorf("tmux terminal: invalid resolved pane %q", pane)
	}
	return pane, err
}

func (m *Model) resolvePaneCmd(scope MessageScope, target string) tea.Cmd {
	return func() tea.Msg {
		return m.withActivationMessage(scope, func() tea.Msg {
			if target == "" {
				return paneResolvedMsg{Scope: scope, Err: fmt.Errorf("tmux terminal: empty target")}
			}
			pane, err := terminalResolvePane(target)
			return paneResolvedMsg{Scope: scope, Pane: pane, Err: err}
		})
	}
}

func enqueueTerminalControl(mailbox *terminalMailbox, event terminalControlEvent) {
	if mailbox == nil {
		return
	}
	select {
	case mailbox.events <- event:
	default:
		// Coalescing a frame is safe only while byte continuity is known. Treat
		// any pressure as lost presentation and let Update perform a clean reseed.
		mailbox.overflowGen.Store(event.gen)
	}
}

func (m *Model) listenControl() tea.Cmd {
	mailbox, done, scope := m.mailbox, m.mailboxDone, m.Scope()
	if mailbox == nil || done == nil {
		return nil
	}
	return func() tea.Msg {
		select {
		case event := <-mailbox.events:
			return terminalControlMsg{Scope: scope, Event: event, OverflowGen: mailbox.overflowGen.Swap(0)}
		case <-done:
			return nil
		}
	}
}

func (m *Model) startControl() {
	if !m.IsActive() || m.State.TargetSession == "" || !controlPanePattern.MatchString(m.State.TargetPane) {
		return
	}
	if m.subscription != nil {
		m.subscription.Close()
		m.subscription = nil
	}
	m.controlGen++
	m.modelPresentationSet = false
	gen := m.controlGen
	mailbox := m.mailbox
	request := ControlRequest{
		Session: m.State.TargetSession, Pane: m.State.TargetPane,
		Width: m.Width, Height: m.Height, Scrollback: m.Config.ScrollbackLines,
		Visible: m.visible, Focused: m.focused, ModelPresentation: true,
	}
	request.OnModelFrame = func(frame ModelFrame) {
		enqueueTerminalControl(mailbox, terminalControlEvent{kind: terminalFrameEvent, frame: frame, gen: gen})
	}
	request.OnSnapshot = func(snapshot ControlSnapshot) {
		enqueueTerminalControl(mailbox, terminalControlEvent{kind: terminalSnapshotEvent, snapshot: snapshot, gen: gen})
	}
	request.OnModelInvalid = func(invalid ModelInvalidation) {
		enqueueTerminalControl(mailbox, terminalControlEvent{kind: terminalInvalidEvent, invalid: invalid, gen: gen})
	}
	request.OnFallback = func(err error) {
		enqueueTerminalControl(mailbox, terminalControlEvent{kind: terminalFallbackEvent, err: err, gen: gen})
	}
	sub, err := m.control.Subscribe(request)
	if err != nil {
		enqueueTerminalControl(mailbox, terminalControlEvent{kind: terminalFallbackEvent, err: err, gen: gen})
		return
	}
	m.subscription = sub
}

func (m *Model) stopControl() {
	if m.subscription != nil {
		m.subscription.Close()
		m.subscription = nil
	}
	m.controlGen++
}

func (m *Model) retryControl() tea.Cmd {
	scope, gen := m.Scope(), m.controlGen
	return tea.Tick(250*time.Millisecond, func(time.Time) tea.Msg {
		return terminalControlRetryMsg{Scope: scope, Gen: gen}
	})
}

// restartControlForResize makes geometry a subscription-generation boundary.
// A frame queued before the resize describes the old grid and must never
// restore model-backed presentation. The replacement request carries the new dimensions
// and seeds cleanly while capture polling remains provisional.
func (m *Model) restartControlForResize() tea.Cmd {
	m.modelLive = false
	m.stopControl()
	if m.visible {
		m.startControl()
	}
	return m.schedulePoll(0)
}

func (m *Model) handleControlDelivery(msg terminalControlMsg) tea.Cmd {
	if !m.owns(msg.Scope) {
		return nil
	}
	if !m.visible {
		return m.listenControl()
	}
	if msg.Event.gen != m.controlGen {
		return m.listenControl()
	}
	if msg.OverflowGen == m.controlGen {
		m.modelLive = false
		m.stopControl()
		m.recoveryPending = true
		m.consecutiveRecoveryBlanks = 0
		return tea.Batch(m.schedulePoll(0), m.listenControl())
	}

	var cmd tea.Cmd
	switch msg.Event.kind {
	case terminalFrameEvent:
		frame := msg.Event.frame
		if frame.Seeds < 1 || frame.Session != m.State.TargetSession || frame.Pane != m.State.TargetPane {
			break
		}
		output := frame.Frame.CombinedOutput()
		if m.preserveRecoveryBlank(output) {
			// A replacement control client can capture the alternate screen during
			// its attach/geometry redraw window. Keep the known-good subprocess
			// fallback visible and ask for another clean seed rather than allowing
			// a transient blank bootstrap to reclaim presentation authority. The
			// bound lets a terminal that was genuinely cleared eventually win.
			m.stopControl()
			cmd = m.retryControl()
			break
		}
		m.applyModelFrameOutput(frame.Frame, output)
	case terminalSnapshotEvent:
		if !m.modelLive {
			s := msg.Event.snapshot
			if !m.preserveRecoveryBlank(s.Output) {
				m.applySnapshot(s)
			}
		}
	case terminalInvalidEvent:
		m.modelLive = false
		if msg.Event.invalid.Terminal {
			m.stopControl()
			m.recoveryPending = true
			m.consecutiveRecoveryBlanks = 0
			cmd = m.schedulePoll(0)
			break
		}
		cmd = m.schedulePoll(0)
	case terminalFallbackEvent:
		m.modelLive = false
		m.stopControl()
		// A remote pane learns that its session has ended here or nowhere.
		//
		// Locally, a control channel that fails falls back to polling
		// capture-pane, and the first capture answers "can't find pane", which
		// is what ends the mode. A remote model has no capture fallback on
		// purpose — pane IDs are per-server, so a local capture-pane for a
		// remote %4 does not fail, it paints an unrelated local pane — so that
		// answer never arrives. The mode stayed on, the retry loop respawned
		// ssh every 250ms forever, and every keystroke went into a session that
		// no longer existed: the row went away and the preview did not.
		//
		// The evidence is the attach error, which now carries the child's
		// stderr (see newProcessControlChannelCommand). "can't find session"
		// ends the mode; an ssh failure does not match a gone marker and so
		// keeps retrying, which is the difference between a dead session and a
		// dead link and the reason this is not simply a retry budget.
		if m.remote && IsSessionDeadError(msg.Event.err) {
			return m.endDeadSession()
		}
		m.recoveryPending = true
		m.consecutiveRecoveryBlanks = 0
		cmd = m.schedulePoll(0)
	}
	return tea.Batch(cmd, m.listenControl())
}

// applyModelFrame adopts one model presentation and reports whether anything a
// terminal consumer observes changed. OutputBuffer owns row-derived revision;
// cursor and mode-only changes deliberately do not invalidate row caches.
func (m *Model) applyModelFrame(frame screenmodel.Frame) bool {
	return m.applyModelFrameOutput(frame, frame.CombinedOutput())
}

func (m *Model) applyModelFrameOutput(frame screenmodel.Frame, output string) bool {
	// The frame knows its own split exactly and publishes it with the content it
	// describes: LoadedHistory.Rows() rows above pane row 0, then Height grid
	// rows. Nothing downstream has to re-derive it from the serialized form.
	bufferChanged := m.State.OutputBuf.ApplySnapshot(PaneSnapshot{
		Output:      output,
		BaseLine:    frame.CaptureBase,
		Absolute:    frame.HasHistory,
		HistoryRows: frame.LoadedHistory.Rows(),
		PaneRows:    frame.Height,
	})
	nextHistory := HistoryInfo{}
	if frame.HasHistory {
		nextHistory = HistoryInfo{
			HistorySize: frame.HistorySize,
			CaptureBase: frame.CaptureBase,
			HasHistory:  true,
		}
	}
	nextInteraction := modelInteractionState{
		cursorStyle: frame.CursorStyle,
		altScreen:   frame.AltScreen,
		mouse:       frame.Mouse,
	}
	stateChanged := !m.modelLive || m.history != nextHistory ||
		m.State.CursorRow != frame.CursorRow || m.State.CursorCol != frame.CursorCol ||
		m.State.CursorVisible != frame.CursorVisible ||
		m.State.PaneHeight != frame.Height || m.State.PaneWidth != frame.Width ||
		m.State.BracketedPasteEnabled != frame.BracketedPaste ||
		m.State.MouseReportingEnabled != frame.Mouse.Any() ||
		!m.modelPresentationSet || m.modelPresentation != nextInteraction

	m.history = nextHistory
	m.State.CursorRow = frame.CursorRow
	m.State.CursorCol = frame.CursorCol
	m.State.CursorVisible = frame.CursorVisible
	m.State.PaneHeight = frame.Height
	m.State.PaneWidth = frame.Width
	m.State.BracketedPasteEnabled = frame.BracketedPaste
	m.State.MouseReportingEnabled = frame.Mouse.Any()
	m.modelPresentation = nextInteraction
	m.modelPresentationSet = true
	wasModelLive := m.modelLive
	m.modelLive = true
	m.fallbackEstablished = false
	m.consecutiveRecoveryBlanks = 0
	if !wasModelLive {
		m.State.PollGeneration++ // reject every provisional capture/timer
	}
	return bufferChanged || stateChanged
}

// applySnapshot adopts one control-mode capture as presentation. The snapshot
// counted its own rows while it still had them as a line slice, so the split it
// carries survives being joined into a string — where a blank final pane row is
// indistinguishable from a trailing terminator (td-d29821).
func (m *Model) applySnapshot(s ControlSnapshot) {
	changed := m.State.OutputBuf.ApplySnapshot(PaneSnapshot{
		Output:      s.Output,
		BaseLine:    s.CaptureBase,
		Absolute:    s.HasHistory,
		HistoryRows: s.HistoryRows,
		PaneRows:    s.PaneRows,
	})
	if s.HasHistory {
		m.history = HistoryInfo{HistorySize: s.HistorySize, CaptureBase: s.CaptureBase, HasHistory: true}
	} else {
		m.history = HistoryInfo{}
	}
	m.State.CursorRow, m.State.CursorCol, m.State.CursorVisible = s.CursorRow, s.CursorCol, s.CursorVisible
	m.State.PaneHeight, m.State.PaneWidth = s.PaneHeight, s.PaneWidth
	m.State.MouseReportingEnabled = s.MouseReporting
	if changed {
		m.State.BracketedPasteEnabled = DetectBracketedPasteMode(s.Output)
	}
}

func terminalOutputBlank(output string) bool {
	return strings.TrimSpace(ansi.Strip(output)) == ""
}

// preserveRecoveryBlank rejects a transient blank presentation candidate while
// a known-good fallback is being established or remains authoritative. The
// counter is consecutive across capture, snapshot, and model sources: every
// accepted nonblank candidate starts a fresh budget.
func (m *Model) preserveRecoveryBlank(output string) bool {
	if !terminalOutputBlank(output) {
		m.consecutiveRecoveryBlanks = 0
		return false
	}
	if (m.recoveryPending || m.fallbackEstablished) &&
		!terminalOutputBlank(m.State.OutputBuf.String()) &&
		m.consecutiveRecoveryBlanks < terminalRecoveryBlankLimit {
		m.consecutiveRecoveryBlanks++
		return true
	}
	return false
}

// SetVisible controls transport activity without destroying the target.
func (m *Model) SetVisible(visible bool) tea.Cmd {
	if m.visible == visible {
		if visible && m.IsActive() && m.subscription == nil {
			m.startControl()
			return m.schedulePoll(0)
		}
		return nil
	}
	m.visible = visible
	if !m.IsActive() || !visible {
		if m.State != nil {
			m.State.PollGeneration++
		}
		m.modelLive = false
		m.recoveryPending = false
		m.fallbackEstablished = false
		m.consecutiveRecoveryBlanks = 0
		m.stopControl()
		return nil
	}
	if m.subscription == nil {
		m.startControl()
	}
	return m.schedulePoll(0)
}

// SetFocused updates focus policy for the current target.
func (m *Model) SetFocused(focused bool) {
	m.focused = focused
	if m.subscription != nil {
		m.subscription.SetFocused(focused)
	}
}

// Resize is the terminal-surface spelling of SetDimensions.
func (m *Model) Resize(width, height int) tea.Cmd { return m.SetDimensions(width, height) }
