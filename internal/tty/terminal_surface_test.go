package tty

import (
	"errors"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

type fakeTerminalControlSource struct {
	requests []ControlRequest
	handles  []*fakeTerminalControlSubscription
	err      error
}

func (f *fakeTerminalControlSource) Subscribe(request ControlRequest) (terminalControlSubscription, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return nil, f.err
	}
	h := &fakeTerminalControlSubscription{}
	f.handles = append(f.handles, h)
	return h, nil
}

type fakeTerminalControlSubscription struct {
	visible []bool
	focused []bool
	resizes [][2]int
	closed  int
}

func (f *fakeTerminalControlSubscription) SetVisible(v bool) { f.visible = append(f.visible, v) }
func (f *fakeTerminalControlSubscription) SetFocused(v bool) { f.focused = append(f.focused, v) }
func (f *fakeTerminalControlSubscription) Resize(w, h int) {
	f.resizes = append(f.resizes, [2]int{w, h})
}
func (f *fakeTerminalControlSubscription) Close() { f.closed++ }

type fakeTerminalInputCall struct {
	kind    string
	target  string
	keys    []KeySpec
	text    string
	up      bool
	col     int
	row     int
	notches int
}

type fakeTerminalInputSender struct{ calls []fakeTerminalInputCall }

func (f *fakeTerminalInputSender) record(call fakeTerminalInputCall) tea.Cmd {
	f.calls = append(f.calls, call)
	return nil
}

func (f *fakeTerminalInputSender) SendKeys(_ MessageScope, target string, keys ...KeySpec) tea.Cmd {
	return f.record(fakeTerminalInputCall{kind: "keys", target: target, keys: keys})
}
func (f *fakeTerminalInputSender) SendPaste(_ MessageScope, target, text string) tea.Cmd {
	return f.record(fakeTerminalInputCall{kind: "paste", target: target, text: text})
}
func (f *fakeTerminalInputSender) SendEscapePaste(_ MessageScope, target, text string) tea.Cmd {
	return f.record(fakeTerminalInputCall{kind: "escape-paste", target: target, text: text})
}
func (f *fakeTerminalInputSender) PasteClipboard(_ MessageScope, target string) tea.Cmd {
	return f.record(fakeTerminalInputCall{kind: "clipboard", target: target})
}
func (f *fakeTerminalInputSender) SendMouse(_ MessageScope, target string, col, row int) tea.Cmd {
	return f.record(fakeTerminalInputCall{kind: "mouse", target: target})
}
func (f *fakeTerminalInputSender) SendWheel(_ MessageScope, target string, up bool, col, row, notches int) tea.Cmd {
	return f.record(fakeTerminalInputCall{kind: "wheel", target: target, up: up, col: col, row: row, notches: notches})
}

func newContractTerminal(source terminalControlSource) *Model {
	m := New(nil)
	m.control = source
	return m
}

func deliverTerminalEvent(t *testing.T, m *Model) terminalControlMsg {
	t.Helper()
	cmd := m.listenControl()
	if cmd == nil {
		t.Fatal("terminal has no mailbox listener")
	}
	msg, ok := cmd().(terminalControlMsg)
	if !ok {
		t.Fatalf("listener returned %T, want terminalControlMsg", msg)
	}
	m.Update(msg)
	return msg
}

func seededFrame(session, pane, output string) ModelFrame {
	return seededFrameAt(session, pane, output, 80, 24)
}

func seededFrameAt(session, pane, output string, width, height int) ModelFrame {
	return ModelFrame{
		Session: session, Pane: pane, Seeds: 1,
		Frame: screenmodel.Frame{
			Output: output, Width: width, Height: height,
			CursorRow: 2, CursorCol: 3, CursorVisible: true,
			BracketedPaste: true,
			Mouse:          screenmodel.MouseState{Normal: true},
		},
	}
}

func seededHistoryFrame(session, pane, output string, captureBase, historySize int) ModelFrame {
	frame := seededFrame(session, pane, output)
	frame.Frame.HasHistory = true
	frame.Frame.CaptureBase = captureBase
	frame.Frame.HistorySize = historySize
	return frame
}

func TestTerminalContractSeededModelOwnsHealthySteadyState(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%1"})
	if len(source.requests) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(source.requests))
	}
	request := source.requests[0]
	if !request.ModelPresentation || request.OnModelFrame == nil || request.OnFallback == nil {
		t.Fatal("shared terminal did not request the complete model/fallback contract")
	}

	pollGeneration := m.State.PollGeneration
	request.OnModelFrame(seededFrame("editor", "%1", "seeded"))
	deliverTerminalEvent(t, m)
	if !m.modelLive || m.State.OutputBuf.String() != "seeded" {
		t.Fatalf("seeded frame did not own presentation: live=%v output=%q", m.modelLive, m.State.OutputBuf.String())
	}
	if m.State.PollGeneration <= pollGeneration {
		t.Fatal("first seed did not invalidate provisional polling")
	}
	liveGeneration := m.State.PollGeneration
	if cmd := m.schedulePoll(0); cmd != nil || m.State.PollGeneration != liveGeneration {
		t.Fatal("healthy model state scheduled capture polling")
	}

	request.OnModelFrame(seededFrame("editor", "%1", "next burst"))
	deliverTerminalEvent(t, m)
	if got := m.State.OutputBuf.String(); got != "next burst" {
		t.Fatalf("steady-state frame output = %q", got)
	}
	if m.State.PollGeneration != liveGeneration {
		t.Fatal("steady-state byte burst changed poll generation")
	}
	if !m.State.BracketedPasteEnabled || !m.State.MouseReportingEnabled {
		t.Fatal("model modes were not applied")
	}
}

