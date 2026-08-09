package tty

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"
)

// The byte-fed screen model's bootstrap contract can only be proved against a
// real tmux: the whole question is what tmux's command/notification boundary
// guarantees. Every tmux invocation in this file — including teardown — carries
// an explicit -S inside the test's own temp dir and runs with TMUX scrubbed, so
// nothing here can reach the developer's live default server.
//
// The proof shape is the plan's: a pane writes continuously numbered lines while
// the seed transaction runs, and the resulting model must contain every number
// exactly once, in order, with no gap.

type modelTmux struct {
	t       *testing.T
	sock    string
	conf    string
	session string
	pane    string
}

const modelTmuxConf = `set -g status off
set -g default-terminal "tmux-256color"
set -g history-limit 5000
`

func startModelTmux(t *testing.T) *modelTmux {
	t.Helper()
	if testing.Short() {
		t.Skip("skipping tmux integration in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	root, err := os.MkdirTemp("", "scmodel")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	sock := filepath.Join(root, "s")
	if len(sock) > 100 {
		t.Fatalf("socket path too long for a unix socket: %q", sock)
	}
	conf := filepath.Join(root, "tmux.conf")
	if err := os.WriteFile(conf, []byte(modelTmuxConf), 0o600); err != nil {
		t.Fatalf("write tmux.conf: %v", err)
	}
	s := &modelTmux{t: t, sock: sock, conf: conf, session: "model"}
	t.Cleanup(func() { _ = s.cmd("kill-server").Run() })
	s.run("new-session", "-d", "-s", s.session, "-x", "80", "-y", "24")
	s.pane = strings.TrimSpace(s.run("display-message", "-p", "-t", s.session, "#{pane_id}"))
	if !controlPanePattern.MatchString(s.pane) {
		t.Fatalf("pane id = %q", s.pane)
	}
	return s
}

func (s *modelTmux) cmd(args ...string) *exec.Cmd {
	full := append([]string{"-f", s.conf, "-S", s.sock}, args...)
	c := exec.Command("tmux", full...) //nolint:gosec
	c.Env = append(os.Environ(), "TMUX=")
	return c
}

func (s *modelTmux) run(args ...string) string {
	s.t.Helper()
	out, err := s.cmd(args...).CombinedOutput()
	if err != nil {
		s.t.Fatalf("tmux %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// startWriter makes the pane emit continuously numbered lines. delay is a shell
// sleep argument; "" means as fast as the shell can go.
func (s *modelTmux) startWriter(delay string) {
	s.t.Helper()
	sleep := ""
	if delay != "" {
		sleep = "; sleep " + delay
	}
	script := "clear; i=0; while [ $i -lt 200000 ]; do printf 'L%06d\\n' $i; i=$((i+1))" + sleep + "; done"
	s.run("send-keys", "-t", s.pane, "-l", script)
	s.run("send-keys", "-t", s.pane, "Enter")
}

func (s *modelTmux) stopWriter() {
	s.t.Helper()
	s.run("send-keys", "-t", s.pane, "C-c")
}

var modelNumberPattern = regexp.MustCompile(`(?m)^L(\d{6})\s*$`)

// numbersIn extracts the numbered lines from a rendered screen, ignoring the
// shell prompt, the echoed command, and any styling.
func numbersIn(output string) []int {
	stripped := ansiEscapePattern.ReplaceAllString(output, "")
	matches := modelNumberPattern.FindAllStringSubmatch(stripped, -1)
	out := make([]int, 0, len(matches))
	for _, match := range matches {
		value, err := strconv.Atoi(match[1])
		if err != nil {
			continue
		}
		out = append(out, value)
	}
	return out
}

var ansiEscapePattern = regexp.MustCompile("\x1b\\[[0-9;:?]*[ -/]*[@-~]|\x1b\\][^\x07\x1b]*(\x07|\x1b\\\\)")

// assertContinuous is the slice-1 exit criterion in one function: within the
// window the model retains, every number appears exactly once and consecutively.
// A duplicated byte shows up as a repeat; an omitted byte shows up as a gap.
func assertContinuous(t *testing.T, label string, numbers []int) {
	t.Helper()
	if len(numbers) < 2 {
		t.Fatalf("%s: only %d numbered lines in the model", label, len(numbers))
	}
	for i := 1; i < len(numbers); i++ {
		if numbers[i] == numbers[i-1] {
			t.Fatalf("%s: number %d duplicated at index %d", label, numbers[i], i)
		}
		if numbers[i] != numbers[i-1]+1 {
			t.Fatalf("%s: gap between %d and %d at index %d", label, numbers[i-1], numbers[i], i)
		}
	}
}

type modelHarness struct {
	tmux     *modelTmux
	manager  *ControlManager
	recorder *modelRecorder

	mu        sync.Mutex
	fallbacks []error
	channels  []controlChannel
	slow      time.Duration
}

func newModelHarness(t *testing.T) *modelHarness {
	t.Helper()
	tmuxServer := startModelTmux(t)
	h := &modelHarness{tmux: tmuxServer, recorder: &modelRecorder{}}
	h.manager = newControlManager(func(session string) (controlChannel, error) {
		channel, err := newProcessControlChannelForSocket(tmuxServer.sock, session)
		if err == nil {
			h.mu.Lock()
			h.channels = append(h.channels, channel)
			h.mu.Unlock()
		}
		return channel, err
	}, 10*time.Millisecond)
	t.Cleanup(h.manager.Stop)
	return h
}

func (h *modelHarness) subscribe(t *testing.T) *ControlSubscription {
	t.Helper()
	sub, err := h.manager.Subscribe(ControlRequest{
		Session: h.tmux.session, Pane: h.tmux.pane, Visible: true, Focused: true,
		Scrollback: 600,
		OnSnapshot: func(ControlSnapshot) {},
		OnFallback: func(err error) {
			h.mu.Lock()
			h.fallbacks = append(h.fallbacks, err)
			h.mu.Unlock()
		},
		OnModelFrame: func(frame ModelFrame) {
			h.mu.Lock()
			slow := h.slow
			h.mu.Unlock()
			if slow > 0 {
				time.Sleep(slow)
			}
			h.recorder.onFrame(frame)
		},
		OnModelInvalid: h.recorder.onInvalid,
	})
	if err != nil {
		t.Fatal(err)
	}
	return sub
}

func waitUntil(t *testing.T, timeout time.Duration, what string, condition func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if condition() {
			return
		}
		time.Sleep(2 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s", what)
}

// waitForFrames waits for a frame whose numbers advance past the current tail.
func (h *modelHarness) waitForAdvance(t *testing.T, timeout time.Duration, what string) []int {
	t.Helper()
	start := h.tailNumber()
	var numbers []int
	waitUntil(t, timeout, what, func() bool {
		frame, ok := h.recorder.lastFrame()
		if !ok {
			return false
		}
		numbers = numbersIn(frame.Frame.Output)
		return len(numbers) > 1 && numbers[len(numbers)-1] > start
	})
	return numbers
}

func (h *modelHarness) tailNumber() int {
	frame, ok := h.recorder.lastFrame()
	if !ok {
		return -1
	}
	numbers := numbersIn(frame.Frame.Output)
	if len(numbers) == 0 {
		return -1
	}
	return numbers[len(numbers)-1]
}

// Attach: the model is seeded while the pane is actively writing. The seed cut
// must include every byte tmux already rendered and exclude none of the rest.
func TestModelAttachMidStreamLosesNoBytes(t *testing.T) {
	h := newModelHarness(t)
	h.tmux.startWriter("0.01")
	// Let the pane get well ahead so the seed transaction runs mid-stream.
	waitUntil(t, 15*time.Second, "the pane to start writing", func() bool {
		return len(numbersIn(h.tmux.run("capture-pane", "-p", "-t", h.tmux.pane))) > 10
	})
	beforeAttach := lastNumber(numbersIn(h.tmux.run("capture-pane", "-p", "-t", h.tmux.pane)))
	sub := h.subscribe(t)
	defer sub.Close()

	numbers := h.waitForAdvance(t, 15*time.Second, "first model frames after attach")
	assertContinuous(t, "attach", numbers)
	// The retained window must straddle the seed cut: lines tmux had already
	// rendered before the seed, and lines that only arrived as replayed bytes.
	if numbers[0] > beforeAttach || lastNumber(numbers) <= beforeAttach {
		t.Fatalf("model window [%d,%d] does not straddle the attach point %d",
			numbers[0], lastNumber(numbers), beforeAttach)
	}

	// Independent oracle: with the pane quiescent, tmux's own capture and the
	// model must agree on the tail.
	h.tmux.stopWriter()
	time.Sleep(500 * time.Millisecond)
	var modelTail, tmuxTail []int
	waitUntil(t, 10*time.Second, "model and tmux to agree", func() bool {
		frame, ok := h.recorder.lastFrame()
		if !ok {
			return false
		}
		modelTail = lastN(numbersIn(frame.Frame.Output), 10)
		tmuxTail = lastN(numbersIn(h.tmux.run("capture-pane", "-p", "-t", h.tmux.pane)), 10)
		return len(modelTail) == 10 && equalInts(modelTail, tmuxTail)
	})
}

func lastN(values []int, n int) []int {
	if len(values) <= n {
		return values
	}
	return values[len(values)-n:]
}

func equalInts(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// Resize: tmux is resized first, the model reseeds from the authoritative
// resulting geometry, and the byte stream stays continuous across the boundary.
func TestModelResizeReseedsAndLosesNoBytes(t *testing.T) {
	h := newModelHarness(t)
	h.tmux.startWriter("0.01")
	sub := h.subscribe(t)
	defer sub.Close()
	numbers := h.waitForAdvance(t, 15*time.Second, "frames before resize")
	assertContinuous(t, "pre-resize", numbers)
	seedsBefore := h.seeds()

	sub.Resize(100, 30)
	waitUntil(t, 15*time.Second, "a reseed after resize", func() bool { return h.seeds() > seedsBefore })
	numbers = h.waitForAdvance(t, 15*time.Second, "frames after resize")
	assertContinuous(t, "post-resize", numbers)

	frame, _ := h.recorder.lastFrame()
	width, height := h.paneSize()
	if frame.Frame.Width != width || frame.Frame.Height != height {
		t.Fatalf("model geometry %dx%d, tmux geometry %dx%d",
			frame.Frame.Width, frame.Frame.Height, width, height)
	}
	if !h.sawReason(ResyncResize) && !h.sawReason(ResyncLayout) {
		t.Fatalf("resize did not report a resync: %v", h.recorder.reasons())
	}
}

func (h *modelHarness) paneSize() (int, int) {
	out := strings.TrimSpace(h.tmux.run("display-message", "-p", "-t", h.tmux.pane, "#{pane_width},#{pane_height}"))
	parts := strings.Split(out, ",")
	if len(parts) != 2 {
		h.tmux.t.Fatalf("pane size = %q", out)
	}
	width, _ := strconv.Atoi(parts[0])
	height, _ := strconv.Atoi(parts[1])
	return width, height
}

func (h *modelHarness) seeds() int {
	frame, ok := h.recorder.lastFrame()
	if !ok {
		return 0
	}
	return frame.Seeds
}

func (h *modelHarness) sawReason(reason ResyncReason) bool {
	for _, seen := range h.recorder.reasons() {
		if seen == reason {
			return true
		}
	}
	return false
}

// Pause/continue: a consumer slow enough to make tmux pause the pane must end up
// resynchronized rather than showing a stitched-together screen. tmux drops the
// pane's buffered output while paused, so the recovery is a fresh seed and the
// resulting frame must still be internally continuous.
func TestModelPauseContinueForcesReseedAndStaysContinuous(t *testing.T) {
	h := newModelHarness(t)
	h.mu.Lock()
	h.slow = 150 * time.Millisecond
	h.mu.Unlock()
	h.tmux.startWriter("")
	sub := h.subscribe(t)
	defer sub.Close()

	waitUntil(t, 30*time.Second, "tmux to pause the pane", func() bool {
		return h.sawReason(ResyncPause)
	})
	h.mu.Lock()
	h.slow = 0
	h.mu.Unlock()

	waitUntil(t, 30*time.Second, "a reseed after pause", func() bool { return h.seeds() >= 2 })

	// Recovery is a fresh seed, so the model must converge on exactly what tmux
	// has — no stitched-together screen, no duplicated or dropped tail.
	h.tmux.stopWriter()
	var modelTail, tmuxTail []int
	waitUntil(t, 30*time.Second, "the model to converge on tmux after continue", func() bool {
		frame, ok := h.recorder.lastFrame()
		if !ok {
			return false
		}
		modelTail = lastN(numbersIn(frame.Frame.Output), 10)
		tmuxTail = lastN(numbersIn(h.tmux.run("capture-pane", "-p", "-t", h.tmux.pane)), 10)
		return len(modelTail) == 10 && equalInts(modelTail, tmuxTail)
	})
	frame, _ := h.recorder.lastFrame()
	assertContinuous(t, "post-pause", numbersIn(frame.Frame.Output))

	// And the stream is live again: bytes written after the continue arrive.
	h.tmux.run("send-keys", "-t", h.tmux.pane, "-l", "echo AFTER_CONTINUE_MARKER")
	h.tmux.run("send-keys", "-t", h.tmux.pane, "Enter")
	waitUntil(t, 20*time.Second, "post-continue bytes to reach the model", func() bool {
		frame, ok := h.recorder.lastFrame()
		return ok && strings.Contains(frame.Frame.Output, "AFTER_CONTINUE_MARKER")
	})
}

// Reconnect: a dead control client returns the consumer to its fallback and the
// pane model stops. A fresh subscription seeds from scratch and is continuous.
func TestModelReconnectFallsBackThenReseeds(t *testing.T) {
	h := newModelHarness(t)
	h.tmux.startWriter("0.01")
	sub := h.subscribe(t)
	numbers := h.waitForAdvance(t, 15*time.Second, "frames before reconnect")
	assertContinuous(t, "pre-reconnect", numbers)

	h.mu.Lock()
	channel := h.channels[0]
	h.mu.Unlock()
	_ = channel.Close()
	waitUntil(t, 15*time.Second, "the consumer to fall back", func() bool {
		h.mu.Lock()
		defer h.mu.Unlock()
		return len(h.fallbacks) > 0
	})
	waitUntil(t, 5*time.Second, "the model to report reconnect", func() bool {
		return h.sawReason(ResyncReconnect)
	})
	sub.Close()

	// Sidecar's real recovery is a new subscription, exactly as after a restart.
	h.recorder.mu.Lock()
	h.recorder.frames = nil
	h.recorder.mu.Unlock()
	second := h.subscribe(t)
	defer second.Close()
	numbers = h.waitForAdvance(t, 20*time.Second, "frames after reconnect")
	assertContinuous(t, "post-reconnect", numbers)
	h.tmux.stopWriter()
}

// Unsubscribe: no frame may be delivered after Close returns.
func TestModelUnsubscribeStopsFrames(t *testing.T) {
	h := newModelHarness(t)
	h.tmux.startWriter("0.01")
	sub := h.subscribe(t)
	h.waitForAdvance(t, 15*time.Second, "frames before unsubscribe")
	sub.Close()
	count := h.recorder.frameCount()
	time.Sleep(500 * time.Millisecond)
	if h.recorder.frameCount() != count {
		t.Fatalf("frames delivered after unsubscribe: %d -> %d", count, h.recorder.frameCount())
	}
	h.tmux.stopWriter()
}

// Generation replacement: hiding and re-showing a subscription replaces its
// control client and bumps its generation. Frames from the old generation must
// stop and the new generation must seed cleanly.
func TestModelGenerationReplacementReseeds(t *testing.T) {
	h := newModelHarness(t)
	h.tmux.startWriter("0.01")
	sub := h.subscribe(t)
	defer sub.Close()
	h.waitForAdvance(t, 15*time.Second, "frames before hiding")
	first, _ := h.recorder.lastFrame()

	sub.SetVisible(false)
	time.Sleep(200 * time.Millisecond)
	sub.SetVisible(true)

	var second ModelFrame
	waitUntil(t, 20*time.Second, "a frame from the new generation", func() bool {
		frame, ok := h.recorder.lastFrame()
		if !ok || frame.Generation <= first.Generation {
			return false
		}
		second = frame
		return true
	})
	if second.Seeds != 1 {
		t.Fatalf("a replaced generation reused a model: seeds = %d", second.Seeds)
	}
	assertContinuous(t, "post-generation", numbersIn(second.Frame.Output))
	h.tmux.stopWriter()
}

// The seed transaction's two halves must describe the same moment. tmux 3.6b
// executes a pair of command lines written together back to back and never
// interleaves a notification between their response blocks; this asserts that
// against a pane writing as fast as it can.
func TestSeedTransactionHalvesAreNotInterleaved(t *testing.T) {
	h := newModelHarness(t)
	h.tmux.startWriter("")
	sub := h.subscribe(t)
	defer sub.Close()
	h.waitForAdvance(t, 20*time.Second, "frames under maximum output")
	for range 8 {
		sub.Resize(80+len(h.recorder.reasons())%4, 24)
		time.Sleep(120 * time.Millisecond)
	}
	if h.sawReason(ResyncSeedRace) {
		t.Fatal("pane bytes were observed between the seed metadata and capture responses")
	}
	h.tmux.stopWriter()
}


func lastNumber(values []int) int {
	if len(values) == 0 {
		return -1
	}
	return values[len(values)-1]
}
