package tty

import (
	"fmt"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
)

// The callback side of ControlManager is intentionally hidden here. The
// manager's ordered actor must never wait for Bubble Tea, and Bubble Tea must
// be the only goroutine mutating Model state.
const terminalMailboxCapacity = 32

type terminalMailbox struct {
	events      chan terminalControlEvent
	overflowGen atomic.Uint64
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
}

type terminalCaptureSource interface {
	Capture(target string, scrollback int) (output string, row, col, height, width int, visible bool, err error)
}

type defaultTerminalCaptureSource struct{}

// terminalRecoveryBlankLimit covers the short attach/geometry redraw window
// observed during control replacement (about 400ms of fast captures, or 2s of
// replacement seed attempts) while bounding how long a genuinely cleared pane
// can retain its previous presentation.
const terminalRecoveryBlankLimit = 8

func (defaultTerminalCaptureSource) Capture(target string, scrollback int) (string, int, int, int, int, bool, error) {
	output, err := CapturePaneOutput(target, scrollback)
	if err != nil {
		return "", 0, 0, 0, 0, false, err
	}
	row, col, height, width, visible, _ := QueryCursorPositionSync(target)
	return output, row, col, height, width, visible, nil
}

type defaultTerminalInputSender struct{}

func (defaultTerminalInputSender) SendKeys(scope MessageScope, target string, keys ...KeySpec) tea.Cmd {
	return SendKeysCmd(scope, target, keys...)
}

func (defaultTerminalInputSender) SendPaste(scope MessageScope, target, text string) tea.Cmd {
	return SendPasteInputCmd(scope, target, text)
}

func (defaultTerminalInputSender) SendEscapePaste(scope MessageScope, target, text string) tea.Cmd {
	return func() tea.Msg {
		if err := SendKeyToTmux(target, "Escape"); err != nil && IsSessionDeadError(err) {
			return SessionDeadMsg{Scope: scope}
		}
		if err := SendPasteToTmux(target, text); err != nil && IsSessionDeadError(err) {
			return SessionDeadMsg{Scope: scope}
		}
		return nil
	}
}

func (defaultTerminalInputSender) PasteClipboard(scope MessageScope, target string) tea.Cmd {
	return PasteClipboardToTmuxCmd(scope, target)
}

func (defaultTerminalInputSender) SendMouse(scope MessageScope, target string, col, row int) tea.Cmd {
	return func() tea.Msg {
		if err := SendSGRMouse(target, 0, col, row, false); err != nil {
			if IsSessionDeadError(err) {
				return SessionDeadMsg{Scope: scope}
			}
			return nil
		}
		if err := SendSGRMouse(target, 0, col, row, true); err != nil && IsSessionDeadError(err) {
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

func resolvePaneCmd(scope MessageScope, target string) tea.Cmd {
	return func() tea.Msg {
		if target == "" {
			return paneResolvedMsg{Scope: scope, Err: fmt.Errorf("tmux terminal: empty target")}
		}
		out, err := exec.Command("tmux", "display-message", "-p", "-t", target, "#{pane_id}").Output()
		pane := strings.TrimSpace(string(out))
		if err == nil && !controlPanePattern.MatchString(pane) {
			err = fmt.Errorf("tmux terminal: invalid resolved pane %q", pane)
		}
		return paneResolvedMsg{Scope: scope, Pane: pane, Err: err}
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
		m.recoveryBlankCaptures = 0
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
		if m.fallbackEstablished && terminalOutputBlank(output) && !terminalOutputBlank(m.State.OutputBuf.String()) && m.recoveryBlankCaptures < terminalRecoveryBlankLimit {
			// A replacement control client can capture the alternate screen during
			// its attach/geometry redraw window. Keep the known-good subprocess
			// fallback visible and ask for another clean seed rather than allowing
			// a transient blank bootstrap to reclaim presentation authority. The
			// bound lets a terminal that was genuinely cleared eventually win.
			m.recoveryBlankCaptures++
			m.stopControl()
			cmd = m.retryControl()
			break
		}
		if frame.Frame.HasHistory {
			m.State.OutputBuf.UpdateSnapshot(output, frame.Frame.CaptureBase)
			m.history = HistoryInfo{
				HistorySize: frame.Frame.HistorySize,
				CaptureBase: frame.Frame.CaptureBase,
				HasHistory:  true,
			}
		} else {
			m.State.OutputBuf.Update(output)
			m.history = HistoryInfo{}
		}
		m.State.CursorRow = frame.Frame.CursorRow
		m.State.CursorCol = frame.Frame.CursorCol
		m.State.CursorVisible = frame.Frame.CursorVisible
		m.State.PaneHeight = frame.Frame.Height
		m.State.PaneWidth = frame.Frame.Width
		m.State.BracketedPasteEnabled = frame.Frame.BracketedPaste
		m.State.MouseReportingEnabled = frame.Frame.Mouse.Any()
		if !m.modelLive {
			m.modelLive = true
			m.fallbackEstablished = false
			m.recoveryBlankCaptures = 0
			m.State.PollGeneration++ // reject every provisional capture/timer
		}
	case terminalSnapshotEvent:
		if !m.modelLive && !m.fallbackEstablished {
			s := msg.Event.snapshot
			m.applyOutput(s.Output, s.CursorRow, s.CursorCol, s.CursorVisible, s.PaneHeight, s.PaneWidth, s.MouseReporting,
				s.HasHistory, s.CaptureBase, s.HistorySize)
		}
	case terminalInvalidEvent:
		m.modelLive = false
		if msg.Event.invalid.Terminal {
			m.stopControl()
			m.recoveryPending = true
			m.recoveryBlankCaptures = 0
			cmd = m.schedulePoll(0)
			break
		}
		cmd = m.schedulePoll(0)
	case terminalFallbackEvent:
		m.modelLive = false
		m.stopControl()
		m.recoveryPending = true
		m.recoveryBlankCaptures = 0
		cmd = m.schedulePoll(0)
	}
	return tea.Batch(cmd, m.listenControl())
}

func (m *Model) applyOutput(output string, row, col int, visible bool, height, width int, mouse, hasHistory bool, captureBase, historySize int) {
	var changed bool
	if hasHistory {
		changed = m.State.OutputBuf.UpdateSnapshot(output, captureBase)
		m.history = HistoryInfo{HistorySize: historySize, CaptureBase: captureBase, HasHistory: true}
	} else {
		changed = m.State.OutputBuf.Update(output)
		m.history = HistoryInfo{}
	}
	m.State.CursorRow, m.State.CursorCol, m.State.CursorVisible = row, col, visible
	m.State.PaneHeight, m.State.PaneWidth = height, width
	m.State.MouseReportingEnabled = mouse
	if changed {
		m.State.BracketedPasteEnabled = DetectBracketedPasteMode(output)
	}
}

func terminalOutputBlank(output string) bool {
	return strings.TrimSpace(ansi.Strip(output)) == ""
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
		m.recoveryBlankCaptures = 0
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
