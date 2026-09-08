package tty

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"
)

func TestHeadlessMachineOutputWithoutALocale(t *testing.T) {
	name := fmt.Sprintf("sidecar-locale-free-headless-%d", time.Now().UnixNano())
	if out, err := exec.Command("tmux", "new-session", "-d", "-x", "40", "-y", "6", "-s", name).CombinedOutput(); err != nil {
		t.Fatalf("start private tmux session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", name).Run() })
	paneOut, err := exec.Command("tmux", "display-message", "-p", "-t", name, "#{pane_id}").Output()
	if err != nil {
		t.Fatal(err)
	}
	pane := strings.TrimSpace(string(paneOut))
	if out, err := exec.Command("tmux", "send-keys", "-l", "-t", pane, "printf 'history-\\347\\225\\214\\n'; printf 'live-e\\314\\201\\n'").CombinedOutput(); err != nil {
		t.Fatalf("write private pane: %v: %s", err, out)
	}
	if out, err := exec.Command("tmux", "send-keys", "-t", pane, "Enter").CombinedOutput(); err != nil {
		t.Fatalf("execute private pane command: %v: %s", err, out)
	}
	withoutLocaleTTY(t)
	identity, err := InspectHeadlessTarget(context.Background(), name)
	if err != nil {
		t.Fatal(err)
	}
	if identity.Pane != pane || identity.Width != 40 || identity.Height != 6 {
		t.Fatalf("identity = %#v", identity)
	}
	var capture CaptureRange
	deadline := time.Now().Add(2 * time.Second)
	for {
		capture, err = CapturePaneRangeBounded(pane, -20, 5, 1<<20)
		if err == nil && strings.Contains(capture.Output, "history-界") && strings.Contains(capture.Output, "live-é") {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("locale-free capture never reached markers: %v: %q", err, capture.Output)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if capture.Pane != pane || capture.Session != name || capture.PaneWidth != 40 || capture.PaneHeight != 6 {
		t.Fatalf("capture metadata = %#v", capture)
	}
	channel, err := newProcessControlChannel(name)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = channel.Close() })
	reply := make(chan controlResponse, 1)
	if err := channel.Send("display-message -p '#{session_name}\t#{pane_id}'", func(response controlResponse) { reply <- response }); err != nil {
		t.Fatal(err)
	}
	controlDeadline := time.After(2 * time.Second)
	for {
		select {
		case event := <-channel.Events():
			if event.Callback != nil {
				event.Callback(event.Response)
				goto response
			}
		case <-controlDeadline:
			t.Fatal("timed out waiting for locale-free control event")
		}
	}

response:
	select {
	case response := <-reply:
		if response.Err != nil || len(response.Lines) != 1 || response.Lines[0] != name+"\t"+pane {
			t.Fatalf("control response = %#v", response)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timed out waiting for locale-free control response")
	}
}

func withoutLocaleTTY(t *testing.T) {
	t.Helper()
	for _, key := range []string{"LANG", "LC_ALL", "LC_CTYPE", "TERM"} {
		value := os.Getenv(key)
		t.Setenv(key, value)
		if err := os.Unsetenv(key); err != nil {
			t.Fatal(err)
		}
	}
}
