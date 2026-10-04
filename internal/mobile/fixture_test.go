package mobile

import (
	"github.com/marcus/sidecar/internal/mobileproto"
	"github.com/marcus/sidecar/internal/tty"
	"testing"
	"time"
)

func TestEchoTerminalIsolatesPanesAndConditionallyReleasesOwners(t *testing.T) {
	adapter := &EchoTerminal{}
	oneFrames, twoFrames := []tty.ControlSnapshot{}, []tty.ControlSnapshot{}
	one, err := adapter.Subscribe(tty.ControlRequest{Session: "one", Pane: "%1", OnSnapshot: func(s tty.ControlSnapshot) { oneFrames = append(oneFrames, s) }})
	if err != nil {
		t.Fatal(err)
	}
	defer one.Close()
	two, err := adapter.Subscribe(tty.ControlRequest{Session: "two", Pane: "%2", OnSnapshot: func(s tty.ControlSnapshot) { twoFrames = append(twoFrames, s) }})
	if err != nil {
		t.Fatal(err)
	}
	defer two.Close()
	identity := tty.HeadlessTargetIdentity{Session: "one", Pane: "%1"}
	old, err := adapter.Geometry(identity, "old")
	if err != nil {
		t.Fatal(err)
	}
	if err := old.ClaimResize(40, 10); err != nil {
		t.Fatal(err)
	}
	current, err := adapter.Geometry(identity, "current")
	if err != nil {
		t.Fatal(err)
	}
	if err := current.ClaimResize(40, 10); err != nil {
		t.Fatal(err)
	}
	if err := old.Release(); err != nil {
		t.Fatal(err)
	}
	if err := old.SendLiteral([]byte("wrong-owner")); err == nil {
		t.Fatal("old owner sent input")
	}
	if err := current.SendLiteral([]byte("echo")); err != nil {
		t.Fatal(err)
	}
	if len(oneFrames) != 3 || oneFrames[1].Output != "" || oneFrames[1].PaneWidth != 40 || oneFrames[2].Output != "echo" || oneFrames[2].PaneWidth != 40 {
		t.Fatal(oneFrames)
	}
	if len(twoFrames) != 1 || twoFrames[0].Output != "" || twoFrames[0].PaneWidth != 80 {
		t.Fatal(twoFrames)
	}
	if err := current.Release(); err != nil {
		t.Fatal(err)
	}
	if err := current.Heartbeat(); err == nil {
		t.Fatal("released owner renewed lease")
	}
}

