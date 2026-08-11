package tty

import (
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

// seedCommands finds the nth seed transaction's outer commands. The metadata half
// is identified by a format field only the seed asks for, so it can never be
// confused with the capture path's own display-message. The saved-main capture
// sits between these two and is acknowledged automatically by pushResponse.
func (f *fakeControlChannel) seedCommands(index int) (metadata, capture fakeControlCommand, ok bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	found := 0
	for i, command := range f.commands {
		if !strings.Contains(command.text, seedMetadataMarker) {
			continue
		}
		if i+2 >= len(f.commands) || !strings.Contains(f.commands[i+1].text, "capture-pane") ||
			!strings.Contains(f.commands[i+2].text, "capture-pane") {
			continue
		}
		if found == index {
			return command, f.commands[i+2], true
		}
		found++
	}
	return fakeControlCommand{}, fakeControlCommand{}, false
}

func (f *fakeControlChannel) seedCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, command := range f.commands {
		if strings.Contains(command.text, seedMetadataMarker) {
			count++
		}
	}
	return count
}

// seedMetadataMarker identifies the byte-fed model's seed transaction in the
// fake channel's command log. It must be a field only the seed asks for:
// alternate_on is also in the ordinary capture metadata, which the renderer
// reads to tell a TUI canvas from scrollback highlighting.
const seedMetadataMarker = "#{alternate_saved_x}"

const testSeedMetadata = "0,2,1,6,20,0,0,0,0,0,0,0,%1,0,2"

// pushResponse delivers a command response on the ordered event stream, which
// is where the real transport delivers it. Calling the callback directly would
// bypass the ordering barrier under test.
func pushResponse(channel *fakeControlChannel, command fakeControlCommand, lines []string) {
	channel.events <- controlEvent{
		Kind:     controlEventResponse,
		Callback: command.callback,
		Response: controlResponse{Lines: lines},
	}
	if strings.Contains(command.text, seedMetadataMarker) {
		channel.mu.Lock()
		for i := len(channel.commands) - 1; i >= 0; i-- {
			if channel.commands[i].text == command.text && i+1 < len(channel.commands) {
				main := channel.commands[i+1]
				channel.mu.Unlock()
				channel.events <- controlEvent{Kind: controlEventResponse, Callback: main.callback}
				return
			}
		}
		channel.mu.Unlock()
	}
}

func pushOutput(channel *fakeControlChannel, pane, text string) {
	channel.events <- controlEvent{Kind: controlEventOutput, Pane: pane, Payload: encodeControlBytesForTest(text)}
}

// encodeControlBytesForTest is tmux's octal escaping, so the test exercises the
// real decode path rather than handing the model pre-decoded bytes.
func encodeControlBytesForTest(text string) string {
	var b strings.Builder
	for i := range len(text) {
		c := text[i]
		if c == '\\' {
			b.WriteString("\\\\")
			continue
		}
		if c < 0x20 || c > 0x7e {
			b.WriteString("\\")
			b.WriteByte('0' + (c>>6)&7)
			b.WriteByte('0' + (c>>3)&7)
			b.WriteByte('0' + c&7)
			continue
		}
		b.WriteByte(c)
	}
	return b.String()
}

type modelRecorder struct {
	mu            sync.Mutex
	frames        []ModelFrame
	invalidations []ModelInvalidation
}

func (r *modelRecorder) onFrame(frame ModelFrame) {
	r.mu.Lock()
	r.frames = append(r.frames, frame)
	r.mu.Unlock()
}

func (r *modelRecorder) onInvalid(event ModelInvalidation) {
	r.mu.Lock()
	r.invalidations = append(r.invalidations, event)
	r.mu.Unlock()
}

func (r *modelRecorder) lastFrame() (ModelFrame, bool) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if len(r.frames) == 0 {
		return ModelFrame{}, false
	}
	return r.frames[len(r.frames)-1], true
}

func (r *modelRecorder) frameCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.frames)
}

