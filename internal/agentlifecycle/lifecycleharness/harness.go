// Package lifecycleharness provides a reusable fake provider and hook harness
// for agent lifecycle work.
//
// It exists so that lifecycle tests can exercise the awkward cases — events
// arriving out of order, the same event twice, a rotated provider session, a
// child agent, a cancelled turn, a dead process, and a hook that simply fails —
// against real tmux pane identity, without needing a real provider and without
// going anywhere near the developer's own tmux server or Sidecar state.
//
// # Isolation
//
// Both axes are isolated, because they are independent and isolating only one
// is how a proof run has previously destroyed real user state:
//
//   - tmux: the harness creates its own socket under the test's temporary
//     directory and passes -S explicitly on every single tmux invocation. It
//     never runs a bare tmux command, so no environment variable, and no
//     mistake in one, can route a call to the machine's default server. The
//     server is killed by socket path on cleanup.
//   - Sidecar state: the harness points config at a temporary state directory
//     and asserts, before returning, that nothing resolved inside the real user
//     tree.
//
// # Reusability
//
// The harness writes into a [Sink]. Phase A ships [Recorder], an in-memory sink
// that applies the sequence and identity rules the persistent store will later
// own, so the scenarios below are meaningful before any store exists. Phase B
// implements the JSONL store against the same interface and reuses every
// scenario unchanged.
package lifecycleharness

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/agentlifecycle"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/tmuxserver"
)

// Sink receives reports. The persistent store implements this in Phase B.
type Sink interface {
	// Append validates and records a report. It returns how the report was
	// accepted, or an error describing why it was not.
	Append(agentlifecycle.Report) (agentlifecycle.Acceptance, error)
	// Latest returns the most recent accepted report for a sequence key.
	Latest(agentlifecycle.Key) (agentlifecycle.Report, bool)
}

// Harness owns one isolated tmux server, one live pane, and one private Sidecar
// state tree.
type Harness struct {
	// Socket is the private tmux socket path. Every tmux call the harness
	// makes passes this with -S.
	Socket string
	// StateDir is the private Sidecar state tree.
	StateDir string
	// Session is the tmux session name the harness created.
	Session string
	// PaneID is the live pane, e.g. "%0".
	PaneID string
	// PanePID is the pane's process.
	PanePID int
	// ServerIncarnation identifies this tmux server's lifetime.
	ServerIncarnation string
	// Host is the identity reports are bound to.
	Host string
	// Recorder is the default in-memory sink.
	Recorder *Recorder

	t *testing.T
}

// Start brings up the isolated environment and returns a harness bound to a
// live pane. Cleanup is registered on t.
func Start(t *testing.T) *Harness {
	t.Helper()

	// The socket deliberately does not live under t.TempDir(). That path embeds
	// the test's name, and a Unix socket path is capped near 104 bytes on
	// darwin, so a descriptively named test fails with "File name too long"
	// rather than anything that points at the real cause. A short prefix keeps
	// every test in this package under the limit regardless of its name.
	socketDir, err := os.MkdirTemp("", "sclh")
	if err != nil {
		t.Fatal(err)
	}
	socket := filepath.Join(socketDir, "s")

	stateDir := filepath.Join(t.TempDir(), "state")
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		t.Fatal(err)
	}

	// Point Sidecar's state at the temp tree and prove it took. Asserting the
	// resolved path is the difference between believing the run is isolated and
	// knowing it.
	config.SetTestStateDir(stateDir)
	t.Cleanup(config.ResetTestStateDir)
	if err := config.AssertIsolatedPath(config.StateDir()); err != nil {
		t.Fatalf("state isolation failed: %v", err)
	}

	h := &Harness{
		Socket:   socket,
		StateDir: stateDir,
		Session:  "lifecycle-harness",
		Host:     "harness-local",
		Recorder: NewRecorder(),
		t:        t,
	}

	// A pane running `cat` is a live, quiet, killable process: it holds the
	// pane open so pane identity is stable, and it dies on demand so the
	// process-exit scenario is real rather than simulated.
	h.tmux(t, "new-session", "-d", "-s", h.Session, "-x", "80", "-y", "24", "cat")
	t.Cleanup(func() {
		// Kill the server by explicit socket path, never a bare kill-server: -S
		// bounds the blast radius to the file this harness created, whatever the
		// environment happens to say by then.
		_ = exec.Command("tmux", "-S", socket, "kill-server").Run()
		_ = os.RemoveAll(socketDir)
	})

	h.PaneID = strings.TrimSpace(h.tmux(t, "display-message", "-p", "-t", h.Session, "#{pane_id}"))
	pidText := strings.TrimSpace(h.tmux(t, "display-message", "-p", "-t", h.Session, "#{pane_pid}"))
	pid, err := strconv.Atoi(pidText)
	if err != nil {
		t.Fatalf("pane pid %q: %v", pidText, err)
	}
	h.PanePID = pid
	h.ServerIncarnation = tmuxserver.FromPath(socket).String()

	if h.PaneID == "" || h.ServerIncarnation == "" {
		t.Fatalf("harness did not resolve pane identity: pane=%q incarnation=%q", h.PaneID, h.ServerIncarnation)
	}
	return h
}

