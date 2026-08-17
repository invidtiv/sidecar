package workspace

import (
	"bytes"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/tty"
)

func TestLosingFocusClosesVisibleTerminalModelsAndHiddenResizeDoesNotOwnGeometry(t *testing.T) {
	p := newTerminalEmbeddingTestPlugin()
	p.width, p.height = 100, 30
	model := openTestTerminal(t, p, workspaceTerminalPrimary, workspaceTerminalTarget{
		Session: "project", Pane: "%1", Source: "agent", SourceID: "worktree",
	})
	if !model.IsActive() {
		t.Fatal("test premise: project terminal did not open")
	}

	p.SetFocused(false)
	if model.IsActive() || p.primaryTerminalTarget != (workspaceTerminalTarget{}) {
		t.Fatalf("covered project retained terminal ownership: active=%v target=%+v", model.IsActive(), p.primaryTerminalTarget)
	}
	_, cmd := p.Update(tea.WindowSizeMsg{Width: 120, Height: 40})
	if cmd != nil {
		t.Fatal("covered project scheduled tmux geometry work on resize")
	}
	if p.width != 120 || p.height != 40 {
		t.Fatalf("covered project lost return geometry: %dx%d", p.width, p.height)
	}
}

func TestRegainingFocusReconcilesTheSelectedTerminalOnce(t *testing.T) {
	p := newTerminalEmbeddingTestPlugin()
	p.width, p.height = 100, 30
	p.sidebarVisible = false
	p.worktrees = []*Worktree{{
		Key: "worktree", Name: "worktree",
		Agent: &Agent{TmuxSession: "project", TmuxPane: "%1"},
	}}
	p.SetFocused(false)
	p.SetFocused(true)

	_, _ = p.Update(app.PluginFocusedMsg{})
	if p.primaryTerminal == nil || !p.primaryTerminal.IsActive() {
		t.Fatal("returning to project focus did not reopen the selected terminal")
	}
	if got := p.primaryTerminalTarget; got.Session != "project" || got.Pane != "%1" {
		t.Fatalf("restored terminal target = %+v", got)
	}
	firstGeneration := p.primaryTerminal.Scope().Generation
	_, _ = p.Update(app.PluginFocusedMsg{})
	if got := p.primaryTerminal.Scope().Generation; got != firstGeneration {
		t.Fatalf("duplicate focus notification reopened terminal: generation %d -> %d", firstGeneration, got)
	}
}

func TestTerminalCaptureTraceIsOptInAndMetadataOnly(t *testing.T) {
	var output bytes.Buffer
	logger := slog.New(slog.NewTextHandler(&output, nil))

	traceTerminalCapture(logger, "workspace", "agent", "semantic_activity", 4)
	if output.Len() != 0 {
		t.Fatalf("trace emitted without opt-in: %q", output.String())
	}

	t.Setenv(terminalTraceEnv, "1")
	traceTerminalCapture(logger, "workspace", "agent", "semantic_activity", 4)
	got := output.String()
	for _, want := range []string{"surface=workspace", "role=agent", "reason=semantic_activity", "generation=4"} {
		if !strings.Contains(got, want) {
			t.Fatalf("trace %q missing %q", got, want)
		}
	}
	if strings.Contains(got, "session=") || strings.Contains(got, "pane=") {
		t.Fatalf("trace exposed target identity: %q", got)
	}
}