func (r *modelRecorder) reasons() []ResyncReason {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]ResyncReason, 0, len(r.invalidations))
	for _, event := range r.invalidations {
		out = append(out, event.Reason)
	}
	return out
}

func startModelSubscription(t *testing.T, recorder *modelRecorder) (*ControlManager, *ControlSubscription, *fakeControlChannel) {
	t.Helper()
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, time.Millisecond)
	t.Cleanup(manager.Stop)
	sub, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true, Scrollback: 100,
		OnModelFrame:   recorder.onFrame,
		OnModelInvalid: recorder.onInvalid,
		OnFallback:     func(error) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(sub.Close)
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("one")
		return channel != nil && channel.seedCount() == 1
	})
	return manager, sub, channel
}

// The barrier: everything received before the seed's capture response is
// already rendered into that capture and must be dropped; everything after it
// must be replayed. Nothing may be counted twice and nothing may be lost.
func TestControlModelSeedBarrierDiscardsCapturedBytesAndReplaysTheRest(t *testing.T) {
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, ok := channel.seedCommands(0)
	if !ok {
		t.Fatal("seed transaction not issued")
	}

	// These bytes are in the capture below; replaying them would duplicate.
	pushOutput(channel, "%1", "L000001\r\nL000002\r\n")
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"L000001", "L000002", ""})
	// These are after the capture and must be replayed.
	pushOutput(channel, "%1", "L000003\r\n")
	pushOutput(channel, "%1", "L000004\r\n")

	waitFor(t, func() bool {
		frame, ok := recorder.lastFrame()
		return ok && strings.Contains(frame.Frame.CombinedOutput(), "L000004")
	})
	frame, _ := recorder.lastFrame()
	numbers := extractNumbers(frame.Frame.CombinedOutput())
	if got := strings.Join(numbers, ","); got != "000001,000002,000003,000004" {
		t.Fatalf("model numbers = %q (frame %q)", got, frame.Frame.CombinedOutput())
	}
	if frame.Pane != "%1" || frame.Session != "one" || frame.Generation != 1 || frame.Seeds != 1 {
		t.Fatalf("frame identity = %#v", frame)
	}
}

func extractNumbers(output string) []string {
	var out []string
	for _, line := range strings.Split(output, "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "L") && len(line) >= 7 {
			out = append(out, line[1:7])
		}
	}
	return out
}

// No frame may be published before the seed and its replay complete: until then
// provisional capture remains the presentation source.
func TestControlModelPublishesNoFrameBeforeSeedCompletes(t *testing.T) {
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, _ := channel.seedCommands(0)
	pushOutput(channel, "%1", "early\r\n")
	time.Sleep(20 * time.Millisecond)
	if recorder.frameCount() != 0 {
		t.Fatalf("published %d frames before seeding", recorder.frameCount())
	}
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"seeded"})
	pushOutput(channel, "%1", "after\r\n")
	waitFor(t, func() bool { return recorder.frameCount() > 0 })
	frame, _ := recorder.lastFrame()
	if strings.Contains(frame.Frame.CombinedOutput(), "early") {
		t.Fatalf("pre-seed bytes replayed: %q", frame.Frame.CombinedOutput())
	}
}

