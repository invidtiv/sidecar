package workspace

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestExpandedProviderListKanbanGolden(t *testing.T) {
	providers := []AgentType{AgentPi, AgentCopilot, AgentCursor, AgentOpenCode, AgentAmp}
	p := &Plugin{ctx: &plugin.Context{}}
	var lines []string
	for _, provider := range providers {
		agent := &Agent{Type: provider, Activity: agentactivity.Tracker{State: agentactivity.StateBlocked}}
		wt := &Worktree{Name: string(provider) + "-proof", Status: StatusWaiting, Agent: agent}
		list := strings.Join(strings.Fields(ansi.Strip(p.renderWorktreeItem(wt, false, 64))), " ")
		kanban := strings.Join(strings.Fields(ansi.Strip(p.renderKanbanCardLine(wt, 0, 32, false)+" "+p.renderKanbanCardLine(wt, 1, 32, false))), " ")
		lines = append(lines, string(provider)+" | List: "+list+" | Kanban: "+kanban)
	}
	got := strings.Join(lines, "\n") + "\n"
	want, err := os.ReadFile(filepath.Join("testdata", "expanded-provider-surfaces.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if got != string(want) {
		t.Fatalf("surface proof mismatch\ngot:\n%s\nwant:\n%s", got, want)
	}
}

func TestActivityRenderingParityForWorktreeAndAgentShell(t *testing.T) {
	p := &Plugin{activePane: PaneSidebar, ctx: &plugin.Context{}}
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateBlocked}}
	wt := &Worktree{Name: "feature", Status: StatusActive, Agent: agent}
	shell := &ShellSession{Name: "review", ChosenAgent: AgentCodex, Agent: agent}

	for name, output := range map[string]string{
		"worktree": p.renderWorktreeItem(wt, true, 40),
		"shell":    p.renderShellEntryForSession(shell, true, 40),
	} {
		// ◆ is the blocked status glyph; selected rows use plain AgentLabel
		// (▶ codex) so the selection background stays uniform.
		if !strings.Contains(output, "◆") || !strings.Contains(output, "blocked") {
			t.Fatalf("%s rendering lacks glyph/text parity: %q", name, output)
		}
		if !strings.Contains(output, "▶") || !strings.Contains(ansi.Strip(output), "codex") {
			t.Fatalf("%s rendering lacks agent chip label: %q", name, output)
		}
	}
}

func TestUnselectedShellUsesAgentChip(t *testing.T) {
	p := &Plugin{activePane: PaneSidebar, ctx: &plugin.Context{}}
	agent := &Agent{Type: AgentGrok, Activity: agentactivity.Tracker{State: agentactivity.StateIdle}}
	agent.Activity.Acknowledge() // seen idle → ○ + "idle"
	shell := &ShellSession{Name: "GROK", ChosenAgent: AgentGrok, Agent: agent}
	got := p.renderShellEntryForSession(shell, false, 40)
	stripped := ansi.Strip(got)
	if !strings.Contains(stripped, "✦") || !strings.Contains(stripped, "grok") {
		t.Fatalf("unselected shell missing agent chip: %q", stripped)
	}
	if !strings.Contains(stripped, "idle") {
		t.Fatalf("unselected shell missing status: %q", stripped)
	}
}

func TestSharedRowKeepsProjectSpecificWorktreeLabels(t *testing.T) {
	p := &Plugin{activePane: PaneSidebar, ctx: &plugin.Context{}}
	wt := &Worktree{
		Name: "feature", Branch: "feature/rows", TaskID: "td-row", PRURL: "https://example.test/pr/1",
		ChosenAgentType: AgentClaude, Status: StatusWaiting, Stats: &GitStats{Additions: 12, Deletions: 3},
	}
	plain := ansi.Strip(p.renderWorktreeItem(wt, false, 64))
	for _, want := range []string{"feature", " PR", "branch feature/rows", "claude", "td-row", "+12 -3"} {
		if !strings.Contains(plain, want) {
			t.Fatalf("project row lost %q: %q", want, plain)
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
	wt := &Worktree{Name: "feature", Status: StatusWaiting, Agent: agent}
	if got := p.renderWorktreeItem(wt, false, 40); !strings.Contains(got, "○") || !strings.Contains(got, "idle") {
		t.Fatalf("seen worktree idle: %q", got)
	}
}
