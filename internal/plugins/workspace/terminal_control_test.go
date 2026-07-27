package workspace

import (
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
)

type fakeWorkspaceControlManager struct {
	requests []tty.ControlRequest
	focus    []bool
	stopped  int
	err      error
}

func (f *fakeWorkspaceControlManager) Subscribe(request tty.ControlRequest) (*tty.ControlSubscription, error) {
	f.requests = append(f.requests, request)
	if f.err != nil {
		return nil, f.err
	}
	return &tty.ControlSubscription{}, nil
}

func (f *fakeWorkspaceControlManager) SetAppFocused(focused bool) {
	f.focus = append(f.focus, focused)
}

func (f *fakeWorkspaceControlManager) Stop() { f.stopped++ }

func terminalPanelControlPlugin(manager workspaceControlManager) *Plugin {
	p := New()
	p.focused = true
	p.width = 120
	p.height = 40
	p.activePane = PanePreview
	p.viewMode = ViewModeList
	p.previewTab = PreviewTabOutput
	p.termPanelVisible = true
	p.termPanelFocused = true
	p.termPanelSession = "panel-session"
	p.termPanelPaneID = "%7"
	p.termPanelOutput = tty.NewOutputBuffer(outputBufferCap)
	p.controlManager = manager
	p.controlMailbox = newWorkspaceControlMailbox()
	p.controlConsumers = make(map[workspaceControlRole]*workspaceControlConsumer)
	return p
}

func TestTerminalPanelControlStartsVisibleAndKeepsPollUntilSnapshot(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := terminalPanelControlPlugin(manager)
	cmds := p.reconcileTerminalControls()
	if len(manager.requests) != 1 {
		t.Fatalf("control subscriptions = %d, want 1", len(manager.requests))
	}
	request := manager.requests[0]
	if request.Session != "panel-session" || request.Pane != "%7" ||
		!request.Visible || !request.Focused || request.Scrollback != captureLineCount {
		t.Fatalf("control request = %#v", request)
	}
	if len(cmds) != 1 || cmds[0] == nil {
		t.Fatal("fallback poll was not retained while control starts")
	}
	if p.terminalControlUsing(workspaceControlPanel) {
		t.Fatal("control reported active before first snapshot")
	}

	consumer := p.controlConsumers[workspaceControlPanel]
	generationBefore := p.pollScheduler.Current(termPanelPollKey())
	p.applyTerminalControlDelivery(workspaceControlDeliveryMsg{Events: []workspaceControlEvent{{
		Role: workspaceControlPanel, Token: consumer.Token,
		Session: consumer.Session, Pane: consumer.Pane,
		Snapshot: &tty.ControlSnapshot{
			Session: "panel-session", Pane: "%7", Output: "one\ntwo",
			HistorySize: 900, CaptureBase: 300, HasHistory: true,
			CursorRow: 1, CursorCol: 2, CursorVisible: true,
			PaneHeight: 20, PaneWidth: 80,
		},
	}}})
	if !p.terminalControlUsing(workspaceControlPanel) {
		t.Fatal("first accepted snapshot did not activate control")
	}
	if got := p.pollScheduler.Current(termPanelPollKey()); got <= generationBefore {
		t.Fatalf("poll generation = %d, want > %d", got, generationBefore)
	}
	base, _, absolute := p.termPanelOutput.AbsoluteRange()
	if !absolute || base != 300 || p.termPanelOutput.Lines()[1] != "two" {
		t.Fatalf("panel snapshot base=%d absolute=%v lines=%q", base, absolute, p.termPanelOutput.Lines())
	}
	if p.scheduleTermPanelPoll(0) != nil {
		t.Fatal("panel polling continued after control activation")
	}
}

func TestTerminalPanelControlFallbackRestartsOneKeyedPoll(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := terminalPanelControlPlugin(manager)
	p.reconcileTerminalControls()
	consumer := p.controlConsumers[workspaceControlPanel]
	consumer.Using = true

	cmd := p.applyTerminalControlDelivery(workspaceControlDeliveryMsg{Events: []workspaceControlEvent{{
		Role: workspaceControlPanel, Token: consumer.Token,
		Session: consumer.Session, Pane: consumer.Pane,
		Fallback: errors.New("reader EOF"),
	}}})
	if cmd == nil || consumer.Using || !consumer.Degraded {
		t.Fatalf("fallback state = using:%v degraded:%v cmd:%v", consumer.Using, consumer.Degraded, cmd)
	}
	firstGeneration := p.pollScheduler.Current(termPanelPollKey())
	if _, ok := cmd().(termPanelPollMsg); !ok {
		t.Fatalf("fallback command returned %T", cmd())
	}
	duplicate := p.applyTerminalControlDelivery(workspaceControlDeliveryMsg{Events: []workspaceControlEvent{{
		Role: workspaceControlPanel, Token: consumer.Token,
		Session: consumer.Session, Pane: consumer.Pane,
		Fallback: errors.New("duplicate"),
	}}})
	if duplicate != nil {
		t.Fatal("duplicate fallback scheduled another poll")
	}
	if got := p.pollScheduler.Current(termPanelPollKey()); got != firstGeneration {
		t.Fatalf("duplicate fallback advanced generation to %d, want %d", got, firstGeneration)
	}
}

func TestTerminalPanelControlTracksVisibilityAndApplicationFocus(t *testing.T) {
	manager := &fakeWorkspaceControlManager{}
	p := terminalPanelControlPlugin(manager)
	p.reconcileTerminalControls()

	p.termPanelVisible = false
	p.reconcileTerminalControls()
	if p.controlConsumers[workspaceControlPanel] != nil {
		t.Fatal("hidden panel retained control subscription")
	}

	p.Update(tea.BlurMsg{})
	p.Update(tea.FocusMsg{})
	if p.applicationFocused != true || len(manager.focus) != 2 ||
		manager.focus[0] != false || manager.focus[1] != true {
		t.Fatalf("focus propagation = %#v appFocused=%v", manager.focus, p.applicationFocused)
	}
}
