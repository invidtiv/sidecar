package workspace

import (
	"reflect"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestResolveSelectableAgents_EmptyConfigUsesDefault(t *testing.T) {
	got := resolveSelectableAgents(nil, false)
	if !reflect.DeepEqual(got, AgentTypeOrder) {
		t.Fatalf("empty config worktree = %v, want AgentTypeOrder", got)
	}
	gotShell := resolveSelectableAgents(nil, true)
	if !reflect.DeepEqual(gotShell, ShellAgentOrder) {
		t.Fatalf("empty config shell = %v, want ShellAgentOrder", gotShell)
	}
}

func TestResolveSelectableAgents_AllowlistOrder(t *testing.T) {
	cfg := []string{"grok", "claude", "not-a-real-agent", "grok", "  codex  "}
	got := resolveSelectableAgents(cfg, false)
	want := []AgentType{AgentGrok, AgentClaude, AgentCodex, AgentNone}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("worktree allowlist = %v, want %v", got, want)
	}
	gotShell := resolveSelectableAgents(cfg, true)
	wantShell := []AgentType{AgentNone, AgentGrok, AgentClaude, AgentCodex}
	if !reflect.DeepEqual(gotShell, wantShell) {
		t.Fatalf("shell allowlist = %v, want %v", gotShell, wantShell)
	}
}

func TestResolveSelectableAgents_AllUnknownFallsBack(t *testing.T) {
	got := resolveSelectableAgents([]string{"nope", "also-no"}, false)
	if !reflect.DeepEqual(got, AgentTypeOrder) {
		t.Fatalf("all-unknown should fall back to default, got %v", got)
	}
}

func TestClampAgentSelection(t *testing.T) {
	list := []AgentType{AgentGrok, AgentClaude, AgentNone}
	at, idx := clampAgentSelection(list, AgentClaude, -1)
	if at != AgentClaude || idx != 1 {
		t.Fatalf("clamp Claude = %q idx %d, want claude @1", at, idx)
	}
	at, idx = clampAgentSelection(list, AgentCopilot, -1)
	if at != AgentGrok || idx != 0 {
		t.Fatalf("hidden agent fallback = %q idx %d, want grok @0", at, idx)
	}
}

func TestWithPreferredAgent(t *testing.T) {
	list := []AgentType{AgentGrok, AgentNone}
	got := withPreferredAgent(list, AgentCopilot)
	want := []AgentType{AgentGrok, AgentCopilot, AgentNone}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("withPreferred = %v, want %v", got, want)
	}
	// Already present: unchanged
	got = withPreferredAgent(list, AgentGrok)
	if !reflect.DeepEqual(got, list) {
		t.Fatalf("already present should not change list")
	}
}

func TestPluginSelectableAgents_FromConfig(t *testing.T) {
	cfg := config.Default()
	cfg.Plugins.Workspace.Agents = []string{"grok", "claude"}
	p := &Plugin{ctx: &plugin.Context{Config: cfg}}
	got := p.selectableAgentTypes()
	want := []AgentType{AgentGrok, AgentClaude, AgentNone}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("selectableAgentTypes = %v, want %v", got, want)
	}
	gotShell := p.selectableShellAgentTypes()
	wantShell := []AgentType{AgentNone, AgentGrok, AgentClaude}
	if !reflect.DeepEqual(gotShell, wantShell) {
		t.Fatalf("selectableShellAgentTypes = %v, want %v", gotShell, wantShell)
	}
}

func TestGetDefaultCreateAgentType_ClampsToAllowlist(t *testing.T) {
	cfg := config.Default()
	cfg.Plugins.Workspace.DefaultAgentType = string(AgentCopilot)
	cfg.Plugins.Workspace.Agents = []string{"grok", "claude"}
	p := &Plugin{ctx: &plugin.Context{Config: cfg}}
	if got := p.getDefaultCreateAgentType(); got != AgentGrok {
		t.Fatalf("default clamped = %q, want grok (first in allowlist)", got)
	}
}

func TestIsKnownAgentType_Grok(t *testing.T) {
	if !isKnownAgentType(AgentGrok) {
		t.Fatal("AgentGrok should be known")
	}
}

func TestAgentConfigModalListStableWhenNavigating(t *testing.T) {
	// Simulate open: preferred hidden agent is in the fixed list.
	cfg := config.Default()
	cfg.Plugins.Workspace.Agents = []string{"claude", "grok"}
	p := &Plugin{ctx: &plugin.Context{Config: cfg}}
	preferred := AgentCopilot
	p.agentConfigAgentList = withPreferredAgent(p.selectableAgentTypes(), preferred)
	p.agentConfigAgentType, p.agentConfigAgentIdx = clampAgentSelection(p.agentConfigAgentList, preferred, -1)
	if p.agentConfigAgentType != AgentCopilot {
		t.Fatalf("open selection = %q, want copilot", p.agentConfigAgentType)
	}
	// Navigate away then back: list length must stay stable (include preferred).
	lenOpen := len(p.agentConfigAgentList)
	// Select claude
	for i, at := range p.agentConfigAgentList {
		if at == AgentClaude {
			p.agentConfigAgentIdx = i
			p.agentConfigAgentType = p.agentConfigAgentList[i]
			break
		}
	}
	if len(p.agentConfigAgentList) != lenOpen {
		t.Fatalf("list length changed after navigate: %d -> %d", lenOpen, len(p.agentConfigAgentList))
	}
	// Select back to preferred index
	for i, at := range p.agentConfigAgentList {
		if at == AgentCopilot {
			p.agentConfigAgentIdx = i
			p.agentConfigAgentType = p.agentConfigAgentList[i]
			break
		}
	}
	if p.agentConfigAgentType != AgentCopilot {
		t.Fatalf("reselect preferred = %q, want copilot", p.agentConfigAgentType)
	}
}
