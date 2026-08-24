package tty

import (
	"fmt"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/terminalperf"
	terminalfixture "github.com/marcus/sidecar/internal/testfixture/terminal"
)

// This is the Slice 0 publication baseline: the synthetic OpenCode workload
// travels through the ordered control actor and callback without a tmux server.
func TestOpenCodeFixturePublishesRepeatedControlFramesWithoutTmux(t *testing.T) {
	fixture := terminalfixture.NewOpenCode(160, 44)
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)

	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, ok := channel.seedCommands(0)
	if !ok {
		t.Fatal("seed transaction not issued")
	}
	meta := fmt.Sprintf("0,2,1,%d,%d,0,0,0,0,0,0,0,%%1,0,2", fixture.Height, fixture.Width)
	pushResponse(channel, metadata, []string{meta})
	pushResponse(channel, capture, strings.Split(fixture.Frame(0), "\n"))
	waitFor(t, func() bool { return recorder.frameCount() == 1 })

	for step := range 4 {
		before := recorder.frameCount()
		pushOutput(channel, "%1", string(fixture.Burst(step)))
		waitFor(t, func() bool { return recorder.frameCount() > before })
	}
	// Recorder visibility occurs inside the subscriber callback. Cross the
	// actor once more so teardown from an earlier test cannot contribute a late
	// model build to this process-global diagnostic snapshot.
	controlActorBarrier(t, channel)
	snapshot := counters.Snapshot()
	if snapshot.ModelFramesBuilt != 5 || snapshot.ModelFramesPublished != 5 {
		t.Fatalf("publication counters = %+v, want five built and published frames", snapshot)
	}
}

func TestModelFramePublicationCounterSkipsDeactivatedDelivery(t *testing.T) {
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)

	gate := newSubscriberDeliveryGate()
	// Reproduce the reviewed Close race deterministically: the actor already
	// holds the gate from its membership check, then Close deactivates it before
	// delivery begins.
	gate.deactivate()
	callbackCalls := 0
	if delivered := deliverModelFrame(gate, func(ModelFrame) { callbackCalls++ }, ModelFrame{}); delivered {
		t.Fatal("deactivated delivery gate reported an invocation")
	}
	if callbackCalls != 0 {
		t.Fatalf("callback calls = %d, want 0", callbackCalls)
	}
	if published := counters.Snapshot().ModelFramesPublished; published != 0 {
		t.Fatalf("published frames = %d, want 0 when delivery is suppressed", published)
	}
}

func TestControlModelSuppressesPresentationNeutralWrites(t *testing.T) {
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, ok := channel.seedCommands(0)
	if !ok {
		t.Fatal("seed transaction not issued")
	}
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"seeded"})
	waitFor(t, func() bool { return recorder.frameCount() == 1 })

	built := counters.Snapshot().ModelFramesBuilt
	// Query replies are drained, SGR changes no painted cell, and both modes end
	// in their original state. The ordered model consumes every byte, but Bubble
	// Tea must not receive an identical presentation.
	pushOutput(channel, "%1", "\x1b[6n\x1b[31m\x1b[0m\x1b[?2004h\x1b[?2004l\x1b[?1002h\x1b[?1002l")
	waitFor(t, func() bool { return counters.Snapshot().ModelFramesBuilt > built })
	if got := recorder.frameCount(); got != 1 {
		t.Fatalf("presentation-neutral writes published %d frames, want the seed only", got)
	}
	if got := counters.Snapshot().ModelFramesPublished; got != 1 {
		t.Fatalf("published counter = %d, want 1", got)
	}
}

func TestControlModelPublishesEveryInteractionStateDelta(t *testing.T) {
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, _ := channel.seedCommands(0)
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"seeded"})
	waitFor(t, func() bool { return recorder.frameCount() == 1 })

	for _, tc := range []struct {
		name    string
		payload string
	}{
		{"cursor position", "\x1b[2;2H"},
		{"cursor style", "\x1b[5 q"},
		{"bracketed paste", "\x1b[?2004h"},
		{"mouse mode", "\x1b[?1002h"},
		// Mouse.Any remains true across this transition; the individual mode bits
		// are still part of the complete interaction identity.
		{"mouse mode with same any", "\x1b[?1002l\x1b[?1003h"},
		{"alternate screen", "\x1b[?1049h"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			before := recorder.frameCount()
			pushOutput(channel, "%1", tc.payload)
			waitFor(t, func() bool { return recorder.frameCount() == before+1 })
		})
	}
}

