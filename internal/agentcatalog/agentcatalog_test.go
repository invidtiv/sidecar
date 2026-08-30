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

func TestEveryFamilyBuildsStructuredLaunchArgv(t *testing.T) {
	for _, family := range Families() {
		argv, err := family.LaunchArgv([]string{"--model", "space value"}, true)
		if err != nil {
			t.Fatalf("%s: %v", family.ID, err)
		}
		if len(argv) < 3 || argv[0] != family.Command || argv[len(argv)-1] != "space value" {
			t.Fatalf("%s argv = %#v", family.ID, argv)
		}
		argv[len(argv)-1] = "changed"
		if family.Command == "changed" {
			t.Fatalf("%s launch aliased catalog storage", family.ID)
		}
	}
	if _, err := BuildLaunch("not-real", nil, false); err == nil {
		t.Fatal("unknown family received a launch")
	}
}

func TestOpaqueLaunchIsExplicitShellBoundary(t *testing.T) {
	got, err := OpaqueLaunchArgv("custom-agent --profile 'team one'")
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"sh", "-lc", "custom-agent --profile 'team one'"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("opaque argv = %#v, want %#v", got, want)
	}
}

func TestLegacyAiderLaunchDoesNotEnterSelectableFamilies(t *testing.T) {
	if _, ok := Find("aider"); ok {
		t.Fatal("legacy aider appeared in selectable catalog")
	}
	family, ok := FindLaunch("aider")
	if !ok || family.Command != "aider" || family.SkipPermissionsArg != "--yes" {
		t.Fatalf("legacy aider = %+v, %v", family, ok)
	}
	for _, family := range Families() {
		if family.ID == "aider" {
			t.Fatal("legacy aider appeared in Families")
		}
	}
}
