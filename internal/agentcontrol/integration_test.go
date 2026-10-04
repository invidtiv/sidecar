package agentcontrol

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

func TestIsolatedTmuxFakeProviderSteelThread(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	session := fmt.Sprintf("sidecar-agentcontrol-m0-%d", time.Now().UnixNano())
	// Explicit argv bypasses the user's login-shell startup and tmux default
	// command, so readiness and Launch's recheck observe the fixture's shell.
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "/bin/bash", "--noprofile", "--norc", "-i").CombinedOutput(); err != nil {
		t.Fatalf("new isolated session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })
	terminal := NewLocalTerminal()
	t.Cleanup(terminal.Close)
	target := Target{Host: "local", Project: "fixture", Session: session, Namespace: tmuxenv.Namespace()}
	// Do not identify the provider until its first marker appears. Input being
	// queued in the shell is not evidence that the provider ran or exited.
	detect := fakeProviderDetect
	svc := Service{Terminal: terminal, Poll: 20 * time.Millisecond, Detect: detect, ShellInitGrace: 10 * time.Second}
	// Markers are built from $m so the echoed launch line does not itself read
	// as a finished agent; see fakeProviderScript for why that matters.
	script := `m=FAKE; printf '%s_IDLE\n' "$m"; while IFS= read -r line; do printf '%s_WORKING:%s\n' "$m" "$line"; sleep 0.2; if [ "$line" = block ]; then printf '%s_BLOCKED\n' "$m"; else printf '%s_DONE\n' "$m"; fi; done`
	ready, err := svc.WaitShellReady(context.Background(), target, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.Start(context.Background(), StartRequest{Target: ready.Target, Kind: "fake", Argv: []string{"sh", "-c", script}, Timeout: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	pinned := agent.Target
	snap, err := terminal.Inspect(context.Background(), pinned)
	if err != nil {
		t.Fatal(err)
	}
	prompt := "first line\n雪 $HOME ; 'quoted' #{pane_id}"
	if err := terminal.Submit(context.Background(), snap, prompt); err != nil {
		t.Fatal(err)
	}
	sawWorking, sawDone, sawExactPaste := false, false, false
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		s, err := terminal.Inspect(context.Background(), pinned)
		if err != nil {
			t.Fatal(err)
		}
		if !sameOccupant(pinned, s.Target) {
			t.Fatal("target changed")
		}
		state := detect(s, &agentactivity.Tracker{})
		sawWorking = sawWorking || state.Status == StatusWorking
		sawDone = sawDone || state.Status == StatusDone
		sawExactPaste = sawExactPaste || strings.Contains(s.Screen, "FAKE_WORKING:雪 $HOME ; 'quoted' #{pane_id}")
		if sawWorking && sawDone && sawExactPaste {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !sawWorking || !sawDone || !sawExactPaste {
		t.Fatalf("journey incomplete: working=%v done=%v exactPaste=%v", sawWorking, sawDone, sawExactPaste)
	}

	blockedStart, err := terminal.Inspect(context.Background(), pinned)
	if err != nil {
		t.Fatal(err)
	}
	if err := terminal.Submit(context.Background(), blockedStart, "block"); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		read, inspectErr := terminal.Inspect(context.Background(), pinned)
		if inspectErr != nil {
			t.Fatal(inspectErr)
		}
		if !sameOccupant(pinned, read.Target) {
			t.Fatal("target changed before blocked read")
		}
		if detect(read, &agentactivity.Tracker{}).Status == StatusBlocked && strings.Contains(read.Screen, "FAKE_BLOCKED") {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatal("fake provider never reached blocked/read state")
}

// The package TestMain owns a private server, and none of these fixtures runs
// in parallel. An inherited default command must not determine their occupant.
func TestIsolatedIntegrationFixturesIgnoreDefaultShellCommand(t *testing.T) {
	requireTmux(t)
	previous, err := exec.Command("tmux", "show-options", "-gqv", "default-command").Output()
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if out, err := exec.Command("tmux", "set-option", "-g", "default-command", strings.TrimSpace(string(previous))).CombinedOutput(); err != nil {
			t.Errorf("restore private server default command: %v: %s", err, out)
		}
	})
	if out, err := exec.Command("tmux", "set-option", "-g", "default-command", "exec sleep 30").CombinedOutput(); err != nil {
		t.Fatalf("set private server default command: %v: %s", err, out)
	}
	t.Run("steel thread", TestIsolatedTmuxFakeProviderSteelThread)
	t.Run("refusals", TestIsolatedTmuxRefusesBusyForegroundAndCopyMode)
}

func TestIsolatedTmuxRefusesBusyForegroundAndCopyMode(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	for _, mode := range []string{"busy foreground", "copy mode"} {
		t.Run(mode, func(t *testing.T) {
			session := fmt.Sprintf("sidecar-agentcontrol-refusal-%d", time.Now().UnixNano())
			if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "/bin/bash", "--noprofile", "--norc", "-i").CombinedOutput(); err != nil {
				t.Fatalf("new isolated session: %v: %s", err, out)
			}
			t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })

			terminal := NewLocalTerminal()
			t.Cleanup(terminal.Close)
			target := Target{Host: "local", Project: "fixture", Session: session, Namespace: tmuxenv.Namespace()}
			svc := Service{Terminal: terminal, Poll: 20 * time.Millisecond, ShellStableFor: 100 * time.Millisecond}
			ready, err := svc.WaitShellReady(context.Background(), target, 3*time.Second)
			if err != nil {
				t.Fatal(err)
			}

			switch mode {
			case "busy foreground":
				if err := terminal.Submit(context.Background(), ready, "sleep 30"); err != nil {
					t.Fatal(err)
				}
				deadline := time.Now().Add(2 * time.Second)
				for {
					snapshot, inspectErr := terminal.Inspect(context.Background(), ready.Target)
					if inspectErr != nil {
						t.Fatal(inspectErr)
					}
					if snapshot.CurrentCommand == "sleep" && !snapshot.ShellReady {
						break
					}
					if time.Now().After(deadline) {
						t.Fatalf("foreground command never became busy: %+v", snapshot)
					}
					time.Sleep(20 * time.Millisecond)
				}
			case "copy mode":
				if out, err := exec.Command("tmux", "copy-mode", "-t", ready.PaneID).CombinedOutput(); err != nil {
					t.Fatalf("enter copy mode: %v: %s", err, out)
				}
				snapshot, inspectErr := terminal.Inspect(context.Background(), ready.Target)
				if inspectErr != nil {
					t.Fatal(inspectErr)
				}
				if !snapshot.CopyMode {
					t.Fatalf("copy mode not observed: %+v", snapshot)
				}
			}

			_, err = svc.Start(context.Background(), StartRequest{Target: ready.Target, Kind: "codex", Argv: []string{"codex"}, Timeout: time.Second})
			var typed *Error
			if !AsError(err, &typed) || typed.Code != ErrPaneBusy {
				t.Fatalf("Start() err = %T %v, want %s", err, err, ErrPaneBusy)
			}
		})
	}
}