func newTerminalEmbeddingTestPlugin() *Plugin {
	p := New()
	p.applicationFocused = true
	p.viewMode = ViewModeList
	p.SetFocused(true)
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

func TestNestedShellSelectionOpensPrimaryTerminalFromSessionOnly(t *testing.T) {
	p := nestedSidebarPlugin(t)
	p.primaryTerminal = p.newWorkspaceTerminal()
	p.panelTerminal = p.newWorkspaceTerminal()
	const session = "sidecar-sh-sidecar-feature-1"
	parent, shell := p.findNestedShell(session)
	if shell == nil {
		t.Fatal("nested shell fixture missing")
	}
	shell.Agent = &Agent{
		Type: AgentShell, TmuxSession: session,
		OutputBuf: tty.NewOutputBuffer(outputBufferCap),
	}
	p.selectNestedShell(parent, session)

	target, wanted := p.desiredPrimaryTerminal()
	if !wanted {
		t.Fatal("session-only nested shell did not request the primary terminal")
	}
	if target.Session != session || target.Pane != "" || target.Source != "shell" || target.SourceID != session {
		t.Fatalf("nested terminal target = %#v", target)
	}

	model := openTestTerminal(t, p, workspaceTerminalPrimary, target)
	if model.GetTarget() != session {
		t.Fatalf("session-only model target = %q, want %q", model.GetTarget(), session)
	}
	if shell.Agent.OutputBuf != model.State.OutputBuf {
		t.Fatal("nested shell did not adopt the tty.Model presentation buffer")
	}
	applyFallbackCapture(model, "nested live frame")
	p.syncTerminalModels()

	view := ansi.Strip(p.renderShellOutput(100, 20))
	if !strings.Contains(view, "nested live frame") || strings.Contains(view, "No output yet") {
		t.Fatalf("nested shell output did not render model frame:\n%s", view)
	}
}

func TestNestedShellSwitchRejectsPriorTerminalFrame(t *testing.T) {
	p := nestedSidebarPlugin(t)
	p.primaryTerminal = p.newWorkspaceTerminal()
	p.panelTerminal = p.newWorkspaceTerminal()
	const nestedSession = "sidecar-sh-sidecar-feature-1"
	parent, nested := p.findNestedShell(nestedSession)
	nested.Agent = &Agent{
		Type: AgentShell, TmuxSession: nestedSession, TmuxPane: "%8",
		OutputBuf: tty.NewOutputBuffer(outputBufferCap),
	}
	p.selectNestedShell(parent, nestedSession)
	nestedTarget, wanted := p.desiredPrimaryTerminal()
	if !wanted {
		t.Fatal("nested shell did not request terminal")
	}
	model := openTestTerminal(t, p, workspaceTerminalPrimary, nestedTarget)
	oldScope, oldPoll := model.Scope(), model.State.PollGeneration
	applyFallbackCapture(model, "nested current")
	p.syncTerminalModels()

	top := p.shells[0]
	top.Agent = &Agent{
		Type: AgentShell, TmuxSession: top.TmuxName, TmuxPane: "%9",
		OutputBuf: tty.NewOutputBuffer(outputBufferCap),
	}
	p.selectTopShellAt(0)
	topTarget, wanted := p.desiredPrimaryTerminal()
	if !wanted {
		t.Fatal("top shell did not request terminal after nested selection")
	}
	model = openTestTerminal(t, p, workspaceTerminalPrimary, topTarget)
	model.Update(tty.CaptureResultMsg{
		Scope: oldScope, PollGeneration: oldPoll, Target: "%8", Output: "STALE NESTED FRAME",
	})
	applyFallbackCapture(model, "top current")
	p.syncTerminalModels()

	view := ansi.Strip(p.renderShellOutput(100, 20))
	if !strings.Contains(view, "top current") || strings.Contains(view, "STALE NESTED FRAME") || strings.Contains(view, "nested current") {
		t.Fatalf("shell switch rendered stale nested content:\n%s", view)
	}
	if p.primaryTerminalTarget.SourceID != top.TmuxName {
		t.Fatalf("primary target after switch = %#v", p.primaryTerminalTarget)
	}
}

func TestFocusedPanelShortcutRoutesAllInteractiveInputToPanelModel(t *testing.T) {
	for _, primaryKind := range []string{"worktree-agent", "project-shell"} {
		t.Run(primaryKind, func(t *testing.T) {
			logPath := installSuccessfulFakeTmux(t)
			p := newTerminalEmbeddingTestPlugin()
			p.width, p.height = 100, 30
			p.sidebarVisible = false
			p.activePane = PanePreview
			p.termPanelVisible = true
			p.termPanelFocused = true
			p.termPanelLayout = TermPanelBottom
			p.termPanelSession = "panel-session"
			p.termPanelPaneID = "%2"

			primaryTarget := workspaceTerminalTarget{
				Session: "primary-session", Pane: "%1", Source: "agent", SourceID: "worktree-key",
			}
			if primaryKind == "project-shell" {
				p.shellSelected = true
				p.selectedShellIdx = 0
				p.shells = []*ShellSession{{
					TmuxName: "primary-session",
					Agent:    &Agent{TmuxSession: "primary-session", TmuxPane: "%1"},
				}}
				primaryTarget.Source = "shell"
				primaryTarget.SourceID = "primary-session"
			} else {
				p.worktrees = []*Worktree{{
					Key: "worktree-key", Name: "worktree",
					Agent: &Agent{TmuxSession: "primary-session", TmuxPane: "%1"},
				}}
			}

			primary := openTestTerminal(t, p, workspaceTerminalPrimary, primaryTarget)
			panel := openTestTerminal(t, p, workspaceTerminalPanel, workspaceTerminalTarget{
				Session: "panel-session", Pane: "%2", Source: "panel", SourceID: "panel-session",
			})

			p.handleListKeys(keyPressFor("E"))
			if p.interactiveState == nil || !p.interactiveState.Active || !p.interactiveState.TermPanel {
				t.Fatalf("focused panel E entry chose primary interaction: %#v", p.interactiveState)
			}
			if p.interactiveState.TargetSession != "panel-session" || p.interactiveState.TargetPane != "%2" {
				t.Fatalf("panel interaction target = %#v", p.interactiveState)
			}
			if got := p.activeInteractiveTerminal(); got != panel {
				t.Fatalf("active terminal = %p, want panel %p", got, panel)
			}
			if !primary.IsActive() || primary.GetTarget() != "%1" {
				t.Fatalf("primary model target changed: active=%v target=%q", primary.IsActive(), primary.GetTarget())
			}

			assertOnlyPanelTarget := func(kind string) {
				t.Helper()
				tty.WaitForPendingSends()
				logged, err := os.ReadFile(logPath)
				if err != nil {
					t.Fatal(err)
				}
				if !strings.Contains(string(logged), "-t %2") {
					t.Fatalf("%s did not target panel pane: %s", kind, logged)
				}
				if strings.Contains(string(logged), "-t %1") {
					t.Fatalf("%s reached primary pane: %s", kind, logged)
				}
			}
			clearLog := func() {
				t.Helper()
				tty.WaitForPendingSends()
				if err := os.WriteFile(logPath, nil, 0o600); err != nil {
					t.Fatal(err)
				}
			}

			clearLog()
			runCommandTree(p.handleInteractiveKeys(keyPressForText("PANEL_KEY")))
			assertOnlyPanelTarget("key")

			clearLog()
			runCommandTree(p.handleInteractivePaste("PANEL_PASTE"))
			assertOnlyPanelTarget("paste")

			clearLog()
			panel.State.MouseReportingEnabled = true
			p.interactiveState.PaneWidth = 40
			p.interactiveState.PaneHeight = 10
			var mouseX, mouseY int
			found := false
			for y := 0; y < p.height && !found; y++ {
				for x := 0; x < p.width; x++ {
					if _, _, ok := p.terminalMouseCoords(true, x, y); ok {
						mouseX, mouseY, found = x, y, true
						break
					}
				}
			}
			if !found {
				t.Fatal("could not find a visible panel cell for mouse routing")
			}
			cmd := p.forwardClickToTmux(mouseX, mouseY)
			if cmd == nil {
				t.Fatal("panel mouse click produced no command")
			}
			runCommandTree(cmd)
			assertOnlyPanelTarget("mouse")
		})
	}
}

func TestWorkspaceHealthyTerminalKeepsMetadataObservation(t *testing.T) {
	p := newTerminalEmbeddingTestPlugin()
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
		t.Fatal("model presentation stopped independent semantic activity cadence")
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
