package agentcontrol

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

// TestLiveProviderStartGet is an opt-in, no-prompt compatibility proof for the
// real provider CLIs installed on a developer machine. TestMain puts every
// tmux command on a private server; this test launches only an empty composer,
// observes it through the production service, and never submits a user prompt.
//
// Run with:
//
//	SIDECAR_LIVE_AGENT_PROVIDERS=codex,claude go test ./internal/agentcontrol -run TestLiveProviderStartGet -count=1 -v
func TestLiveProviderStartGet(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("SIDECAR_LIVE_AGENT_PROVIDERS"))
	if raw == "" {
		t.Skip("set SIDECAR_LIVE_AGENT_PROVIDERS to opt into real provider startup")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	for _, kind := range strings.Split(raw, ",") {
		kind = strings.TrimSpace(kind)
		if kind == "" {
			continue
		}
		t.Run(kind, func(t *testing.T) {
			argv, buildErr := agentcatalog.BuildLaunch(kind, nil, false)
			if buildErr != nil {
				t.Fatal(buildErr)
			}
			if _, lookErr := exec.LookPath(argv[0]); lookErr != nil {
				t.Skipf("%s unavailable: %v", argv[0], lookErr)
			}

			session := fmt.Sprintf("sidecar-live-%s-%d", kind, time.Now().UnixNano())
			if out, startErr := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", root).CombinedOutput(); startErr != nil {
				t.Fatalf("new isolated session: %v: %s", startErr, out)
			}
			t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })

			svc := Service{Terminal: NewLocalTerminal(), Poll: 100 * time.Millisecond}
			target := Target{Host: "local", Project: "sidecar-agent-control", Session: session, Namespace: tmuxenv.Namespace()}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			ready, readyErr := svc.WaitShellReady(ctx, target, 5*time.Second)
			if readyErr != nil {
				t.Fatal(readyErr)
			}
			started, startErr := svc.Start(ctx, StartRequest{Target: ready.Target, Kind: kind, Argv: argv, Timeout: 25 * time.Second})
			if startErr != nil {
				t.Fatal(startErr)
			}
			if started.Agent.Kind != kind || !started.Agent.InteractiveReady {
				t.Fatalf("start = %+v, want ready %s", started, kind)
			}

			var got Agent
			getDeadline := time.Now().Add(5 * time.Second)
			for {
				got, err = svc.Get(ctx, started.Target)
				if err != nil {
					t.Fatal(err)
				}
				if got.Target == started.Target && got.Agent.Kind == kind && got.Agent.InteractiveReady {
					break
				}
				if time.Now().After(getDeadline) {
					t.Fatalf("get = %+v, want same pinned ready %s target", got, kind)
				}
				time.Sleep(100 * time.Millisecond)
			}
			t.Logf("provider=%s status=%s evidence=%s pane=%s", kind, got.Agent.Status, got.Agent.Evidence, got.Target.PaneID)
		})
	}
}
