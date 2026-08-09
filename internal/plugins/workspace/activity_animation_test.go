package workspace

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/agentactivity"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestWorkingActivityUsesSmoothFixedWidthPulse(t *testing.T) {
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	p := &Plugin{ctx: &plugin.Context{}}

	var rendered []string
	for _, frame := range []int{0, 3, 7} {
		p.activityAnimationFrame = frame
		icon, text, style, ok := p.animatedActivityPresentation(agent)
		if !ok || text != "working" || ansi.StringWidth(icon) != 1 {
			t.Fatalf("frame %d presentation=(%q,%q,%v)", frame, icon, text, ok)
		}
		rendered = append(rendered, style.Render(icon))
	}
	if rendered[0] == rendered[1] || rendered[1] == rendered[2] {
		t.Fatalf("working pulse frames did not change smoothly: %#v", rendered)
	}
	p.activityAnimationFrame = 7
	if icon, _, _, _ := p.animatedActivityPresentation(agent); icon != "∙" {
		t.Fatalf("low working frame icon=%q, want subtle dot", icon)
	}
}

func TestActivityAnimationCadenceIsSlowAndContinuous(t *testing.T) {
	workingCycle := activityAnimationInterval * time.Duration(len(workingPulse))
	blockedCycle := activityAnimationInterval * time.Duration(len(blockedPulse))
	if workingCycle < 2300*time.Millisecond || workingCycle > 2600*time.Millisecond {
		t.Fatalf("working cycle=%v, want a relaxed roughly 2.5s breath", workingCycle)
	}
	if blockedCycle < 2700*time.Millisecond || blockedCycle > 3*time.Second {
		t.Fatalf("blocked cycle=%v, want a relaxed roughly 2.8s heartbeat", blockedCycle)
	}
	for i, level := range workingPulse {
		next := workingPulse[(i+1)%len(workingPulse)]
		if level == next {
			t.Fatalf("working pulse pauses between frames %d and %d at %.2f", i, (i+1)%len(workingPulse), level)
		}
	}
}

func TestBlockedActivityUsesHeartbeatWithRest(t *testing.T) {
	agent := &Agent{Type: AgentClaude, Activity: agentactivity.Tracker{State: agentactivity.StateBlocked}}
	p := &Plugin{}

	want := map[int]string{0: "◆", 2: "◇", 3: "◆", 4: "◆", 8: "◇"}
	for frame, wantIcon := range want {
		p.activityAnimationFrame = frame
		icon, text, _, ok := p.animatedActivityPresentation(agent)
		if !ok || text != "blocked" || icon != wantIcon || ansi.StringWidth(icon) != 1 {
			t.Fatalf("frame %d presentation=(%q,%q,%v), want icon %q", frame, icon, text, ok, wantIcon)
		}
	}
}

func TestOrdinaryRunningShellDoesNotAnimate(t *testing.T) {
	shell := &ShellSession{Name: "plain", Agent: &Agent{Type: AgentShell}}
	p := &Plugin{}

	p.activityAnimationFrame = 0
	first := ansi.Strip(p.renderShellEntryForSession(shell, false, 40))
	p.activityAnimationFrame = 7
	second := ansi.Strip(p.renderShellEntryForSession(shell, false, 40))
	if first != second || !strings.Contains(first, "●") || !strings.Contains(first, "running") {
		t.Fatalf("ordinary shell changed across animation frames:\n%q\n%q", first, second)
	}
}

func TestAnimatedRowsKeepTheirWidthAcrossViewsAndSelection(t *testing.T) {
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	wt := &Worktree{Name: "animated", Agent: agent}
	p := &Plugin{ctx: &plugin.Context{}}

	for frame := 0; frame < len(workingPulse); frame++ {
		p.activityAnimationFrame = frame
		for _, selected := range []bool{false, true} {
			for _, line := range strings.Split(p.renderWorktreeItem(wt, selected, 40), "\n") {
				if width := ansi.StringWidth(line); width != 40 {
					t.Fatalf("frame %d selected=%v list width=%d, want 40: %q", frame, selected, width, line)
				}
			}
			if line := p.renderKanbanCardLine(wt, 0, 24, selected); ansi.StringWidth(line) != 24 {
				t.Fatalf("frame %d selected=%v kanban width=%d, want 24: %q", frame, selected, ansi.StringWidth(line), line)
			}
		}
	}
}

func TestAnimatedKanbanShellWithWideNameKeepsValidWidth(t *testing.T) {
	agent := &Agent{Type: AgentClaude, Activity: agentactivity.Tracker{State: agentactivity.StateBlocked}}
	shell := &ShellSession{Name: "修正作業セッション", Agent: agent}
	p := &Plugin{}

	for frame := 0; frame < len(blockedPulse); frame++ {
		p.activityAnimationFrame = frame
		line := p.renderKanbanShellCardLine(shell, 0, 12, false)
		if width := ansi.StringWidth(line); width != 12 {
			t.Fatalf("frame %d width=%d, want 12: %q", frame, width, line)
		}
		if !strings.Contains(ansi.Strip(line), "◆") && !strings.Contains(ansi.Strip(line), "◇") {
			t.Fatalf("frame %d lost animated icon after truncation: %q", frame, line)
		}
	}
}