func TestEchoTerminalPresenceLabelsTakeoverAndExpiry(t *testing.T) {
	e := &EchoTerminal{}
	sub, err := e.Subscribe(tty.ControlRequest{Session: "one", Pane: "%1"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	identity := tty.HeadlessTargetIdentity{Session: "one", Pane: "%1"}
	first, err := e.Geometry(identity, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.Geometry(identity, "second")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.SetHolderLabel("ios", "iPhone"); err != nil {
		t.Fatal(err)
	}
	if err := second.SetHolderLabel("browser", "Browser"); err != nil {
		t.Fatal(err)
	}
	if owned, err := first.Presence(true, true, 0, 60, 20, false); err != nil || !owned {
		t.Fatalf("claim: %v %v", owned, err)
	}
	// Merely constructing/polling a second handle cannot seize the pane.
	if kind, label, err := second.Holder(); err != nil || kind != "ios" || label != "iPhone" {
		t.Fatalf("holder: %s %s %v", kind, label, err)
	}
	if owned, err := second.Presence(true, true, 0, 40, 10, false); err != nil || owned {
		t.Fatalf("fresh owner preempted: %v %v", owned, err)
	}
	if owned, err := first.Presence(true, true, 10*time.Second, 60, 20, false); err != nil || !owned {
		t.Fatalf("owner presence: %v %v", owned, err)
	}
	// Publish elapsed idle evidence without sleeping.
	e.panes["one\x00%1"].lastWrite = time.Now().Add(-10 * time.Second)
	if owned, err := first.Presence(true, true, 10*time.Second, 60, 20, false); err != nil || !owned {
		t.Fatalf("idle owner: %v %v", owned, err)
	}
	if owned, err := second.Presence(true, true, 0, 40, 10, false); err != nil || !owned {
		t.Fatalf("idle preemption: %v %v", owned, err)
	}
	if owned, err := first.Presence(false, true, 0, 60, 20, false); err != nil || owned {
		t.Fatalf("blur: %v %v", owned, err)
	}
	if kind, label, err := first.Holder(); err != nil || kind != "browser" || label != "Browser" {
		t.Fatalf("foreign blur cleared holder: %s %s %v", kind, label, err)
	}
	// Explicit input outranks a fresh foreign owner and echoes exactly once.
	if err := first.ClaimInput([]byte("paste"), 70, 22, true); err != nil {
		t.Fatal(err)
	}
	pane := e.panes["one\x00%1"]
	if pane.snapshot.Output != "paste" || pane.snapshot.PaneWidth != 70 || pane.snapshot.PaneHeight != 22 {
		t.Fatal(pane.snapshot)
	}
	if kind, label, err := second.Holder(); err != nil || kind != "ios" || label != "iPhone" {
		t.Fatalf("input holder: %s %s %v", kind, label, err)
	}
	pane.touched = time.Now().Add(-time.Duration(mobileproto.PresenceTimeoutMS+1) * time.Millisecond)
	if expired, err := first.ExpirePresence(); err != nil || !expired {
		t.Fatalf("expiry: %v %v", expired, err)
	}
	if kind, label, err := second.Holder(); err != nil || kind != "" || label != "" {
		t.Fatalf("expired holder: %s %s %v", kind, label, err)
	}
	if err := first.Paste([]byte("after expiry")); err == nil {
		t.Fatal("paste bypassed expiry")
	}
}

func TestEchoTerminalRejectsInvalidGeometryWithoutMutation(t *testing.T) {
	e := &EchoTerminal{}
	sub, err := e.Subscribe(tty.ControlRequest{Session: "one", Pane: "%1"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	g, err := e.Geometry(tty.HeadlessTargetIdentity{Session: "one", Pane: "%1"}, "owner")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ClaimResize(80, 24); err != nil {
		t.Fatal(err)
	}
	for _, size := range [][2]int{{1, 24}, {513, 24}, {80, 0}, {80, 257}} {
		if err := g.ClaimResize(size[0], size[1]); err == nil {
			t.Fatal("claim accepted", size)
		}
		if err := g.Resize(size[0], size[1]); err == nil {
			t.Fatal("resize accepted", size)
		}
		if _, err := g.Presence(true, true, 0, size[0], size[1], false); err == nil {
			t.Fatal("presence accepted", size)
		}
		if err := g.ClaimInput([]byte("wrong"), size[0], size[1], false); err == nil {
			t.Fatal("input accepted", size)
		}
	}
	pane := e.panes["one\x00%1"]
	if pane.snapshot.PaneWidth != 80 || pane.snapshot.PaneHeight != 24 || pane.snapshot.Output != "" || pane.owner != "owner" {
		t.Fatal(pane.snapshot, pane.owner)
	}
}
func TestEchoTerminalPresenceReclaimsElapsedStaleForeignOwner(t *testing.T) {
	e := &EchoTerminal{}
	sub, err := e.Subscribe(tty.ControlRequest{Session: "one", Pane: "%1"})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	identity := tty.HeadlessTargetIdentity{Session: "one", Pane: "%1"}
	first, err := e.Geometry(identity, "first")
	if err != nil {
		t.Fatal(err)
	}
	second, err := e.Geometry(identity, "second")
	if err != nil {
		t.Fatal(err)
	}
	if err := first.ClaimResize(80, 24); err != nil {
		t.Fatal(err)
	}
	// An idle observer cannot reclaim on request count; elapsed stale evidence
	// must include a prior observation of exactly this token.
	for i := 0; i < 10; i++ {
		if owned, err := second.Presence(true, true, time.Hour, 40, 10, false); err != nil || owned {
			t.Fatalf("fresh owner: %v %v", owned, err)
		}
	}
	// Test-only internal seam, with no concurrent service or real clock wait.
	observer := &echoGeometry{pane: e.panes["one\x00%1"], owner: "second"}
	if _, err := observer.Presence(true, true, time.Hour, 40, 10, false); err != nil {
		t.Fatal(err)
	}
	observer.observedSince = time.Now().Add(-61 * time.Second)
	if owned, err := observer.Presence(true, true, time.Hour, 40, 10, false); err != nil || !owned {
		t.Fatalf("stale owner: %v %v", owned, err)
	}
}
