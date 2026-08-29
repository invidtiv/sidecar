package tty

// Phase 0 spike harness for proxied tmux control mode over SSH.
//
// This is the measurement rig behind the plan's only existential question:
// does a pane served by a remote tmux, over a real link, behave and feel like
// a local one? It lives in package tty because the seams it exercises —
// newControlManager's factory, the ordered actor, the byte-fed screen model —
// are unexported, and reaching them through an exported shim would be
// measuring the shim.
//
// It is skipped unless SIDECAR_SPIKE_HOST names an ssh target, so an ordinary
// `go test ./...` never touches the network or another machine.
//
//	SIDECAR_SPIKE_HOST=marcusbook \
//	SIDECAR_SPIKE_SOCKET=/tmp/sidecar-spike-marcus/tmux/tmux-501/default \
//	SIDECAR_SPIKE_SESSION=spike-codex \
//	go test ./internal/tty -run TestRemoteControlSpike -v -timeout 300s
//
// SIDECAR_SPIKE_SOCKET is required, not optional. Attaching to a remote
// machine's DEFAULT tmux server would put this harness on top of live agent
// sessions belonging to a person.

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

type spikeTarget struct {
	host    string
	socket  string
	session string
	label   string
}

func spikeTargetFromEnv(t *testing.T) spikeTarget {
	t.Helper()
	host := os.Getenv("SIDECAR_SPIKE_HOST")
	if host == "" {
		t.Skip("SIDECAR_SPIKE_HOST unset; remote spike is opt-in")
	}
	socket := os.Getenv("SIDECAR_SPIKE_SOCKET")
	if socket == "" {
		t.Fatal("SIDECAR_SPIKE_SOCKET must name the isolated remote tmux socket; refusing to attach to a default server")
	}
	if !strings.HasPrefix(socket, "/tmp/") && !strings.HasPrefix(socket, "/private/tmp/") {
		t.Fatalf("refusing socket %q: an isolated spike socket must live under /tmp", socket)
	}
	session := os.Getenv("SIDECAR_SPIKE_SESSION")
	if session == "" {
		session = "spike-codex"
	}
	label := os.Getenv("SIDECAR_SPIKE_LABEL")
	if label == "" {
		label = host
	}
	return spikeTarget{host: host, socket: socket, session: session, label: label}
}

// sshArgs is the transport recipe internal/hosts builds for the app, repeated
// here so the harness measures the same connection the product would use.
func (s spikeTarget) sshArgs() []string {
	args := []string{
		"-T",
		"-o", "ControlMaster=auto",
		"-o", "ControlPath=/tmp/sidecar-spike-ctl-" + fmt.Sprint(os.Getuid()) + "/ctl-%C",
		"-o", "ControlPersist=300",
		"-o", "ServerAliveInterval=15",
		"-o", "ServerAliveCountMax=4",
		"-o", "BatchMode=yes",
	}
	// SIDECAR_SPIKE_SSH_EXTRA carries whatever the shaped-latency column needs
	// — a port, a user, relaxed host-key checking for the loopback listener.
	// Splitting on whitespace is enough: these are flags, not paths with
	// spaces, and the alternative is a second env var per option.
	if extra := os.Getenv("SIDECAR_SPIKE_SSH_EXTRA"); extra != "" {
		args = append(args, strings.Fields(extra)...)
	}
	return args
}

// controlCommand builds the remote attach. The tmux binary is addressed
// absolutely because a non-interactive ssh shell has no Homebrew on PATH — the
// single most common way this whole feature appears broken on a stock macOS
// host.
func (s spikeTarget) controlCommand(session string) *exec.Cmd {
	remote := fmt.Sprintf("exec %s -S %s -C attach-session -f ignore-size -t %s",
		s.remoteTmux(), shellWord(s.socket), shellWord(session))
	args := append(s.sshArgs(), s.host, remote)
	return exec.Command("ssh", args...) //nolint:gosec
}

func (s spikeTarget) remoteTmux() string {
	if path := os.Getenv("SIDECAR_SPIKE_TMUX"); path != "" {
		return path
	}
	return "/opt/homebrew/bin/tmux"
}