func TestTerminalContractModelHistoryPreservesAbsoluteCoordinatesAndOverlap(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%14"})
	request := source.requests[0]

	request.OnModelFrame(seededHistoryFrame("editor", "%14", "line40\nline41\nlive42", 40, 100))
	deliverTerminalEvent(t, m)
	if got := m.History(); got != (HistoryInfo{
		HistorySize: 100, CaptureBase: 40, LoadedStart: 40, LoadedEnd: 43, HasHistory: true,
	}) {
		t.Fatalf("initial history = %+v", got)
	}
	if got := m.LinesAbsoluteRange(40, 43); !reflect.DeepEqual(got, []string{"line40", "line41", "live42"}) {
		t.Fatalf("initial absolute lines = %#v", got)
	}

	if !m.PrependHistory("line38\nline39\nstale-line40", 38) {
		t.Fatal("overlapping older history was not prepended")
	}
	if got := m.LinesAbsoluteRange(38, 43); !reflect.DeepEqual(got,
		[]string{"line38", "line39", "line40", "line41", "live42"}) {
		t.Fatalf("prepended absolute lines = %#v", got)
	}

	request.OnModelFrame(seededHistoryFrame("editor", "%14", "line41 changed\nlive42 changed\nlive43", 41, 101))
	deliverTerminalEvent(t, m)
	if got := m.History(); got != (HistoryInfo{
		HistorySize: 101, CaptureBase: 41, LoadedStart: 38, LoadedEnd: 44, HasHistory: true,
	}) {
		t.Fatalf("updated history = %+v", got)
	}
	if got := m.LinesAbsoluteRange(38, 44); !reflect.DeepEqual(got,
		[]string{"line38", "line39", "line40", "line41 changed", "live42 changed", "live43"}) {
		t.Fatalf("updated absolute lines = %#v", got)
	}
}

func TestTerminalContractProvisionalSnapshotRetainsHistoryMetadata(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%15"})
	request := source.requests[0]
	request.OnSnapshot(ControlSnapshot{
		Session: "editor", Pane: "%15", Output: "history10\nlive11",
		CaptureBase: 10, HistorySize: 50, HasHistory: true,
		PaneWidth: 80, PaneHeight: 24,
	})
	deliverTerminalEvent(t, m)
	if m.modelLive {
		t.Fatal("provisional capture incorrectly gained model presentation")
	}
	if got := m.History(); got != (HistoryInfo{
		HistorySize: 50, CaptureBase: 10, LoadedStart: 10, LoadedEnd: 12, HasHistory: true,
	}) {
		t.Fatalf("provisional history = %+v", got)
	}
}

func TestTerminalContractOpenCloseReopenAndTargetSwitchRejectStaleDelivery(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "one", Pane: "%1"})
	first := source.requests[0]
	firstScope, firstGen := m.Scope(), m.controlGen
	m.recoveryPending = true
	m.fallbackEstablished = true
	m.consecutiveRecoveryBlanks = 3
	m.Close()
	if source.handles[0].closed != 1 || m.IsActive() {
		t.Fatal("close did not release the first activation")
	}
	if m.recoveryPending || m.fallbackEstablished || m.consecutiveRecoveryBlanks != 0 {
		t.Fatalf("close retained recovery authority: pending=%v fallback=%v blanks=%d",
			m.recoveryPending, m.fallbackEstablished, m.consecutiveRecoveryBlanks)
	}

	m.Open(Target{Session: "two", Pane: "%2"})
	second := source.requests[1]
	first.OnModelFrame(seededFrame("one", "%1", "stale"))
	m.Update(terminalControlMsg{
		Scope: firstScope,
		Event: terminalControlEvent{kind: terminalFrameEvent, frame: seededFrame("one", "%1", "stale"), gen: firstGen},
	})
	if got := m.State.OutputBuf.String(); got != "" {
		t.Fatalf("stale activation changed new target: %q", got)
	}
	second.OnModelFrame(seededFrame("two", "%2", "current"))
	deliverTerminalEvent(t, m)
	if got := m.State.OutputBuf.String(); got != "current" {
		t.Fatalf("current activation output = %q", got)
	}
}

