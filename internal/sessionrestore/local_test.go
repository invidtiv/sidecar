package sessionrestore

import (
	"context"
	"fmt"
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

const prefillTestPrompt = "sidecar-prefill> "

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
			if out, err := exec.Command("tmux", "new-session", "-d", "-s", name,
				"-e", "PS1="+prefillTestPrompt, "-e", "PROMPT="+prefillTestPrompt,
				"-e", "PROMPT_COMMAND=", "-e", "RPROMPT=", "-e", "RPS1=", "-e", "EDITOR=emacs", "-e", "VISUAL=emacs", "-e", "TERM=xterm-256color", shell.command).CombinedOutput(); err != nil {
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
				wantX := len(prefillTestPrompt + "echo foo #")
				if atStart {
					wantX = len(prefillTestPrompt)
				}
				waitPrefillTestScreen(t, name, wantX, prefillTestPrompt+"echo foo #")
				bx, by, before, err := prefillPaneScreen(context.Background(), name)
				if err != nil {
					t.Fatal(err)
				}
				empty, _, err := prefillInputEmpty(context.Background(), name)
				waitPrefillTestScreen(t, name, bx, prefillTestPrompt+"echo foo #")
				ax, ay, after, screenErr := prefillPaneScreen(context.Background(), name)
				if screenErr != nil {
					t.Fatal(screenErr)
				}
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
	// Process identity does not prove that shell startup or a Ctrl-C redraw
	// has finished. Wait for the fixture's actual prompt and editor cursor.
	waitPrefillTestScreen(t, target, len(prefillTestPrompt), prefillTestPrompt)
}

func waitPrefillTestScreen(t *testing.T, target string, wantX int, wantLine string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	tick := time.NewTicker(5 * time.Millisecond)
	defer tick.Stop()
	var x, y int
	var rows []string
	for {
		nx, ny, nextRows, err := prefillPaneScreen(ctx, target)
		if err != nil {
			t.Fatalf("read fixture screen for %q at x=%d: %v; last cursor=(%d,%d), rows=%q", wantLine, wantX, err, x, y, rows)
		}
		x, y, rows = nx, ny, nextRows
		if x == wantX && y >= 0 && y < len(rows) && rows[y] == strings.TrimRight(wantLine, " ") {
			return
		}
		select {
		case <-ctx.Done():
			t.Fatalf("fixture did not render %q at cursor x=%d; got (%d,%d), %q", wantLine, wantX, x, y, rows)
		case <-tick.C:
		}
	}
}

func TestWaitShellPromptRequiresRenderedPrompt(t *testing.T) {
	// A current command already identifies the shell while its startup files
	// are still running. Model that ordering without a load-dependent delay:
	// the first screen is startup output; only the next screen has the prompt.
	dir := t.TempDir()
	observed := filepath.Join(dir, "startup-observed")
	script := fmt.Sprintf(`#!/bin/sh
case "$1" in
  display-message)
    case "$*" in
      *pane_current_command*) printf 'bash\n' ;;
      *) if [ -f %q ]; then printf '%d|0|24\n'; else printf '0|0|24\n'; fi ;;
    esac ;;
  capture-pane)
    if [ -f %q ]; then printf '%%s\n' %q; else printf 'running startup files\n'; touch %q; fi ;;
  *) exit 1 ;;
esac
`, observed, len(prefillTestPrompt), observed, strings.TrimRight(prefillTestPrompt, " "), observed)
	if err := os.WriteFile(filepath.Join(dir, "tmux"), []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	waitShellPrompt(t, "starting-shell")
	x, y, rows, err := prefillPaneScreen(context.Background(), "starting-shell")
	if err != nil || x != len(prefillTestPrompt) || y != 0 || len(rows) == 0 || rows[0] != strings.TrimRight(prefillTestPrompt, " ") {
		t.Fatalf("readiness returned before the rendered prompt: cursor=(%d,%d) rows=%q error=%v", x, y, rows, err)
	}
}