// run executes a one-shot tmux command on the remote isolated server.
func (s spikeTarget) run(t *testing.T, args ...string) string {
	t.Helper()
	remote := fmt.Sprintf("%s -S %s %s", s.remoteTmux(), shellWord(s.socket), strings.Join(args, " "))
	full := append(s.sshArgs(), s.host, remote)
	out, err := exec.Command("ssh", full...).CombinedOutput() //nolint:gosec
	if err != nil {
		t.Fatalf("remote tmux %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func shellWord(s string) string {
	if s == "" {
		return "''"
	}
	if !strings.ContainsAny(s, " \t\n\"'\\$`&;|<>()*?[]{}~#!") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// countingChannel wraps a control channel to account for bytes and events
// without disturbing ordering. It is the only way to get honest wire numbers:
// the manager owns the pipes, so accounting has to sit at the channel seam.
type countingChannel struct {
	inner  controlChannel
	events chan controlEvent

	bytesIn   atomic.Int64
	outputs   atomic.Int64
	responses atomic.Int64
	commands  atomic.Int64
}

func newCountingChannel(inner controlChannel) *countingChannel {
	c := &countingChannel{inner: inner, events: make(chan controlEvent, 256)}
	go func() {
		defer close(c.events)
		for event := range inner.Events() {
			switch event.Kind {
			case controlEventOutput:
				c.outputs.Add(1)
				// Payload is the still-escaped %output substring, which is
				// exactly what crossed the wire — decoding it here would
				// under-report the real cost of the link.
				c.bytesIn.Add(int64(len(event.Payload)))
			case controlEventResponse:
				c.responses.Add(1)
				for _, line := range event.Response.Lines {
					c.bytesIn.Add(int64(len(line)) + 1)
				}
			}
			c.events <- event
		}
	}()
	return c
}

func (c *countingChannel) Send(command string, cb func(controlResponse)) error {
	c.commands.Add(1)
	return c.inner.Send(command, cb)
}

func (c *countingChannel) SendPair(a, b string, ca, cb func(controlResponse)) error {
	c.commands.Add(2)
	return c.inner.SendPair(a, b, ca, cb)
}

func (c *countingChannel) SendTriple(a, b, d string, ca, cb, cd func(controlResponse)) error {
	c.commands.Add(3)
	return c.inner.SendTriple(a, b, d, ca, cb, cd)
}

func (c *countingChannel) Events() <-chan controlEvent { return c.events }
func (c *countingChannel) Done() <-chan error          { return c.inner.Done() }
func (c *countingChannel) Close() error                { return c.inner.Close() }

// TestRemoteControlSpike is Phase 0 items 1, 3 and 4: attach and seed cost,
// byte continuity, idle cost, output-burst throughput, resize cost, and
// in-band input round trip — all through the production control stack with
// only the spawned command changed.
func TestRemoteControlSpike(t *testing.T) {
	target := spikeTargetFromEnv(t)

	var counter atomic.Pointer[countingChannel]
	factory := func(session string) (controlChannel, error) {
		inner, err := newProcessControlChannelCommand(session, target.controlCommand(session))
		if err != nil {
			return nil, err
		}
		wrapped := newCountingChannel(inner)
		counter.Store(wrapped)
		return wrapped, nil
	}

	// Start from a known-empty pane. Seed cost is proportional to what is on
	// the screen and in the scrollback — a pane left full of a previous run's
	// output seeded at 465 KB where an empty one seeds at 294 bytes — so
	// without this the first number measured depends on what ran last.
	target.run(t, "clear-history", "-t", shellWord(target.session))
	target.run(t, "send-keys", "-t", shellWord(target.session), "-l", shellWord("clear"))
	target.run(t, "send-keys", "-t", shellWord(target.session), "Enter")
	time.Sleep(700 * time.Millisecond)
	target.run(t, "clear-history", "-t", shellWord(target.session))

	manager := newControlManager(factory, 12*time.Millisecond)
	defer manager.Stop()

	var (
		mu        sync.Mutex
		frames    int
		seeds     int
		lastFrame time.Time
		firstSeen time.Time
		fallback  error
	)
	frameSignal := make(chan struct{}, 64)

	attachStart := time.Now()
	sub, err := manager.Subscribe(ControlRequest{
		Session:           target.session,
		Pane:              target.run(t, "display-message", "-p", "-t", shellWord(target.session), "'#{pane_id}'"),
		Width:             120,
		Height:            40,
		Scrollback:        2000,
		Visible:           true,
		Focused:           true,
		ModelPresentation: true,
		OnModelFrame: func(f ModelFrame) {
			mu.Lock()
			frames++
			if firstSeen.IsZero() {
				firstSeen = time.Now()
			}
			if f.Seeds > seeds {
				seeds = f.Seeds
			}
			lastFrame = time.Now()
			mu.Unlock()
			select {
			case frameSignal <- struct{}{}:
			default:
			}
		},
		OnFallback: func(e error) {
			mu.Lock()
			fallback = e
			mu.Unlock()
		},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	waitFrame := func(timeout time.Duration) bool {
		deadline := time.After(timeout)
		for {
			select {
			case <-frameSignal:
				return true
			case <-deadline:
				return false
			}
		}
	}

	if !waitFrame(30 * time.Second) {
		mu.Lock()
		e := fallback
		mu.Unlock()
		t.Fatalf("no model frame within 30s (fallback=%v)", e)
	}
	mu.Lock()
	seedLatency := firstSeen.Sub(attachStart)
	mu.Unlock()
	channel := counter.Load()
	seedBytes := channel.bytesIn.Load()

	report := func(format string, args ...any) {
		t.Logf("SPIKE[%s] "+format, append([]any{target.label}, args...)...)
	}
	report("attach+seed: %v, %d bytes, %d commands", seedLatency.Round(time.Millisecond), seedBytes, channel.commands.Load())

	// --- Idle cost. The plan asserts an idle pane should cost zero bytes.
	idleStart := channel.bytesIn.Load()
	idleOutputs := channel.outputs.Load()
	time.Sleep(5 * time.Second)
	idleBytes := channel.bytesIn.Load() - idleStart
	report("idle 5s: %d bytes, %d output notifications", idleBytes, channel.outputs.Load()-idleOutputs)

	// --- Output burst. Type a generator into the pane and measure how long the
	// local model takes to converge and what the link cost.
	//
	// The command is typed into a real interactive shell rather than injected
	// with `tmux run-shell`: run-shell executes beside the pane, not in it, so
	// it produces no %output at all — which is why an earlier version of this
	// measurement reported 16 bytes and looked like a fast link rather than an
	// absent one.
	const burstBytes = 256 * 1024
	burstStart := channel.bytesIn.Load()
	mu.Lock()
	framesBefore := frames
	mu.Unlock()
	sentAt := time.Now()
	target.run(t, "send-keys", "-t", shellWord(target.session), "-l",
		shellWord(fmt.Sprintf("head -c %d /dev/urandom | base64", burstBytes)))
	target.run(t, "send-keys", "-t", shellWord(target.session), "Enter")

	quiet := 0
	var burstEnd time.Time
	for deadline := time.Now().Add(60 * time.Second); time.Now().Before(deadline); {
		if !waitFrame(1500 * time.Millisecond) {
			quiet++
			if quiet >= 2 {
				break
			}
			continue
		}
		quiet = 0
		mu.Lock()
		burstEnd = lastFrame
		mu.Unlock()
	}
	burstWire := channel.bytesIn.Load() - burstStart
	mu.Lock()
	burstFrames := frames - framesBefore
	mu.Unlock()
	if !burstEnd.IsZero() && burstEnd.After(sentAt) {
		elapsed := burstEnd.Sub(sentAt)
		report("burst: %d generated -> %d wire bytes in %v (%.0f KiB/s wire), %d frames (%.1f fps)",
			burstBytes, burstWire, elapsed.Round(time.Millisecond),
			float64(burstWire)/elapsed.Seconds()/1024,
			burstFrames, float64(burstFrames)/elapsed.Seconds())
	} else {
		report("burst: %d wire bytes, no convergence observed", burstWire)
	}

	// --- Resize cost. assertDimensions restarts the control transport on every
	// geometry change; over ssh that is a remote process respawn. This is the
	// number that decides whether reseed-without-restart is Phase A work.
	for _, size := range [][2]int{{100, 30}, {120, 40}} {
		mu.Lock()
		seedsBefore := seeds
		mu.Unlock()
		resizeStart := time.Now()
		sub.Resize(size[0], size[1])
		reseeded := false
		for deadline := time.Now().Add(20 * time.Second); time.Now().Before(deadline); {
			if !waitFrame(500 * time.Millisecond) {
				continue
			}
			mu.Lock()
			got := seeds
			mu.Unlock()
			if got > seedsBefore {
				reseeded = true
				break
			}
		}
		report("resize to %dx%d: reseed=%v in %v", size[0], size[1], reseeded,
			time.Since(resizeStart).Round(time.Millisecond))
	}

	// --- In-band input round trip. This is the Phase B sender's bottom half: a
	// send-keys written to the already-open control channel rather than a fresh
	// `ssh tmux send-keys` per keystroke.
	//
	// It runs on its own channel rather than the manager's. The manager restarts
	// its transport on every geometry change, so a handle captured before a
	// resize is attached to a dead process — which shows up as a send that never
	// gets a response, and reads like a protocol bug rather than a stale handle.
	// A separate channel also keeps the measurement free of the manager's own
	// traffic.
	pane := target.run(t, "display-message", "-p", "-t", shellWord(target.session), "'#{pane_id}'")
	inband, err := newProcessControlChannelCommand(target.session, target.controlCommand(target.session))
	if err != nil {
		t.Fatalf("in-band channel: %v", err)
	}
	defer func() { _ = inband.Close() }()
	// A control channel's events must be drained or the transport blocks.
	inbandDone := make(chan struct{})
	go func() {
		defer close(inbandDone)
		for event := range inband.Events() {
			if event.Callback != nil {
				event.Callback(event.Response)
			}
		}
	}()

	// Quiet the shell first so the pane is not echoing the previous burst.
	target.run(t, "send-keys", "-t", shellWord(pane), "-l", shellWord("clear"))
	target.run(t, "send-keys", "-t", shellWord(pane), "Enter")
	time.Sleep(500 * time.Millisecond)

	const batches = 20
	var total, worst time.Duration
	best := time.Hour
	for i := 0; i < batches; i++ {
		done := make(chan struct{})
		start := time.Now()
		// The response to send-keys IS the round trip: tmux has read, parsed and
		// executed the command by the time it answers.
		if err := inband.Send(InBandSendLiteral(pane, fmt.Sprintf("IB%03d", i)), func(controlResponse) { close(done) }); err != nil {
			t.Fatalf("in-band send %d: %v", i, err)
		}
		select {
		case <-done:
		case <-time.After(15 * time.Second):
			t.Fatalf("in-band send %d: no response in 15s", i)
		}
		elapsed := time.Since(start)
		total += elapsed
		if elapsed > worst {
			worst = elapsed
		}
		if elapsed < best {
			best = elapsed
		}
	}
	report("in-band send-keys RTT: %d batches, mean %v, best %v, worst %v",
		batches, (total / batches).Round(100*time.Microsecond), best.Round(100*time.Microsecond), worst.Round(100*time.Microsecond))

	// --- Out-of-band comparison: one `ssh tmux send-keys` per batch, which is
	// what the unmodified local sender would do if pointed at a remote host.
	// This is the number that justifies building an in-band sender at all.
	oobStart := time.Now()
	const oobBatches = 5
	for i := 0; i < oobBatches; i++ {
		target.run(t, "send-keys", "-t", shellWord(pane), "-l", shellWord(fmt.Sprintf("OOB%03d", i)))
	}
	report("out-of-band send-keys RTT: %d batches, mean %v (one ssh exec each)",
		oobBatches, (time.Since(oobStart) / oobBatches).Round(100*time.Microsecond))

	// --- FIFO ordering under fast typing. Every batch goes out back to back
	// with no waiting; the pane must show them in order.
	target.run(t, "send-keys", "-t", shellWord(pane), "-l", shellWord("clear"))
	target.run(t, "send-keys", "-t", shellWord(pane), "Enter")
	time.Sleep(500 * time.Millisecond)
	var ordered []string
	for i := 0; i < 40; i++ {
		token := fmt.Sprintf("%02d", i)
		ordered = append(ordered, token)
		if err := inband.Send(InBandSendLiteral(pane, token), func(controlResponse) {}); err != nil {
			t.Fatalf("fifo send %d: %v", i, err)
		}
	}
	time.Sleep(3 * time.Second)
	painted := target.run(t, "capture-pane", "-p", "-t", shellWord(pane))
	wanted := strings.Join(ordered, "")
	if !strings.Contains(strings.ReplaceAll(painted, "\n", ""), wanted) {
		report("FIFO: NOT preserved — wanted %q, pane tail %q", wanted, tailOf(painted, 120))
		t.Errorf("in-band FIFO ordering not preserved")
	} else {
		report("FIFO: preserved across %d back-to-back in-band batches", len(ordered))
	}

	mu.Lock()
	report("totals: %d frames, %d seeds, %d output notifications, %d wire bytes, fallback=%v",
		frames, seeds, channel.outputs.Load(), channel.bytesIn.Load(), fallback)
	mu.Unlock()
}

func tailOf(s string, n int) string {
	s = strings.TrimSpace(strings.ReplaceAll(s, "\n", ""))
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

// TestRemoteControlDropSpike is Phase 0 item 5's link-failure axis: an ssh
// connection that dies mid-stream must surface as a fallback, not as a pane
// that silently stops updating while still looking live.
//
// The drop is simulated by killing the ssh child directly, which is what a
// severed link looks like to this process once ssh's own keepalives give up.
// The keepalive settings decide how long that takes on a real network
// (ServerAliveInterval 15 x CountMax 4 = a one-minute detection floor); this
// test measures the local reaction, not the network's detection time.
func TestRemoteControlDropSpike(t *testing.T) {
	target := spikeTargetFromEnv(t)

	var spawned []*exec.Cmd
	var spawnMu sync.Mutex
	factory := func(session string) (controlChannel, error) {
		cmd := target.controlCommand(session)
		spawnMu.Lock()
		spawned = append(spawned, cmd)
		spawnMu.Unlock()
		return newProcessControlChannelCommand(session, cmd)
	}

	manager := newControlManager(factory, 12*time.Millisecond)
	defer manager.Stop()

	frames := make(chan struct{}, 16)
	fallbacks := make(chan error, 4)
	sub, err := manager.Subscribe(ControlRequest{
		Session:           target.session,
		Pane:              target.run(t, "display-message", "-p", "-t", shellWord(target.session), "'#{pane_id}'"),
		Width:             120,
		Height:            40,
		Scrollback:        2000,
		Visible:           true,
		Focused:           true,
		ModelPresentation: true,
		OnModelFrame: func(ModelFrame) {
			select {
			case frames <- struct{}{}:
			default:
			}
		},
		OnFallback: func(e error) {
			select {
			case fallbacks <- e:
			default:
			}
		},
	})
	if err != nil {
		t.Fatalf("subscribe: %v", err)
	}
	defer sub.Close()

	select {
	case <-frames:
	case <-time.After(30 * time.Second):
		t.Fatal("no frame before the drop; nothing to drop")
	}

	spawnMu.Lock()
	victim := spawned[len(spawned)-1]
	spawnMu.Unlock()
	if victim.Process == nil {
		t.Fatal("no ssh process to kill")
	}
	killedAt := time.Now()
	if err := victim.Process.Kill(); err != nil {
		t.Fatalf("kill ssh: %v", err)
	}

	select {
	case reason := <-fallbacks:
		t.Logf("SPIKE[%s] ssh drop: fallback engaged in %v (%v)",
			target.label, time.Since(killedAt).Round(time.Millisecond), reason)
	case <-time.After(30 * time.Second):
		t.Fatal("ssh died and no fallback engaged within 30s: a dead pane would keep looking live")
	}

	// Reattaching must work: a dropped link is a recoverable condition, not a
	// terminal one. Toggling visibility is what the UI does when a pane scrolls
	// out of view and back.
	sub.SetVisible(false)
	time.Sleep(500 * time.Millisecond)
	sub.SetVisible(true)
	select {
	case <-frames:
		t.Logf("SPIKE[%s] ssh drop: reattached cleanly after the drop", target.label)
	case <-time.After(40 * time.Second):
		t.Error("no frame after reattach; the host never recovers from a link drop")
	}
}