// A malformed seed must invalidate only that pane model. The control reader
// must survive and the capture path must keep delivering snapshots.
func TestControlModelFaultIsolatesOnlyThatPane(t *testing.T) {
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, time.Millisecond)
	defer manager.Stop()
	recorder := &modelRecorder{}
	var snapshotMu sync.Mutex
	var snapshots []ControlSnapshot
	var fallbacks []error
	sub, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true,
		OnSnapshot: func(snapshot ControlSnapshot) {
			snapshotMu.Lock()
			snapshots = append(snapshots, snapshot)
			snapshotMu.Unlock()
		},
		OnFallback: func(err error) {
			snapshotMu.Lock()
			fallbacks = append(fallbacks, err)
			snapshotMu.Unlock()
		},
		OnModelFrame:   recorder.onFrame,
		OnModelInvalid: recorder.onInvalid,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("one")
		return channel != nil && channel.seedCount() == 1
	})
	metadata, capture, _ := channel.seedCommands(0)
	pushResponse(channel, metadata, []string{"not,valid,metadata"})
	pushResponse(channel, capture, []string{"screen"})
	waitFor(t, func() bool {
		for _, event := range recorder.reasons() {
			if event == ResyncModelFault {
				return true
			}
		}
		return false
	})

	// The capture path is untouched: its own transaction still delivers.
	channel.respondCapture(0, controlResponse{Lines: []string{"4,2,1,24,80,7", "capture still authoritative"}})
	waitFor(t, func() bool {
		snapshotMu.Lock()
		defer snapshotMu.Unlock()
		return len(snapshots) == 1
	})
	snapshotMu.Lock()
	defer snapshotMu.Unlock()
	if snapshots[0].Output != "capture still authoritative" {
		t.Fatalf("snapshot = %#v", snapshots[0])
	}
	if len(fallbacks) != 0 {
		t.Fatalf("a model fault forced the consumer off control mode: %v", fallbacks)
	}
	if !sub.UsingControl() {
		t.Fatal("a model fault killed the control client")
	}
}

// Pause, layout, and resize each require a fresh seed.
func TestControlModelResyncTriggers(t *testing.T) {
	for _, tc := range []struct {
		name   string
		fire   func(sub *ControlSubscription, channel *fakeControlChannel)
		reason ResyncReason
	}{
		{"pause", func(_ *ControlSubscription, channel *fakeControlChannel) {
			channel.events <- controlEvent{Kind: controlEventPause, Pane: "%1"}
		}, ResyncPause},
		{"continue", func(_ *ControlSubscription, channel *fakeControlChannel) {
			channel.events <- controlEvent{Kind: controlEventContinue, Pane: "%1"}
		}, ResyncPause},
		{"layout", func(_ *ControlSubscription, channel *fakeControlChannel) {
			channel.events <- controlEvent{Kind: controlEventLayout, Pane: "%1"}
		}, ResyncLayout},
		{"resize", func(sub *ControlSubscription, _ *fakeControlChannel) {
			sub.Resize(100, 30)
		}, ResyncResize},
	} {
		t.Run(tc.name, func(t *testing.T) {
			recorder := &modelRecorder{}
			_, sub, channel := startModelSubscription(t, recorder)
			metadata, capture, _ := channel.seedCommands(0)
			pushResponse(channel, metadata, []string{testSeedMetadata})
			pushResponse(channel, capture, []string{"seeded"})
			waitFor(t, func() bool { return recorder.frameCount() > 0 })

			tc.fire(sub, channel)
			waitFor(t, func() bool { return channel.seedCount() == 2 })
			metadata, capture, _ = channel.seedCommands(1)
			pushResponse(channel, metadata, []string{testSeedMetadata})
			pushResponse(channel, capture, []string{"reseeded"})
			waitFor(t, func() bool {
				frame, ok := recorder.lastFrame()
				return ok && frame.Seeds == 2 && strings.Contains(frame.Frame.CombinedOutput(), "reseeded")
			})
			found := false
			for _, reason := range recorder.reasons() {
				if reason == tc.reason {
					found = true
				}
			}
			if !found {
				t.Fatalf("resync reasons = %v, want %v", recorder.reasons(), tc.reason)
			}
		})
	}
}

// A pane replaced under the same target must not be seeded from the stranger's
// screen.
func TestControlModelRejectsPaneIdentityChange(t *testing.T) {
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, _ := channel.seedCommands(0)
	pushResponse(channel, metadata, []string{"0,0,1,6,20,0,0,0,0,0,0,0,%9,0,0"})
	pushResponse(channel, capture, []string{"stranger"})
	waitFor(t, func() bool {
		for _, reason := range recorder.reasons() {
			if reason == ResyncPaneIdentity {
				return true
			}
		}
		return false
	})
	if recorder.frameCount() != 0 {
		t.Fatal("published a frame from a different pane")
	}
}

