package agentcatalog

import (
	"slices"
	"testing"

	"github.com/marcus/sidecar/internal/testenv"
)

func TestCodexLaunchAndResumeDefaultToSupportedScrollback(t *testing.T) {
	for _, advertised := range []bool{false, true} {
		t.Run(map[bool]string{false: "older", true: "inline-supported"}[advertised], func(t *testing.T) {
			help := "usage: codex\n --no-daemon\n"
			if advertised {
				help += " --no-alt-screen\n"
			}
			testenv.ProviderHelp(t, "codex", help)
			prefix := []string{"codex", "--no-daemon"}
			if advertised {
				prefix = append(prefix, "--no-alt-screen")
			}
			launch, err := BuildLaunch("codex", []string{"--model", "space value"}, false)
			if want := append(slices.Clone(prefix), "--model", "space value"); err != nil || !slices.Equal(launch, want) {
				t.Fatalf("launch = %v, %v; want %v", launch, err, want)
			}
			resume, err := BuildResume("codex", "id", "session-id", nil)
			if want := append(slices.Clone(prefix), "resume", "session-id"); err != nil || !slices.Equal(resume, want) {
				t.Fatalf("resume = %v, %v; want %v", resume, err, want)
			}
		})
	}
}

func TestCodexScrollbackRespectsExplicitScreenArguments(t *testing.T) {
	testenv.ProviderHelp(t, "codex", "usage: codex\n --no-daemon\n --no-alt-screen\n")
	for _, tc := range []struct {
		name   string
		extra  []string
		inline bool
	}{
		{"long config", []string{"--config", `tui.alternate_screen="always"`}, false},
		{"joined long config", []string{`--config=tui.alternate_screen="always"`}, false},
		{"short config", []string{"-c", `tui.alternate_screen="always"`}, false},
		{"attached short config", []string{`-ctui.alternate_screen="always"`}, false},
		{"short equals config", []string{`-c=tui.alternate_screen="always"`}, false},
		{"config whitespace", []string{"-c", ` tui.alternate_screen = "always"`}, false},
		{"whole tui table", []string{"-c", `tui={alternate_screen="always"}`}, false},
		{"unrelated config", []string{"-c", `model="custom"`}, true},
		{"unrelated tui setting", []string{"-c", "tui.animations=false"}, true},
		{"explicit inline", []string{"--no-alt-screen"}, true},
		{"positional prompt", []string{"--", `--config=tui.alternate_screen="always"`}, true},
		{"positional inline flag text", []string{"--", "--no-alt-screen"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			for _, resume := range []bool{false, true} {
				var argv []string
				var err error
				if resume {
					argv, err = BuildResume("codex", "id", "session-id", tc.extra)
				} else {
					argv, err = BuildLaunch("codex", tc.extra, false)
				}
				if err != nil {
					t.Fatal(err)
				}
				count := 0
				for _, arg := range argv {
					if arg == "--" {
						break
					}
					if arg == "--no-alt-screen" {
						count++
					}
				}
				if want := map[bool]int{false: 0, true: 1}[tc.inline]; count != want {
					t.Fatalf("resume=%v argv=%v; inline flag count %d, want %d", resume, argv, count, want)
				}
				if !slices.Equal(argv[len(argv)-len(tc.extra):], tc.extra) {
					t.Fatalf("caller arguments changed: %v", argv)
				}
			}
		})
	}
}
