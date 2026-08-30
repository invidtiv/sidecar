package tty

import (
	"errors"
	"fmt"
	"os/exec"
	"strings"
	"sync"
	"testing"
	"time"
)

type fakeControlCommand struct {
	text     string
	callback func(controlResponse)
}

type fakeControlChannel struct {
	mu       sync.Mutex
	commands []fakeControlCommand
	events   chan controlEvent
	done     chan error
	closed   int
}

func newFakeControlChannel() *fakeControlChannel {
	return &fakeControlChannel{
		events: make(chan controlEvent, 64),
		done:   make(chan error, 4),
	}
}

func (f *fakeControlChannel) Send(command string, callback func(controlResponse)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands, fakeControlCommand{text: command, callback: callback})
	return nil
}

func (f *fakeControlChannel) SendBatch(commands []string, callbacks []func(controlResponse)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i := range commands {
		f.commands = append(f.commands, fakeControlCommand{text: commands[i], callback: callbacks[i]})
	}
	return nil
}

func (f *fakeControlChannel) SendPair(first, second string, firstCallback, secondCallback func(controlResponse)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands,
		fakeControlCommand{text: first, callback: firstCallback},
		fakeControlCommand{text: second, callback: secondCallback},
	)
	return nil
}

func (f *fakeControlChannel) SendTriple(first, second, third string, firstCallback, secondCallback, thirdCallback func(controlResponse)) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.commands = append(f.commands,
		fakeControlCommand{text: first, callback: firstCallback},
		fakeControlCommand{text: second, callback: secondCallback},
		fakeControlCommand{text: third, callback: thirdCallback},
	)
	return nil
}

func (f *fakeControlChannel) Events() <-chan controlEvent { return f.events }
func (f *fakeControlChannel) Done() <-chan error          { return f.done }
func (f *fakeControlChannel) Close() error {
	f.mu.Lock()
	f.closed++
	f.mu.Unlock()
	return nil
}

func (f *fakeControlChannel) commandCountContaining(needle string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	count := 0
	for _, command := range f.commands {
		if strings.Contains(command.text, needle) {
			count++
		}
	}
	return count
}

func (f *fakeControlChannel) closeCount() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.closed
}

func (f *fakeControlChannel) respondCapture(index int, response controlResponse) bool {
	f.mu.Lock()
	var metadata []fakeControlCommand
	var captures []fakeControlCommand
	// Seed transactions are the byte-fed model's, not the capture path's. They
	// use the same tmux commands, so they are excluded by the format field
	// only the seed asks for.
	skipCaptures := 0
	for _, command := range f.commands {
		if strings.Contains(command.text, seedMetadataMarker) {
			skipCaptures = 2 // saved main followed by the active grid
			continue
		}
		if strings.Contains(command.text, "display-message") {
			metadata = append(metadata, command)
		}
		if strings.Contains(command.text, "capture-pane") {
			if skipCaptures > 0 {
				skipCaptures--
				continue
			}
			captures = append(captures, command)
		}
	}
	f.mu.Unlock()
	if index >= len(metadata) || index >= len(captures) {
		return false
	}
	metaLines := []string{"0,0,1,24,80,0"}
	captureResponse := response
	if len(response.Lines) > 0 {
		metaLines = response.Lines[:1]
		captureResponse.Lines = response.Lines[1:]
	}
	metadata[index].callback(controlResponse{Lines: metaLines})
	captures[index].callback(captureResponse)
	return true
}

type fakeControlFactory struct {
	mu       sync.Mutex
	channels map[string]*fakeControlChannel
	fail     map[string]error
	calls    map[string]int
}

func newFakeControlFactory() *fakeControlFactory {
	return &fakeControlFactory{
		channels: make(map[string]*fakeControlChannel),
		fail:     make(map[string]error),
		calls:    make(map[string]int),
	}
}

func (f *fakeControlFactory) create(session string) (controlChannel, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls[session]++
	if err := f.fail[session]; err != nil {
		return nil, err
	}
	channel := newFakeControlChannel()
	f.channels[session] = channel
	return channel, nil
}

func (f *fakeControlFactory) channel(session string) *fakeControlChannel {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.channels[session]
}

func (f *fakeControlFactory) callCount(session string) int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.calls[session]
}