func TestControlModelPublishesAHistoryOnlyDelta(t *testing.T) {
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, _ := channel.seedCommands(0)
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"same"})
	waitFor(t, func() bool { return recorder.frameCount() == 1 })
	before, _ := recorder.lastFrame()

	// Scroll one row into history, repaint the six-row live grid exactly, and
	// restore the seeded cursor. Output remains identical; only the absolute and
	// loaded history state advances.
	var payload strings.Builder
	payload.WriteString("\x1b[1S")
	for row := 1; row <= 6; row++ {
		fmt.Fprintf(&payload, "\x1b[%d;1H\x1b[2K", row)
		if row == 1 {
			payload.WriteString("same")
		}
	}
	payload.WriteString("\x1b[3;1H")
	pushOutput(channel, "%1", payload.String())
	waitFor(t, func() bool { return recorder.frameCount() == 2 })
	after, _ := recorder.lastFrame()
	if after.Frame.Output != before.Frame.Output {
		t.Fatalf("history-only fixture changed live output:\n before %q\n  after %q", before.Frame.Output, after.Frame.Output)
	}
	if after.Frame.HistorySize <= before.Frame.HistorySize ||
		after.Frame.LoadedHistory.Rows() <= before.Frame.LoadedHistory.Rows() {
		t.Fatalf("history did not advance: before size/rows %d/%d, after %d/%d",
			before.Frame.HistorySize, before.Frame.LoadedHistory.Rows(),
			after.Frame.HistorySize, after.Frame.LoadedHistory.Rows())
	}
}

func TestControlModelPublishesAnIdenticalReseed(t *testing.T) {
	recorder := &modelRecorder{}
	_, _, channel := startModelSubscription(t, recorder)
	metadata, capture, _ := channel.seedCommands(0)
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"same"})
	waitFor(t, func() bool { return recorder.frameCount() == 1 })

	channel.events <- controlEvent{Kind: controlEventLayout, Pane: "%1"}
	waitFor(t, func() bool { return channel.seedCount() == 2 })
	metadata, capture, _ = channel.seedCommands(1)
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"same"})
	waitFor(t, func() bool { return recorder.frameCount() == 2 })
	frame, _ := recorder.lastFrame()
	if frame.Seeds != 2 {
		t.Fatalf("identical reseed published seed count %d, want 2", frame.Seeds)
	}
}

func TestControlModelPublishesTheFirstFrameOfANewGeneration(t *testing.T) {
	recorder := &modelRecorder{}
	manager, sub, channel := startModelSubscription(t, recorder)
	metadata, capture, _ := channel.seedCommands(0)
	pushResponse(channel, metadata, []string{testSeedMetadata})
	pushResponse(channel, capture, []string{"same"})
	waitFor(t, func() bool { return recorder.frameCount() == 1 })
	first, _ := recorder.lastFrame()

	sub.SetVisible(false)
	sub.SetVisible(true)
	var replacement *fakeControlChannel
	waitFor(t, func() bool {
		manager.mu.Lock()
		client := manager.clients["one"]
		if client != nil {
			replacement, _ = client.channel.(*fakeControlChannel)
		}
		manager.mu.Unlock()
		return client != nil && replacement != nil && replacement != channel && replacement.seedCount() == 1
	})
	metadata, capture, _ = replacement.seedCommands(0)
	pushResponse(replacement, metadata, []string{testSeedMetadata})
	pushResponse(replacement, capture, []string{"same"})
	waitFor(t, func() bool { return recorder.frameCount() == 2 })
	frame, _ := recorder.lastFrame()
	if frame.Generation <= first.Generation {
		t.Fatalf("replacement frame generation = %d, want newer than %d", frame.Generation, first.Generation)
	}
}
