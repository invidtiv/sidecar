package tty

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/terminalperf"
)

type manualModelCadenceTimer struct {
	clock    *manualModelCadenceClock
	due      time.Time
	callback func()
	active   bool
}

func (t *manualModelCadenceTimer) Stop() bool {
	t.clock.mu.Lock()
	defer t.clock.mu.Unlock()
	wasActive := t.active
	t.active = false
	return wasActive
}

type manualModelCadenceClock struct {
	mu     sync.Mutex
	now    time.Time
	timers []*manualModelCadenceTimer
}

func newManualModelCadenceClock() *manualModelCadenceClock {
	return &manualModelCadenceClock{now: time.Unix(1_000, 0)}
}

func (c *manualModelCadenceClock) config() modelCadenceConfig {
	return modelCadenceConfig{
		interval: modelPublicationInterval,
		now: func() time.Time {
			c.mu.Lock()
			defer c.mu.Unlock()
			return c.now
		},
		afterFunc: func(delay time.Duration, callback func()) modelCadenceTimer {
			c.mu.Lock()
			defer c.mu.Unlock()
			timer := &manualModelCadenceTimer{
				clock: c, due: c.now.Add(delay), callback: callback, active: true,
			}
			c.timers = append(c.timers, timer)
			return timer
		},
	}
}

func (c *manualModelCadenceClock) time() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualModelCadenceClock) advance(delta time.Duration) {
	c.mu.Lock()
	c.now = c.now.Add(delta)
	var callbacks []func()
	for _, timer := range c.timers {
		if timer.active && !timer.due.After(c.now) {
			timer.active = false
			callbacks = append(callbacks, timer.callback)
		}
	}
	c.mu.Unlock()
	for _, callback := range callbacks {
		callback()
	}
}

func (c *manualModelCadenceClock) activeTimers() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	active := 0
	for _, timer := range c.timers {
		if timer.active {
			active++
		}
	}
	return active
}

type timedModelRecorder struct {
	mu         sync.Mutex
	clock      *manualModelCadenceClock
	frames     []ModelFrame
	times      []time.Time
	afterFrame func(int)
}

func (r *timedModelRecorder) onFrame(frame ModelFrame) {
	r.mu.Lock()
	r.frames = append(r.frames, frame)
	r.times = append(r.times, r.clock.time())
	count := len(r.frames)
	afterFrame := r.afterFrame
	r.mu.Unlock()
	if afterFrame != nil {
		afterFrame(count)
	}
}

func (r *timedModelRecorder) snapshot() ([]ModelFrame, []time.Time) {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]ModelFrame(nil), r.frames...), append([]time.Time(nil), r.times...)
}

func (r *timedModelRecorder) frameCount() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.frames)
}

func startCadenceSubscription(t *testing.T, clock *manualModelCadenceClock, recorder *timedModelRecorder) (*ControlManager, *ControlSubscription, *fakeControlChannel) {
	t.Helper()
	factory := newFakeControlFactory()
	manager := newControlManager(factory.create, time.Millisecond)
	manager.modelCadence = clock.config()
	sub, err := manager.Subscribe(ControlRequest{
		Session: "one", Pane: "%1", Visible: true, Focused: true, Scrollback: 100,
		OnModelFrame: recorder.onFrame,
		OnFallback:   func(error) {},
	})
	if err != nil {
		manager.Stop()
		t.Fatal(err)
	}
	var channel *fakeControlChannel
	waitFor(t, func() bool {
		channel = factory.channel("one")
		return channel != nil && channel.seedCount() == 1
	})
	return manager, sub, channel
}

func seedCadenceSubscription(t *testing.T, channel *fakeControlChannel, recorder *timedModelRecorder) {
	t.Helper()
	metadata, capture, ok := channel.seedCommands(0)
	if !ok {
		t.Fatal("seed transaction not issued")
	}
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"seeded"})
	waitFor(t, func() bool { return recorder.frameCount() == 1 })
}

func controlActorBarrier(t *testing.T, channel *fakeControlChannel) {
	t.Helper()
	done := make(chan struct{})
	channel.events <- controlEvent{Kind: controlEventResponse, Callback: func(controlResponse) { close(done) }}
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("control actor did not reach barrier")
	}
}

