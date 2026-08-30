package agentcontrol

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/tty"
)

type measuredRunner struct{ calls atomic.Int64 }

func (r *measuredRunner) Run(ctx context.Context, args ...string) ([]byte, error) {
	r.calls.Add(1)
	return execRunner{}.Run(ctx, args...)
}

func childCPU() time.Duration {
	var usage syscall.Rusage
	if err := syscall.Getrusage(syscall.RUSAGE_CHILDREN, &usage); err != nil {
		return 0
	}
	return time.Duration(usage.Utime.Sec+usage.Stime.Sec)*time.Second + time.Duration(usage.Utime.Usec+usage.Stime.Usec)*time.Microsecond
}

func waitUntil(t *testing.T, timeout time.Duration, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !condition() && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if !condition() {
		t.Fatal("condition did not become true")
	}
}

func sendBurst(t *testing.T, pane, marker string) {
	t.Helper()
	command := fmt.Sprintf("i=0; while [ $i -lt 250 ]; do printf 'burst-%%03d\\n' $i; i=$((i+1)); done; printf '%s\\n'", marker)
	if out, err := exec.Command("tmux", "send-keys", "-t", pane, command, "Enter").CombinedOutput(); err != nil {
		t.Fatalf("send burst: %v: %s", err, out)
	}
}

func parseCPUTime(value string) (time.Duration, error) {
	parts := strings.Split(value, ":")
	seconds := 0.0
	for _, part := range parts {
		value, err := strconv.ParseFloat(part, 64)
		if err != nil {
			return 0, err
		}
		seconds = seconds*60 + value
	}
	return time.Duration(seconds * float64(time.Second)), nil
}

func controlChildCPU() (time.Duration, bool) {
	out, err := exec.Command("ps", "-axo", "ppid=,time=,command=").Output()
	if err != nil {
		return 0, false
	}
	wantParent := strconv.Itoa(os.Getpid())
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) < 3 || fields[0] != wantParent || !strings.Contains(strings.Join(fields[2:], " "), "tmux -C attach-session") {
			continue
		}
		cpu, parseErr := parseCPUTime(fields[1])
		return cpu, parseErr == nil
	}
	return 0, false
}