func TestControlManagerPoolsBySessionAndOnlyAttachesVisibleConsumers(t *testing.T) {
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, 0)
	defer manager.Stop()

	hidden, err := manager.Subscribe(ControlRequest{Session: "one", Pane: "%1", Visible: false, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	if factory.callCount("one") != 0 {
		t.Fatal("hidden consumer started a control client")
	}
	hidden.SetVisible(true)
	waitFor(t, func() bool { return factory.callCount("one") == 1 })
	second, err := manager.Subscribe(ControlRequest{Session: "one", Pane: "%2", Visible: true, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	if factory.callCount("one") != 1 {
		t.Fatal("same session did not reuse control client")
	}
	other, err := manager.Subscribe(ControlRequest{Session: "two", Pane: "%3", Visible: true, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, func() bool { return factory.callCount("two") == 1 })
	hidden.Close()
	second.Close()
	other.Close()
}

func TestControlManagerStartsClientOffSubscriptionPath(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	channel := newFakeControlChannel()
	manager := newControlManager(func(string) (controlChannel, error) {
		close(started)
		<-release
		return channel, nil
	}, 0)

	returned := make(chan error, 1)
	go func() {
		_, err := manager.Subscribe(ControlRequest{
			Session: "one", Pane: "%1", Visible: true, Focused: true,
		})
		returned <- err
	}()
	select {
	case err := <-returned:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("subscription blocked on tmux process startup")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("background client startup did not begin")
	}

	manager.Stop()
	close(release)
	waitFor(t, func() bool { return channel.closeCount() == 1 })
}

func TestControlManagerNotificationCaptureCoalescingKeepsVisiblePaneCurrentWhileAppBlurred(t *testing.T) {
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, 2*time.Millisecond)
	defer manager.Stop()

	var mu sync.Mutex
	var snapshots []ControlSnapshot
	sub, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true,
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
		return channel != nil
	})

	waitFor(t, func() bool { return channel.commandCountContaining("capture-pane") == 1 })
	channel.respondCapture(0, controlResponse{Lines: []string{"4,2,1,24,80,700", "initial"}})
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(snapshots) == 1
	})

	for i := 0; i < 20; i++ {
		channel.events <- controlEvent{Kind: controlEventOutput, Pane: "%1"}
	}
	waitFor(t, func() bool { return channel.commandCountContaining("capture-pane") == 2 })
	// More output while capture 2 is in flight must coalesce into one follow-up.
	for i := 0; i < 20; i++ {
		channel.events <- controlEvent{Kind: controlEventOutput, Pane: "%1"}
	}
	time.Sleep(5 * time.Millisecond)
	if got := channel.commandCountContaining("capture-pane"); got != 2 {
		t.Fatalf("in-flight burst spawned %d captures, want 2", got)
	}
	channel.respondCapture(1, controlResponse{Lines: []string{"5,3,1,24,80,700", "burst"}})
	waitFor(t, func() bool { return channel.commandCountContaining("capture-pane") == 3 })
	channel.respondCapture(2, controlResponse{Lines: []string{"6,4,1,24,80,700", "follow-up"}})

	manager.SetAppFocused(false)
	channel.events <- controlEvent{Kind: controlEventOutput, Pane: "%1"}
	waitFor(t, func() bool { return channel.commandCountContaining("capture-pane") == 4 })
	channel.respondCapture(3, controlResponse{Lines: []string{"7,5,1,24,80,700", "blurred-visible"}})
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(snapshots) == 4
	})
	manager.SetAppFocused(true)

	mu.Lock()
	first := snapshots[0]
	mu.Unlock()
	if first.Output != "initial" || first.CursorRow != 2 || first.CursorCol != 4 ||
		first.PaneHeight != 24 || first.PaneWidth != 80 || first.Generation != 1 ||
		!first.HasHistory || first.HistorySize != 700 || first.CaptureBase != 100 {
		t.Fatalf("snapshot = %#v", first)
	}
}

