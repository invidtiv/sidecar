package mobile

import (
	"github.com/marcus/sidecar/internal/tty"
	"testing"
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
	old, err := adapter.ClaimGeometry(identity, "old", 40, 10)
	if err != nil {
		t.Fatal(err)
	}
	current, err := adapter.ClaimGeometry(identity, "current", 40, 10)
	if err != nil {
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
	if len(oneFrames) != 2 || oneFrames[1].Output != "echo" || oneFrames[1].PaneWidth != 40 {
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
