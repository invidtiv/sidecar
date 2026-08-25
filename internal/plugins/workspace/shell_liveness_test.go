package workspace

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/shellliveness"
	"github.com/marcus/sidecar/internal/tmuxserver"
	"github.com/marcus/sidecar/internal/tty"
)

// Typing `exit` is the ordinary way a shell ends, and until td-6a4100 the row
// and its shells.json entry both outlived it. These tests pin the two halves of
// the fix: a confirmed death closes the shell everywhere, and an unconfirmed
// one changes nothing.
//
// Package isolation (tmux server and Sidecar state root) comes from TestMain in
// tmux_isolation_test.go. No test here starts tmux; the probe is stubbed,
// because what is under test is the decision, not tmux's CLI.

const livenessSession = "sidecar-sh-sidecar-1"

func shellDeathPlugin(t *testing.T) (*Plugin, string) {
	t.Helper()
	workDir := filepath.Join(t.TempDir(), "sidecar")
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	manifestPath := filepath.Join(t.TempDir(), "shells.json")
	manifest, err := LoadShellManifest(manifestPath)
	if err != nil {
		t.Fatalf("LoadShellManifest() error = %v", err)
	}
	if err := manifest.AddShell(ShellDefinition{TmuxName: livenessSession, DisplayName: "Shell 1", WorkDir: workDir}); err != nil {
		t.Fatalf("AddShell() error = %v", err)
	}

	p := New()
	p.SetFocused(true)
	p.ctx = &plugin.Context{WorkDir: workDir, ProjectRoot: workDir}
	p.viewMode = ViewModeList
	p.shellManifest = manifest
	p.shells = []*ShellSession{{
		Name:     "Shell 1",
		TmuxName: livenessSession,
		WorkDir:  workDir,
		Agent: &Agent{
			Type: AgentShell, TmuxSession: livenessSession, TmuxPane: "%9",
			OutputBuf: tty.NewOutputBuffer(outputBufferCap),
		},
	}}
	// Production records liveness from a successful discovery listing, a
	// successful capture, or a create. The fixture stands in for the first.
	p.noteShellAlive(livenessSession)
	return p, manifestPath
}

func stubLivenessProbe(t *testing.T, verdict shellliveness.Verdict) *int {
	t.Helper()
	calls := 0
	previous := shellLivenessProbe
	shellLivenessProbe = func(string) shellliveness.Verdict {
		calls++
		return verdict
	}
	t.Cleanup(func() { shellLivenessProbe = previous })
	return &calls
}

// drive runs one command and feeds its message back into the plugin, which is
// what the Bubble Tea runtime would do.
func drive(t *testing.T, p *Plugin, cmd tea.Cmd) (*Plugin, tea.Msg, tea.Cmd) {
	t.Helper()
	if cmd == nil {
		t.Fatal("expected a command, got nil")
	}
	msg := cmd()
	if msg == nil {
		t.Fatal("command produced no message")
	}
	updated, next := p.Update(msg)
	plug, ok := updated.(*Plugin)
	if !ok {
		t.Fatalf("Update returned %T, want *Plugin", updated)
	}
	return plug, msg, next
}

func manifestContains(t *testing.T, path, tmuxName string) bool {
	t.Helper()
	manifest, err := LoadShellManifest(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatalf("load manifest: %v", err)
	}
	for _, shell := range manifest.Shells {
		if shell.TmuxName == tmuxName {
			return true
		}
	}
	return false
}

func TestExitedShellLeavesTheListAndTheManifest(t *testing.T) {
	p, manifestPath := shellDeathPlugin(t)
	stubLivenessProbe(t, shellliveness.Gone)
	if !manifestContains(t, manifestPath, livenessSession) {
		t.Fatal("precondition: the shell is not in the manifest")
	}

	generation := p.pollScheduler.Invalidate(shellPollKey(livenessSession))
	p, msg, closeCmd := drive(t, p, p.handleShellDeathSuspected(shellDeathSuspectedMsg{
		TmuxName: livenessSession, Generation: generation,
	}))
	if probed, ok := msg.(shellDeathProbedMsg); !ok || probed.Verdict != shellliveness.Gone {
		t.Fatalf("suspicion produced %#v, want a Gone probe result", msg)
	}
	// The confirmed probe hands back the close; running it is what the runtime
	// would do next.
	p, _, _ = drive(t, p, closeCmd)

	if len(p.shells) != 0 {
		t.Fatalf("shells after a confirmed death = %d, want the row gone", len(p.shells))
	}
	if manifestContains(t, manifestPath, livenessSession) {
		t.Fatal("a stale shells.json entry survived the close")
	}
}

