package lifecycleenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

const hintOccupantHelperEnv = "SIDECAR_TEST_HINT_OCCUPANT_HELPER"

// TestAnAgentHintCannotChangeTheOccupant is the regression for the seam that
// keeps SIDECAR_AGENT out of lifecycle authority.
//
// OccupantKind feeds VerifyReportedKind, which *refuses* a report whose claimed
// kind disagrees with the pane's occupant. SIDECAR_AGENT is an environment
// variable, so anything running in the session can set it. If it reached this
// function, exporting `SIDECAR_AGENT=codex` in a Claude pane would make Sidecar
// reject Claude's own reports — a writable variable would have acquired the
// power to switch a lifecycle lane off. agentactivity keeps the two resolvers
// apart for exactly that reason; this test pins the consequence from the side
// that would be harmed, so a future "simplification" that merges them fails
// here rather than in production.
//
// It proves both halves against the real platform adapter rather than a stub:
// the hint IS visible to the display resolver, so the test cannot pass merely
// because the hint never arrived, and is NOT visible to OccupantKind.
//
// The pane runs this test binary in a helper mode, which is not an incidental
// choice. macOS redacts the environment section of kern.procargs2 for
// SIP-protected system binaries, so a pane running /bin/sleep publishes no
// readable hint at all and the test would pass for the wrong reason. A binary
// this test built is readable, which is the same reason the darwin process
// identity test re-execs itself.
//
// The pane is a private tmux server on its own socket, per AGENTS.md. The
// machine's default server is never touched, and only the session this test
// created is killed.
func TestAnAgentHintCannotChangeTheOccupant(t *testing.T) {
	if os.Getenv(hintOccupantHelperEnv) == "1" {
		// Helper mode: occupy the pane long enough to be inspected, then exit
		// on its own so nothing is left behind if the cleanup below is skipped.
		time.Sleep(30 * time.Second)
		return
	}
	if !agentactivity.HasProcessIdentity() {
		t.Skip("no process-identity adapter on this platform, so there is no occupant to confuse")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux unavailable")
	}
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	// tmux's Unix socket path has a small platform limit, and Go's ordinary
	// test temp path includes the full test name and exceeds it on macOS.
	root, err := os.MkdirTemp("/tmp", "sc-hint-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(root) })
	socket := filepath.Join(root, "tmux.sock")
	session := "occupant-hint"

	// The pane's program is deliberately not an agent. The only thing here that
	// names a provider is the exported hint, so any provider OccupantKind
	// reports can only have come from it.
	start := exec.Command("tmux", "-S", socket, "-f", "/dev/null", "new-session", "-d", "-s", session,
		"-e", agentactivity.AgentHintEnv+"=codex",
		"-e", hintOccupantHelperEnv+"=1",
		self, "-test.run=^TestAnAgentHintCannotChangeTheOccupant$")
	start.Env = append(os.Environ(), "TMUX=")
	if out, err := start.CombinedOutput(); err != nil {
		t.Skipf("could not start a private tmux server: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	// The pane's command is read alongside its pid because the display resolver
	// takes both: pane_current_command is what decides how hard it may look, and
	// this pane's command — a test binary — is exactly the unrecognised-wrapper
	// shape the hint exists for, resolved with no process-table walk.
	var panePID int
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		out, inspectErr := exec.Command("tmux", "-S", socket, "display-message", "-p", "-t", session,
			"#{pane_pid} #{pane_current_command}").Output()
		if inspectErr == nil {
			rawPID, command, _ := strings.Cut(strings.TrimSpace(string(out)), " ")
			if pid, parseErr := strconv.Atoi(rawPID); parseErr == nil && pid > 0 {
				if agentactivity.ResolveForegroundAgent(pid, command) == "codex" {
					panePID = pid
					break
				}
			}
		}
		time.Sleep(20 * time.Millisecond)
	}
	if panePID == 0 {
		t.Fatal("the private pane never published a readable hint, so the half of this test that " +
			"proves the hint arrived did not run; ResolveForegroundAgent never saw SIDECAR_AGENT=codex")
	}

	if got := OccupantKind(panePID); got != "" {
		t.Fatalf("OccupantKind = %q for a pane whose only provider evidence is %s=codex.\n"+
			"An environment variable has become able to name the pane's occupant, which means it\n"+
			"can also make VerifyReportedKind refuse a legitimate provider's reports. The hint\n"+
			"belongs to agentactivity.ResolveForegroundAgent and must never reach\n"+
			"ResolveForegroundProcess.", got, agentactivity.AgentHintEnv)
	}

	// And therefore a report claiming any kind is still accepted, because an
	// unnamable occupant is an absence of evidence rather than a mismatch.
	if err := VerifyReportedKind(panePID, "claude"); err != nil {
		t.Fatalf("VerifyReportedKind refused a report on a pane with no identifiable occupant: %v", err)
	}
}