// Unsubscribing must stop the feed: no frame may arrive after Close returns.
func TestControlModelUnsubscribeStopsDelivery(t *testing.T) {
	recorder := &modelRecorder{}
	_, sub, channel := startModelSubscription(t, recorder)
	metadata, capture, _ := channel.seedCommands(0)
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"seeded"})
	waitFor(t, func() bool { return recorder.frameCount() > 0 })
	before := recorder.frameCount()
	sub.Close()
	pushOutput(channel, "%1", "after close\r\n")
	time.Sleep(30 * time.Millisecond)
	if recorder.frameCount() != before {
		t.Fatalf("frames delivered after unsubscribe: %d -> %d", before, recorder.frameCount())
	}
}

// With OnModelFrame nil nothing about the existing path may change: no model
// command is issued and the delivered snapshot is identical.
func TestCaptureDeliveryUnchangedWhenModelPathOff(t *testing.T) {
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, time.Millisecond)
	defer manager.Stop()
	var mu sync.Mutex
	var snapshots []ControlSnapshot
	sub, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true, Scrollback: 900,
		OnSnapshot: func(snapshot ControlSnapshot) {
			mu.Lock()
			snapshots = append(snapshots, snapshot)
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("one")
		return channel != nil && channel.commandCountContaining("capture-pane") == 1
	})
	channel.respondCapture(0, controlResponse{
		Lines: []string{"9,4,0,30,100,1250,1,0,node,Action Required", "line one", "line two"},
	})
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(snapshots) == 1
	})
	pushOutput(channel, "%1", "more output\r\n")
	waitFor(t, func() bool { return channel.commandCountContaining("capture-pane") == 2 })
	time.Sleep(20 * time.Millisecond)

	if channel.seedCount() != 0 {
		t.Fatal("a seed transaction was issued with the model path off")
	}
	if got := channel.commandCountContaining("#{client_discarded}"); got != 0 {
		t.Fatalf("%d discard probes issued with the model path off", got)
	}
	mu.Lock()
	defer mu.Unlock()
	want := ControlSnapshot{
		Session: "one", Pane: "%1", Output: "line one\nline two",
		HistorySize: 1250, CaptureBase: 350, HasHistory: true,
		// The capture carried two rows for a 30-row pane, so it is all pane and
		// no history: an undersized capture has no scrolled-off rows in it.
		HistoryRows: 0, PaneRows: 2,
		CursorRow: 4, CursorCol: 9, CursorVisible: false,
		PaneHeight: 30, PaneWidth: 100, Generation: 1,
		MouseReporting: true, PaneTitle: "Action Required", CurrentCommand: "node",
	}
	if snapshots[0] != want {
		t.Fatalf("snapshot = %#v, want %#v", snapshots[0], want)
	}
}

func TestModelPresentationSuppressesOnlySteadyStateOutputCaptures(t *testing.T) {
	recorder := &modelRecorder{}
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, time.Millisecond)
	defer manager.Stop()
	sub, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true, Scrollback: 100,
		ModelPresentation: true,
		OnModelFrame:      recorder.onFrame,
		OnModelInvalid:    recorder.onInvalid,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("one")
		return channel != nil && channel.seedCount() == 1
	})
	metadata, capture, ok := channel.seedCommands(0)
	if !ok {
		t.Fatal("seed transaction not issued")
	}
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"seeded"})
	waitFor(t, func() bool { return recorder.frameCount() > 0 })

	captures := channel.commandCountContaining("capture-pane")
	metadataQueries := channel.commandCountContaining("display-message")
	pushOutput(channel, "%1", "steady state\r\n")
	waitFor(t, func() bool {
		frame, ok := recorder.lastFrame()
		return ok && strings.Contains(frame.Frame.CombinedOutput(), "steady state")
	})
	time.Sleep(10 * time.Millisecond)
	if got := channel.commandCountContaining("capture-pane"); got != captures {
		t.Fatalf("steady-state output issued capture-pane: %d -> %d", captures, got)
	}
	if got := channel.commandCountContaining("display-message"); got != metadataQueries {
		t.Fatalf("steady-state output issued metadata query: %d -> %d", metadataQueries, got)
	}
}