func TestTerminalContractResolvedSessionTargetKeepsActivationScope(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor"})
	scope := m.Scope()
	m.Update(paneResolvedMsg{Scope: scope, Pane: "%9"})
	if m.Scope() != scope {
		t.Fatalf("pane resolution changed activation scope: got %+v want %+v", m.Scope(), scope)
	}
	if len(source.requests) != 1 || source.requests[0].Pane != "%9" {
		t.Fatalf("resolved subscription = %+v", source.requests)
	}
	source.requests[0].OnModelFrame(seededFrame("editor", "%9", "resolved"))
	deliverTerminalEvent(t, m)
	if got := m.State.OutputBuf.String(); got != "resolved" {
		t.Fatalf("resolved target output = %q", got)
	}
}

func TestTerminalContractSessionDeathClosesActivation(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%7"})
	m.Update(SessionDeadMsg{Scope: m.Scope()})
	if m.IsActive() || source.handles[0].closed != 1 {
		t.Fatalf("session death did not close terminal: active=%v closed=%d", m.IsActive(), source.handles[0].closed)
	}
}

func TestTerminalContractVisibilityFocusAndResize(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%3"})
	h := source.handles[0]

	m.SetFocused(false)
	m.SetVisible(false)
	if m.modelLive {
		t.Fatal("hidden terminal retained model presentation")
	}
	m.SetVisible(true)
	if h.closed != 1 || len(source.requests) != 2 {
		t.Fatalf("visibility cycle did not replace subscription: closed=%d requests=%d", h.closed, len(source.requests))
	}
	active := source.handles[1]
	source.requests[1].OnModelFrame(seededFrame("editor", "%3", "before resize"))
	deliverTerminalEvent(t, m)
	if !m.modelLive {
		t.Fatal("seed did not establish model presentation before resize")
	}
	m.Resize(100, 40)
	if m.modelLive {
		t.Fatal("resize did not restore provisional capture presentation")
	}
	if len(h.focused) != 1 || h.focused[0] {
		t.Fatalf("focus forwarding mismatch: focused=%v", h.focused)
	}
	if active.closed != 1 || len(source.requests) != 3 {
		t.Fatalf("resize did not replace subscription: closed=%d requests=%d", active.closed, len(source.requests))
	}
	if got := source.requests[2]; got.Width != 100 || got.Height != 40 {
		t.Fatalf("replacement geometry = %dx%d", got.Width, got.Height)
	}
}

func TestTerminalResizeCommandRevalidatesActivationAfterClose(t *testing.T) {
	m := New(nil)
	m.Open(Target{Session: "editor", Pane: "%93"})
	cmd := m.SetDimensions(100, 40)
	if cmd == nil {
		t.Fatal("active terminal did not construct resize command")
	}
	m.Close()
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		if _, resized := msg.(PaneResizedMsg); resized {
			t.Fatal("stale resize ran after Close")
		}
		return
	}
	for _, child := range batch {
		if _, resized := child().(PaneResizedMsg); resized {
			t.Fatal("stale resize ran after Close")
		}
	}
}

func TestTerminalCloseDrainsPaneResolutionAndRejectsQueuedStaleResolve(t *testing.T) {
	originalResolve := terminalResolvePane
	started := make(chan struct{})
	release := make(chan struct{})
	var resolves atomic.Int32
	terminalResolvePane = func(string) (string, error) {
		if resolves.Add(1) == 1 {
			close(started)
			<-release
		}
		return "%97", nil
	}
	t.Cleanup(func() { terminalResolvePane = originalResolve })

	m := New(nil)
	m.Open(Target{Session: "resolve-session"})
	scope := m.Scope()
	inFlight := m.resolvePaneCmd(scope, "resolve-session")
	stale := m.resolvePaneCmd(scope, "resolve-session")
	resolveDone := make(chan tea.Msg, 1)
	go func() { resolveDone <- inFlight() }()
	<-started
	originalBeforeClose := terminalBeforeClose
	closeEntered := make(chan struct{})
	terminalBeforeClose = func() { close(closeEntered) }
	t.Cleanup(func() { terminalBeforeClose = originalBeforeClose })
	closed := make(chan struct{})
	go func() {
		m.Close()
		close(closed)
	}()
	<-closeEntered
	select {
	case <-closed:
		t.Fatal("Close returned while pane resolution held activation")
	default:
	}
	close(release)
	select {
	case <-resolveDone:
	case <-time.After(time.Second):
		t.Fatal("pane resolution did not drain")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after pane resolution drained")
	}
	if msg := stale(); msg != nil {
		t.Fatalf("stale pane resolution returned %T after Close", msg)
	}
	if resolves.Load() != 1 {
		t.Fatalf("stale pane resolution began after Close: resolves=%d", resolves.Load())
	}
}

