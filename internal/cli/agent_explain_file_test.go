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
	rows := func(trailing string) string {
		var b strings.Builder
		b.WriteString("pane_current_command: codex\nscreen:\n")
		for i := 1; i <= 40; i++ {
			b.WriteString("row ")
			b.WriteString(string(rune('0'+i%10)) + "\n")
		}
		b.WriteString(trailing)
		return b.String()
	}
	window := func(t *testing.T, fixture string) []string {
		t.Helper()
		path := writeFixture(t, fixture)
		code, out, errOut := runLifecycleCLI(t, "agent", "explain", "--file", path, "--agent", "codex",
			"--rows", "5", "--print-window")
		if code != 0 {
			t.Fatalf("exit %d (stderr: %s)", code, errOut)
		}
		if out == "" {
			return nil
		}
		return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
	}

	// Forty rows of output ending on the cursor row: a five-row pane shows the
	// last four of them plus that blank cursor row, which trims away.
	lines := window(t, rows(""))
	if len(lines) != 4 {
		t.Fatalf("window has %d rows, want 4:\n%q", len(lines), lines)
	}
	if lines[len(lines)-1] != "row 0" || lines[0] != "row 7" {
		t.Fatalf("window spans %q..%q, want row 7..row 0", lines[0], lines[len(lines)-1])
	}

	// The same output with three blank rows below it. Those rows are inside the
	// five-row window and are not backfilled, so the window is one row long —
	// which is what the pane shows. A window that trimmed before selecting would
	// still print five rows here, reaching four rows further up than the pane
	// can display, and that is how a resolved historical prompt wins a rule.
	if lines := window(t, rows("\n\n   \n")); len(lines) != 1 || lines[0] != "row 0" {
		t.Fatalf("padded window = %q, want the single row the pane still shows", lines)
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
		{"file with current", []string{"agent", "explain", "--file", path, "--agent", "codex", "--current"}, 2},
		{"agent without file", []string{"agent", "explain", "--agent", "codex"}, 2},
		{"rows without a value", []string{"agent", "explain", "--file", path, "--agent", "codex", "--rows"}, 2},
		{"rows is not a number", []string{"agent", "explain", "--file", path, "--agent", "codex", "--rows", "wide"}, 2},

		// The command line is well formed in these two; a value inside it was
		// refused. That is exitInputRejected everywhere else in the CLI, and
		// exit 1 here would point a caller at the store this command never
		// opens.
		{"unknown agent", []string{"agent", "explain", "--file", path, "--agent", "nosuch"}, exitInputRejected},
		{"missing file", []string{"agent", "explain", "--file", filepath.Join(t.TempDir(), "absent.txt"), "--agent", "codex"}, exitInputRejected},
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

// TestExplainFileRejectsZeroRows closes a gap that made --rows lie. Zero parsed
// and was then silently ignored -- the read window fell back to the fixture
// header or to 24 -- so a caller pinning the window to nothing got a verdict
// from a window it did not ask for and no indication of it.
func TestExplainFileRejectsZeroRows(t *testing.T) {
	t.Setenv(shellstate.ManagedEnv, "")
	path := writeFixture(t, "pane_current_command: codex\nscreen:\nquiet\n")

	code, _, errOut := runLifecycleCLI(t, "agent", "explain", "--file", path, "--agent", "codex", "--rows", "0")
	if code != 2 {
		t.Fatalf("exit %d, want a usage error (stderr: %s)", code, errOut)
	}
	if !strings.Contains(errOut, "--rows must be a positive integer") {
		t.Fatalf("refusal does not say what is wrong: %s", errOut)
	}
	if !strings.Contains(errOut, "24-row fallback") {
		t.Fatalf("refusal does not say how to get the fallback: %s", errOut)
	}
}

// TestExplainExitCodesAreDocumented keeps the published table honest about the
// two codes this command actually returns for a rejected input.
func TestExplainExitCodesAreDocumented(t *testing.T) {
	cmd := RootCommand().FindSubcommand("agent").FindSubcommand("explain")
	byCode := map[int]string{}
	for _, ec := range cmd.ExitCodes {
		byCode[ec.Code] = ec.Summary
	}
	if summary, ok := byCode[1]; !ok || strings.Contains(summary, "stored") {
		t.Fatalf("exit 1 = %q; explain stores nothing, so it must not claim a store failure", summary)
	}
	if _, ok := byCode[exitInputRejected]; !ok {
		t.Fatalf("exit %d is undocumented for explain: %+v", exitInputRejected, cmd.ExitCodes)
	}
}
