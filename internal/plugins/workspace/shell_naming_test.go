package workspace

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/shellstate"
)

func TestWithShellNamingInstructionOnlyForSupportedHarnesses(t *testing.T) {
	got := withShellNamingInstruction("claude", AgentClaude)
	if !strings.HasPrefix(got, "claude --append-system-prompt ") {
		t.Fatalf("command = %q, want appended system prompt", got)
	}
	if !strings.Contains(got, "sidecar shell rename") {
		t.Fatalf("command = %q, want the rename instruction", got)
	}
	// A single-quoted argument keeps the instruction's backticks and quotes
	// from reaching the shell that runs the launch command.
	if strings.Contains(got, "$(") || strings.Contains(got, "\n") {
		t.Fatalf("command = %q, want a fully quoted instruction", got)
	}
	// A harness with no documented append flag is launched unchanged rather
	// than with a guessed flag that would break the launch.
	if got := withShellNamingInstruction("codex", AgentCodex); got != "codex" {
		t.Fatalf("codex command = %q, want unchanged", got)
	}
}

func TestShellEnvArgsPublishIdentity(t *testing.T) {
	args := shellEnvArgs("sidecar-sh-demo-3", "Shell 3")
	joined := strings.Join(args, " ")
	if !strings.Contains(joined, shellstate.NameEnv+"=Shell 3") {
		t.Fatalf("args = %v, want display name", args)
	}
	if !strings.Contains(joined, shellstate.SessionEnv+"=sidecar-sh-demo-3") {
		t.Fatalf("args = %v, want session name", args)
	}
	for i := 0; i < len(args); i += 2 {
		if args[i] != "-e" {
			t.Fatalf("args = %v, want each value preceded by -e", args)
		}
	}
}
