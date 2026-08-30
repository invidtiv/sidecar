package workspace

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentcontrol"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestWithShellNamingInstructionOnlyForSupportedHarnesses(t *testing.T) {
	got := withShellNamingInstruction("claude", AgentClaude)
	if !strings.HasPrefix(got, "claude --append-system-prompt ") {
		t.Fatalf("command = %q, want appended system prompt", got)
	}
	if !strings.Contains(got, "sidecar shell rename") {
		t.Fatalf("command = %q, want the rename instruction", got)
	}
	// Trigger is task mismatch, not only the generated default name.
	if !strings.Contains(got, "previous task") {
		t.Fatalf("command = %q, want stale previous-task guidance", got)
	}
	if !strings.Contains(got, "sidecar shell name") {
		t.Fatalf("command = %q, want shell name lookup fallback", got)
	}
	// A single-quoted argument keeps the instruction's backticks and quotes
	// from reaching the shell that runs the launch command.
	if strings.Contains(got, "$(") || strings.Contains(got, "\n") {
		t.Fatalf("command = %q, want a fully quoted instruction", got)
	}

	// Grok documents --rules (alias --append-system-prompt) for session rules.
	grok := withShellNamingInstruction("grok", AgentGrok)
	if !strings.HasPrefix(grok, "grok --rules ") {
		t.Fatalf("grok command = %q, want --rules append", grok)
	}
	if !strings.Contains(grok, "sidecar shell rename") {
		t.Fatalf("grok command = %q, want the rename instruction", grok)
	}

	// A harness with no documented append flag is launched unchanged rather
	// than with a guessed flag that would break the launch.
	if got := withShellNamingInstruction("codex", AgentCodex); got != "codex" {
		t.Fatalf("codex command = %q, want unchanged", got)
	}
}

func TestProjectShellCatalogLaunchUsesStructuredAgentControl(t *testing.T) {
	root := t.TempDir()
	p := &Plugin{
		ctx:    &plugin.Context{ProjectRoot: root, WorkDir: root, Config: config.Default()},
		shells: []*ShellSession{{Name: "Reviewer", TmuxName: "sidecar-sh-reviewer"}},
	}
	originalWait, originalStart := waitWorkspaceShellReady, startWorkspaceAgent
	defer func() { waitWorkspaceShellReady, startWorkspaceAgent = originalWait, originalStart }()
	waitWorkspaceShellReady = func(_ context.Context, target agentcontrol.Target, _ time.Duration) (agentcontrol.Snapshot, error) {
		return agentcontrol.Snapshot{Target: target}, nil
	}
	var request agentcontrol.StartRequest
	startWorkspaceAgent = func(_ context.Context, got agentcontrol.StartRequest) (agentcontrol.Agent, error) {
		request = got
		return agentcontrol.Agent{Target: got.Target}, nil
	}

	msg := p.startAgentInShell("sidecar-sh-reviewer", AgentClaude, true)()
	if result, ok := msg.(ShellAgentErrorMsg); ok {
		t.Fatalf("start failed: %v", result.Err)
	}
	if _, ok := msg.(ShellAgentStartedMsg); !ok {
		t.Fatalf("result = %T, want ShellAgentStartedMsg", msg)
	}
	if request.Kind != "claude" || request.Target.Name != "Reviewer" || request.Target.Project != workspaceinventory.CanonicalPath(root) || request.Timeout != agentStartTimeout {
		t.Fatalf("request = %+v", request)
	}
	wantPrefix := []string{"claude", "--dangerously-skip-permissions", "--append-system-prompt"}
	if len(request.Argv) != 4 || strings.Join(request.Argv[:3], " ") != strings.Join(wantPrefix, " ") || request.Argv[3] != shellstate.NamingInstruction {
		t.Fatalf("structured argv = %#v", request.Argv)
	}
}
