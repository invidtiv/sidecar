package configchecks

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
)

// fakeEnv describes an environment instead of arranging one, which is the whole
// reason the checks take an Env.
func fakeEnv(vars map[string]string, present map[string]string, outputs map[string]string) Env {
	return Env{
		Getenv: func(name string) string { return vars[name] },
		LookPath: func(name string) (string, error) {
			if path, ok := present[name]; ok {
				return path, nil
			}
			return "", errors.New("not found")
		},
		Output: func(name string, args ...string) ([]byte, error) {
			key := strings.Join(append([]string{name}, args...), " ")
			if out, ok := outputs[key]; ok {
				return []byte(out), nil
			}
			return nil, errors.New("no such command")
		},
		GOOS: "darwin",
	}
}

func TestParseTmuxVersion(t *testing.T) {
	cases := map[string]struct {
		major, minor int
		ok           bool
	}{
		"tmux 3.4":          {3, 4, true},
		"tmux 3.2a":         {3, 2, true},
		"tmux next-3.6":     {3, 6, true},
		"tmux 2.8":          {2, 8, true},
		"tmux 4":            {4, 0, true},
		"tmux openbsd-7.4":  {7, 4, true},
		"tmux master":       {0, 0, false},
		"":                  {0, 0, false},
		"no version at all": {0, 0, false},
	}
	for raw, want := range cases {
		got, ok := ParseTmuxVersion(raw)
		if ok != want.ok {
			t.Fatalf("%q: ok = %v, want %v", raw, ok, want.ok)
		}
		if ok && (got.Major != want.major || got.Minor != want.minor) {
			t.Fatalf("%q: parsed %d.%d, want %d.%d", raw, got.Major, got.Minor, want.major, want.minor)
		}
	}
	if v, _ := ParseTmuxVersion("tmux 3.0"); !v.AtLeast(3, 0) {
		t.Fatal("3.0 does not meet the 3.0 minimum")
	}
	if v, _ := ParseTmuxVersion("tmux 2.9a"); v.AtLeast(3, 0) {
		t.Fatal("2.9a claimed to meet the 3.0 minimum")
	}
}

func TestTmuxCheckStates(t *testing.T) {
	missing := checkTmux(Input{Env: fakeEnv(nil, nil, nil)})
	if missing.OK || missing.Repair != RepairTmux || missing.Badge != BadgeFix {
		t.Fatalf("missing tmux = %#v", missing)
	}

	old := checkTmux(Input{Env: fakeEnv(nil,
		map[string]string{"tmux": "/usr/bin/tmux"},
		map[string]string{"tmux -V": "tmux 2.8\n"})})
	if old.OK || !strings.Contains(old.Summary, "older") {
		t.Fatalf("old tmux = %#v", old)
	}

	good := checkTmux(Input{Env: fakeEnv(nil,
		map[string]string{"tmux": "/opt/homebrew/bin/tmux"},
		map[string]string{"tmux -V": "tmux 3.5a\n"})})
	if !good.OK || good.Repair != RepairNone {
		t.Fatalf("current tmux = %#v", good)
	}

	// An unreadable version is not a fault: tmux is installed, which is what
	// gates workspaces.
	unknown := checkTmux(Input{Env: fakeEnv(nil, map[string]string{"tmux": "/usr/bin/tmux"}, nil)})
	if !unknown.OK {
		t.Fatalf("unparseable version reported a fault: %#v", unknown)
	}
}

func TestTmuxInstallCommandNeverUsesSudoWithHomebrew(t *testing.T) {
	brew := fakeEnv(nil, map[string]string{"brew": "/opt/homebrew/bin/brew"}, nil)
	if got := TmuxInstallCommand(brew); got != "brew install tmux" {
		t.Fatalf("homebrew command = %q", got)
	}
	if !TmuxRepairPrefillable(brew) {
		t.Fatal("a brew install command should be prefillable")
	}
	bare := fakeEnv(nil, nil, nil)
	if strings.Contains(TmuxInstallCommand(bare), "sudo") {
		t.Fatalf("macOS without Homebrew was told to sudo: %q", TmuxInstallCommand(bare))
	}
	if TmuxRepairPrefillable(bare) {
		t.Fatal("a non-command recommendation must not be prefilled into a shell")
	}
}

