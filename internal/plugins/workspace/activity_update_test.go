package workspace

import (
	"bytes"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/plugin"
)

func currentPollGeneration(p *Plugin, name string) int {
	return p.pollScheduler.Current(agentPollKey(name))
}

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

func TestUnchangedPollSwitchesLiveProviderBeforeApplyingActivity(t *testing.T) {
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	p := &Plugin{worktrees: []*Worktree{{Name: "FABLE", Agent: agent}}, selectedIdx: -1}
	p.update(AgentPollUnchangedMsg{
		WorkspaceName: "FABLE",
		AgentType:     AgentClaude,
		Activity:      agentactivity.Result{State: agentactivity.StateBlocked, Evidence: "claude.screen.blocked"},
	})
	if agent.Type != AgentClaude {
		t.Fatalf("live agent type = %q, want Claude", agent.Type)
	}
	if agent.Activity.State != agentactivity.StateBlocked || agent.Activity.Evidence != "claude.screen.blocked" {
		t.Fatalf("activity = %#v, want Claude blocked", agent.Activity)
	}
}

func TestUnchangedPollReturningToShellClearsStaleProviderState(t *testing.T) {
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	wt := &Worktree{Name: "FABLE", Status: StatusActive, Agent: agent}
	p := &Plugin{worktrees: []*Worktree{wt}, selectedIdx: -1}
	p.update(AgentPollUnchangedMsg{WorkspaceName: "FABLE", AgentType: AgentShell, CurrentStatus: StatusActive})
	if agent.Type != AgentShell || agent.Activity.State != agentactivity.StateUnknown || wt.Status != StatusPaused {
		t.Fatalf("returned shell = type %q activity %q status %v", agent.Type, agent.Activity.State, wt.Status)
	}
}

func TestShellOutputSwitchesLiveProviderWithoutChangingLaunchPreference(t *testing.T) {
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	shell := &ShellSession{Name: "FABLE", TmuxName: "sidecar-sh-fable", ChosenAgent: AgentCodex, Agent: agent}
	p := &Plugin{shells: []*ShellSession{shell}, selectedShellIdx: -1}
	p.update(ShellOutputMsg{
		TmuxName:  shell.TmuxName,
		AgentType: AgentClaude,
		Activity:  agentactivity.Result{State: agentactivity.StateBlocked, Evidence: "claude.screen.blocked"},
	})
	if agent.Type != AgentClaude || shell.ChosenAgent != AgentCodex {
		t.Fatalf("live type = %q, launch preference = %q", agent.Type, shell.ChosenAgent)
	}
	rendered := ansi.Strip(p.renderShellEntryForSession(shell, false, 40))
	// Agent chip uses lowercased styles.AgentLabel ("◆ claude"), not the
	// title-case abbreviation.
	if !strings.Contains(rendered, "claude") || strings.Contains(rendered, "codex") {
		t.Fatalf("shell row did not use live provider: %q", rendered)
	}
}