func TestTerminalCloseDrainsInFlightResizeAndExcludesStaleResize(t *testing.T) {
	originalQuery := terminalQueryPaneSize
	originalResize := terminalResizePane
	started := make(chan struct{})
	release := make(chan struct{})
	var queries atomic.Int32
	var resizes atomic.Int32
	terminalQueryPaneSize = func(string) (int, int, bool) {
		if queries.Add(1) == 1 {
			close(started)
			<-release
		}
		return 1, 1, true
	}
	terminalResizePane = func(string, int, int) { resizes.Add(1) }
	t.Cleanup(func() {
		terminalQueryPaneSize = originalQuery
		terminalResizePane = originalResize
	})

	m := New(nil)
	m.SetResizeDebounce(0)
	m.Open(Target{Session: "editor", Pane: "%93-resize-drain"})
	// The control replacement is not part of this lease proof; leave only the
	// direct geometry command so its effect boundary is deterministic.
	m.subscription = nil
	inFlight := m.SetDimensions(100, 40)
	stale := m.SetDimensions(101, 41)
	if inFlight == nil || stale == nil {
		t.Fatal("active terminal did not construct resize commands")
	}
	resizeDone := make(chan tea.Msg, 1)
	go func() { resizeDone <- inFlight() }()
	<-started
	originalBeforeClose := terminalBeforeClose
	closeEntered := make(chan struct{})
	terminalBeforeClose = func() { close(closeEntered) }
	t.Cleanup(func() { terminalBeforeClose = originalBeforeClose })
	closed := make(chan struct{})
	go func() {
		m.Close()
		close(closed)
	}()
	<-closeEntered
	select {
	case <-closed:
		t.Fatal("Close returned while an old-scope resize effect was in flight")
	default:
	}
	close(release)
	select {
	case <-resizeDone:
	case <-time.After(time.Second):
		t.Fatal("in-flight resize did not drain")
	}
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after resize drained")
	}
	if got := stale(); got != nil {
		t.Fatalf("stale resize returned %T after Close", got)
	}
	if queries.Load() != 1 || resizes.Load() != 1 {
		t.Fatalf("geometry effects after Close: queries=%d resizes=%d", queries.Load(), resizes.Load())
	}
}

func TestTerminalInputQueuedBeforeCloseRevalidatesInsideOrderedJob(t *testing.T) {
	WaitForPendingSends()
	t.Cleanup(WaitForPendingSends)
	originalSendKeys := terminalSendKeys
	var sends atomic.Int32
	terminalSendKeys = func(string, ...KeySpec) error {
		sends.Add(1)
		return nil
	}
	t.Cleanup(func() { terminalSendKeys = originalSendKeys })

	target := "%94-queued-close"
	blockerEntered := make(chan struct{})
	releaseBlocker := make(chan struct{})
	_ = SendOrdered(target, func() error {
		close(blockerEntered)
		<-releaseBlocker
		return nil
	})
	<-blockerEntered

	m := New(nil)
	m.Open(Target{Session: "editor", Pane: target})
	cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if cmd == nil {
		t.Fatal("active terminal did not construct input command")
	}
	m.Close()
	close(releaseBlocker)
	WaitForPendingSends()
	if sends.Load() != 0 {
		t.Fatalf("queued stale input began %d tmux effect(s) after Close", sends.Load())
	}
}

func TestTerminalCloseDrainsInFlightInputAndExcludesOldScopeAfterReturn(t *testing.T) {
	WaitForPendingSends()
	t.Cleanup(WaitForPendingSends)
	originalSendKeys := terminalSendKeys
	started := make(chan struct{})
	release := make(chan struct{})
	var sends atomic.Int32
	terminalSendKeys = func(string, ...KeySpec) error {
		if sends.Add(1) == 1 {
			close(started)
			<-release
		}
		return nil
	}
	t.Cleanup(func() { terminalSendKeys = originalSendKeys })

	target := "%95-in-flight-close"
	m := New(nil)
	m.Open(Target{Session: "editor", Pane: target})
	oldScope := m.Scope()
	if cmd := m.Update(tea.KeyPressMsg{Code: 'x', Text: "x"}); cmd == nil {
		t.Fatal("active terminal did not enqueue input")
	}
	<-started
	originalBeforeClose := terminalBeforeClose
	closeEntered := make(chan struct{})
	terminalBeforeClose = func() { close(closeEntered) }
	t.Cleanup(func() { terminalBeforeClose = originalBeforeClose })
	closed := make(chan struct{})
	go func() {
		m.Close()
		close(closed)
	}()
	<-closeEntered
	select {
	case <-closed:
		t.Fatal("Close returned while an old-scope input effect was in flight")
	default:
	}
	close(release)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after the input effect drained")
	}

	// A sender already holding the old scope may still enqueue after Close, but
	// the ordered job must fail its lease check before the tmux effect begins.
	_ = defaultTerminalInputSender{model: m}.SendKeys(oldScope, target, KeySpec{"y", true})
	WaitForPendingSends()
	if sends.Load() != 1 {
		t.Fatalf("old scope began a tmux effect after Close returned: sends=%d", sends.Load())
	}
}

