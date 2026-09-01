package tty

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/testenv"
)

func TestControlManagerIsolatedTmuxSessionPool(t *testing.T) {
	testenv.RequireTmux(t)
	socket := fmt.Sprintf("/tmp/sidecar-control-test-%d-%d.sock", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	for _, session := range []string{"one", "two"} {
		if output, err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", session).CombinedOutput(); err != nil {
			t.Fatalf("create %s: %v: %s", session, err, output)
		}
	}
	paneOne := isolatedPaneID(t, socket, "one")
	paneTwo := isolatedPaneID(t, socket, "two")
	widthBefore, heightBefore := isolatedPaneSize(t, socket, paneOne)

	var factoryMu sync.Mutex
	factoryCalls := make(map[string]int)
	manager := newControlManager(func(session string) (controlChannel, error) {
		factoryMu.Lock()
		factoryCalls[session]++
		factoryMu.Unlock()
		return newProcessControlChannelForSocket(socket, session)
	}, 5*time.Millisecond)
	defer manager.Stop()

	var snapshotMu sync.Mutex
	snapshots := map[string][]ControlSnapshot{"one": nil, "two": nil}
	subscribe := func(session, pane string) *ControlSubscription {
		sub, err := manager.Subscribe(ControlRequest{
			Session: session,
			Pane:    pane,
			Visible: true,
			Focused: true,
			OnSnapshot: func(snapshot ControlSnapshot) {
				snapshotMu.Lock()
				snapshots[session] = append(snapshots[session], snapshot)
				snapshotMu.Unlock()
			},
			OnFallback: func(err error) {
				t.Errorf("%s unexpectedly fell back: %v", session, err)
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return sub
	}
	one := subscribe("one", paneOne)
	defer one.Close()
	two := subscribe("two", paneTwo)
	defer two.Close()

	waitFor(t, func() bool {
		snapshotMu.Lock()
		defer snapshotMu.Unlock()
		return len(snapshots["one"]) >= 1 && len(snapshots["two"]) >= 1
	})
	factoryMu.Lock()
	if factoryCalls["one"] != 1 || factoryCalls["two"] != 1 {
		t.Fatalf("factory calls = %#v, want one client per session", factoryCalls)
	}
	factoryMu.Unlock()

	sendIsolatedTmuxText(t, socket, paneOne, "CONTROL_ONE")
	sawControlOne := func() bool {
		snapshotMu.Lock()
		defer snapshotMu.Unlock()
		for _, snapshot := range snapshots["one"] {
			if strings.Contains(snapshot.Output, "CONTROL_ONE") {
				return true
			}
		}
		return false
	}
	deadline := time.Now().Add(time.Second)
	for !sawControlOne() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !sawControlOne() {
		snapshotMu.Lock()
		t.Fatalf("session one snapshots after output = %#v", snapshots["one"])
	}
	time.Sleep(20 * time.Millisecond)
	snapshotMu.Lock()
	twoSnaps := append([]ControlSnapshot(nil), snapshots["two"]...)
	snapshotMu.Unlock()
	for _, snapshot := range twoSnaps {
		if strings.Contains(snapshot.Output, "CONTROL_ONE") {
			t.Fatalf("session two captured output from session one: %q", snapshot.Output)
		}
	}

	sendIsolatedTmuxText(t, socket, paneTwo, "CONTROL_TWO")
	waitFor(t, func() bool {
		snapshotMu.Lock()
		defer snapshotMu.Unlock()
		for _, snapshot := range snapshots["two"] {
			if strings.Contains(snapshot.Output, "CONTROL_TWO") {
				return true
			}
		}
		return false
	})

	widthAfter, heightAfter := isolatedPaneSize(t, socket, paneOne)
	if widthAfter != widthBefore || heightAfter != heightBefore {
		t.Fatalf("ignore-size control client resized pane: before=%dx%d after=%dx%d",
			widthBefore, heightBefore, widthAfter, heightAfter)
	}
}

func TestControlBarrierUnsetsLeaseBeforeLastClientCloses(t *testing.T) {
	testenv.RequireTmux(t)
	socket := fmt.Sprintf("/tmp/sidecar-control-barrier-test-%d-%d.sock", os.Getpid(), time.Now().UnixNano())
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
	const session = "release-barrier"
	if output, err := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", session).CombinedOutput(); err != nil {
		t.Fatalf("create session: %v: %s", err, output)
	}
	pane := isolatedPaneID(t, socket, session)
	if output, err := exec.Command("tmux", "-S", socket, "set-option", "-t", session,
		leaseOptionName, "viewer:1:0").CombinedOutput(); err != nil {
		t.Fatalf("seed lease: %v: %s", err, output)
	}

	manager := newControlManager(func(string) (controlChannel, error) {
		return newProcessControlChannelForSocket(socket, session)
	}, 5*time.Millisecond)
	defer manager.Stop()
	sub, err := manager.Subscribe(ControlRequest{
		Session: session,
		Pane:    pane,
		Visible: true,
		Focused: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	waitFor(t, sub.UsingControl)
	if err := manager.sendControlBarrier(session,
		"set-option -u -t "+controlQuote(session)+" "+leaseOptionName); err != nil {
		t.Fatal(err)
	}
	// This is the reproduction: before the barrier lifetime ref, Close killed
	// the control process immediately after the pipe write and tmux could retain
	// the option indefinitely.
	sub.Close()
	waitFor(t, func() bool {
		output, err := exec.Command("tmux", "-S", socket, "show-options", "-v", "-t", session, leaseOptionName).Output()
		return err != nil || strings.TrimSpace(string(output)) == ""
	})
}

func isolatedPaneID(t *testing.T, socket, session string) string {
	t.Helper()
	output, err := exec.Command("tmux", "-S", socket, "display-message", "-p", "-t", session, "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	return strings.TrimSpace(string(output))
}

func isolatedPaneSize(t *testing.T, socket, pane string) (int, int) {
	t.Helper()
	output, err := exec.Command("tmux", "-S", socket, "display-message", "-p", "-t", pane, "#{pane_width},#{pane_height}").Output()
	if err != nil {
		t.Fatal(err)
	}
	parts := strings.Split(strings.TrimSpace(string(output)), ",")
	if len(parts) != 2 {
		t.Fatalf("pane size = %q", output)
	}
	width, _ := strconv.Atoi(parts[0])
	height, _ := strconv.Atoi(parts[1])
	return width, height
}

func sendIsolatedTmuxText(t *testing.T, socket, pane, text string) {
	t.Helper()
	if output, err := exec.Command("tmux", "-S", socket, "send-keys", "-l", "-t", pane, text).CombinedOutput(); err != nil {
		t.Fatalf("send text: %v: %s", err, output)
	}
	if output, err := exec.Command("tmux", "-S", socket, "send-keys", "-t", pane, "Enter").CombinedOutput(); err != nil {
		t.Fatalf("send enter: %v: %s", err, output)
	}
}
