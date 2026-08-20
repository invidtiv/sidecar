package agentcatalog

import (
	"reflect"
	"testing"
)

func TestResolvePickerOrdersNoneByKind(t *testing.T) {
	shell := ResolvePicker(nil, true)
	if len(shell) == 0 || shell[0] != "" {
		t.Fatalf("shell empty config None first: %v", shell)
	}
	worktree := ResolvePicker(nil, false)
	if len(worktree) == 0 || worktree[len(worktree)-1] != "" {
		t.Fatalf("worktree empty config None last: %v", worktree)
	}

	cfg := []string{"grok", "claude", "not-real", "grok", "  codex  "}
	if got := ResolvePicker(cfg, true); !reflect.DeepEqual(got, []string{"", "grok", "claude", "codex"}) {
		t.Fatalf("shell allowlist = %v", got)
	}
	if got := ResolvePicker(cfg, false); !reflect.DeepEqual(got, []string{"grok", "claude", "codex", ""}) {
		t.Fatalf("worktree allowlist = %v", got)
	}
}

func TestResolvePickerUnrecognizedFallsBackToAllFamilies(t *testing.T) {
	want := ResolvePicker(nil, false)
	got := ResolvePicker([]string{"nope", "also-no"}, false)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("unrecognized allowlist = %v, want all families %v", got, want)
	}
	if len(got) != len(Families())+1 {
		t.Fatalf("picker length = %d, want %d families plus None", len(got), len(Families()))
	}
}

func TestLabel(t *testing.T) {
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
		"nonesuch":    "nonesuch",
	}
	for id, want := range cases {
		if got := Label(id); got != want {
			t.Fatalf("Label(%q) = %q, want %q", id, got, want)
		}
	}
}