func TestTerminalOldScopeSkipsEveryQueuedInputEffect(t *testing.T) {
	WaitForPendingSends()
	t.Cleanup(WaitForPendingSends)
	originalPaste := terminalSendPaste
	originalKey := terminalSendKey
	originalRawPaste := terminalSendPasteRaw
	originalMouse := terminalSendMouse
	originalWheel := terminalSendWheel
	originalClipboard := terminalReadClipboard
	var effects atomic.Int32
	terminalSendPaste = func(string, string) error { effects.Add(1); return nil }
	terminalSendKey = func(string, string) error { effects.Add(1); return nil }
	terminalSendPasteRaw = func(string, string) error { effects.Add(1); return nil }
	terminalSendMouse = func(string, int, int, int, bool) error { effects.Add(1); return nil }
	terminalSendWheel = func(string, bool, int, int, int) error { effects.Add(1); return nil }
	terminalReadClipboard = func() (string, error) { effects.Add(1); return "clipboard", nil }
	t.Cleanup(func() {
		terminalSendPaste = originalPaste
		terminalSendKey = originalKey
		terminalSendPasteRaw = originalRawPaste
		terminalSendMouse = originalMouse
		terminalSendWheel = originalWheel
		terminalReadClipboard = originalClipboard
	})

	m := New(nil)
	target := "%96-all-input"
	m.Open(Target{Session: "editor", Pane: target})
	oldScope := m.Scope()
	m.Close()
	sender := defaultTerminalInputSender{model: m}
	_ = sender.SendPaste(oldScope, target, "paste")
	_ = sender.SendEscapePaste(oldScope, target, "escape paste")
	_ = sender.PasteClipboard(oldScope, target)
	_ = sender.SendMouse(oldScope, target, 2, 3)
	_ = sender.SendWheel(oldScope, target, true, 2, 3, 1)
	WaitForPendingSends()
	if effects.Load() != 0 {
		t.Fatalf("old scope reached %d paste/clipboard/mouse effects", effects.Load())
	}
}

func TestTerminalContractQueuedPreResizeFrameIsRejectedUntilNewSeed(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%12"})
	first := source.requests[0]
	first.OnModelFrame(seededFrameAt("editor", "%12", "live 80x24", 80, 24))
	deliverTerminalEvent(t, m)
	if !m.modelLive || m.State.PaneWidth != 80 || m.State.PaneHeight != 24 {
		t.Fatalf("initial seed = live %v geometry %dx%d", m.modelLive, m.State.PaneWidth, m.State.PaneHeight)
	}

	first.OnModelFrame(seededFrameAt("editor", "%12", "queued old geometry", 80, 24))
	pollGeneration := m.State.PollGeneration
	resizeCmd := m.Resize(100, 40)
	if m.modelLive || len(source.requests) != 2 || source.handles[0].closed != 1 {
		t.Fatalf("resize boundary = live %v requests %d closed %d", m.modelLive, len(source.requests), source.handles[0].closed)
	}
	if m.State.PollGeneration != pollGeneration+1 {
		t.Fatalf("resize poll generation = %d, want %d", m.State.PollGeneration, pollGeneration+1)
	}
	if resizeCmd == nil {
		t.Fatal("resize did not retain a provisional capture command")
	}

	deliverTerminalEvent(t, m)
	if m.modelLive || m.State.OutputBuf.String() != "live 80x24" {
		t.Fatalf("pre-resize frame restored presentation: live=%v output=%q", m.modelLive, m.State.OutputBuf.String())
	}
	second := source.requests[1]
	if second.Width != 100 || second.Height != 40 {
		t.Fatalf("post-resize request geometry = %dx%d", second.Width, second.Height)
	}
	second.OnModelFrame(seededFrameAt("editor", "%12", "live 100x40", 100, 40))
	deliverTerminalEvent(t, m)
	if !m.modelLive || m.State.OutputBuf.String() != "live 100x40" || m.State.PaneWidth != 100 || m.State.PaneHeight != 40 {
		t.Fatalf("post-resize seed = live %v output %q geometry %dx%d", m.modelLive, m.State.OutputBuf.String(), m.State.PaneWidth, m.State.PaneHeight)
	}
}

func TestTerminalContractImmediateResizeAlsoReplacesGeneration(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%13"})
	first := source.requests[0]
	first.OnModelFrame(seededFrameAt("editor", "%13", "initial", 80, 24))
	deliverTerminalEvent(t, m)
	first.OnModelFrame(seededFrameAt("editor", "%13", "stale", 80, 24))
	pollGeneration := m.State.PollGeneration
	resizedAt := m.State.LastResizeAt
	cmd := m.ResizeAndPollImmediate(120, 50)
	if cmd == nil || len(source.requests) != 2 || source.handles[0].closed != 1 || m.modelLive {
		t.Fatalf("immediate resize boundary failed: cmd=%v requests=%d closed=%d live=%v", cmd != nil, len(source.requests), source.handles[0].closed, m.modelLive)
	}
	if m.State.PollGeneration != pollGeneration+1 {
		t.Fatalf("immediate resize poll generation = %d, want %d", m.State.PollGeneration, pollGeneration+1)
	}
	// The boundary is half the contract. The other half is the geometry itself:
	// tmux takes a new window size only from resize-window, so a restart alone
	// leaves the pane letterboxed at the size it already had.
	if !m.State.LastResizeAt.After(resizedAt) {
		t.Fatal("the immediate resize replaced the transport without giving the pane its size")
	}
	deliverTerminalEvent(t, m)
	if got := m.State.OutputBuf.String(); got != "initial" || m.modelLive {
		t.Fatalf("immediate resize accepted stale frame: output=%q live=%v", got, m.modelLive)
	}
}