func TestModelCadencePublishesIdleLeadingAndNewestTrailingFrame(t *testing.T) {
	clock := newManualModelCadenceClock()
	recorder := &timedModelRecorder{clock: clock}
	manager, sub, channel := startCadenceSubscription(t, clock, recorder)
	t.Cleanup(manager.Stop)
	t.Cleanup(sub.Close)
	seedCadenceSubscription(t, channel, recorder)

	clock.advance(modelPublicationInterval)
	pushOutput(channel, "%1", "A")
	controlActorBarrier(t, channel)
	waitFor(t, func() bool { return recorder.frameCount() == 2 })
	if clock.activeTimers() != 0 {
		t.Fatal("idle-leading publication left a timer armed")
	}

	pushOutput(channel, "%1", "B")
	pushOutput(channel, "%1", "C")
	controlActorBarrier(t, channel)
	if got := recorder.frameCount(); got != 2 {
		t.Fatalf("burst published %d frames before eligibility, want 2", got)
	}
	if got := clock.activeTimers(); got != 1 {
		t.Fatalf("active timers = %d, want exactly one trailing timer", got)
	}

	clock.advance(modelPublicationInterval - time.Nanosecond)
	controlActorBarrier(t, channel)
	if got := recorder.frameCount(); got != 2 {
		t.Fatalf("published early at %s: frames=%d", modelPublicationInterval-time.Nanosecond, got)
	}
	clock.advance(time.Nanosecond)
	waitFor(t, func() bool { return recorder.frameCount() == 3 })
	frames, times := recorder.snapshot()
	if output := frames[len(frames)-1].Frame.CombinedOutput(); !strings.Contains(output, "ABC") {
		t.Fatalf("trailing frame stranded model bytes: %q", output)
	}
	if gap := times[2].Sub(times[1]); gap < modelPublicationInterval {
		t.Fatalf("publication gap = %s, want at least %s", gap, modelPublicationInterval)
	}
	if got := clock.activeTimers(); got != 0 {
		t.Fatalf("active timers after trailing delivery = %d, want 0", got)
	}
}

func TestModelCadencePublishesOnlyCompletedSynchronizedUpdate(t *testing.T) {
	clock := newManualModelCadenceClock()
	recorder := &timedModelRecorder{clock: clock}
	manager, sub, channel := startCadenceSubscription(t, clock, recorder)
	t.Cleanup(manager.Stop)
	t.Cleanup(sub.Close)
	seedCadenceSubscription(t, channel, recorder)

	clock.advance(modelPublicationInterval)
	pushOutput(channel, "%1", "\x1b[?2026h\x1b[2Jpartial")
	controlActorBarrier(t, channel)
	if got := recorder.frameCount(); got != 1 {
		t.Fatalf("open synchronized update published an intermediate frame: frames=%d", got)
	}
	if got := clock.activeTimers(); got != 1 {
		t.Fatalf("open synchronized update timers = %d, want one bounded hold", got)
	}

	pushOutput(channel, "%1", " complete\x1b[?2026l")
	controlActorBarrier(t, channel)
	waitFor(t, func() bool { return recorder.frameCount() == 2 })
	frames, _ := recorder.snapshot()
	output := frames[1].Frame.CombinedOutput()
	if !strings.Contains(output, "partial complete") {
		t.Fatalf("completed synchronized frame = %q", output)
	}
	if got := clock.activeTimers(); got != 0 {
		t.Fatalf("completed synchronized update left %d timers", got)
	}
}

func TestModelCadenceBoundsAnUnclosedSynchronizedUpdate(t *testing.T) {
	clock := newManualModelCadenceClock()
	recorder := &timedModelRecorder{clock: clock}
	manager, sub, channel := startCadenceSubscription(t, clock, recorder)
	t.Cleanup(manager.Stop)
	t.Cleanup(sub.Close)
	seedCadenceSubscription(t, channel, recorder)

	clock.advance(modelPublicationInterval)
	pushOutput(channel, "%1", "\x1b[?2026hstuck")
	controlActorBarrier(t, channel)
	if got := recorder.frameCount(); got != 1 {
		t.Fatalf("open synchronized update published before its hold ceiling: frames=%d", got)
	}
	clock.advance(maxSynchronizedOutputHold - time.Nanosecond)
	controlActorBarrier(t, channel)
	if got := recorder.frameCount(); got != 1 {
		t.Fatalf("open synchronized update published early: frames=%d", got)
	}
	clock.advance(time.Nanosecond)
	waitFor(t, func() bool { return recorder.frameCount() == 2 })
	frames, _ := recorder.snapshot()
	if output := frames[1].Frame.CombinedOutput(); !strings.Contains(output, "stuck") {
		t.Fatalf("bounded synchronized frame = %q", output)
	}
}

