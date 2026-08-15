package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
)

func TestClearAgentConfigModal(t *testing.T) {
	p := &Plugin{
		agentConfigWorktree:  &Worktree{Name: "test"},
		agentConfigIsRestart: true,
		agentConfigAgentType: AgentClaude,
		agentConfigAgentIdx:  3,
		agentConfigSkipPerms: true,
	}
	p.clearAgentConfigModal()

	if p.agentConfigWorktree != nil {
		t.Error("worktree not cleared")
	}
	if p.agentConfigIsRestart {
		t.Error("isRestart not cleared")
	}
	if p.agentConfigAgentType != "" {
		t.Error("agentType not cleared")
	}
	if p.agentConfigAgentIdx != 0 {
		t.Error("agentIdx not cleared")
	}
	if p.agentConfigSkipPerms {
		t.Error("skipPerms not cleared")
	}
	if p.agentConfigModal != nil {
		t.Error("modal not cleared")
	}
	if p.agentConfigModalWidth != 0 {
		t.Error("modalWidth not cleared")
	}
}

func TestShouldShowAgentConfigSkipPerms(t *testing.T) {
	tests := []struct {
		name      string
		agentType AgentType
		want      bool
	}{
		{"claude has flag", AgentClaude, true},
		{"codex has flag", AgentCodex, true},
		{"none has no flag", AgentNone, false},
		{"opencode has no flag", AgentOpenCode, false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := &Plugin{agentConfigAgentType: tt.agentType}
			if got := p.shouldShowAgentConfigSkipPerms(); got != tt.want {
				t.Errorf("shouldShowAgentConfigSkipPerms() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestExecuteAgentConfig_FreshStart(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	wt := &Worktree{Name: "test-wt", Path: "/tmp/test"}
	p := &Plugin{
		ctx:                  &plugin.Context{},
		agentConfigWorktree:  wt,
		agentConfigIsRestart: false,
		agentConfigAgentType: AgentClaude,
		agentConfigSkipPerms: true,
		viewMode:             ViewModeAgentConfig,
	}

	cmd := p.executeAgentConfig()

	if p.viewMode != ViewModeList {
		t.Errorf("expected ViewModeList, got %v", p.viewMode)
	}
	if p.agentConfigWorktree != nil {
		t.Error("worktree should be cleared")
	}
	if cmd == nil {
		t.Error("expected non-nil cmd for fresh start")
	}
	if state.GetLastCreateAgent() != string(AgentClaude) {
		t.Errorf("last create agent = %q, want claude", state.GetLastCreateAgent())
	}
	if !state.GetAgentAutoApprove(string(AgentClaude)) {
		t.Error("auto-approve should persist on Start")
	}
}

func TestExecuteAgentConfig_Restart(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	wt := &Worktree{Name: "test-wt", Path: "/tmp/test"}
	p := &Plugin{
		ctx:                  &plugin.Context{},
		agentConfigWorktree:  wt,
		agentConfigIsRestart: true,
		agentConfigAgentType: AgentCodex,
		agentConfigSkipPerms: false,
		viewMode:             ViewModeAgentConfig,
	}

	cmd := p.executeAgentConfig()

	if p.viewMode != ViewModeList {
		t.Errorf("expected ViewModeList, got %v", p.viewMode)
	}
	if cmd == nil {
		t.Error("expected non-nil cmd for restart")
	}
}

func TestExecuteAgentConfig_NilWorktree(t *testing.T) {
	p := &Plugin{
		agentConfigWorktree: nil,
		viewMode:            ViewModeAgentConfig,
	}

	cmd := p.executeAgentConfig()

	if p.viewMode != ViewModeList {
		t.Errorf("expected ViewModeList, got %v", p.viewMode)
	}
	if cmd != nil {
		t.Error("expected nil cmd for nil worktree")
	}
}

func TestOpenAgentConfigModalLoadsAutoApprove(t *testing.T) {
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	if err := state.SetAgentAutoApprove(string(AgentClaude), true); err != nil {
		t.Fatal(err)
	}
	p := New()
	p.ctx = &plugin.Context{}
	p.openAgentConfigModal(&Worktree{Name: "wt", Path: "/tmp"}, false)
	if !p.agentConfigSkipPerms {
		t.Error("expected persisted auto-approve for claude")
	}
}