func TestTransientTmuxFailureKeepsTheShell(t *testing.T) {
	p, manifestPath := shellDeathPlugin(t)
	stubLivenessProbe(t, shellliveness.Unknown)

	generation := p.pollScheduler.Invalidate(shellPollKey(livenessSession))
	p, msg, cmd := drive(t, p, p.handleShellDeathSuspected(shellDeathSuspectedMsg{
		TmuxName: livenessSession, Generation: generation,
	}))
	if probed, ok := msg.(shellDeathProbedMsg); !ok || probed.Verdict != shellliveness.Unknown {
		t.Fatalf("suspicion produced %#v, want an Unknown probe result", msg)
	}

	if len(p.shells) != 1 {
		t.Fatalf("shells after an unreachable tmux = %d, want the shell kept", len(p.shells))
	}
	if !manifestContains(t, manifestPath, livenessSession) {
		t.Fatal("an unconfirmed death deleted the manifest entry")
	}
	if cmd == nil {
		t.Fatal("an unconfirmed death stopped polling the shell")
	}
}

// A shell this plugin never saw running is an offline row a reboot left behind,
// which the recreate path owns. The gate is the tracker's liveness record, not
// the presence of an Agent — the nested sibling projection synthesises Agents
// for rows on tmux servers this instance cannot even see.
func TestUnobservedShellIsNeverProbedIntoOblivion(t *testing.T) {
	p, manifestPath := shellDeathPlugin(t)
	p.shellLivenessTracker().Forget(livenessSession)
	p.shells[0].IsOrphaned = true
	calls := stubLivenessProbe(t, shellliveness.Gone)

	generation := p.pollScheduler.Invalidate(shellPollKey(livenessSession))
	if cmd := p.handleShellDeathSuspected(shellDeathSuspectedMsg{
		TmuxName: livenessSession, Generation: generation,
	}); cmd != nil {
		if msg := cmd(); msg != nil {
			updated, _ := p.Update(msg)
			p = updated.(*Plugin)
		}
	}
	if len(p.shells) != 1 || !manifestContains(t, manifestPath, livenessSession) {
		t.Fatalf("an offline row was auto-closed (shells=%d, probes=%d)", len(p.shells), *calls)
	}
	// Not merely refused at the end: the gate must stop it before tmux is
	// asked at all, which is the rule both surfaces now share.
	if *calls != 0 {
		t.Fatalf("probes taken for a never-observed shell = %d, want 0", *calls)
	}
}

// A poll result that arrives under a superseded generation belongs to a shell
// the plugin has already moved on from, and must not close anything.
func TestStaleGenerationCannotCloseAShell(t *testing.T) {
	p, manifestPath := shellDeathPlugin(t)
	stubLivenessProbe(t, shellliveness.Gone)

	stale := p.pollScheduler.Invalidate(shellPollKey(livenessSession))
	p.pollScheduler.Invalidate(shellPollKey(livenessSession))

	if cmd := p.handleShellDeathSuspected(shellDeathSuspectedMsg{
		TmuxName: livenessSession, Generation: stale,
	}); cmd != nil {
		t.Fatal("a stale suspicion probed tmux")
	}
	updated, _ := p.Update(shellDeathProbedMsg{
		TmuxName: livenessSession, Generation: stale, Verdict: shellliveness.Gone,
	})
	p = updated.(*Plugin)
	if len(p.shells) != 1 || !manifestContains(t, manifestPath, livenessSession) {
		t.Fatal("a stale probe result closed the shell")
	}
}

// The generation-0 suspicions — an embedded terminal reporting its pane gone,
// a detach, a refused send — deliberately bypass the poll-generation check, so
// the incarnation is the only thing standing between a stale verdict and a
// deleted shell. Recreating an offline row reuses its tmux name, and
// ShellCreatedMsg is what marks the new life (td-6a4100).
func TestShellRecreatedWhileConfirmingIsNotClosed(t *testing.T) {
	p, manifestPath := shellDeathPlugin(t)
	stubLivenessProbe(t, shellliveness.Gone)

	// A terminal reports the pane gone: suspicion with no poll owner.
	cmd := p.handleShellDeathSuspected(shellDeathSuspectedMsg{TmuxName: livenessSession})
	if cmd == nil {
		t.Fatal("a generation-0 suspicion did not probe")
	}
	probed, ok := cmd().(shellDeathProbedMsg)
	if !ok || probed.Verdict != shellliveness.Gone {
		t.Fatalf("probe produced %#v, want a Gone verdict", cmd())
	}

	// Before the verdict is applied, the user recreates the shell under the
	// same tmux name.
	updated, _ := p.Update(ShellCreatedMsg{SessionName: livenessSession, DisplayName: "Shell 1", PaneID: "%42"})
	p = updated.(*Plugin)

	updated, closeCmd := p.Update(probed)
	p = updated.(*Plugin)
	// Whatever the verdict produced must be carried all the way through, or the
	// test passes on a close that simply had not run yet.
	if closeCmd != nil {
		if msg := closeCmd(); msg != nil {
			updated, _ = p.Update(msg)
			p = updated.(*Plugin)
		}
	}

	if len(p.shells) != 1 {
		t.Fatal("a verdict from before the recreate closed the live shell")
	}
	if !manifestContains(t, manifestPath, livenessSession) {
		t.Fatal("a verdict from before the recreate deleted the live shell's manifest entry")
	}
}

