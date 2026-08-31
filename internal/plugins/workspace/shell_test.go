package workspace

import (
	"context"
	"os/exec"
	"reflect"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/agentcatalog"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestTmuxNotationToHuman(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"C-b notation", "C-b", "Ctrl-b"},
		{"C-a notation", "C-a", "Ctrl-a"},
		{"M-x notation", "M-x", "Alt-x"},
		{"M-a notation", "M-a", "Alt-a"},
		{"Short input single char", "C", "C"},
		{"Short input empty", "", ""},
		{"Unhandled notation", "F1", "F1"},
		{"Unknown prefix", "X-y", "X-y"},
		{"Lowercase c prefix", "c-b", "c-b"},
		{"Multiple dashes", "C-b-c", "Ctrl-b-c"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := tmuxNotationToHuman(tt.input)
			if got != tt.expected {
				t.Errorf("tmuxNotationToHuman(%q) = %q, want %q", tt.input, got, tt.expected)
			}
		})
	}
}

func TestGetTmuxPrefix(t *testing.T) {
	// getTmuxPrefix() uses sync.Once for caching, so we can only test it once per process.
	// This test verifies the function returns a valid prefix format.
	prefix := getTmuxPrefix()

	// Should return a non-empty string
	if prefix == "" {
		t.Error("getTmuxPrefix() returned empty string")
	}

	// Should return a human-readable format (contains "Ctrl-" or "Alt-")
	// or the default "Ctrl-b"
	if !strings.Contains(prefix, "Ctrl-") && !strings.Contains(prefix, "Alt-") {
		t.Errorf("getTmuxPrefix() = %q, expected format with 'Ctrl-' or 'Alt-'", prefix)
	}

	// Verify caching: calling again should return the same value
	prefix2 := getTmuxPrefix()
	if prefix != prefix2 {
		t.Errorf("getTmuxPrefix() caching failed: first call = %q, second call = %q", prefix, prefix2)
	}
}

func TestGetTmuxPrefixConcurrency(t *testing.T) {
	// Test concurrent access to cached value
	done := make(chan string, 10)
	for i := 0; i < 10; i++ {
		go func() {
			done <- getTmuxPrefix()
		}()
	}

	// Collect all results
	var results []string
	for i := 0; i < 10; i++ {
		results = append(results, <-done)
	}

	// All results should be identical (cached value)
	first := results[0]
	for i, result := range results {
		if result != first {
			t.Errorf("Concurrent call %d returned %q, expected %q", i, result, first)
		}
	}
}

// hostileSessionID exercises every character class a shell acts on: whitespace,
// both quotes, a backslash, command substitution in both spellings, parameter
// expansion, globs, brace and bracket grouping, redirection, the separators,
// and the expansion characters. Nothing in it is destructive if quoting fails —
// the round-trip test below would report the failure rather than cause damage.
const hostileSessionID = "a b'c\"d\\e`echo bt`$HOME$(echo sub)*?[x]{y}<z>|&;#!~^\tf"

