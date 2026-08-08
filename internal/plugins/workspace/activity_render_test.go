package workspace

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestActivityRenderingParityForWorktreeAndAgentShell(t *testing.T) {
	p := &Plugin{activePane: PaneSidebar, ctx: &plugin.Context{}}
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateBlocked}}
	wt := &Worktree{Name: "feature", Agent: agent}
	shell := &ShellSession{Name: "review", ChosenAgent: AgentCodex, Agent: agent}

	for name, output := range map[string]string{
		"worktree": p.renderWorktreeItem(wt, true, 40),
		"shell":    p.renderShellEntryForSession(shell, true, 40),
	} {
		if !strings.Contains(output, "◆") || !strings.Contains(output, "blocked") {
			t.Fatalf("%s rendering lacks glyph/text parity: %q", name, output)
		}
	}
}

func TestActivityRenderingShowsUnseenDoneThenSeenIdle(t *testing.T) {
	p := &Plugin{activePane: PaneSidebar, ctx: &plugin.Context{}}
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateIdle}}
	shell := &ShellSession{Name: "review", ChosenAgent: AgentCodex, Agent: agent}
	if got := p.renderShellEntryForSession(shell, false, 40); !strings.Contains(got, "✓") || !strings.Contains(got, "done") {
		t.Fatalf("unseen idle: %q", got)
	}
	agent.Activity.Acknowledge()
	if got := p.renderShellEntryForSession(shell, false, 40); !strings.Contains(got, "○") || !strings.Contains(got, "idle") {
		t.Fatalf("seen idle: %q", got)
	}
}