func TestControlManagerFallbackOnStartAndClientFailure(t *testing.T) {
	factory := newFakeControlFactory()
	factory.fail["bad"] = errors.New("unsupported")
	manager := newControlManager(factory.create, 0)
	defer manager.Stop()

	var mu sync.Mutex
	var fallbacks []error
	record := func(err error) {
		mu.Lock()
		fallbacks = append(fallbacks, err)
		mu.Unlock()
	}
	bad, err := manager.Subscribe(ControlRequest{
		Session: "bad", Pane: "%1", Visible: true, Focused: true, OnFallback: record,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer bad.Close()
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(fallbacks) == 1
	})
	if bad.UsingControl() {
		t.Fatal("failed start reported control active")
	}

	good, err := manager.Subscribe(ControlRequest{
		Session: "good", Pane: "%2", Visible: true, Focused: true, OnFallback: record,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer good.Close()
	waitFor(t, good.UsingControl)
	var goodChannel *fakeControlChannel
	waitFor(t, func() bool {
		goodChannel = factory.channel("good")
		return goodChannel != nil
	})
	goodChannel.done <- errors.New("reader EOF")
	waitFor(t, func() bool {
		mu.Lock()
		defer mu.Unlock()
		return len(fallbacks) == 2
	})
	if good.UsingControl() {
		t.Fatal("dead client still reported control active")
	}
}

// A saturated transport must not consume the only client-death error while it
// releases a blocked dispatcher. The manager needs that error to fire the
// consumer's fallback callback, which is what makes workspace polling resume.
func TestControlManagerFallbackWhenProcessEventsAreSaturated(t *testing.T) {
	channel := &processControlChannel{
		cmd:     &exec.Cmd{},
		stdin:   &testWriteCloser{},
		events:  make(chan controlEvent, 1),
		done:    make(chan error, 1),
		dead:    make(chan struct{}),
		ready:   make(chan struct{}),
		readyOK: true,
	}
	manager := newControlManager(func(string) (controlChannel, error) {
		return channel, nil
	}, 0)
	defer manager.Stop()

	fallback := make(chan error, 1)
	sub, err := manager.Subscribe(ControlRequest{
		Session: "saturated", Pane: "%1", Visible: true, Focused: true,
		OnFallback: func(err error) { fallback <- err },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	waitFor(t, sub.UsingControl)

	manager.mu.Lock()
	client := manager.clients["saturated"]
	manager.mu.Unlock()
	if client == nil {
		t.Fatal("control client did not start")
	}

	actorBlocked := make(chan struct{})
	releaseActor := make(chan struct{})
	client.post(func() {
		close(actorBlocked)
		<-releaseActor
	})
	<-actorBlocked

	// Fill the only event slot, then prove a second dispatch cannot finish
	// until the transport dies. This reproduces the production backpressure
	// condition without touching the machine's default tmux server.
	channel.events <- controlEvent{Kind: controlEventLayout, Pane: "%1"}
	dispatchReturned := make(chan struct{})
	go func() {
		channel.dispatch(controlEvent{Kind: controlEventLayout, Pane: "%1"})
		close(dispatchReturned)
	}()
	select {
	case <-dispatchReturned:
		t.Fatal("dispatch did not block on the saturated event queue")
	case <-time.After(20 * time.Millisecond):
	}

	want := errors.New("reader EOF under backpressure")
	channel.finish(want)
	select {
	case <-dispatchReturned:
	case <-time.After(time.Second):
		t.Fatal("transport death did not release the saturated dispatcher")
	}
	close(releaseActor)

	select {
	case got := <-fallback:
		if !errors.Is(got, want) {
			t.Fatalf("fallback error = %v, want %v", got, want)
		}
	case <-time.After(time.Second):
		t.Fatal("saturated transport consumed client death; fallback did not fire")
	}
	if sub.UsingControl() {
		t.Fatal("dead saturated client still reported control active")
	}
}

func TestControlManagerDropsStaleGenerationAndStopsIdempotently(t *testing.T) {
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, 0)
	delivered := 0
	sub, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true,
		OnSnapshot: func(ControlSnapshot) { delivered++ },
	})
	if err != nil {
		t.Fatal(err)
	}
	var oldChannel *fakeControlChannel
	waitFor(t, func() bool {
		oldChannel = factory.channel("one")
		return oldChannel != nil
	})
	waitFor(t, func() bool { return oldChannel.commandCountContaining("capture-pane") == 1 })

	sub.SetVisible(false)
	sub.SetVisible(true)
	var newChannel *fakeControlChannel
	waitFor(t, func() bool {
		newChannel = factory.channel("one")
		return newChannel != nil && newChannel != oldChannel
	})
	if newChannel == oldChannel {
		t.Fatal("visibility teardown did not replace unused session client")
	}
	oldChannel.respondCapture(0, controlResponse{Lines: []string{"0,0,1,24,80,0", "stale"}})
	time.Sleep(5 * time.Millisecond)
	if delivered != 0 {
		t.Fatal("stale generation delivered a snapshot")
	}

	sub.Close()
	sub.Close()
	manager.Stop()
	manager.Stop()
}

func TestControlManagerRevalidatesQueuedDeliveriesAfterClose(t *testing.T) {
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, 0)
	var callbackMu sync.Mutex
	callbacks := 0
	firstStarted := make(chan string, 1)
	releaseFirst := make(chan struct{})
	var firstOnce sync.Once
	onSnapshot := func(id string) func(ControlSnapshot) {
		return func(ControlSnapshot) {
			callbackMu.Lock()
			callbacks++
			callbackMu.Unlock()
			firstOnce.Do(func() {
				firstStarted <- id
				<-releaseFirst
			})
		}
	}
	first, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true, OnSnapshot: onSnapshot("first"),
	})
	if err != nil {
		t.Fatal(err)
	}
	second, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true, OnSnapshot: onSnapshot("second"),
	})
	if err != nil {
		t.Fatal(err)
	}
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("one")
		return channel != nil && channel.commandCountContaining("capture-pane") == 1
	})

	responseDone := make(chan struct{})
	go func() {
		channel.respondCapture(0, controlResponse{Lines: []string{"0,0,1,24,80,0", "screen"}})
		close(responseDone)
	}()
	var active, queued *ControlSubscription
	select {
	case id := <-firstStarted:
		if id == "first" {
			active, queued = first, second
		} else {
			active, queued = second, first
		}
	case <-time.After(time.Second):
		t.Fatal("first queued callback did not start")
	}
	// Closing the subscriber whose callback has not begun must invalidate its
	// queued delivery immediately. Closing the active subscriber is a barrier
	// and returns after that callback is released.
	queued.Close()
	activeClosed := make(chan struct{})
	go func() {
		active.Close()
		close(activeClosed)
	}()
	close(releaseFirst)
	select {
	case <-activeClosed:
	case <-time.After(time.Second):
		t.Fatal("active subscriber close did not drain callback")
	}
	select {
	case <-responseDone:
	case <-time.After(time.Second):
		t.Fatal("capture callback did not finish")
	}

	callbackMu.Lock()
	defer callbackMu.Unlock()
	if callbacks != 1 {
		t.Fatalf("callbacks after closing queued subscribers = %d, want 1", callbacks)
	}
}

