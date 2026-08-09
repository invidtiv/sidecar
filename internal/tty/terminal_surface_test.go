package tty

import (
	"errors"
	"testing"

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
	return ModelFrame{
		Session: session, Pane: pane, Seeds: 1,
		Frame: screenmodel.Frame{
			Output: output, Width: 80, Height: 24,
			CursorRow: 2, CursorCol: 3, CursorVisible: true,
			BracketedPaste: true,
			Mouse:          screenmodel.MouseState{Normal: true},
		},
	}
}

func TestTerminalContractSeededModelOwnsHealthySteadyState(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "editor", Pane: "%1"})
	if len(source.requests) != 1 {
		t.Fatalf("subscriptions = %d, want 1", len(source.requests))
	}
	request := source.requests[0]
	if !request.ModelAuthority || request.OnModelFrame == nil || request.OnFallback == nil {
		t.Fatal("shared terminal did not request the complete model/fallback contract")
	}

	pollGeneration := m.State.PollGeneration
	request.OnModelFrame(seededFrame("editor", "%1", "seeded"))
	deliverTerminalEvent(t, m)
	if !m.modelLive || m.State.OutputBuf.String() != "seeded" {
		t.Fatalf("seeded frame not authoritative: live=%v output=%q", m.modelLive, m.State.OutputBuf.String())
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

func TestTerminalContractOpenCloseReopenAndTargetSwitchRejectStaleDelivery(t *testing.T) {
	source := &fakeTerminalControlSource{}
	m := newContractTerminal(source)
	m.Open(Target{Session: "one", Pane: "%1"})
	first := source.requests[0]
	firstScope, firstGen := m.Scope(), m.controlGen
	m.Close()
	if source.handles[0].closed != 1 || m.IsActive() {
		t.Fatal("close did not release the first activation")
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
		t.Fatal("hidden terminal retained model authority")
	}
	m.SetVisible(true)
	source.requests[0].OnModelFrame(seededFrame("editor", "%3", "before resize"))
	deliverTerminalEvent(t, m)
	if !m.modelLive {
		t.Fatal("seed did not establish model authority before resize")
	}
	m.Resize(100, 40)
	if m.modelLive {
		t.Fatal("resize did not restore provisional capture authority")
	}
	if len(h.focused) != 1 || h.focused[0] || len(h.visible) != 2 || h.visible[0] || !h.visible[1] {
		t.Fatalf("lifecycle forwarding mismatch: focused=%v visible=%v", h.focused, h.visible)
	}
	if len(h.resizes) != 1 || h.resizes[0] != [2]int{100, 40} {
		t.Fatalf("resize forwarding = %v", h.resizes)
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
		t.Fatalf("fallback did not restart subscription: live=%v requests=%d closed=%d", m.modelLive, len(source.requests), source.handles[0].closed)
	}
	if m.schedulePoll(0) == nil {
		t.Fatal("fallback did not restore capture polling")
	}
	m.Update(terminalControlRetryMsg{Scope: m.Scope(), Gen: m.controlGen})
	if len(source.requests) != 2 {
		t.Fatalf("control retry subscriptions = %d, want 2", len(source.requests))
	}

	second := source.requests[1]
	second.OnModelFrame(seededFrame("editor", "%4", "reseeded"))
	deliverTerminalEvent(t, m)
	if !m.modelLive || m.State.OutputBuf.String() != "reseeded" {
		t.Fatalf("fallback recovery failed: live=%v output=%q", m.modelLive, m.State.OutputBuf.String())
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
	if !msg.Overflow {
		t.Fatal("mailbox pressure was not surfaced to Bubble Tea")
	}
	if len(source.requests) != 1 || source.handles[0].closed != 1 {
		t.Fatalf("pressure did not establish a clean subscription: requests=%d closed=%d", len(source.requests), source.handles[0].closed)
	}
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
		t.Fatal("invalidated model retained authority")
	}
	generation := m.State.PollGeneration
	if generation == 0 || m.schedulePoll(0) == nil || m.State.PollGeneration != generation+1 {
		t.Fatal("fallback polling did not retain one current generation")
	}
}
