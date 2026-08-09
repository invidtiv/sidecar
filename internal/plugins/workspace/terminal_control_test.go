package workspace

import (
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/tty"
)

func newTerminalEmbeddingTestPlugin() *Plugin {
	p := New()
	p.focused = true
	p.applicationFocused = true
	p.viewMode = ViewModeList
	p.previewTab = PreviewTabOutput
	p.primaryTerminal = p.newWorkspaceTerminal()
	p.panelTerminal = p.newWorkspaceTerminal()
	return p
}

func openTestTerminal(t *testing.T, p *Plugin, role workspaceTerminalRole, target workspaceTerminalTarget) *tty.Model {
	t.Helper()
	// Zero geometry keeps this a fake-only contract: Open neither resizes nor
	// executes any command returned by the model.
	target.Width, target.Height = 0, 0
	p.reconcileTerminalModel(role, target, true)
	model, _ := p.terminalModelAndTarget(role)
	if model == nil || model.State == nil {
		t.Fatal("terminal did not open")
	}
	return model
}

func applyFallbackCapture(model *tty.Model, output string) {
	model.Update(tty.CaptureResultMsg{
		Scope: model.Scope(), PollGeneration: model.State.PollGeneration,
		Target: model.GetTarget(), Output: output,
		CursorRow: 1, CursorCol: 2, CursorVisible: true,
		PaneHeight: 20, PaneWidth: 80,
	})
}

func TestWorkspaceTerminalSwitchRejectsStaleCapture(t *testing.T) {
	p := newTerminalEmbeddingTestPlugin()
	first := workspaceTerminalTarget{Session: "first", Source: "agent", SourceID: "first-key"}
	model := openTestTerminal(t, p, workspaceTerminalPrimary, first)
	oldScope := model.Scope()
	oldPoll := model.State.PollGeneration

	second := workspaceTerminalTarget{Session: "second", Source: "agent", SourceID: "second-key"}
	openTestTerminal(t, p, workspaceTerminalPrimary, second)
	model.Update(tty.CaptureResultMsg{
		Scope: oldScope, PollGeneration: oldPoll, Target: "first", Output: "STALE-FIRST",
	})
	applyFallbackCapture(model, "SECOND")

	if got := model.State.OutputBuf.String(); got != "SECOND" {
		t.Fatalf("switched output = %q, want only SECOND", got)
	}
	if p.primaryTerminalTarget.SourceID != "second-key" {
		t.Fatalf("selected target = %#v", p.primaryTerminalTarget)
	}
}

func TestWorkspaceTerminalFallbackBindsModelBuffer(t *testing.T) {
	p := newTerminalEmbeddingTestPlugin()
	wt := &Worktree{Key: "wt-key", Name: "wt", Agent: &Agent{TmuxSession: "agent"}}
	p.worktrees = []*Worktree{wt}
	target := workspaceTerminalTarget{Session: "agent", Source: "agent", SourceID: wt.IdentityKey()}
	model := openTestTerminal(t, p, workspaceTerminalPrimary, target)
	p.bindTerminalBuffer(workspaceTerminalPrimary, target, model)
	applyFallbackCapture(model, "fallback visible")
	p.syncTerminalModels()

	if wt.Agent.OutputBuf != model.State.OutputBuf || wt.Agent.OutputBuf.String() != "fallback visible" {
		t.Fatalf("workspace did not render shared fallback buffer: %q", wt.Agent.OutputBuf.String())
	}
}

func TestWorkspaceHealthyTerminalHasNoPresentationPoll(t *testing.T) {
	p := newTerminalEmbeddingTestPlugin()
	p.termPanelSession = "panel"
	openTestTerminal(t, p, workspaceTerminalPanel, workspaceTerminalTarget{
		Session: "panel", Source: "panel", SourceID: "panel",
	})
	if cmd := p.scheduleTermPanelPoll(0); cmd != nil {
		t.Fatal("model-owned panel scheduled legacy presentation capture")
	}

	shell := &ShellSession{TmuxName: "shell", Agent: &Agent{Type: AgentShell, TmuxSession: "shell"}}
	p.shells = []*ShellSession{shell}
	openTestTerminal(t, p, workspaceTerminalPrimary, workspaceTerminalTarget{
		Session: "shell", Source: "shell", SourceID: "shell",
	})
	if shellSemanticNeedsScreen(shell.Agent.Type) {
		t.Fatal("plain model-owned shell requested screen capture for semantic evidence")
	}
	if cmd := p.scheduleShellPollByName("shell", 0); cmd == nil {
		t.Fatal("plain shell lost metadata-only agent discovery cadence")
	}
}

func TestWorkspaceActivityCadenceDoesNotOverwriteModelPresentation(t *testing.T) {
	p := newTerminalEmbeddingTestPlugin()
	wt := &Worktree{
		Key: "agent-key", Name: "agent", Status: StatusActive,
		Agent: &Agent{Type: AgentCodex, TmuxSession: "agent", OutputBuf: tty.NewOutputBuffer(20)},
	}
	p.worktrees = []*Worktree{wt}
	p.selectedIdx = 0
	target := workspaceTerminalTarget{Session: "agent", Source: "agent", SourceID: wt.IdentityKey()}
	model := openTestTerminal(t, p, workspaceTerminalPrimary, target)
	p.bindTerminalBuffer(workspaceTerminalPrimary, target, model)
	applyFallbackCapture(model, "model frame")
	p.syncTerminalModels()

	gen := p.pollScheduler.Invalidate(agentPollKey(wt.IdentityKey()))
	msg := AgentOutputMsg{
		WorkspaceName: wt.IdentityKey(), Generation: gen, AgentType: AgentCodex,
		Output: "semantic capture must not render", Status: StatusThinking,
		Activity:   agentactivity.Result{State: agentactivity.StateWorking, Evidence: "codex.title.working", VisibleWorking: true},
		CapturedAt: time.Now(),
	}
	p.update(msg)

	if got := wt.Agent.OutputBuf.String(); got != "model frame" {
		t.Fatalf("semantic capture overwrote model presentation: %q", got)
	}
	if wt.Agent.Activity.State != agentactivity.StateWorking || wt.Status != StatusActive {
		t.Fatalf("semantic activity not applied: activity=%s status=%s", wt.Agent.Activity.State, wt.Status)
	}
	if p.scheduleAgentPoll(wt.IdentityKey(), 0) == nil {
		t.Fatal("model authority stopped independent semantic activity cadence")
	}

	// List and Kanban consume the same activity projection. Kanban owns no
	// terminal rendering surface, so parity here is status/lane truthfulness.
	_, text, _, ok := activityPresentation(wt.Agent)
	if !ok || text != "working" {
		t.Fatalf("list activity projection = %q, ok=%v", text, ok)
	}
	columns := p.getKanbanColumns()
	if len(columns[kanbanLaneWorking]) != 1 || columns[kanbanLaneWorking][0] != wt {
		t.Fatalf("kanban working lane = %#v", columns[kanbanLaneWorking])
	}
}
