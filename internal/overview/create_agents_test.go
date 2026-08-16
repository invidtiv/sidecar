package overview

import (
	"reflect"
	"testing"
)

func TestResolveCreateAgentsOrdersNoneByKind(t *testing.T) {
	if got := resolveCreateAgents(nil, true); got[0] != "" {
		t.Fatalf("shell empty config None first: %v", got)
	}
	worktree := resolveCreateAgents(nil, false)
	if worktree[len(worktree)-1] != "" {
		t.Fatalf("worktree empty config None last: %v", worktree)
	}

	cfg := []string{"grok", "claude", "not-real", "grok", "  codex  "}
	if got := resolveCreateAgents(cfg, true); !reflect.DeepEqual(got, []string{"", "grok", "claude", "codex"}) {
		t.Fatalf("shell allowlist = %v", got)
	}
	if got := resolveCreateAgents(cfg, false); !reflect.DeepEqual(got, []string{"grok", "claude", "codex", ""}) {
		t.Fatalf("worktree allowlist = %v", got)
	}
}

func TestCreateAgentLabelMatchesProjectModal(t *testing.T) {
	cases := map[string]string{
		"":            "None (attach only)",
		"claude":      "Claude Code",
		"codex":       "Codex CLI",
		"copilot":     "GitHub Copilot CLI",
		"antigravity": "Antigravity",
		"cursor":      "Cursor Agent",
		"opencode":    "OpenCode",
		"pi":          "Pi Agent",
		"amp":         "Amp",
		"grok":        "Grok",
		"shell":       "Project Shell",
	}
	for id, want := range cases {
		if got := createAgentLabel(id); got != want {
			t.Fatalf("label(%q) = %q, want %q", id, got, want)
		}
	}
}
