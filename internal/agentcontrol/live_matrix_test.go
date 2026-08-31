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

// TestLiveProviderMatrix is M5's rollout gate: the non-mutating half of the
// live provider matrix, across every catalog provider installed on this
// machine.
//
// It is the evidence the plan requires before agent_control's default-off flag
// is flipped on. Each provider is launched into an empty composer on the
// package's private tmux server and then only *observed*: start, get, status,
// and every passive read source. No prompt is ever submitted, so nothing here
// can create paid or externally mutating work — which is exactly why this half
// can run unattended while the prompt and resume halves stay operator-opt-in.
//
// Run with:
//
//	SIDECAR_LIVE_AGENT_MATRIX=codex,claude,opencode,grok,cursor,pi \
//	  go test ./internal/agentcontrol -run TestLiveProviderMatrix -count=1 -v
//
// TestMain has already pointed every tmux command at a throwaway server, so no
// session created here can reach the developer's own. HOME is deliberately NOT
// redirected: a provider CLI in a credential-less sandbox HOME is a different
// program from the one users run, and what this gate is asking is whether
// Sidecar correctly identifies and observes the real one. The cost of that
// choice is stated rather than hidden — launching a real provider may create an
// empty session entry in its own store. No conversation content is produced,
// because no prompt is sent.
// readSettle is how long the matrix lets a provider paint before reading it.
// See the comment at the read loop for why it is needed and what was ruled out.
const readSettle = 6 * time.Second

func TestLiveProviderMatrix(t *testing.T) {
	raw := strings.TrimSpace(os.Getenv("SIDECAR_LIVE_AGENT_MATRIX"))
	if raw == "" {
		t.Skip("set SIDECAR_LIVE_AGENT_MATRIX to opt into the live provider matrix")
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
				t.Fatalf("catalog has no launch for %q: %v", kind, buildErr)
			}
			binary, lookErr := exec.LookPath(argv[0])
			if lookErr != nil {
				t.Skipf("%s unavailable: %v", argv[0], lookErr)
			}
			t.Logf("provider=%s binary=%s argv=%v", kind, binary, argv)

			session := fmt.Sprintf("sidecar-matrix-%s-%d", kind, time.Now().UnixNano())
			if out, startErr := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", root).CombinedOutput(); startErr != nil {
				t.Fatalf("new isolated session: %v: %s", startErr, out)
			}
			t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })

			svc := Service{Terminal: NewLocalTerminal(), Poll: 100 * time.Millisecond}
			target := Target{Host: "local", Project: "sidecar-agent-control", Session: session, Namespace: tmuxenv.Namespace()}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()

			ready, readyErr := svc.WaitShellReady(ctx, target, 10*time.Second)
			if readyErr != nil {
				t.Fatalf("shell never became ready: %v", readyErr)
			}

			// start: returns only at positively identified readiness.
			started, startErr := svc.Start(ctx, StartRequest{Target: ready.Target, Kind: kind, Argv: argv, Timeout: 60 * time.Second})
			if startErr != nil {
				t.Fatalf("start %s: %v", kind, startErr)
			}
			if started.Agent.Kind != kind {
				t.Fatalf("start identified %q, want %q", started.Agent.Kind, kind)
			}
			if !started.Agent.InteractiveReady {
				t.Fatalf("start returned before the composer was interactive: %+v", started.Agent)
			}

			// get: the same pinned target, observed passively.
			got, getErr := svc.Get(ctx, started.Target)
			if getErr != nil {
				t.Fatalf("get %s: %v", kind, getErr)
			}
			if got.Target != started.Target {
				t.Fatalf("get returned a different pin:\n start=%+v\n   get=%+v", started.Target, got.Target)
			}
			if got.Agent.Kind != kind {
				t.Fatalf("get identified %q, want %q", got.Agent.Kind, kind)
			}

			// status must be a real lane, never the unknown fallthrough: an
			// unknown here would mean the provider launched and Sidecar could
			// not say anything true about it, which is the failure this gate
			// exists to catch.
			if got.Agent.Status == StatusUnknown {
				t.Fatalf("%s reached no known lane: %+v", kind, got.Agent)
			}

			// Let the provider paint before reading it.
			//
			// This settle is a finding rather than a convenience, and the matrix
			// is where it was found. Readiness is established from process,
			// title, and screen identity, and for several providers all of those
			// are true before the alternate-screen UI has drawn anything: grok is
			// identified from its terminal title, opencode and cursor from a live
			// process. A read issued in that window returns an empty or partial
			// screen, and the four sources sample slightly different instants of
			// a screen still being drawn, which is why they disagreed.
			//
			// It was checked against the alternative explanation rather than
			// assumed: on a settled alternate-screen pane, `capture-pane -p`,
			// `-S -40`, and `-S -40 -J` return identical content for both
			// opencode and grok, so there is no capture defect here — only a
			// provider that had not painted yet.
			//
			// The documented workflow is unaffected, because it is start,
			// prompt, wait, then read, and a wait always outlasts the first
			// paint. A caller who reads immediately after start is the one who
			// sees this, so it is written down rather than papered over.
			time.Sleep(readSettle)

			// read: every passive source, proving none of them disturbs the
			// pane and all of them answer for a real provider UI.
			for _, source := range ReadSources() {
				if source == SourceTranscript {
					// transcript needs an exact reported binding, which this
					// gate deliberately does not establish; it is M3's proof.
					continue
				}
				result, readErr := svc.Read(ctx, ReadRequest{Target: started.Target, Source: source, Lines: 40})
				if readErr != nil {
					t.Fatalf("read %s --source %s: %v", kind, source, readErr)
				}
				t.Logf("READ %s --source %-16s bytes=%d", kind, source, len(strings.TrimSpace(result.Text)))
				if strings.TrimSpace(result.Text) == "" {
					t.Errorf("read %s --source %s returned nothing", kind, source)
				}
			}

			// The pane must still be the same one after all that reading, and
			// the status must settle back to a real lane.
			//
			// The poll is here because of grok, and the reason is worth keeping:
			// grok's launcher re-execs into a differently named binary
			// (grok-1.0.13-mac) a few seconds after start, and Sidecar correctly
			// notices the pane's live process changed and declines to assert a
			// lane from evidence it gathered about the previous one —
			// `live-process-changed`, status unknown. That is the conservative
			// behaviour and it is right, but it means a single `get` timed into
			// that window reports unknown. What must be true is that it
			// recovers, because a `wait` that never recovered would hang until
			// its timeout on a perfectly healthy agent.
			deadline := time.Now().Add(20 * time.Second)
			var after Agent
			for {
				var afterErr error
				after, afterErr = svc.Get(ctx, started.Target)
				if afterErr != nil {
					t.Fatalf("get after reads: %v", afterErr)
				}
				if after.Target != started.Target {
					t.Fatalf("reading changed the pin:\n before=%+v\n  after=%+v", started.Target, after.Target)
				}
				if after.Agent.Status != StatusUnknown {
					break
				}
				if time.Now().After(deadline) {
					t.Fatalf("%s never recovered a lane after its process changed: %+v", kind, after.Agent)
				}
				time.Sleep(250 * time.Millisecond)
			}

			t.Logf("MATRIX %s: status=%s freshness=%s evidence=%q interactiveReady=%v pane=%s",
				kind, after.Agent.Status, after.Agent.Freshness, after.Agent.Evidence,
				after.Agent.InteractiveReady, after.Target.PaneID)
		})
	}
}