func TestModelPresentationDoesNotSuppressSamePaneUnfocusedCaptureSubscriber(t *testing.T) {
	recorder := &modelRecorder{}
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, time.Millisecond)
	defer manager.Stop()
	authority, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true, Scrollback: 100,
		ModelPresentation: true, OnModelFrame: recorder.onFrame,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer authority.Close()
	var captureSnapshots atomic.Int32
	capture, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: false, Scrollback: 100,
		OnSnapshot: func(ControlSnapshot) { captureSnapshots.Add(1) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer capture.Close()
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("one")
		return channel != nil && channel.seedCount() == 1 && channel.commandCountContaining("capture-pane") >= 3
	})
	metadata, seedCapture, _ := channel.seedCommands(0)
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, seedCapture, []string{"seeded"})
	waitFor(t, func() bool { return recorder.frameCount() > 0 })
	channel.respondCapture(0, controlResponse{Lines: []string{"0,0,1,6,20,0", "initial"}})
	waitFor(t, func() bool { return captureSnapshots.Load() > 0 })
	captureSnapshots.Store(0)

	before := channel.commandCountContaining("capture-pane")
	pushOutput(channel, "%1", "shared pane\r\n")
	waitFor(t, func() bool { return channel.commandCountContaining("capture-pane") > before })
	channel.respondCapture(1, controlResponse{Lines: []string{"0,0,1,6,20,0", "shared pane"}})
	waitFor(t, func() bool { return captureSnapshots.Load() > 0 })
}

func TestParseSeedMetadata(t *testing.T) {
	meta, err := parseSeedMetadata("9,4,0,30,100,1250,1,1,0,1,1,64,%12,7,8")
	if err != nil {
		t.Fatal(err)
	}
	if meta.CursorCol != 9 || meta.CursorRow != 4 || meta.CursorVisible ||
		meta.Height != 30 || meta.Width != 100 || meta.HistorySize != 1250 ||
		!meta.AltScreen || !meta.Mouse.Normal || meta.Mouse.ButtonEvent ||
		!meta.Mouse.AnyEvent || !meta.Mouse.SGR || meta.Discarded != 64 || meta.PaneID != "%12" {
		t.Fatalf("metadata = %#v", meta)
	}
	for _, bad := range []string{
		"", "1,2,3", "9,4,0,0,100,1250,1,1,0,1,1,0,%12", "x,4,0,30,100,1250,1,1,0,1,1,0,%12",
		"9,4,0,30,100,-1,1,1,0,1,1,0,%12",
	} {
		if _, err := parseSeedMetadata(bad); err == nil {
			t.Fatalf("accepted impossible metadata %q", bad)
		}
	}
	// client_discarded is empty outside a control client; that is not a fault.
	if meta, err := parseSeedMetadata("0,0,1,24,80,0,0,0,0,0,0,,%1,0,0"); err != nil || meta.Discarded != 0 {
		t.Fatalf("empty client_discarded = %#v, %v", meta, err)
	}
}

