package sessionrestore

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/tmuxenv"
)

func TestRenderedInputRightEmpty(t *testing.T) {
	tests := []struct {
		name string
		rows []string
		x, y int
		want bool
	}{
		{"empty prompt", []string{"repo %", ""}, 6, 0, true},
		{"input right of cursor", []string{"repo % echo foo #", ""}, 7, 0, false},
		{"visible autosuggestion", []string{"repo % git status", ""}, 6, 0, false},
		{"wrapped input below", []string{"repo %", "continued"}, 6, 0, false},
		{"invalid cursor", []string{"repo %"}, 0, 2, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := renderedInputRightEmpty(tc.rows, tc.x, tc.y); got != tc.want {
				t.Fatalf("got %v, want %v", got, tc.want)
			}
		})
	}
}

func TestPrefillInputEmptyRealShells(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	for _, shell := range []struct{ name, command string }{{"bash", "/bin/bash --norc"}, {"zsh", "/bin/zsh -f"}} {
		if _, err := os.Stat(strings.Fields(shell.command)[0]); err != nil {
			continue
		}
		t.Run(shell.name, func(t *testing.T) {
			t.Setenv("TMUX", "")
			t.Setenv("TMUX_PANE", "")
			dir, err := os.MkdirTemp("/tmp", "scpf")
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = os.RemoveAll(dir) })
			t.Setenv("TMUX_TMPDIR", dir)
			socket := tmuxenv.SocketPath()
			_ = os.MkdirAll(filepath.Dir(socket), 0700)
			name := "prefill-" + shell.name
			if out, err := exec.Command("tmux", "new-session", "-d", "-s", name, shell.command).CombinedOutput(); err != nil {
				t.Fatalf("start: %v: %s", err, out)
			}
			t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
			waitShellPrompt(t, name)
			if empty, _, err := prefillInputEmpty(context.Background(), name); err != nil || !empty {
				t.Fatalf("empty prompt = %v, %v", empty, err)
			}

			for _, atStart := range []bool{false, true} {
				if out, err := exec.Command("tmux", "send-keys", "-l", "-t", name, "echo foo #").CombinedOutput(); err != nil {
					t.Fatalf("type fixture: %v: %s", err, out)
				}
				if atStart {
					if out, err := exec.Command("tmux", "send-keys", "-t", name, "C-a").CombinedOutput(); err != nil {
						t.Fatalf("move fixture cursor: %v: %s", err, out)
					}
				}
				time.Sleep(40 * time.Millisecond)
				bx, by, before, _ := prefillPaneScreen(context.Background(), name)
				empty, _, err := prefillInputEmpty(context.Background(), name)
				time.Sleep(40 * time.Millisecond)
				ax, ay, after, _ := prefillPaneScreen(context.Background(), name)
				if err != nil || empty {
					t.Fatalf("nonempty start=%v = %v, %v", atStart, empty, err)
				}
				if bx != ax || by != ay || strings.Join(before, "\n") != strings.Join(after, "\n") {
					t.Fatalf("refusal changed buffer/cursor start=%v", atStart)
				}
				if out, err := exec.Command("tmux", "send-keys", "-t", name, "C-c").CombinedOutput(); err != nil {
					t.Fatalf("clear fixture: %v: %s", err, out)
				}
				waitShellPrompt(t, name)
			}
		})
	}
}

func waitShellPrompt(t *testing.T, target string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		out, _ := exec.Command("tmux", "display-message", "-p", "-t", target, "#{pane_current_command}").Output()
		if strings.TrimSpace(string(out)) != "" {
			time.Sleep(60 * time.Millisecond)
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("shell prompt did not become ready")
}