func TestTerminalContractQueuedPreHideFrameIsRejectedAndShowReseeds(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%10"})
	first := source.requests[0]
	first.OnModelFrame(seededFrame("editor", "%10", "queued before hide"))
	m.SetVisible(false)
	deliverTerminalEvent(t, m)
	if got := m.State.OutputBuf.String(); got != "" || m.modelLive {
		t.Fatalf("hidden queued frame applied: output=%q live=%v", got, m.modelLive)
	}

	m.SetVisible(true)
	if len(source.requests) != 2 || m.modelLive {
		t.Fatalf("show did not restart provisional control: requests=%d live=%v", len(source.requests), m.modelLive)
	}
	second := source.requests[1]
	second.OnModelFrame(seededFrame("editor", "%10", "reseeded after show"))
	deliverTerminalEvent(t, m)
	if got := m.State.OutputBuf.String(); got != "reseeded after show" || !m.modelLive {
		t.Fatalf("show reseed failed: output=%q live=%v", got, m.modelLive)
	}
}

func TestTerminalContractHiddenFallbackDoesNotStrandControl(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%11"})
	first := source.requests[0]
	first.OnFallback(errors.New("queued EOF"))
	m.SetVisible(false)
	deliverTerminalEvent(t, m)
	if len(source.requests) != 1 || m.subscription != nil {
		t.Fatalf("hidden fallback unexpectedly restarted control: requests=%d subscription=%v", len(source.requests), m.subscription != nil)
	}

	m.SetVisible(true)
	if len(source.requests) != 2 || m.subscription == nil {
		t.Fatalf("show left control stranded: requests=%d subscription=%v", len(source.requests), m.subscription != nil)
	}
	source.requests[1].OnModelFrame(seededFrame("editor", "%11", "recovered"))
	deliverTerminalEvent(t, m)
	if got := m.State.OutputBuf.String(); got != "recovered" || !m.modelLive {
		t.Fatalf("hidden fallback recovery failed: output=%q live=%v", got, m.modelLive)
	}
}

func TestTerminalContractAllInputTargetsDisplayedPane(t *testing.T) {
	source := &fakeTerminalControlSource{}
	input := &fakeTerminalInputSender{}
	m := newContractTerminal(source)
	m.input = input
	m.Open(Target{Session: "multi-pane-session", Pane: "%9"})

	m.Update(tea.KeyPressMsg{Code: 'a', Text: "a"})
	m.Update(tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt})
	m.Update(tea.PasteMsg{Content: "paste message"})
	m.State.EscapePressed = true
	m.Update(tea.KeyPressMsg{Code: 'b', Text: "b"})
	m.State.EscapePressed = true
	m.Update(tea.KeyPressMsg{Code: 'x', Text: "a long pasted value"})
	m.State.EscapePressed = true
	m.State.EscapeTimerPending = true
	m.Update(EscapeTimerMsg{Scope: m.Scope()})
	m.State.MouseReportingEnabled = true
	m.SendClick(3, 4)

	if len(input.calls) != 7 {
		t.Fatalf("input calls = %#v", input.calls)
	}
	wantKinds := []string{"keys", "clipboard", "paste", "keys", "escape-paste", "keys", "mouse"}
	for i, call := range input.calls {
		if call.target != "%9" {
			t.Errorf("%s targeted %q, want displayed pane %%9", call.kind, call.target)
		}
		if call.kind != wantKinds[i] {
			t.Errorf("input call %d kind = %q, want %q", i, call.kind, wantKinds[i])
		}
	}
	if got := input.calls[3]; got.kind != "keys" || len(got.keys) != 2 || got.keys[0].Value != "Escape" {
		t.Errorf("pending escape key call = %#v", got)
	}
	if got := input.calls[4]; got.kind != "escape-paste" {
		t.Errorf("pending escape paste call = %#v", got)
	}
}

