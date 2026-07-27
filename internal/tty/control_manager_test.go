package tty

import (
	"errors"
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
	for _, command := range f.commands {
		if strings.Contains(command.text, "display-message") {
			metadata = append(metadata, command)
		}
		if strings.Contains(command.text, "capture-pane") {
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

func TestControlManagerNotificationCaptureCoalescingAndFocusGate(t *testing.T) {
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
	time.Sleep(8 * time.Millisecond)
	if got := channel.commandCountContaining("capture-pane"); got != 3 {
		t.Fatalf("blurred output spawned capture %d", got)
	}
	manager.SetAppFocused(true)
	waitFor(t, func() bool { return channel.commandCountContaining("capture-pane") == 4 })

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
	waitFor(t, func() bool { return channel.commandCountContaining("refresh-client -A %7:continue") == 1 })
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
		"9,4,0,30,100,1250", "line one", "%output %12 pane content",
	})
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Output != "line one\n%output %12 pane content" ||
		snapshot.CursorVisible || snapshot.CursorCol != 9 || snapshot.CursorRow != 4 ||
		!snapshot.HasHistory || snapshot.HistorySize != 1250 || snapshot.CaptureBase != 350 {
		t.Fatalf("snapshot = %#v", snapshot)
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