func TestActivityAnimationTickerIsSharedAndDemandDriven(t *testing.T) {
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	p := &Plugin{
		focused:            true,
		applicationFocused: true,
		viewMode:           ViewModeList,
		sidebarVisible:     true,
		visibleCount:       2,
		worktrees:          []*Worktree{{Name: "one", Agent: agent}, {Name: "two", Agent: agent}},
	}

	if cmd := p.startActivityAnimation(); cmd == nil || !p.activityAnimationScheduled {
		t.Fatal("working activity did not start ticker")
	}
	if cmd := p.startActivityAnimation(); cmd != nil {
		t.Fatal("second working item scheduled a duplicate ticker")
	}

	p.update(activityAnimationTickMsg{generation: p.activityAnimationGeneration})
	if p.activityAnimationFrame != 1 || !p.activityAnimationScheduled {
		t.Fatalf("tick did not advance and reschedule: frame=%d scheduled=%v", p.activityAnimationFrame, p.activityAnimationScheduled)
	}

	agent.Activity.State = agentactivity.StateIdle
	p.update(activityAnimationTickMsg{generation: p.activityAnimationGeneration})
	if p.activityAnimationFrame != 0 || p.activityAnimationScheduled {
		t.Fatalf("idle activity left ticker running: frame=%d scheduled=%v", p.activityAnimationFrame, p.activityAnimationScheduled)
	}
}

func TestActivityAnimationDoesNotRunWhenHiddenOrInteractive(t *testing.T) {
	agent := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	p := &Plugin{worktrees: []*Worktree{{Agent: agent}}, applicationFocused: true, viewMode: ViewModeList}
	if cmd := p.startActivityAnimation(); cmd != nil {
		t.Fatal("unfocused workspace scheduled animation")
	}
	p.focused = true
	p.applicationFocused = false
	if cmd := p.startActivityAnimation(); cmd != nil {
		t.Fatal("backgrounded app scheduled animation")
	}
	p.applicationFocused = true
	p.sidebarVisible = false
	if cmd := p.startActivityAnimation(); cmd != nil {
		t.Fatal("hidden sidebar scheduled invisible status animation")
	}
	p.sidebarVisible = true
	p.viewMode = ViewModeInteractive
	if cmd := p.startActivityAnimation(); cmd != nil {
		t.Fatal("interactive view scheduled invisible status animation")
	}
}

func TestActivityAnimationIgnoresOffscreenListWork(t *testing.T) {
	idle := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateIdle, Seen: true}}
	working := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	p := &Plugin{
		focused: true, applicationFocused: true, viewMode: ViewModeList, sidebarVisible: true,
		visibleCount: 1, worktrees: []*Worktree{{Name: "visible", Agent: idle}, {Name: "hidden", Agent: working}},
	}
	if cmd := p.startActivityAnimation(); cmd != nil {
		t.Fatal("offscreen worktree scheduled animation")
	}
	p.scrollOffset = 1
	if cmd := p.startActivityAnimation(); cmd == nil {
		t.Fatal("visible working worktree did not schedule animation")
	}
}

func TestActivityAnimationUsesListVisibilityForNarrowKanbanFallback(t *testing.T) {
	idle := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateIdle, Seen: true}}
	working := &Agent{Type: AgentCodex, Activity: agentactivity.Tracker{State: agentactivity.StateWorking}}
	p := &Plugin{
		focused: true, applicationFocused: true, viewMode: ViewModeKanban,
		width: 80, height: 10, sidebarVisible: true, scrollOffset: 1, visibleCount: 1,
		worktrees: []*Worktree{{Name: "first", Agent: idle}, {Name: "visible-in-list", Agent: working}},
	}
	if !kanbanUsesListFallback(p.width) {
		t.Fatal("test width no longer exercises kanban list fallback")
	}
	if cmd := p.startActivityAnimation(); cmd == nil {
		t.Fatal("visible list-fallback worktree did not schedule animation")
	}
	p.activityAnimationScheduled = false
	p.sidebarVisible = false
	if cmd := p.startActivityAnimation(); cmd != nil {
		t.Fatal("hidden list-fallback sidebar scheduled animation")
	}
}

func TestActivityAnimationIgnoresStaleGeneration(t *testing.T) {
	p := &Plugin{activityAnimationFrame: 4, activityAnimationGeneration: 2}
	p.update(activityAnimationTickMsg{generation: 1})
	if p.activityAnimationFrame != 4 {
		t.Fatalf("stale tick advanced frame to %d", p.activityAnimationFrame)
	}
}
