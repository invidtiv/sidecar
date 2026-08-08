package workspace

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
)

func TestActivityTitleOnlyUnchangedPollUpdatesWorktree(t *testing.T) {
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateIdle, Seen: true}}
	p := &Plugin{worktrees: []*Worktree{{Name: "w", Agent: agent}}, selectedIdx: -1}
	p.update(AgentPollUnchangedMsg{
		WorkspaceName: "w",
		Activity:      agentactivity.Result{State: agentactivity.StateWorking, Evidence: "codex.title.working"},
		PaneTitle:     "⠼ repo", CurrentCommand: "node",
	})
	if agent.Activity.State != agentactivity.StateWorking {
		t.Fatalf("title-only unchanged poll left activity=%q evidence=%q", agent.Activity.State, agent.Activity.Evidence)
	}
}

func TestBackgroundedWorktreeDoesNotAcknowledgeCompletion(t *testing.T) {
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	p := &Plugin{
		worktrees: []*Worktree{{Name: "w", Agent: agent}}, selectedIdx: 0,
		previewTab: PreviewTabOutput, viewMode: ViewModeList, focused: false,
	}
	idle := AgentOutputMsg{WorkspaceName: "w", Activity: agentactivity.Result{State: agentactivity.StateIdle, Evidence: "codex.screen.idle"}}
	p.update(idle)
	time.Sleep(agentactivity.IdleDebounce)
	idle.Generation = p.pollScheduler.Current(agentPollKey("w"))
	p.update(idle)
	if got := agent.Activity.DisplayState(); got != "done" {
		t.Fatalf("background completion displays %q, want done", got)
	}
}

func TestBackgroundedShellDoesNotAcknowledgeCompletion(t *testing.T) {
	agent := &Agent{Type: AgentCodex, OutputBuf: nil, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	shell := &ShellSession{Name: "s", TmuxName: "sidecar-sh-test", ChosenAgent: AgentCodex, Agent: agent}
	p := &Plugin{
		shells: []*ShellSession{shell}, shellSelected: true, selectedShellIdx: 0,
		previewTab: PreviewTabOutput, viewMode: ViewModeList, focused: false,
	}
	idle := ShellOutputMsg{TmuxName: shell.TmuxName, Activity: agentactivity.Result{State: agentactivity.StateIdle, Evidence: "codex.screen.idle"}}
	p.update(idle)
	time.Sleep(agentactivity.IdleDebounce)
	idle.Generation = p.pollScheduler.Current(shellPollKey(shell.TmuxName))
	p.update(idle)
	if got := agent.Activity.DisplayState(); got != "done" {
		t.Fatalf("background shell completion displays %q, want done", got)
	}
}

func TestVisibleFocusedEntriesAcknowledgeIdle(t *testing.T) {
	worktreeAgent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateIdle}}
	p := &Plugin{
		worktrees: []*Worktree{{Name: "w", Agent: worktreeAgent}}, selectedIdx: 0,
		previewTab: PreviewTabOutput, viewMode: ViewModeList, focused: true,
	}
	p.update(AgentPollUnchangedMsg{WorkspaceName: "w", Activity: agentactivity.Result{State: agentactivity.StateIdle, Evidence: "idle"}})
	if got := worktreeAgent.Activity.DisplayState(); got != "idle" {
		t.Fatalf("visible worktree displays %q", got)
	}

	shellAgent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateIdle}}
	shell := &ShellSession{TmuxName: "sidecar-sh-test", ChosenAgent: AgentCodex, Agent: shellAgent}
	p = &Plugin{shells: []*ShellSession{shell}, shellSelected: true, selectedShellIdx: 0, previewTab: PreviewTabOutput, viewMode: ViewModeList, focused: true}
	p.update(ShellOutputMsg{TmuxName: shell.TmuxName, Activity: agentactivity.Result{State: agentactivity.StateIdle, Evidence: "idle"}})
	if got := shellAgent.Activity.DisplayState(); got != "idle" {
		t.Fatalf("visible shell displays %q", got)
	}
}