func TestModelCadenceSustainedRateAndLatencyStayBounded(t *testing.T) {
	clock := newManualModelCadenceClock()
	recorder := &timedModelRecorder{clock: clock}
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)
	manager, sub, channel := startCadenceSubscription(t, clock, recorder)
	t.Cleanup(manager.Stop)
	t.Cleanup(sub.Close)
	seedCadenceSubscription(t, channel, recorder)

	const events = 125
	for event := 1; event <= events; event++ {
		pushOutput(channel, "%1", fmt.Sprintf("\r%03d", event))
		controlActorBarrier(t, channel)
		clock.advance(8 * time.Millisecond)
		controlActorBarrier(t, channel)
	}
	clock.advance(modelPublicationInterval)
	waitFor(t, func() bool {
		frames, _ := recorder.snapshot()
		return strings.Contains(frames[len(frames)-1].Frame.CombinedOutput(), "125")
	})

	frames, times := recorder.snapshot()
	for index := 2; index < len(times); index++ { // exclude the unconditional seed
		if gap := times[index].Sub(times[index-1]); gap < modelPublicationInterval {
			t.Fatalf("publication %d gap = %s, want at least %s", index, gap, modelPublicationInterval)
		}
	}
	if got, max := len(frames)-1, 30; got > max {
		t.Fatalf("changed publications in one sustained second = %d, want <= %d", got, max)
	}
	snapshot := counters.Snapshot()
	if snapshot.OutputToFrameSamples != uint64(len(frames)-1) {
		t.Fatalf("latency samples = %d, changed publications = %d", snapshot.OutputToFrameSamples, len(frames)-1)
	}
	if snapshot.OutputToFrameP95US > 50_000 {
		t.Fatalf("output-to-frame p95 = %dus, want <= 50000us", snapshot.OutputToFrameP95US)
	}
	t.Logf("one-second sustained stream: changed publications=%d, p95=%dus, max=%dus",
		len(frames)-1, snapshot.OutputToFrameP95US, snapshot.OutputToFrameMaxUS)
}

func TestModelCadenceTimerStopsWithControlClient(t *testing.T) {
	clock := newManualModelCadenceClock()
	recorder := &timedModelRecorder{clock: clock}
	manager, sub, channel := startCadenceSubscription(t, clock, recorder)
	seedCadenceSubscription(t, channel, recorder)

	pushOutput(channel, "%1", "pending")
	controlActorBarrier(t, channel)
	if got := clock.activeTimers(); got != 1 {
		t.Fatalf("active timers before close = %d, want 1", got)
	}
	manager.mu.Lock()
	client := manager.clients["one"]
	manager.mu.Unlock()
	if client == nil {
		t.Fatal("test control client disappeared before close")
	}
	sub.Close()
	select {
	case <-client.actorDone:
	default:
		t.Fatal("subscription close returned before the control actor terminated")
	}
	manager.Stop()
	waitFor(t, func() bool { return clock.activeTimers() == 0 })
	clock.advance(modelPublicationInterval)
	if got := recorder.frameCount(); got != 1 {
		t.Fatalf("closed client published a trailing frame: frames=%d", got)
	}
}

func TestModelCadenceDoesNotMistakeSlowDeliveryForIdle(t *testing.T) {
	clock := newManualModelCadenceClock()
	recorder := &timedModelRecorder{clock: clock}
	manager, sub, channel := startCadenceSubscription(t, clock, recorder)
	t.Cleanup(manager.Stop)
	t.Cleanup(sub.Close)
	seedCadenceSubscription(t, channel, recorder)

	recorder.mu.Lock()
	recorder.afterFrame = func(count int) {
		if count == 2 {
			clock.advance(500 * time.Millisecond)
			pushOutput(channel, "%1", "B")
		}
	}
	recorder.mu.Unlock()
	clock.advance(modelPublicationInterval)
	pushOutput(channel, "%1", "A")
	waitFor(t, func() bool { return recorder.frameCount() == 2 })
	controlActorBarrier(t, channel)
	if got := recorder.frameCount(); got != 2 {
		t.Fatalf("queued output after slow delivery published immediately: frames=%d", got)
	}
	if got := clock.activeTimers(); got != 1 {
		t.Fatalf("active timers after slow delivery = %d, want one sustained timer", got)
	}
	clock.advance(modelPublicationInterval)
	waitFor(t, func() bool { return recorder.frameCount() == 3 })
	frames, _ := recorder.snapshot()
	if output := frames[len(frames)-1].Frame.CombinedOutput(); !strings.Contains(output, "AB") {
		t.Fatalf("slow-delivery trailing frame lost queued bytes: %q", output)
	}
}

func TestTerminalInputDoesNotWaitForPresentationCadence(t *testing.T) {
	clock := newManualModelCadenceClock()
	recorder := &timedModelRecorder{clock: clock}
	manager, sub, channel := startCadenceSubscription(t, clock, recorder)
	t.Cleanup(manager.Stop)
	t.Cleanup(sub.Close)
	seedCadenceSubscription(t, channel, recorder)
	pushOutput(channel, "%1", "pending")
	controlActorBarrier(t, channel)
	if clock.activeTimers() != 1 {
		t.Fatal("test did not establish a pending presentation timer")
	}

	input := &fakeTerminalInputSender{}
	terminal := newContractTerminal(&fakeTerminalControlSource{})
	terminal.input = input
	terminal.Open(Target{Session: "editor", Pane: "%1"})
	terminal.Update(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if len(input.calls) != 1 || input.calls[0].kind != "keys" || input.calls[0].keys[0].Value != "x" {
		t.Fatalf("input while presentation timer pending = %#v", input.calls)
	}
	if recorder.frameCount() != 1 {
		t.Fatal("input path advanced the independent presentation cadence")
	}
}
