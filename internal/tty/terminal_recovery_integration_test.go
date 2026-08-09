package tty

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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
	tmux.run("send-keys", "-t", tmux.pane, "-l", "nvim -u NONE -n "+path)
	tmux.run("send-keys", "-t", tmux.pane, "Enter")
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