func TestControlManagerStopInvalidatesInFlightCapture(t *testing.T) {
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, 0)
	var callbackMu sync.Mutex
	callbacks := 0
	_, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true,
		OnSnapshot: func(ControlSnapshot) {
			callbackMu.Lock()
			callbacks++
			callbackMu.Unlock()
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("one")
		return channel != nil && channel.commandCountContaining("capture-pane") == 1
	})

	manager.Stop()
	channel.respondCapture(0, controlResponse{Lines: []string{"0,0,1,24,80,0", "late"}})
	callbackMu.Lock()
	defer callbackMu.Unlock()
	if callbacks != 0 {
		t.Fatalf("callbacks after manager stop = %d, want 0", callbacks)
	}
}

func TestControlClientContinuesPausedPaneAndCapturesLayoutChange(t *testing.T) {
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, 0)
	defer manager.Stop()
	sub, err := manager.Subscribe(ControlRequest{Session: "one", Pane: "%7", Visible: true, Focused: true})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("one")
		return channel != nil
	})
	waitFor(t, func() bool { return channel.commandCountContaining("capture-pane") == 1 })
	channel.respondCapture(0, controlResponse{Lines: []string{"0,0,1,24,80,0"}})

	channel.events <- controlEvent{Kind: controlEventPause, Pane: "%7"}
	// The pane target must be quoted: tmux's parser reads a bare leading '%' as
	// a conditional directive and rejects the whole command, which would leave
	// the pane paused forever.
	waitFor(t, func() bool { return channel.commandCountContaining("refresh-client -A '%7:continue'") == 1 })
	channel.events <- controlEvent{Kind: controlEventLayout}
	waitFor(t, func() bool { return channel.commandCountContaining("capture-pane") == 2 })
}

