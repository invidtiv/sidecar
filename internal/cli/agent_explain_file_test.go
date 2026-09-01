package cli

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentactivity/manifest"
	"github.com/marcus/sidecar/internal/shellstate"
)

func writeFixture(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "screen.txt")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// TestExplainFileNeedsNoManagedShell is the property that makes --file useful:
// it reads a saved capture with no tmux, no lifecycle store, and no running
// agent, so a wrong badge can be reproduced from the fixture that produced it.
func TestExplainFileNeedsNoManagedShell(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")
	path := writeFixture(t, "pane_title: ⠋ llm-proxy\npane_current_command: codex\nscreen:\nsome output\n")

	code, out, errOut := runLifecycleCLI(t, "agent", "explain", "--file", path, "--agent", "codex", "--json")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, errOut)
	}
	var explain manifest.Explain
	if err := json.Unmarshal([]byte(out), &explain); err != nil {
		t.Fatalf("output is not the explain record: %v\n%s", err, out)
	}
	if explain.State != manifest.StateWorking {
		t.Fatalf("state = %q, want working from the braille title", explain.State)
	}
	if explain.MatchedRule == nil || explain.MatchedRule.ID != "osc_title_working" {
		t.Fatalf("matched rule = %+v", explain.MatchedRule)
	}
	if !strings.HasPrefix(explain.ManifestSource, "bundled codex ") {
		t.Fatalf("manifest_source = %q", explain.ManifestSource)
	}
}

func TestExplainFileReadsARawScreenWithNoHeader(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")
	// No `screen:` line: a capture taken straight from `tmux capture-pane -p`.
	path := writeFixture(t, "do you want to continue? [y/n]\n")

	code, out, errOut := runLifecycleCLI(t, "agent", "explain", "--file", path, "--agent", "codex", "--json")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, errOut)
	}
	var explain manifest.Explain
	if err := json.Unmarshal([]byte(out), &explain); err != nil {
		t.Fatal(err)
	}
	if explain.State != manifest.StateBlocked || explain.MatchedRule == nil || explain.MatchedRule.ID != "weak_blocker" {
		t.Fatalf("state = %q rule = %+v", explain.State, explain.MatchedRule)
	}
}

func TestExplainFileTitleFlagSuppliesWhatTheHeaderWouldHave(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")
	path := writeFixture(t, "ordinary output\n")

	code, out, _ := runLifecycleCLI(t, "agent", "explain", "--file", path, "--agent", "codex",
		"--title", "[ ! ] Action Required | project", "--json")
	if code != 0 {
		t.Fatalf("exit %d", code)
	}
	var explain manifest.Explain
	if err := json.Unmarshal([]byte(out), &explain); err != nil {
		t.Fatal(err)
	}
	if explain.MatchedRule == nil || explain.MatchedRule.ID != "osc_title_blocked" {
		t.Fatalf("matched rule = %+v", explain.MatchedRule)
	}
}

// TestExplainFilePrintWindowIsTheTextDetectionSaw is the offline half of
// Herdr's `agent read --source detection`, and it is what the differential
// harness feeds to both engines.
func TestExplainFilePrintWindowIsTheTextDetectionSaw(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")
	var b strings.Builder
	b.WriteString("pane_current_command: codex\nscreen:\n")
	for i := 1; i <= 40; i++ {
		b.WriteString("row ")
		b.WriteString(strings.Repeat("x", 0))
		b.WriteString(string(rune('0'+i%10)) + "\n")
	}
	b.WriteString("\n\n   \n")
	path := writeFixture(t, b.String())

	code, out, errOut := runLifecycleCLI(t, "agent", "explain", "--file", path, "--agent", "codex",
		"--rows", "5", "--print-window")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, errOut)
	}
	lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	if len(lines) != 5 {
		t.Fatalf("window has %d rows, want the 5 requested:\n%q", len(lines), out)
	}
	if lines[len(lines)-1] != "row 0" {
		t.Fatalf("window does not end at the last non-blank row: %q", lines[len(lines)-1])
	}
}

func TestExplainFileTextOutputFollowsHerdrsLayout(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")
	path := writeFixture(t, "pane_current_command: codex\npane_title: llm-proxy\nscreen:\nquiet\n")

	code, out, errOut := runLifecycleCLI(t, "agent", "explain", "--file", path, "--agent", "codex")
	if code != 0 {
		t.Fatalf("exit %d (stderr: %s)", code, errOut)
	}
	for _, want := range []string{"agent: codex", "state: idle", "manifest: bundled codex ", "rule: osc_title_idle", "evaluated_rules:"} {
		if !strings.Contains(out, want) {
			t.Fatalf("output is missing %q:\n%s", want, out)
		}
	}
}

func TestExplainFileRefusals(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")
	path := writeFixture(t, "anything\n")

	tests := []struct {
		name string
		args []string
		code int
	}{
		{"no agent", []string{"agent", "explain", "--file", path}, 2},
		{"unknown agent", []string{"agent", "explain", "--file", path, "--agent", "nosuch"}, 2},
		{"file with current", []string{"agent", "explain", "--file", path, "--agent", "codex", "--current"}, 2},
		{"agent without file", []string{"agent", "explain", "--agent", "codex"}, 2},
		{"missing file", []string{"agent", "explain", "--file", filepath.Join(t.TempDir(), "absent.txt"), "--agent", "codex"}, 1},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			code, _, errOut := runLifecycleCLI(t, tt.args...)
			if code != tt.code {
				t.Fatalf("exit %d, want %d (stderr: %s)", code, tt.code, errOut)
			}
			if errOut == "" {
				t.Fatal("refused without saying why")
			}
		})
	}
}