// shellWords asks /bin/sh itself how it reads a rendered command line. It is
// the only honest way to test a quoter: the assertion is the shell's verdict,
// not ours.
func shellWords(t *testing.T, line string) []string {
	t.Helper()
	script := "set -- " + line + "\nfor a; do printf '%s\\n' \"$a\"; done"
	out, err := exec.Command("/bin/sh", "-c", script).Output()
	if err != nil {
		t.Fatalf("sh failed to parse %q: %v", line, err)
	}
	text := strings.TrimSuffix(string(out), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// TestDisplayCommandKeepsPlainArgvBare is the cosmetic half of the contract:
// for every resume shape the catalog produces, with an ordinary session id, the
// rendered line is the unadorned text Sidecar has always shown.
func TestDisplayCommandKeepsPlainArgvBare(t *testing.T) {
	for _, family := range agentcatalog.Families() {
		if !family.CanResume() {
			continue
		}
		argv, err := family.ResumeArgv("id", "019fef25-eee2-7532-9fc3-e7e23ed49721", nil)
		if err != nil {
			t.Fatalf("%s: ResumeArgv: %v", family.ID, err)
		}
		want := strings.Join(argv, " ")
		if got := agentcatalog.DisplayCommand(argv); got != want {
			t.Errorf("%s: agentcatalog.DisplayCommand() = %q, want %q", family.ID, got, want)
		}
	}
}

// TestDisplayCommandQuotesEveryShellCharacter walks the characters a shell acts
// on one at a time, so a future edit to the safe set cannot quietly let one
// through.
func TestDisplayCommandQuotesEveryShellCharacter(t *testing.T) {
	dangerous := []string{
		" ", "\t", "\n", "'", "\"", "\\", "`", "$", "*", "?", "[", "]",
		"(", ")", "{", "}", "<", ">", "|", "&", ";", "#", "!", "~", "^",
	}
	for _, ch := range dangerous {
		value := "id" + ch + "x"
		got := agentcatalog.DisplayCommand([]string{"claude", "--resume", value})
		if strings.Contains(got, " "+value) || !strings.Contains(got, "'") {
			t.Errorf("agentcatalog.DisplayCommand() left %q unquoted: %q", ch, got)
		}
	}
	// An empty argument must survive as an empty word, not vanish.
	if got := agentcatalog.DisplayCommand([]string{"claude", ""}); got != "claude ''" {
		t.Errorf("agentcatalog.DisplayCommand() with an empty entry = %q, want %q", got, "claude ''")
	}
}

// TestResumePrefillIsSingleShellWord proves the line that is typed at a shell
// prompt cannot be read as more than the arguments it was built from, however
// the session id is spelled.
func TestResumePrefillIsSingleShellWord(t *testing.T) {
	for _, id := range []string{
		"019fef25-eee2-7532-9fc3-e7e23ed49721",
		"ses_abc123",
		"abc; echo INJECTED",
		hostileSessionID,
	} {
		argv, err := agentcatalog.BuildResume("claude-code", "id", id, nil)
		if err != nil {
			t.Fatalf("BuildResume(%q): %v", id, err)
		}
		line := agentcatalog.DisplayCommand(argv)
		got := shellWords(t, line)
		if !reflect.DeepEqual(got, argv) {
			t.Errorf("sh read %q as %q, want %q", line, got, argv)
		}
	}
}

// TestCreateShellWithResumeRendersPrefill pins the typed prefill to
// DisplayCommand: the resume flow must not hand a shell anything it built by
// concatenating a session id.
func TestCreateShellWithResumeRendersPrefill(t *testing.T) {
	root := t.TempDir()
	p := &Plugin{
		ctx:          &plugin.Context{Epoch: 1, ProjectRoot: root, WorkDir: root, Config: config.Default()},
		operationCtx: context.Background(),
	}

	argv, err := agentcatalog.BuildResume("claude-code", "id", hostileSessionID, nil)
	if err != nil {
		t.Fatalf("BuildResume: %v", err)
	}

	// The returned command is deliberately never run: it would create a tmux
	// session. Only the pending prefill it stored is under test.
	_ = p.createShellWithResume(argv)

	want := agentcatalog.DisplayCommand(argv)
	if p.pendingPrefillCmd != want {
		t.Fatalf("pendingPrefillCmd = %q, want %q", p.pendingPrefillCmd, want)
	}
	if got := shellWords(t, p.pendingPrefillCmd); !reflect.DeepEqual(got, argv) {
		t.Errorf("sh read the prefill %q as %q, want %q", p.pendingPrefillCmd, got, argv)
	}

	// Empty argv must not arm an injection at all.
	p.pendingPrefillCmd = ""
	if cmd := p.createShellWithResume(nil); cmd != nil || p.pendingPrefillCmd != "" {
		t.Errorf("createShellWithResume(nil) armed a prefill: %q", p.pendingPrefillCmd)
	}
}