func TestAltSeedPreservesMetadataHistoryRowsOmittedByTmux(t *testing.T) {
	seed, _, err := seedFromResponses(
		[]string{"1,1,1,3,10,2,1,0,0,0,0,0,%1,0,2"},
		[]string{"main-a", "main-b", "main-c"},
		[]string{"alt-a", "alt-b", "alt-c"},
		600,
	)
	if err != nil {
		t.Fatal(err)
	}
	model := screenmodel.New(seed.Width, seed.Height)
	defer model.Close()
	if err := model.Seed(seed); err != nil {
		t.Fatal(err)
	}
	frame, err := model.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if frame.LoadedHistory.Rows() != 2 || frame.HistorySize != 2 {
		t.Fatalf("loaded/absolute history = %d/%d, want 2/2", frame.LoadedHistory.Rows(), frame.HistorySize)
	}
	if !strings.HasSuffix(frame.Output, "alt-c") {
		t.Fatalf("active grid = %q", frame.Output)
	}
}

func TestBuildSeedCommandsRejectsUnsafeTarget(t *testing.T) {
	if _, _, _, err := buildSeedCommands("name; kill-server", 100); err == nil {
		t.Fatal("unsafe pane target accepted")
	}
	metadata, mainCapture, capture, err := buildSeedCommands("%3", 250)
	if err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"#{alternate_on}", "#{mouse_all_flag}", "#{client_discarded}", "#{pane_id}", "#{alternate_saved_x}"} {
		if !strings.Contains(metadata, field) {
			t.Fatalf("seed metadata omits %s: %q", field, metadata)
		}
	}
	if !strings.Contains(capture, "-S -250") || !strings.Contains(capture, "-e") {
		t.Fatalf("seed capture = %q", capture)
	}
	if !strings.Contains(mainCapture, " -a ") || !strings.Contains(mainCapture, "-S -250") {
		t.Fatalf("saved-main seed capture = %q", mainCapture)
	}
}

// client_discarded growth means tmux dropped output for this control client.
// It is the backstop for the case where pause-after flow control does not fire,
// and it must force a fresh seed rather than let the model drift.
func TestControlModelDiscardGrowthForcesReseed(t *testing.T) {
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, _ := channel.seedCommands(0)
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"seeded"})
	waitFor(t, func() bool { return recorder.frameCount() > 0 })

	var probe fakeControlCommand
	waitUntilCommand(t, 3*time.Second, func() bool {
		channel.mu.Lock()
		defer channel.mu.Unlock()
		for _, command := range channel.commands {
			if strings.Contains(command.text, "#{client_discarded}") &&
				!strings.Contains(command.text, "cursor_x") {
				probe = command
				return true
			}
		}
		return false
	})
	pushResponse(channel, probe, []string{"4096"})
	waitUntilCommand(t, 3*time.Second, func() bool { return channel.seedCount() == 2 })
	found := false
	for _, reason := range recorder.reasons() {
		if reason == ResyncDiscarded {
			found = true
		}
	}
	if !found {
		t.Fatalf("discard growth did not report a resync: %v", recorder.reasons())
	}
}

// tmux gives no notification for client_discarded, so between checks a frame can
// be built from a stream tmux silently truncated. Slice 2's shadow comparison
// cannot attribute a mismatch without knowing that, so every frame carries the
// last observed counter and when it was observed.
func TestControlModelFramesCarryDiscardObservationWindow(t *testing.T) {
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, _ := channel.seedCommands(0)
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"seeded"})
	waitUntilCommand(t, 3*time.Second, func() bool { return recorder.frameCount() > 0 })

	seeded, _ := recorder.lastFrame()
	if seeded.DiscardCheckedAt.IsZero() {
		t.Fatal("a seeded frame reports no discard observation time")
	}
	if seeded.Discarded != 0 {
		t.Fatalf("seed metadata reported client_discarded 0, frame says %d", seeded.Discarded)
	}

	var probe fakeControlCommand
	waitUntilCommand(t, 3*time.Second, func() bool {
		channel.mu.Lock()
		defer channel.mu.Unlock()
		for _, command := range channel.commands {
			if strings.Contains(command.text, "#{client_discarded}") &&
				!strings.Contains(command.text, "cursor_x") {
				probe = command
				return true
			}
		}
		return false
	})
	pushResponse(channel, probe, []string{"4096"})

	// The reseed the growth forces republishes with the new counter and a fresh
	// observation time: the window a consumer must discount is now bounded.
	waitUntilCommand(t, 3*time.Second, func() bool { return channel.seedCount() == 2 })
	metadata, capture, _ = channel.seedCommands(1)
	pushResponse(channel, metadata, []string{"0,2,1,6,20,0,0,0,0,0,0,4096,%1,0,2"})
	pushResponse(channel, capture, []string{"reseeded"})
	waitUntilCommand(t, 3*time.Second, func() bool {
		frame, ok := recorder.lastFrame()
		return ok && frame.Discarded == 4096 && frame.DiscardCheckedAt.After(seeded.DiscardCheckedAt)
	})
}