// The whole tmux server going away is not evidence that any one shell exited,
// and it arrives for every shell at once.
func TestTotalTmuxLossClosesNothing(t *testing.T) {
	p, manifestPath := shellDeathPlugin(t)
	// This is what the probe reports when there is no server to ask.
	stubLivenessProbe(t, shellliveness.Unknown)

	generation := p.pollScheduler.Invalidate(shellPollKey(livenessSession))
	p, _, _ = drive(t, p, p.handleShellDeathSuspected(shellDeathSuspectedMsg{
		TmuxName: livenessSession, Generation: generation,
	}))

	if len(p.shells) != 1 || !manifestContains(t, manifestPath, livenessSession) {
		t.Fatal("a tmux server that could not be reached closed a shell")
	}
}

func stubLivenessServer(t *testing.T, inc tmuxserver.Incarnation) *tmuxserver.Incarnation {
	t.Helper()
	current := inc
	previous := shellLivenessServer
	shellLivenessServer = func() tmuxserver.Incarnation { return current }
	t.Cleanup(func() { shellLivenessServer = previous })
	return &current
}

// Sidecar running outside tmux sees a server restart on the next suspicion.
// The binding must ObserveServer on that path — construction is not enough —
// so ShouldProbe stays false and Confirm never fires (td-388929).
func TestServerRestartWhileRunningDoesNotCloseAShell(t *testing.T) {
	p, manifestPath := shellDeathPlugin(t)
	calls := stubLivenessProbe(t, shellliveness.Gone)
	server := stubLivenessServer(t, tmuxserver.Present(1, 2, 3))
	p.observeTmuxServer(*server)
	p.noteShellAlive(livenessSession)

	generation := p.pollScheduler.Invalidate(shellPollKey(livenessSession))
	if cmd := p.handleShellDeathSuspected(shellDeathSuspectedMsg{
		TmuxName: livenessSession, Generation: generation,
	}); cmd == nil {
		t.Fatal("precondition: a live shell on the first server must be probed")
	} else if msg := cmd(); msg != nil {
		if probed, ok := msg.(shellDeathProbedMsg); !ok || probed.Server != *server {
			t.Fatalf("probe tagged %#v, want server %v", msg, *server)
		}
	}

	*server = tmuxserver.Present(9, 10, 11)
	generation = p.pollScheduler.Invalidate(shellPollKey(livenessSession))
	if cmd := p.handleShellDeathSuspected(shellDeathSuspectedMsg{
		TmuxName: livenessSession, Generation: generation,
	}); cmd != nil {
		if msg := cmd(); msg != nil {
			if _, ok := msg.(shellDeathProbedMsg); ok {
				t.Fatal("a server restart was probed as a shell death")
			}
		}
	}
	if p.shellLivenessTracker().SeenAlive(livenessSession) {
		t.Fatal("workspace binding did not reset seenAlive on the live transition")
	}
	if *calls != 1 {
		t.Fatalf("probes = %d, want 1 (the pre-restart suspicion only)", *calls)
	}
	if len(p.shells) != 1 || !manifestContains(t, manifestPath, livenessSession) {
		t.Fatal("a server restart closed a shell")
	}
}

// A verdict taken under the previous server must not close a shell that has
// been re-seen on the new one — same shape as a reused tmux name.
func TestStaleServerVerdictDoesNotCloseAShell(t *testing.T) {
	p, manifestPath := shellDeathPlugin(t)
	stubLivenessProbe(t, shellliveness.Gone)
	server := stubLivenessServer(t, tmuxserver.Present(1, 2, 3))
	p.observeTmuxServer(*server)
	p.noteShellAlive(livenessSession)

	generation := p.pollScheduler.Invalidate(shellPollKey(livenessSession))
	cmd := p.handleShellDeathSuspected(shellDeathSuspectedMsg{
		TmuxName: livenessSession, Generation: generation,
	})
	if cmd == nil {
		t.Fatal("precondition: expected a probe")
	}
	probed, ok := cmd().(shellDeathProbedMsg)
	if !ok || probed.Verdict != shellliveness.Gone {
		t.Fatalf("probe produced %#v", cmd())
	}

	*server = tmuxserver.Present(9, 10, 11)
	p.observeTmuxServer(*server)
	p.noteShellAlive(livenessSession)

	updated, closeCmd := p.Update(probed)
	p = updated.(*Plugin)
	if closeCmd != nil {
		if msg := closeCmd(); msg != nil {
			updated, _ = p.Update(msg)
			p = updated.(*Plugin)
		}
	}
	if len(p.shells) != 1 || !manifestContains(t, manifestPath, livenessSession) {
		t.Fatal("a verdict from a previous server closed a live shell")
	}
}