func TestTruecolorDetection(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
		want bool
	}{
		{"colorterm truecolor", map[string]string{"COLORTERM": "truecolor"}, true},
		{"colorterm 24bit", map[string]string{"COLORTERM": "24bit"}, true},
		{"term direct", map[string]string{"TERM": "xterm-direct"}, true},
		{"kitty without colorterm", map[string]string{"TERM": "xterm-kitty"}, true},
		{"apple terminal", map[string]string{"TERM": "xterm-256color", "TERM_PROGRAM": "Apple_Terminal"}, false},
		{"bare 256color", map[string]string{"TERM": "xterm-256color"}, false},
	}
	for _, tc := range cases {
		if got := TruecolorAvailable(fakeEnv(tc.vars, nil, nil)); got != tc.want {
			t.Fatalf("%s: truecolor = %v, want %v", tc.name, got, tc.want)
		}
	}
}

func TestTerminalIdentificationAndGuides(t *testing.T) {
	guide, ok := IdentifyTerminal(fakeEnv(map[string]string{"TERM_PROGRAM": "iTerm.app"}, nil, nil))
	if !ok || guide.Name != "iTerm2" {
		t.Fatalf("iTerm2 identified as %#v (ok=%v)", guide, ok)
	}
	if !strings.Contains(guide.Instructions(), "iTerm2") {
		t.Fatalf("guide instructions lost the terminal name:\n%s", guide.Instructions())
	}

	generic, ok := IdentifyTerminal(fakeEnv(map[string]string{"TERM": "xterm-256color"}, nil, nil))
	if ok {
		t.Fatalf("an unknown terminal was reported as recognized: %#v", generic)
	}
	if generic.Instructions() == "" {
		t.Fatal("the generic guide has no copyable instructions")
	}

	result := checkTerminalColors(Input{Env: fakeEnv(map[string]string{
		"TERM": "xterm-256color", "TERM_PROGRAM": "Apple_Terminal",
	}, nil, nil)})
	if result.OK || result.Repair != RepairTerminalColors {
		t.Fatalf("Terminal.app check = %#v", result)
	}
	if len(result.Evidence) == 0 || !strings.Contains(strings.Join(result.Evidence, "\n"), "TERM=") {
		t.Fatalf("no evidence line for the detected environment: %#v", result.Evidence)
	}
}

func TestProjectsCheck(t *testing.T) {
	none := checkProjects(Input{Config: &config.Config{}})
	if none.OK || none.Badge != BadgeAdd || none.Repair != RepairAddProject {
		t.Fatalf("no projects = %#v", none)
	}
	cfg := &config.Config{}
	cfg.Projects.List = []config.ProjectConfig{{Name: "a", Path: "/a"}, {Name: "b", Path: "/b"}}
	some := checkProjects(Input{Config: cfg})
	if !some.OK || !strings.Contains(some.Summary, "2") {
		t.Fatalf("two projects = %#v", some)
	}
}