// tmux runs one socket-scoped tmux command. There is no variant of this that
// omits -S.
func (h *Harness) tmux(t *testing.T, args ...string) string {
	t.Helper()
	out, err := exec.Command("tmux", append([]string{"-S", h.Socket}, args...)...).CombinedOutput()
	if err != nil {
		t.Fatalf("tmux %s: %v: %s", strings.Join(args, " "), err, out)
	}
	return string(out)
}

// Identity returns the live identity for a run in this harness's pane.
func (h *Harness) Identity(provider, runID string) agentlifecycle.Identity {
	return agentlifecycle.Identity{
		Host:              h.Host,
		ServerIncarnation: h.ServerIncarnation,
		PaneID:            h.PaneID,
		Provider:          provider,
		RunID:             runID,
		ProcessGeneration: fmt.Sprintf("pid=%d", h.PanePID),
	}
}

// KillPaneProcess ends the pane's process, which is the process-exit scenario.
// It returns once the pane is gone.
func (h *Harness) KillPaneProcess(t *testing.T) {
	t.Helper()
	_ = exec.Command("tmux", "-S", h.Socket, "kill-session", "-t", h.Session).Run()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		if !h.PaneAlive() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("pane process did not exit")
}

// PaneAlive reports whether the harness pane still exists.
func (h *Harness) PaneAlive() bool {
	out, err := exec.Command("tmux", "-S", h.Socket, "list-panes", "-a", "-F", "#{pane_id}").Output()
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(out), "\n") {
		if strings.TrimSpace(line) == h.PaneID {
			return true
		}
	}
	return false
}

// Provider returns a fake provider integration bound to this pane.
func (h *Harness) Provider(providerName, source, runID string) *Provider {
	return &Provider{
		harness:  h,
		provider: providerName,
		source:   source,
		version:  "1",
		runID:    runID,
		sink:     h.Recorder,
		now:      time.Date(2026, 8, 30, 17, 0, 0, 0, time.UTC),
	}
}

// Provider emits scripted lifecycle reports as one integration source.
type Provider struct {
	harness  *Harness
	provider string
	source   string
	version  string
	runID    string
	session  string
	sink     Sink
	now      time.Time
	seq      uint64
	failNext bool
	failures int
}

// WithSink redirects this provider at another sink, which is how Phase B points
// the same scenarios at the JSONL store.
func (p *Provider) WithSink(s Sink) *Provider { p.sink = s; return p }

// Advance moves the provider's clock, so freshness can be exercised without
// sleeping.
func (p *Provider) Advance(d time.Duration) { p.now = p.now.Add(d) }

// Now is the provider's current clock reading.
func (p *Provider) Now() time.Time { return p.now }

// RunID is the run this provider reports for.
func (p *Provider) RunID() string { return p.runID }

// Source is this integration's identifier.
func (p *Provider) Source() string { return p.source }

// Identity is the identity this provider stamps on its reports.
func (p *Provider) Identity() agentlifecycle.Identity {
	id := p.harness.Identity(p.provider, p.runID)
	id.SessionFingerprint = p.session
	return id
}

// FailNextHook makes the next emission fail the way a broken hook does: the
// report is never delivered, and the provider carries on. Nothing about the
// agent's own operation changes, which is the property the plan requires.
func (p *Provider) FailNextHook() { p.failNext = true }