func TestM0ObserverPollingVersusControlManagerMeasurement(t *testing.T) {
	if testing.Short() {
		t.Skip("measurement uses real isolated tmux")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	session := fmt.Sprintf("sidecar-agentcontrol-observer-%d", time.Now().UnixNano())
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session).CombinedOutput(); err != nil {
		t.Fatalf("new isolated session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })
	paneOut, err := exec.Command("tmux", "display-message", "-p", "-t", session, "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	pane := strings.TrimSpace(string(paneOut))
	target := Target{Host: "local", Project: "fixture", Session: session}

	// Bounded polling: every Inspect is exactly two short-lived tmux children
	// (metadata plus capture). Measure a 300 ms idle window and then the next
	// 100 ms cadence transition after a 250-line burst.
	runner := &measuredRunner{}
	pollTerminal := &LocalTerminal{Runner: runner, Now: time.Now}
	if _, err := pollTerminal.Inspect(context.Background(), target); err != nil {
		t.Fatal(err)
	}
	baselineCalls := runner.calls.Load()
	pollCPUStart := childCPU()
	for i := 0; i < 3; i++ {
		time.Sleep(100 * time.Millisecond)
		if _, err := pollTerminal.Inspect(context.Background(), target); err != nil {
			t.Fatal(err)
		}
	}
	pollIdleCPU := childCPU() - pollCPUStart
	pollIdleSpawns := runner.calls.Load() - baselineCalls

	pollMarker := "POLL_BURST_COMPLETE_250"
	sendBurst(t, pane, pollMarker)
	pollStarted := time.Now()
	pollBurstSpawns := int64(0)
	for {
		time.Sleep(100 * time.Millisecond)
		before := runner.calls.Load()
		snapshot, inspectErr := pollTerminal.Inspect(context.Background(), target)
		pollBurstSpawns += runner.calls.Load() - before
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if strings.Contains(snapshot.Screen, pollMarker) {
			break
		}
		if time.Since(pollStarted) > 2*time.Second {
			t.Fatal("polling missed output burst")
		}
	}
	pollLatency := time.Since(pollStarted)

	// Control mode: one persistent tmux -C child owns the subscription. The
	// idle window should produce no snapshots, and the same burst should arrive
	// without another process spawn.
	manager := tty.NewControlManager()
	defer manager.Stop()
	var snapshotMu sync.Mutex
	controlSnapshots := 0
	latest := ""
	fallback := make(chan error, 1)
	sub, err := manager.Subscribe(tty.ControlRequest{Session: session, Pane: pane, Scrollback: 300, Visible: true, Focused: true, OnSnapshot: func(snapshot tty.ControlSnapshot) {
		snapshotMu.Lock()
		controlSnapshots++
		latest = snapshot.Output
		snapshotMu.Unlock()
	}, OnFallback: func(err error) {
		select {
		case fallback <- err:
		default:
		}
	}})
	if err != nil {
		t.Fatal(err)
	}
	defer sub.Close()
	waitUntil(t, 2*time.Second, func() bool {
		snapshotMu.Lock()
		defer snapshotMu.Unlock()
		return controlSnapshots > 0
	})
	// The control client may coalesce more than one startup notification. Let
	// that finite startup burst settle before defining the idle-window baseline;
	// otherwise a delayed initial callback is miscounted as idle activity under
	// load in the full repository suite.
	settleDeadline := time.Now().Add(2 * time.Second)
	for {
		snapshotMu.Lock()
		before := controlSnapshots
		snapshotMu.Unlock()
		time.Sleep(50 * time.Millisecond)
		snapshotMu.Lock()
		after := controlSnapshots
		snapshotMu.Unlock()
		if after == before {
			break
		}
		if time.Now().After(settleDeadline) {
			t.Fatal("control startup snapshots did not settle")
		}
	}
	controlCPUStart, controlCPUFound := controlChildCPU()
	snapshotMu.Lock()
	idleBefore := controlSnapshots
	snapshotMu.Unlock()
	time.Sleep(300 * time.Millisecond)
	controlCPUEnd, controlCPUEndFound := controlChildCPU()
	controlIdleCPU := controlCPUEnd - controlCPUStart
	snapshotMu.Lock()
	controlIdleSnapshots := controlSnapshots - idleBefore
	snapshotMu.Unlock()
	select {
	case err := <-fallback:
		t.Fatalf("control fallback: %v", err)
	default:
	}

	controlMarker := "CONTROL_BURST_COMPLETE_250"
	controlStarted := time.Now()
	sendBurst(t, pane, controlMarker)
	waitUntil(t, 2*time.Second, func() bool {
		snapshotMu.Lock()
		defer snapshotMu.Unlock()
		return strings.Contains(latest, controlMarker)
	})
	controlLatency := time.Since(controlStarted)

	// These values are deliberately logged in a stable key=value form so the
	// controlling plan can cite an exact run. Control has one startup process
	// and zero per-observation spawns by construction; polling's runner counts
	// each actual tmux child.
	t.Logf("poll_interval_ms=100 poll_idle_window_ms=300 poll_idle_spawns=%d poll_idle_child_cpu_us=%d poll_burst_lines=250 poll_burst_latency_ms=%d poll_burst_spawns=%d control_clients=1 control_idle_window_ms=300 control_idle_cpu_us=%d control_idle_snapshots=%d control_burst_lines=250 control_burst_latency_ms=%d control_burst_spawns=0", pollIdleSpawns, pollIdleCPU.Microseconds(), pollLatency.Milliseconds(), pollBurstSpawns, controlIdleCPU.Microseconds(), controlIdleSnapshots, controlLatency.Milliseconds())
	if pollIdleSpawns != 6 {
		t.Fatalf("poll idle spawned %d tmux children, want 6", pollIdleSpawns)
	}
	if pollBurstSpawns < 2 || controlIdleSnapshots != 0 {
		t.Fatalf("measurement invariant: poll burst spawns=%d control idle snapshots=%d", pollBurstSpawns, controlIdleSnapshots)
	}
	if !controlCPUFound || !controlCPUEndFound || controlIdleCPU < 0 {
		t.Fatalf("could not measure persistent control client CPU: before=%v after=%v delta=%v", controlCPUFound, controlCPUEndFound, controlIdleCPU)
	}
	if _, err := strconv.Atoi(strings.TrimPrefix(pane, "%")); err != nil {
		t.Fatalf("pane was not pinned: %q", pane)
	}
}