// The seed-race detector is the standing guard on the one thing this slice's
// barrier assumes and tmux does not document: that no pane byte ever lands
// between a seed transaction's metadata response and its capture response. If it
// ever did, the metadata cursor would describe an older screen than the capture
// and every subsequently replayed byte would be written at the wrong coordinate
// — silently.
//
// The integration test can only observe that the detector does not fire against
// tmux 3.6b, which is equally true of a detector that is dead code. This forces
// the interleaving directly on the ordered event stream (no real tmux can be
// made to do it on demand) and requires the detector to fire: the raced seed must
// be discarded, reported, and replaced by a fresh one, and no frame may be built
// from the stale metadata.
func TestControlModelSeedRaceIsDetectedAndReseeds(t *testing.T) {
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, ok := channel.seedCommands(0)
	if !ok {
		t.Fatal("seed transaction not issued")
	}

	// Exactly the forbidden interleaving: metadata, then pane bytes, then the
	// capture that those bytes are already rendered into.
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushOutput(channel, "%1", "RACED\r\n")
	pushResponse(channel, capture, []string{"stale screen"})

	waitUntilCommand(t, 3*time.Second, func() bool {
		for _, reason := range recorder.reasons() {
			if reason == ResyncSeedRace {
				return true
			}
		}
		return false
	})
	// The raced seed is discarded, not accepted: no frame may exist yet, and a
	// replacement transaction must have been issued.
	if recorder.frameCount() != 0 {
		t.Fatalf("published %d frames from a raced seed", recorder.frameCount())
	}
	waitUntilCommand(t, 3*time.Second, func() bool { return channel.seedCount() == 2 })

	// The replacement seed is uncontended and does produce a model.
	metadata, capture, ok = channel.seedCommands(1)
	if !ok {
		t.Fatal("replacement seed transaction not issued")
	}
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"fresh screen"})
	waitUntilCommand(t, 3*time.Second, func() bool {
		frame, ok := recorder.lastFrame()
		return ok && strings.Contains(frame.Frame.CombinedOutput(), "fresh screen")
	})
	// The stale capture must never have been seeded into the model.
	frame, _ := recorder.lastFrame()
	if strings.Contains(frame.Frame.CombinedOutput(), "stale screen") {
		t.Fatalf("raced seed reached the model: %q", frame.Frame.CombinedOutput())
	}
}

// Bytes arriving before the metadata response are not a race: they are provably
// already in the capture that follows, which is the ordinary mid-stream attach.
// The detector must not fire on them, or every busy pane would reseed forever.
func TestControlModelBytesBeforeMetadataAreNotASeedRace(t *testing.T) {
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, _ := channel.seedCommands(0)
	pushOutput(channel, "%1", "before metadata\r\n")
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"seeded"})
	waitUntilCommand(t, 3*time.Second, func() bool { return recorder.frameCount() > 0 })
	for _, reason := range recorder.reasons() {
		if reason == ResyncSeedRace {
			t.Fatalf("pre-metadata bytes were mistaken for a seed race: %v", recorder.reasons())
		}
	}
	if channel.seedCount() != 1 {
		t.Fatalf("an uncontended seed was reissued: %d seeds", channel.seedCount())
	}
}

func waitUntilCommand(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}