func TestTerminalContractControlDeathFallsBackAndRecovers(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%4"})
	first := source.requests[0]
	first.OnModelFrame(seededFrame("editor", "%4", "live"))
	deliverTerminalEvent(t, m)

	first.OnFallback(errors.New("control EOF"))
	deliverTerminalEvent(t, m)
	if m.modelLive || len(source.requests) != 1 || source.handles[0].closed != 1 {
		t.Fatalf("fallback did not release control: live=%v requests=%d closed=%d", m.modelLive, len(source.requests), source.handles[0].closed)
	}
	if !m.recoveryPending {
		t.Fatal("control retry was not held behind fallback capture")
	}
	m.Update(CaptureResultMsg{
		Scope: m.Scope(), PollGeneration: m.State.PollGeneration, Target: "%4",
		Output: "", CursorVisible: true, PaneWidth: 80, PaneHeight: 24,
	})
	if got := m.State.OutputBuf.String(); got != "live" || !m.recoveryPending {
		t.Fatalf("transient blank fallback replaced live content: output=%q pending=%v", got, m.recoveryPending)
	}
	m.Update(CaptureResultMsg{
		Scope: m.Scope(), PollGeneration: m.State.PollGeneration, Target: "%4",
		Output: "fallback visible", CursorVisible: true, PaneWidth: 80, PaneHeight: 24,
	})
	if got := m.State.OutputBuf.String(); got != "fallback visible" || m.recoveryPending {
		t.Fatalf("fallback capture output = %q pending=%v", got, m.recoveryPending)
	}
	if got := m.View(); !strings.Contains(got, "fallback visible") {
		t.Fatalf("fallback capture was not visible: %q", got)
	}
	m.Update(CaptureResultMsg{
		Scope: m.Scope(), PollGeneration: m.State.PollGeneration, Target: "%4",
		Output: "", CursorVisible: true, PaneWidth: 80, PaneHeight: 24,
	})
	if got := m.State.OutputBuf.String(); got != "fallback visible" || m.consecutiveRecoveryBlanks != 1 {
		t.Fatalf("post-establishment blank capture replaced fallback: output=%q blanks=%d", got, m.consecutiveRecoveryBlanks)
	}
	m.Update(CaptureResultMsg{
		Scope: m.Scope(), PollGeneration: m.State.PollGeneration, Target: "%4",
		Output: "fallback refreshed", CursorVisible: true, PaneWidth: 80, PaneHeight: 24,
	})
	if got := m.State.OutputBuf.String(); got != "fallback refreshed" || m.consecutiveRecoveryBlanks != 0 {
		t.Fatalf("nonblank fallback did not reset blank budget: output=%q blanks=%d", got, m.consecutiveRecoveryBlanks)
	}
	m.Update(terminalControlRetryMsg{Scope: m.Scope(), Gen: m.controlGen})
	if len(source.requests) != 2 {
		t.Fatalf("control retry subscriptions = %d, want 2", len(source.requests))
	}

	second := source.requests[1]
	second.OnSnapshot(ControlSnapshot{
		Session: "editor", Pane: "%4", Output: "", PaneWidth: 80, PaneHeight: 24,
	})
	deliverTerminalEvent(t, m)
	if got := m.View(); !strings.Contains(got, "fallback refreshed") || m.consecutiveRecoveryBlanks != 1 {
		t.Fatalf("blank replacement snapshot erased fallback or missed budget: view=%q blanks=%d", got, m.consecutiveRecoveryBlanks)
	}
	second.OnSnapshot(ControlSnapshot{
		Session: "editor", Pane: "%4", Output: "snapshot refreshed", PaneWidth: 80, PaneHeight: 24,
	})
	deliverTerminalEvent(t, m)
	if got := m.State.OutputBuf.String(); got != "snapshot refreshed" || m.consecutiveRecoveryBlanks != 0 {
		t.Fatalf("nonblank snapshot did not reset blank budget: output=%q blanks=%d", got, m.consecutiveRecoveryBlanks)
	}
	blank := seededFrame("editor", "%4", "")
	blank.Frame.AltScreen = true
	second.OnModelFrame(blank)
	deliverTerminalEvent(t, m)
	if source.handles[1].closed != 1 || m.modelLive {
		t.Fatalf("blank replacement seed was accepted: closed=%d live=%v", source.handles[1].closed, m.modelLive)
	}
	if got := m.View(); !strings.Contains(got, "snapshot refreshed") || m.consecutiveRecoveryBlanks != 1 {
		t.Fatalf("blank replacement seed erased fallback or missed fresh budget: view=%q blanks=%d", got, m.consecutiveRecoveryBlanks)
	}
	m.Update(terminalControlRetryMsg{Scope: m.Scope(), Gen: m.controlGen})
	if len(source.requests) != 3 {
		t.Fatalf("second seed attempt subscriptions = %d, want 3", len(source.requests))
	}

	third := source.requests[2]
	reseeded := seededFrame("editor", "%4", "snapshot refreshed")
	reseeded.Frame.AltScreen = true
	third.OnModelFrame(reseeded)
	deliverTerminalEvent(t, m)
	if !m.modelLive || m.State.OutputBuf.String() != "snapshot refreshed" || m.consecutiveRecoveryBlanks != 0 {
		t.Fatalf("fallback recovery failed: live=%v output=%q", m.modelLive, m.State.OutputBuf.String())
	}
	if got := m.View(); !strings.Contains(got, "snapshot refreshed") {
		t.Fatalf("reseeded alternate screen was not visible: %q", got)
	}
}