func TestBuildAndParseControlCapture(t *testing.T) {
	metadata, capture, err := buildControlCaptureCommands("%12", 900)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, "display-message") || !strings.Contains(capture, "-S -900") {
		t.Fatalf("capture commands = %q, %q", metadata, capture)
	}
	if _, _, err := buildControlCaptureCommands("name; kill-server", 10); err == nil {
		t.Fatal("unsafe pane target accepted")
	}
	snapshot, err := parseControlSnapshot("session", "%12", 900, []string{
		"9,4,0,30,100,1250,1,node,Action Required, repo", "line one", "%output %12 pane content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Output != "line one\n%output %12 pane content" ||
		snapshot.CursorVisible || snapshot.CursorCol != 9 || snapshot.CursorRow != 4 ||
		!snapshot.HasHistory || snapshot.HistorySize != 1250 || snapshot.CaptureBase != 350 ||
		snapshot.CurrentCommand != "node" || snapshot.PaneTitle != "Action Required, repo" || !snapshot.MouseReporting {
		t.Fatalf("snapshot = %#v", snapshot)
	}
	if !strings.Contains(metadata, "#{pane_current_command}") || !strings.Contains(metadata, "#{pane_title}") {
		t.Fatalf("metadata command omits fresh activity identity: %q", metadata)
	}
	legacy, err := parseControlSnapshot("session", "%12", 900, []string{"9,4,0,30,100,1250", "line"})
	if err != nil || legacy.MouseReporting || legacy.CurrentCommand != "" || legacy.PaneTitle != "" {
		t.Fatalf("legacy metadata = %#v, err=%v", legacy, err)
	}
}

// td-d29821: the capture path writes its display-message and its capture-pane
// separately, so history_size can be stale by the time the rows arrive. The
// capture's own row count is the only self-consistent source for where those
// rows sit in tmux's absolute line space.
func TestControlSnapshotDerivesCaptureBaseFromTheCapture(t *testing.T) {
	const paneHeight = 4
	metadata := func(historySize int) string {
		return fmt.Sprintf("0,0,1,%d,80,%d", paneHeight, historySize)
	}
	capture := func(rows int) []string {
		lines := make([]string, rows)
		for i := range lines {
			lines[i] = fmt.Sprintf("row-%02d", i)
		}
		return lines
	}

	for _, tc := range []struct {
		name        string
		historySize int
		rows        int
		wantBase    int
	}{
		// 600 history rows delivered, so the first one is absolute 400.
		{"metadata agrees with the capture", 1000, 600 + paneHeight, 400},
		// Two rows scrolled into history after the metadata was read: the capture
		// carries 602 of them, and the base moves with the rows, not the stale
		// count.
		{"capture ran ahead of the metadata", 1000, 602 + paneHeight, 398},
		// Shorter session than the requested window: every history row is loaded,
		// so the capture starts at absolute 0 rather than history_size-scrollback.
		{"history shorter than the request", 12, 12 + paneHeight, 0},
		// Nothing but the live pane.
		{"no history at all", 0, paneHeight, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			lines := append([]string{metadata(tc.historySize)}, capture(tc.rows)...)
			snapshot, err := parseControlSnapshot("session", "%12", 600, lines)
			if err != nil {
				t.Fatal(err)
			}
			if snapshot.CaptureBase != tc.wantBase {
				t.Fatalf("capture base = %d, want %d (history_size %d, %d rows for a %d-row pane)",
					snapshot.CaptureBase, tc.wantBase, tc.historySize, tc.rows, paneHeight)
			}
			// The invariant the viewport depends on: the rows delivered fill the
			// absolute window the snapshot claims.
			if got := snapshot.HistorySize - snapshot.CaptureBase; got != tc.rows-paneHeight {
				t.Fatalf("absolute history window = %d, want the %d delivered history rows",
					got, tc.rows-paneHeight)
			}
			// And the split the consumer places the cursor with is counted from
			// the same delivered rows, while they are still a line slice.
			if snapshot.HistoryRows != tc.rows-paneHeight || snapshot.PaneRows != paneHeight {
				t.Fatalf("split = %d history + %d pane rows, want %d + %d",
					snapshot.HistoryRows, snapshot.PaneRows, tc.rows-paneHeight, paneHeight)
			}
		})
	}

	// Degenerate captures carry no usable row count, so the requested window is
	// still the best available answer.
	short, err := parseControlSnapshot("session", "%12", 600, []string{metadata(1000), "only-one-row"})
	if err != nil {
		t.Fatal(err)
	}
	if short.CaptureBase != 400 {
		t.Fatalf("short-capture base = %d, want the requested-window fallback 400", short.CaptureBase)
	}
	if short.HistoryRows != 0 || short.PaneRows != 1 {
		t.Fatalf("short-capture split = %d history + %d pane rows, want 0 + 1",
			short.HistoryRows, short.PaneRows)
	}
	noGeometry, err := parseControlSnapshot("session", "%12", 600, []string{"0,0,1,0,80,1000", "a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	if noGeometry.CaptureBase != 400 {
		t.Fatalf("no-pane-height base = %d, want the requested-window fallback 400", noGeometry.CaptureBase)
	}
	if noGeometry.PaneRows != 0 {
		t.Fatalf("no-pane-height pane rows = %d, want the split reported as unknown", noGeometry.PaneRows)
	}

	// A capture whose final pane row is blank ends in an empty line. The split
	// has to count it, because once Output is joined that line is exactly what a
	// trailing terminator looks like.
	blankTail, err := parseControlSnapshot("session", "%12", 600,
		[]string{metadata(2), "history-0", "history-1", "pane-0", "pane-1", "pane-2", ""})
	if err != nil {
		t.Fatal(err)
	}
	if blankTail.HistoryRows != 2 || blankTail.PaneRows != paneHeight {
		t.Fatalf("blank-tail split = %d history + %d pane rows, want 2 + %d",
			blankTail.HistoryRows, blankTail.PaneRows, paneHeight)
	}
}

// Mouse tracking is asked of tmux because `capture-pane -e` emits rendering
// escapes only — an app's DECSET 1000/1002/1003/1006 never survives the capture.
func TestControlCaptureReportsPaneMouseTracking(t *testing.T) {
	metadata, _, err := buildControlCaptureCommands("%12", 900)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(metadata, "#{mouse_any_flag}") {
		t.Fatalf("metadata command does not ask for the mouse flag: %q", metadata)
	}

	for _, tc := range []struct {
		flag string
		want bool
	}{{"1", true}, {"0", false}} {
		snapshot, err := parseControlSnapshot("session", "%12", 900, []string{
			"9,4,0,30,100,1250," + tc.flag, "line one",
		})
		if err != nil {
			t.Fatal(err)
		}
		if snapshot.MouseReporting != tc.want {
			t.Fatalf("mouse_any_flag %q parsed as %v", tc.flag, snapshot.MouseReporting)
		}
	}
}

func waitFor(t *testing.T, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("condition not met before timeout")
}

// A control client that closes must tell every model-backed subscriber its
// model is dead, even when the close beats the ordered actor to it.
//
// beginClose used to wipe c.subs and set c.closed before the actor reached
// failAllModels, and invalidate then found no subscription and dropped the
// terminal ModelInvalidation silently. OnFallback is reported outside that
// guard, so the consumer was told to fall back and never told why — it had no
// terminal invalidation to drive a resubscribe from. It reproduced on CI and
// 5 times in 25 local runs of TestModelReconnectFallsBackThenReseeds under
// -cpu=1 -race; this test forces the losing order instead of waiting for it.
func TestClientCloseAlwaysReportsTerminalModelInvalidation(t *testing.T) {
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, 0)
	defer manager.Stop()

	invalidations := make(chan ModelInvalidation, 4)
	sub, err := manager.Subscribe(ControlRequest{
		Session: "one",
		Pane:    "%1",
		Visible: true,
		Focused: true,
		OnModelFrame: func(ModelFrame) {
		},
		OnModelInvalid: func(invalid ModelInvalidation) {
			invalidations <- invalid
		},
		OnFallback: func(error) {},
	})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()

	var client *sessionControlClient
	deadline := time.Now().Add(5 * time.Second)
	for client == nil && time.Now().Before(deadline) {
		manager.mu.Lock()
		client = manager.clients["one"]
		manager.mu.Unlock()
		if client == nil {
			time.Sleep(5 * time.Millisecond)
		}
	}
	if client == nil {
		t.Fatal("no control client for the subscribed session")
	}

	// beginClose, not the actor: this is exactly the order that used to drop
	// the notice on the floor.
	client.beginClose()

	select {
	case invalid := <-invalidations:
		if !invalid.Terminal {
			t.Fatalf("invalidation = %+v, want Terminal", invalid)
		}
		if invalid.Reason != ResyncReconnect {
			t.Fatalf("invalidation reason = %v, want %v", invalid.Reason, ResyncReconnect)
		}
		if invalid.Pane != "%1" || invalid.Session != "one" {
			t.Fatalf("invalidation identifies %s/%s, want one/%%1", invalid.Session, invalid.Pane)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("closing the control client reported no terminal model invalidation")
	}

	// Exactly once: the actor's own failAllModels must not report the same
	// death a second time now that beginClose has claimed it.
	select {
	case dup := <-invalidations:
		t.Fatalf("terminal invalidation reported twice: %+v", dup)
	case <-time.After(500 * time.Millisecond):
	}
}
