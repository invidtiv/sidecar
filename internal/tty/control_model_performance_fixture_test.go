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