func TestConfigurationCheckReadsTheFileFresh(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")

	missing := checkConfiguration(Input{ConfigPath: path})
	if !missing.OK || !strings.Contains(missing.Summary, "defaults") {
		t.Fatalf("absent config = %#v", missing)
	}

	if err := os.WriteFile(path, []byte(`{"ui": {"theme": {"name": "sidecar"}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	valid := checkConfiguration(Input{ConfigPath: path})
	if !valid.OK || valid.Repair != RepairNone {
		t.Fatalf("valid config = %#v", valid)
	}

	if err := os.WriteFile(path, []byte("{\n  \"ui\": {,\n}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	broken := checkConfiguration(Input{ConfigPath: path})
	if broken.OK || broken.Repair != RepairConfiguration || broken.Badge != BadgeFix {
		t.Fatalf("broken config = %#v", broken)
	}
	if len(broken.Evidence) < 2 {
		t.Fatalf("a parse failure must carry the path and the error: %#v", broken.Evidence)
	}
}

func TestAgentInstructionsDetectionAndFilePreference(t *testing.T) {
	env := DefaultEnv()
	dir := t.TempDir()

	// With neither file present the repair targets AGENTS.md.
	if got := AgentInstructionsFile(env, dir); filepath.Base(got) != "AGENTS.md" {
		t.Fatalf("default target = %q", got)
	}
	// CLAUDE.md alone is what the repair would touch.
	claude := filepath.Join(dir, "CLAUDE.md")
	if err := os.WriteFile(claude, []byte("# Project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := AgentInstructionsFile(env, dir); got != claude {
		t.Fatalf("with only CLAUDE.md the target = %q", got)
	}
	// AGENTS.md wins once it exists.
	agents := filepath.Join(dir, "AGENTS.md")
	if err := os.WriteFile(agents, []byte("# Project\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if got := AgentInstructionsFile(env, dir); got != agents {
		t.Fatalf("with both files the target = %q", got)
	}

	if HasAgentInstructions(env, agents) {
		t.Fatal("a file with no Sidecar guidance reported as connected")
	}
	result := checkAgentInstructions(Input{ProjectDir: dir, ProjectName: "demo"})
	if result.OK || result.Repair != RepairAgentInstructions {
		t.Fatalf("missing guidance = %#v", result)
	}

	if err := AddAgentInstructions(agents); err != nil {
		t.Fatal(err)
	}
	if !HasAgentInstructions(env, agents) {
		t.Fatal("the written line was not detected")
	}
	// A healthy row stays navigable — a user may still want to open the file.
	healthy := checkAgentInstructions(Input{ProjectDir: dir, ProjectName: "demo"})
	if !healthy.OK || healthy.Repair != RepairAgentInstructions || healthy.Badge != BadgeOpen {
		t.Fatalf("healthy agent instructions = %#v", healthy)
	}
}

func TestAddAgentInstructionsIsFrontmatterSafeAndNeverOverwrites(t *testing.T) {
	dir := t.TempDir()

	frontmatter := filepath.Join(dir, "AGENTS.md")
	original := "---\ntitle: Demo\ntags: [a]\n---\n\n# Demo\n\nExisting guidance that must survive.\n"
	if err := os.WriteFile(frontmatter, []byte(original), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := AddAgentInstructions(frontmatter); err != nil {
		t.Fatal(err)
	}
	updated, err := os.ReadFile(frontmatter)
	if err != nil {
		t.Fatal(err)
	}
	text := string(updated)
	if !strings.HasPrefix(text, "---\ntitle: Demo") {
		t.Fatalf("frontmatter no longer opens the file:\n%s", text)
	}
	if !strings.Contains(text, "Existing guidance that must survive.") {
		t.Fatalf("existing content was lost:\n%s", text)
	}
	if !strings.Contains(text, AgentInstructionLine) {
		t.Fatalf("the addition is missing:\n%s", text)
	}
	if strings.Index(text, AgentInstructionLine) < strings.Index(text, "# Demo") {
		t.Fatalf("the line landed above the file's own heading:\n%s", text)
	}

	// A second call is a no-op rather than a duplicate.
	if err := AddAgentInstructions(frontmatter); err != nil {
		t.Fatal(err)
	}
	again, _ := os.ReadFile(frontmatter)
	if strings.Count(string(again), AgentInstructionLine) != 1 {
		t.Fatalf("the line was added twice:\n%s", again)
	}

	// A missing file is created containing exactly the reviewed addition.
	fresh := filepath.Join(dir, "sub", "AGENTS.md")
	if err := AddAgentInstructions(fresh); err != nil {
		t.Fatal(err)
	}
	content, err := os.ReadFile(fresh)
	if err != nil {
		t.Fatal(err)
	}
	if string(content) != AgentInstructionsAddition() {
		t.Fatalf("new file = %q, want the reviewed addition", content)
	}
}

func TestRunReportsEveryCheck(t *testing.T) {
	results := Run(Input{
		Config:     &config.Config{},
		ConfigPath: filepath.Join(t.TempDir(), "config.json"),
		Env:        fakeEnv(map[string]string{"COLORTERM": "truecolor"}, nil, nil),
	})
	for _, id := range []ID{CheckTerminalColors, CheckTmux, CheckConfiguration, CheckProjects, CheckAgentInstructions} {
		if _, ok := results.Get(id); !ok {
			t.Fatalf("run did not report %q", id)
		}
	}
	if len(results.Problems())+len(results.Healthy()) != len(results) {
		t.Fatal("problems and healthy do not partition the results")
	}
	// tmux missing and no projects are both problems in this environment.
	if len(results.Problems()) < 2 {
		t.Fatalf("expected tmux and projects problems, got %#v", results.Problems())
	}
}
