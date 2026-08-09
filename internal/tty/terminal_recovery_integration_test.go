package tty

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
)

// This regression uses only startModelTmux's explicit private socket. It
// distinguishes an initial model built from nvim startup bytes from the harder
// recovery case: seeding an already-active alternate screen after control dies.
func TestTerminalRecoverySeedsExistingAlternateScreen(t *testing.T) {
	if _, err := exec.LookPath("nvim"); err != nil {
		t.Skip("nvim not installed")
	}
	tmux := startModelTmux(t)
	path := filepath.Join(t.TempDir(), "recovery.txt")
	if err := os.WriteFile(path, []byte("RECOVERY_ALT_MARKER\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	var mu sync.Mutex
	var channels []controlChannel
	manager := newControlManager(func(session string) (controlChannel, error) {
		channel, err := newProcessControlChannelForSocket(tmux.sock, session)
		if err == nil {
			mu.Lock()
			channels = append(channels, channel)
			mu.Unlock()
		}
		return channel, err
	}, 5*time.Millisecond)
	t.Cleanup(manager.Stop)

	subscribe := func(recorder *modelRecorder, fallback chan error) *ControlSubscription {
		sub, err := manager.Subscribe(ControlRequest{
			Session: tmux.session, Pane: tmux.pane, Visible: true, Focused: true,
			Width: 80, Height: 24, Scrollback: 600, ModelPresentation: true,
			OnModelFrame:   recorder.onFrame,
			OnModelInvalid: recorder.onInvalid,
			OnFallback: func(err error) {
				select {
				case fallback <- err:
				default:
				}
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		return sub
	}

	firstRecorder := &modelRecorder{}
	fallback := make(chan error, 1)
	first := subscribe(firstRecorder, fallback)
	waitUntil(t, 5*time.Second, "initial shell seed", func() bool {
		return firstRecorder.frameCount() > 0
	})
	tmux.run("respawn-pane", "-k", "-t", tmux.pane, "nvim", "-u", "NONE", "-n", path)
	waitUntil(t, 5*time.Second, "nvim alternate screen", func() bool {
		return strings.TrimSpace(tmux.run("display-message", "-p", "-t", tmux.pane, "#{alternate_on}")) == "1"
	})
	waitUntil(t, 5*time.Second, "startup-byte model content", func() bool {
		frame, ok := firstRecorder.lastFrame()
		return ok && strings.Contains(frame.Frame.CombinedOutput(), "RECOVERY_ALT_MARKER")
	})

	mu.Lock()
	channel := channels[len(channels)-1]
	mu.Unlock()
	if err := channel.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-fallback:
	case <-time.After(5 * time.Second):
		t.Fatal("control death did not reach fallback")
	}
	first.Close()

	secondRecorder := &modelRecorder{}
	second := subscribe(secondRecorder, make(chan error, 1))
	defer second.Close()
	waitUntil(t, 5*time.Second, "existing alternate-screen recovery seed", func() bool {
		frame, ok := secondRecorder.lastFrame()
		return ok && strings.Contains(frame.Frame.CombinedOutput(), "RECOVERY_ALT_MARKER")
	})
}

type socketTerminalCapture struct {
	tmux      *modelTmux
	count     atomic.Int32
	blankNext atomic.Bool
}

func (c *socketTerminalCapture) Capture(target string, scrollback int) (string, int, int, int, int, bool, error) {
	c.count.Add(1)
	out, err := c.tmux.cmd("capture-pane", "-p", "-e", "-S", "-"+strconv.Itoa(scrollback), "-t", target).Output()
	if err != nil {
		return "", 0, 0, 0, 0, false, err
	}
	if c.blankNext.Swap(false) {
		out = nil
	}
	meta, err := c.tmux.cmd("display-message", "-p", "-t", target,
		"#{cursor_x},#{cursor_y},#{cursor_flag},#{pane_height},#{pane_width}").Output()
	if err != nil {
		return "", 0, 0, 0, 0, false, err
	}
	parts := strings.Split(strings.TrimSpace(string(meta)), ",")
	if len(parts) != 5 {
		return "", 0, 0, 0, 0, false, errors.New("invalid cursor metadata")
	}
	values := make([]int, len(parts))
	for i := range parts {
		values[i], err = strconv.Atoi(parts[i])
		if err != nil {
			return "", 0, 0, 0, 0, false, err
		}
	}
	return string(out), values[1], values[0], values[3], values[4], values[2] == 1, nil
}

// TestTerminalModelControlDeathFallbackAndAlternateReseed runs the actual Model
// command/message lifecycle. No ambient tmux command is possible: both control
// and capture adapters carry startModelTmux's explicit private socket.
func TestTerminalModelControlDeathFallbackAndAlternateReseed(t *testing.T) {
	if _, err := exec.LookPath("nvim"); err != nil {
		t.Skip("nvim not installed")
	}
	tmux := startModelTmux(t)
	path := filepath.Join(t.TempDir(), "model-recovery.txt")
	if err := os.WriteFile(path, []byte("MODEL_RECOVERY_BASELINE\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	tmux.run("send-keys", "-t", tmux.pane, "-l", "nvim -u NONE -n "+path)
	tmux.run("send-keys", "-t", tmux.pane, "Enter")
	waitUntil(t, 5*time.Second, "model nvim alternate screen", func() bool {
		return strings.TrimSpace(tmux.run("display-message", "-p", "-t", tmux.pane, "#{alternate_on}")) == "1"
	})

	var channelsMu sync.Mutex
	var channels []controlChannel
	manager := newControlManager(func(session string) (controlChannel, error) {
		channel, err := newProcessControlChannelForSocket(tmux.sock, session)
		if err == nil {
			channelsMu.Lock()
			channels = append(channels, channel)
			channelsMu.Unlock()
		}
		return channel, err
	}, 5*time.Millisecond)
	t.Cleanup(manager.Stop)

	capture := &socketTerminalCapture{tmux: tmux}
	model := New(nil)
	model.control = controlManagerSource{manager: manager}
	model.capture = capture
	model.Width, model.Height = 135, 42
	defer model.Close()

	messages := make(chan tea.Msg, 128)
	var submit func(tea.Cmd)
	submit = func(cmd tea.Cmd) {
		if cmd == nil {
			return
		}
		go func() {
			msg := cmd()
			switch msg := msg.(type) {
			case tea.BatchMsg:
				for _, nested := range msg {
					submit(nested)
				}
			default:
				if msg != nil {
					messages <- msg
				}
			}
		}()
	}
	submit(model.Open(Target{Session: tmux.session, Pane: tmux.pane}))

	stepUntil := func(label string, forbidBlank bool, condition func() bool) {
		t.Helper()
		deadline := time.After(8 * time.Second)
		for !condition() {
			select {
			case msg := <-messages:
				submit(model.Update(msg))
				if forbidBlank && strings.TrimSpace(ansiEscapePattern.ReplaceAllString(model.View(), "")) == "" {
					t.Fatalf("%s: terminal view became blank", label)
				}
			case <-deadline:
				t.Fatalf("timed out waiting for %s; live=%v captures=%d view=%q",
					label, model.modelLive, capture.count.Load(), model.View())
			}
		}
	}

	stepUntil("initial model view", false, func() bool {
		return model.modelLive && strings.Contains(model.View(), "MODEL_RECOVERY_BASELINE")
	})
	channelsMu.Lock()
	dead := channels[len(channels)-1]
	channelsMu.Unlock()
	if err := dead.Close(); err != nil {
		t.Fatal(err)
	}
	capture.blankNext.Store(true)
	tmux.run("send-keys", "-t", tmux.pane, "-l", "VISIBLE_FALLBACK_MARKER")

	stepUntil("visible fallback capture", true, func() bool {
		return !model.modelLive && strings.Contains(model.View(), "FALLBACK_MARKER")
	})
	stepUntil("replacement alternate-screen seed", true, func() bool {
		return model.modelLive && strings.Contains(model.View(), "FALLBACK_MARKER")
	})
	if capture.count.Load() == 0 {
		t.Fatal("recovery never used capture fallback")
	}
	channelsMu.Lock()
	channelCount := len(channels)
	channelsMu.Unlock()
	if channelCount < 2 {
		t.Fatalf("replacement control clients = %d, want at least 2", channelCount)
	}
}