func TestTerminalContractBlankReplacementSeedIsBounded(t *testing.T) {
	m := New(nil)
	m.visible = true
	m.State = &State{
		Active:        true,
		TargetSession: "editor",
		TargetPane:    "%4",
		OutputBuf:     NewOutputBuffer(m.Config.ScrollbackLines),
	}
	m.scopeTarget = "%4"
	m.controlGen = 1 // zero is reserved for "no mailbox overflow" in direct deliveries
	m.State.OutputBuf.Update("known fallback")
	m.fallbackEstablished = true

	for i := 0; i < terminalRecoveryBlankLimit; i++ {
		blank := seededFrame("editor", "%4", "")
		blank.Frame.AltScreen = true
		m.handleControlDelivery(terminalControlMsg{
			Scope: m.Scope(),
			Event: terminalControlEvent{kind: terminalFrameEvent, frame: blank, gen: m.controlGen},
		})
		if got := m.State.OutputBuf.String(); got != "known fallback" {
			t.Fatalf("blank seed %d replaced fallback: %q", i+1, got)
		}
	}

	blank := seededFrame("editor", "%4", "")
	blank.Frame.AltScreen = true
	m.handleControlDelivery(terminalControlMsg{
		Scope: m.Scope(),
		Event: terminalControlEvent{kind: terminalFrameEvent, frame: blank, gen: m.controlGen},
	})
	if !m.modelLive || m.fallbackEstablished || m.State.OutputBuf.String() != "" {
		t.Fatalf("bounded blank seed did not become authoritative: live=%v fallback=%v output=%q",
			m.modelLive, m.fallbackEstablished, m.State.OutputBuf.String())
	}
}

func TestTerminalContractFallbackBlankBudgetEventuallyAcceptsClear(t *testing.T) {
	m := New(nil)
	m.visible = true
	m.State = &State{
		Active:        true,
		TargetSession: "editor",
		TargetPane:    "%4",
		OutputBuf:     NewOutputBuffer(m.Config.ScrollbackLines),
	}
	m.scopeTarget = "%4"
	m.State.OutputBuf.Update("known fallback")
	m.fallbackEstablished = true

	for i := 0; i < terminalRecoveryBlankLimit; i++ {
		m.Update(CaptureResultMsg{
			Scope: m.Scope(), PollGeneration: m.State.PollGeneration, Target: "%4",
			Output: "", CursorVisible: true, PaneWidth: 80, PaneHeight: 24,
		})
		if got := m.State.OutputBuf.String(); got != "known fallback" {
			t.Fatalf("blank capture %d replaced fallback: %q", i+1, got)
		}
	}
	m.Update(CaptureResultMsg{
		Scope: m.Scope(), PollGeneration: m.State.PollGeneration, Target: "%4",
		Output: "", CursorVisible: true, PaneWidth: 80, PaneHeight: 24,
	})
	if got := m.State.OutputBuf.String(); got != "" {
		t.Fatalf("bounded blank captures did not accept clear: %q", got)
	}
}

func TestTerminalContractMailboxPressureNeverBlocksAndForcesReseed(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%5"})
	request := source.requests[0]
	for i := 0; i < terminalMailboxCapacity+10; i++ {
		request.OnModelFrame(seededFrame("editor", "%5", "burst"))
	}
	msg := deliverTerminalEvent(t, m)
	if msg.OverflowGen == 0 {
		t.Fatal("mailbox pressure was not surfaced to Bubble Tea")
	}
	if len(source.requests) != 1 || source.handles[0].closed != 1 {
		t.Fatalf("pressure did not establish a clean subscription: requests=%d closed=%d", len(source.requests), source.handles[0].closed)
	}
	m.Update(CaptureResultMsg{
		Scope: m.Scope(), PollGeneration: m.State.PollGeneration, Target: "%5",
		Output: "pressure fallback", PaneWidth: 80, PaneHeight: 24,
	})
	m.Update(terminalControlRetryMsg{Scope: m.Scope(), Gen: m.controlGen})
	if len(source.requests) != 2 {
		t.Fatalf("pressure retry subscriptions = %d, want 2", len(source.requests))
	}
}

func TestTerminalContractModelInvalidationResumesSingleCurrentPoll(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%6"})
	request := source.requests[0]
	request.OnModelFrame(seededFrame("editor", "%6", "live"))
	deliverTerminalEvent(t, m)
	request.OnModelInvalid(ModelInvalidation{Session: "editor", Pane: "%6", Reason: ResyncPause})
	deliverTerminalEvent(t, m)
	if m.modelLive {
		t.Fatal("invalidated model retained presentation")
	}
	generation := m.State.PollGeneration
	if generation == 0 || m.schedulePoll(0) == nil || m.State.PollGeneration != generation+1 {
		t.Fatal("fallback polling did not retain one current generation")
	}
}