func TestSupportedProviderPollStatusIsProjectedOnlyFromSemanticActivity(t *testing.T) {
	for _, agentType := range []AgentType{AgentCodex, AgentClaude, AgentGrok, AgentAntigravity, AgentPi, AgentCopilot, AgentCursor, AgentOpenCode, AgentAmp} {
		t.Run(string(agentType), func(t *testing.T) {
			agent := &Agent{Type: agentType, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
			p := &Plugin{worktrees: []*Worktree{{Name: "w", Status: StatusActive, Agent: agent}}, selectedIdx: -1}
			p.update(AgentOutputMsg{
				WorkspaceName: "w", Generation: currentPollGeneration(p, "w"),
				Status: StatusError, WaitingFor: "legacy override",
				Activity: agentactivity.Result{State: agentactivity.StateBlocked, Evidence: string(agentType) + ".screen.blocked"},
			})
			if agent.Activity.State != agentactivity.StateBlocked || p.worktrees[0].Status != StatusWaiting {
				t.Fatalf("activity=%q worktree=%v", agent.Activity.State, p.worktrees[0].Status)
			}
		})
	}
}

func TestUnsupportedProviderRetainsLegacyPollStatus(t *testing.T) {
	agent := &Agent{Type: AgentCustom}
	p := &Plugin{worktrees: []*Worktree{{Name: "w", Status: StatusActive, Agent: agent}}, selectedIdx: -1}
	p.update(AgentPollUnchangedMsg{
		WorkspaceName: "w", Generation: currentPollGeneration(p, "w"), CurrentStatus: StatusError,
		Activity: agentactivity.Result{State: agentactivity.StateWorking, Evidence: "must-not-apply"},
	})
	if p.worktrees[0].Status != StatusError || agent.Activity.State != "" {
		t.Fatalf("legacy status=%v semantic=%q", p.worktrees[0].Status, agent.Activity.State)
	}
}

func TestActivityPresentationParityAndOrthogonalLiveness(t *testing.T) {
	agent := &Agent{Type: AgentClaude, Activity: agentactivity.Tracker{State: agentactivity.StateBlocked}}
	wi, wt, _, wok := activityPresentation(agent)
	si, st, _, sok := activityPresentation(agent)
	if !wok || !sok || wi != si || wt != st || wi != "◆" || wt != "blocked" {
		t.Fatalf("worktree=(%q,%q,%v) shell=(%q,%q,%v)", wi, wt, wok, si, st, sok)
	}
	shell := &ShellSession{Name: "lost", ChosenAgent: AgentClaude, Agent: agent, IsOrphaned: true}
	rendered := ansi.Strip((&Plugin{}).renderShellEntryForSession(shell, false, 40))
	if !strings.Contains(rendered, "offline") || strings.Contains(rendered, "blocked") {
		t.Fatalf("orphan liveness did not override semantic activity: %q", rendered)
	}
	p := &Plugin{ctx: &plugin.Context{}}
	worktree := &Worktree{Name: "missing", Agent: agent, IsMissing: true, Status: StatusError}
	rendered = ansi.Strip(p.renderWorktreeItem(worktree, false, 40))
	if !strings.Contains(rendered, "folder missing") || strings.Contains(rendered, "blocked") {
		t.Fatalf("worktree health did not override semantic activity: %q", rendered)
	}
}

func TestAgentActivityTransitionLogIsPrivacySafe(t *testing.T) {
	var buf bytes.Buffer
	prior := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: slog.LevelDebug})))
	t.Cleanup(func() { slog.SetDefault(prior) })
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateIdle, Evidence: "old"}}
	now := time.Unix(123, 0)
	applyAgentActivity(agent, agentactivity.Result{State: agentactivity.StateWorking, Evidence: "codex.title.working"}, now.Add(-25*time.Millisecond), now)
	logLine := buf.String()
	for _, want := range []string{"agent=codex", "prior=idle", "new=working", "evidence=codex.title.working", "capture_age=25ms"} {
		if !strings.Contains(logLine, want) {
			t.Fatalf("log missing %q: %s", want, logLine)
		}
	}
	for _, secret := range []string{"terminal secret", "Action Required", "pane title"} {
		if strings.Contains(logLine, secret) {
			t.Fatalf("log leaked terminal content %q: %s", secret, logLine)
		}
	}
	before := buf.Len()
	applyAgentActivity(agent, agentactivity.Result{State: agentactivity.StateWorking, Evidence: "codex.title.working"}, now, now)
	if buf.Len() != before {
		t.Fatal("unchanged activity emitted a transition log")
	}
}

func TestFreshSkipCaptureCannotReuseRetainedBlockerForAttention(t *testing.T) {
	now := time.Unix(500, 0)
	agent := &Agent{Type: AgentCodex}
	wt := &Worktree{Name: "review", Status: StatusWaiting, Agent: agent}

	applyAgentActivity(agent, agentactivity.Result{
		State: agentactivity.StateBlocked, Evidence: "codex.permission.blocked", VisibleBlocker: true,
	}, now, now)
	if got := agentStatusPresentation(wt); !got.Attention {
		t.Fatalf("visible blocker did not produce attention: %#v", got)
	}

	overlayAt := now.Add(time.Second)
	applyAgentActivity(agent, agentactivity.Result{
		State: agentactivity.StateUnknown, Evidence: "codex.viewer.retain", SkipStateUpdate: true,
	}, overlayAt, overlayAt)
	if agent.Activity.State != agentactivity.StateBlocked || agent.ActivityCapturedAt != overlayAt {
		t.Fatalf("skip capture did not retain state/update capture time: tracker=%#v captured=%v", agent.Activity, agent.ActivityCapturedAt)
	}
	if got := agentStatusPresentation(wt); got.Attention {
		t.Fatalf("fresh non-evidence capture reused stale blocker attention: %#v", got)
	}

	idleAt := overlayAt.Add(time.Second)
	applyAgentActivity(agent, agentactivity.Result{
		State: agentactivity.StateIdle, Evidence: "codex.prompt.idle", VisibleIdle: true,
	}, idleAt, idleAt)
	if got := agentStatusPresentation(wt); got.Lane != kanbanLaneDone || got.Attention {
		t.Fatalf("blocked-to-idle transition = %#v, want unseen Done", got)
	}
	agent.Activity.Acknowledge()
	if got := agentStatusPresentation(wt); got.Lane != kanbanLaneIdle || got.Attention {
		t.Fatalf("acknowledged idle = %#v, want Idle", got)
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