// HookFailures counts emissions dropped by FailNextHook.
func (p *Provider) HookFailures() int { return p.failures }

// RotateSession changes the provider's session fingerprint, as a provider does
// when it starts or resumes a different session.
func (p *Provider) RotateSession(fingerprint string) { p.session = fingerprint }

// Child returns a provider representing a subagent: same pane and same source,
// a distinct run. Keeping the run distinct is what lets a test prove a child
// cannot block or complete its parent.
func (p *Provider) Child(runID string) *Provider {
	c := *p
	c.runID = runID
	c.seq = 0
	return &c
}

// build assembles the next report without emitting it.
func (p *Provider) build(kind agentlifecycle.Kind, state agentactivity.State, outcome agentlifecycle.Outcome, reason agentlifecycle.ReasonCode) agentlifecycle.Report {
	p.seq++
	return agentlifecycle.Report{
		SchemaVersion: agentlifecycle.SchemaVersion,
		ID:            fmt.Sprintf("%s-%s-%d", p.source, p.runID, p.seq),
		Kind:          kind,
		Identity:      p.Identity(),
		Source:        p.source,
		SourceVersion: p.version,
		Sequence:      p.seq,
		State:         state,
		Outcome:       outcome,
		ObservedAt:    p.now,
		Reason:        reason,
	}
}

// emit delivers a report unless a hook failure was armed.
func (p *Provider) emit(r agentlifecycle.Report) agentlifecycle.Report {
	if p.failNext {
		p.failNext = false
		p.failures++
		return r
	}
	_, _ = p.sink.Append(r)
	return r
}

// Working, Blocked, and Idle emit ordinary lane reports.
func (p *Provider) Working(reason agentlifecycle.ReasonCode) agentlifecycle.Report {
	return p.emit(p.build(agentlifecycle.KindState, agentactivity.StateWorking, "", reason))
}

func (p *Provider) Blocked(reason agentlifecycle.ReasonCode) agentlifecycle.Report {
	return p.emit(p.build(agentlifecycle.KindState, agentactivity.StateBlocked, "", reason))
}

func (p *Provider) Idle(reason agentlifecycle.ReasonCode) agentlifecycle.Report {
	return p.emit(p.build(agentlifecycle.KindState, agentactivity.StateIdle, "", reason))
}

// SessionReport announces session identity without asserting a lane.
func (p *Provider) SessionReport(fingerprint string) agentlifecycle.Report {
	p.RotateSession(fingerprint)
	return p.emit(p.build(agentlifecycle.KindSession, "", "", agentlifecycle.ReasonSessionStart))
}

// End finishes the run with an outcome. Cancel is the cancellation scenario.
func (p *Provider) End(outcome agentlifecycle.Outcome, reason agentlifecycle.ReasonCode) agentlifecycle.Report {
	return p.emit(p.build(agentlifecycle.KindEnd, "", outcome, reason))
}

func (p *Provider) Cancel() agentlifecycle.Report {
	return p.End(agentlifecycle.OutcomeCancelled, agentlifecycle.ReasonCancelled)
}

// Release surrenders authority without claiming an outcome.
func (p *Provider) Release(reason agentlifecycle.ReasonCode) agentlifecycle.Report {
	return p.emit(p.build(agentlifecycle.KindRelease, "", "", reason))
}

// Replay re-delivers an already-emitted report verbatim. A correct sink treats
// it as a duplicate and does not advance anything.
func (p *Provider) Replay(r agentlifecycle.Report) (agentlifecycle.Acceptance, error) {
	return p.sink.Append(r)
}

// EmitOutOfOrder delivers a report carrying an explicit stale sequence, which
// is what a provider with no ordering guarantee produces under load.
func (p *Provider) EmitOutOfOrder(state agentactivity.State, reason agentlifecycle.ReasonCode, seq uint64) (agentlifecycle.Acceptance, error) {
	r := p.build(agentlifecycle.KindState, state, "", reason)
	p.seq-- // an out-of-order delivery does not consume a new sequence
	r.Sequence = seq
	return p.sink.Append(r)
}

// Latest returns the sink's current record for this provider's run.
func (p *Provider) Latest() (agentlifecycle.Report, bool) {
	return p.sink.Latest(agentlifecycle.Report{Identity: p.Identity(), Source: p.source}.Key())
}
