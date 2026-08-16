package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/shellliveness"
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
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return false
		}
		t.Fatalf("read manifest: %v", err)
	}
	return strings.Contains(string(data), tmuxName)
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

// A shell this plugin never saw running has no Agent, and a probe must not be
// able to speak for it: that is the shape of an offline row a reboot left
// behind, which the recreate path owns.
func TestUnobservedShellIsNeverProbedIntoOblivion(t *testing.T) {
	p, manifestPath := shellDeathPlugin(t)
	p.shells[0].Agent = nil
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
