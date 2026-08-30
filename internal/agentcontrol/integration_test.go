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
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session).CombinedOutput(); err != nil {
		t.Fatalf("new isolated session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })
	terminal := NewLocalTerminal()
	target := Target{Host: "local", Project: "fixture", Session: session, Namespace: tmuxenv.Namespace()}
	detect := func(s Snapshot, _ *agentactivity.Tracker) AgentState {
		status := StatusUnknown
		latest := -1
		for marker, candidate := range map[string]Status{"FAKE_IDLE": StatusIdle, "FAKE_WORKING": StatusWorking, "FAKE_DONE": StatusDone, "FAKE_BLOCKED": StatusBlocked} {
			if at := strings.LastIndex(s.Screen, marker); at > latest {
				latest = at
				status = candidate
			}
		}
		return AgentState{Kind: "fake", Status: status, Freshness: "current", Evidence: "fake.screen", CapturedAt: s.CapturedAt}
	}
	svc := Service{Terminal: terminal, Poll: 20 * time.Millisecond, Detect: detect}
	script := `printf 'FAKE_IDLE\n'; while IFS= read -r line; do printf 'FAKE_WORKING:%s\n' "$line"; sleep 0.2; if [ "$line" = block ]; then printf 'FAKE_BLOCKED\n'; else printf 'FAKE_DONE\n'; fi; done`
	ready, err := svc.WaitShellReady(context.Background(), target, 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	agent, err := svc.Start(context.Background(), StartRequest{Target: ready.Target, Kind: "fake", Argv: []string{"sh", "-c", script}, Timeout: 3 * time.Second})
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

func TestIsolatedTmuxRefusesBusyForegroundAndCopyMode(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	for _, mode := range []string{"busy foreground", "copy mode"} {
		t.Run(mode, func(t *testing.T) {
			session := fmt.Sprintf("sidecar-agentcontrol-refusal-%d", time.Now().UnixNano())
			if out, err := exec.Command("tmux", "new-session", "-d", "-s", session).CombinedOutput(); err != nil {
				t.Fatalf("new isolated session: %v: %s", err, out)
			}
			t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })

			terminal := NewLocalTerminal()
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
