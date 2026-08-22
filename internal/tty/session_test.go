package tty

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/testenv"
)

func installFakeTmux(t *testing.T) string {
	t.Helper()
	// Sends are enqueued at call time and run on a background queue, so an
	// earlier test's send can still be in flight. Bracket this test so its log
	// holds only its own commands.
	WaitForPendingSends()
	t.Cleanup(WaitForPendingSends)
	dir := t.TempDir()
	logPath := filepath.Join(dir, "calls.log")
	script := `#!/bin/sh
printf '%s\n' "$*" >> "$TMUX_FAKE_LOG"
if [ "$1" = "start-server" ] && [ -n "$TMUX_FAKE_FAIL_ONCE" ] && [ ! -e "$TMUX_FAKE_FAIL_ONCE" ]; then
	touch "$TMUX_FAKE_FAIL_ONCE"
	exit 1
fi
if [ "$1" = "show-options" ]; then
	case "$*" in
		*terminal-overrides*)
			if [ -n "$TMUX_FAKE_OVERRIDES_ERROR" ]; then exit 1; fi
			printf '%s\n' "${TMUX_FAKE_OVERRIDES:-}" ;;
		*) printf '%s\n' "${TMUX_FAKE_HISTORY:-2000}" ;;
	esac
fi
if [ "$1" = "send-keys" ] && [ -n "$TMUX_FAKE_SEND_ERROR" ]; then
	printf '%s\n' "$TMUX_FAKE_SEND_ERROR" >&2
	exit 1
fi
`
	path := filepath.Join(dir, "tmux")
	if err := os.WriteFile(path, []byte(script), 0o755); err != nil {
		t.Fatalf("write fake tmux: %v", err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TMUX_FAKE_LOG", logPath)
	return logPath
}

func fakeTmuxCalls(t *testing.T, logPath string) []string {
	t.Helper()
	data, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("read fake tmux log: %v", err)
	}
	return strings.Split(strings.TrimSpace(string(data)), "\n")
}

func TestNewSessionPreparesHistoryBeforeCreation(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("TMUX_FAKE_HISTORY", "2000")
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-ghostty")

	if err := NewSession("new-session", "-d", "-s", "test-session"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	calls := fakeTmuxCalls(t, logPath)
	want := []string{
		"start-server ; set-option -s exit-empty off",
		"show-options -gv history-limit",
		"set-option -g history-limit 10000",
		"show-options -sv terminal-overrides",
		"set-option -sa terminal-overrides ,xterm-ghostty:Tc",
		"new-session -d -s test-session",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tmux call order:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
	}
}

// The claim names sidecar's own TERM. A "*" glob would tell tmux that every
// terminal type on the server renders direct color, including clients sidecar
// never opened and cannot correct.
func TestTruecolorClaimIsScopedToOurTerminal(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "alacritty")

	if err := NewSession("new-session", "-d", "-s", "test-session"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	var appended string
	for _, call := range fakeTmuxCalls(t, logPath) {
		if strings.Contains(call, "set-option -sa terminal-overrides") {
			appended = call
		}
	}
	if appended == "" {
		t.Fatal("a truecolor terminal got no override at all")
	}
	if !strings.HasSuffix(appended, ",alacritty:Tc") {
		t.Fatalf("override was not scoped to TERM: %q", appended)
	}
	if strings.Contains(appended, "*") {
		t.Fatalf("override claims every terminal type: %q", appended)
	}
}

// Without COLORTERM sidecar has no evidence the terminal renders direct color,
// so it says nothing rather than claiming on the terminal's behalf.
func TestNoTruecolorClaimWithoutEvidence(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("COLORTERM", "")
	t.Setenv("TERM", "xterm-256color")

	if err := NewSession("new-session", "-d", "-s", "test-session"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for _, call := range fakeTmuxCalls(t, logPath) {
		if strings.Contains(call, "terminal-overrides") {
			t.Fatalf("sidecar touched terminal-overrides with no COLORTERM:\n%s", call)
		}
	}
}

// A color hint is not worth a shell. Every tmux call advertiseTruecolor makes
// may fail without failing the session the user actually asked for.
func TestTruecolorFailureDoesNotBlockSessionCreation(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-ghostty")
	t.Setenv("TMUX_FAKE_OVERRIDES_ERROR", "1")

	if err := NewSession("new-session", "-d", "-s", "test-session"); err != nil {
		t.Fatalf("a failed color hint blocked session creation: %v", err)
	}
	if !strings.Contains(strings.Join(fakeTmuxCalls(t, logPath), "\n"), "new-session -d -s test-session") {
		t.Fatal("the session was never created")
	}
}

func TestNewSessionPreservesHigherHistoryLimit(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("TMUX_FAKE_HISTORY", "20000")

	if err := NewSession("new-session", "-d", "-s", "test-session"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	calls := strings.Join(fakeTmuxCalls(t, logPath), "\n")
	if strings.Contains(calls, "set-option -g history-limit") {
		t.Fatalf("higher user history-limit was overwritten:\n%s", calls)
	}
	if !strings.HasSuffix(calls, "new-session -d -s test-session") {
		t.Fatalf("new-session did not follow preparation:\n%s", calls)
	}
}

func TestPrepareServerAppendsOverridesOnlyWhenMissing(t *testing.T) {
	// Every launch runs prepareServer, so an unconditional append would grow
	// the override list by one entry per shell opened.
	logPath := installFakeTmux(t)
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-ghostty")
	t.Setenv("TMUX_FAKE_OVERRIDES", "linux*:AX@,xterm-ghostty:Tc")

	if err := NewSession("new-session", "-d", "-s", "test-session"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	for _, call := range fakeTmuxCalls(t, logPath) {
		if strings.Contains(call, "set-option -sa terminal-overrides") {
			t.Fatalf("an override already naming this TERM was appended to again:\n%s", call)
		}
	}
}

// Another terminal's Tc entry is not ours. Matching "Tc" anywhere in the list
// made a user whose config carries xterm-256color:Tc, but who runs under
// alacritty, look configured and silently never get the setting.
func TestAnotherTerminalsOverrideDoesNotCountAsOurs(t *testing.T) {
	logPath := installFakeTmux(t)
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "alacritty")
	t.Setenv("TMUX_FAKE_OVERRIDES", "xterm-256color:Tc")

	if err := NewSession("new-session", "-d", "-s", "test-session"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	var appended bool
	for _, call := range fakeTmuxCalls(t, logPath) {
		if strings.HasSuffix(call, ",alacritty:Tc") {
			appended = true
		}
	}
	if !appended {
		t.Fatal("a different terminal's Tc entry suppressed our own")
	}
}

func TestNewSessionRetriesAfterPreparationFailure(t *testing.T) {
	logPath := installFakeTmux(t)
	failMarker := filepath.Join(filepath.Dir(logPath), "failed-once")
	t.Setenv("TMUX_FAKE_FAIL_ONCE", failMarker)

	if err := NewSession("new-session", "-d", "-s", "first"); err == nil {
		t.Fatal("first NewSession unexpectedly succeeded")
	}
	if err := NewSession("new-session", "-d", "-s", "second"); err != nil {
		t.Fatalf("retry NewSession: %v", err)
	}

	calls := strings.Join(fakeTmuxCalls(t, logPath), "\n")
	if strings.Count(calls, "start-server ; set-option -s exit-empty off") != 2 {
		t.Fatalf("preparation was not retried:\n%s", calls)
	}
	if strings.Contains(calls, "new-session -d -s first") {
		t.Fatalf("session created after failed preparation:\n%s", calls)
	}
	if !strings.Contains(calls, "new-session -d -s second") {
		t.Fatalf("session not created after successful retry:\n%s", calls)
	}
}

func TestIsSessionDeadError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "nil error", err: nil, want: false},
		{name: "can't find pane", err: errors.New("can't find pane: %5"), want: true},
		{name: "no such session", err: errors.New("no such session: sidecar-edit-123"), want: true},
		{name: "session not found", err: errors.New("session not found"), want: true},
		{name: "pane not found", err: errors.New("pane not found"), want: true},
		{name: "unrelated error", err: errors.New("connection refused"), want: false},
		{name: "empty error message", err: errors.New(""), want: false},
		{name: "error containing dead pane substring", err: errors.New("tmux: can't find pane: sidecar-edit-12345"), want: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsSessionDeadError(tt.err); got != tt.want {
				t.Errorf("IsSessionDeadError(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestCapturePaneOutput_CommandArgs(t *testing.T) {
	// Verify the function remains callable with the expected parameter types.
	var _ = CapturePaneOutput
}

func TestSendSGRMouse_BoundsCheck(t *testing.T) {
	tests := []struct {
		name    string
		col     int
		row     int
		wantNil bool
	}{
		{"valid coords", 1, 1, false},
		{"zero col", 0, 1, true},
		{"zero row", 1, 0, true},
		{"negative col", -1, 1, true},
		{"negative row", 1, -1, true},
		{"both zero", 0, 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := SendSGRMouse("nonexistent-session", 0, tt.col, tt.row, false)
			if tt.wantNil && err != nil {
				t.Errorf("SendSGRMouse with invalid coords returned error instead of nil: %v", err)
			}
		})
	}
}

func TestResizeTmuxPane_ZeroDimensions(t *testing.T) {
	ResizeTmuxPane("nonexistent", 0, 0)
	ResizeTmuxPane("nonexistent", -1, -1)
}

// A resize aimed at the pane hosting this process is refused outright: that
// pane is sidecar's own screen, and shrinking it is the td-9cddeb bug.
func TestResizeTmuxPaneRefusesTheHostingPane(t *testing.T) {
	t.Setenv("TMUX_PANE", "")
	if refusesHostingPane("%0") {
		t.Fatal("no hosting pane outside tmux, nothing may be refused")
	}

	t.Setenv("TMUX_PANE", "%142")
	if !refusesHostingPane("%142") {
		t.Fatal("the hosting pane must be refused")
	}
	if refusesHostingPane("%19") || refusesHostingPane("") {
		t.Fatal("other panes are not the hosting pane")
	}
}

func TestPrepareServerAdvertisesTruecolor(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping tmux integration in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	// A short directory, not t.TempDir(): the tmux socket path is this
	// directory plus "tmux-$UID/default", and macOS test temp paths already
	// carry the test name — past the ~104-character Unix socket limit.
	tmpdir, err := os.MkdirTemp("", "sc-tty")
	if err != nil {
		t.Fatalf("tempdir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpdir) })
	t.Setenv("TMUX_TMPDIR", tmpdir)
	// Kill by socket path, never a bare kill-server. A bare one trusts
	// TMUX_TMPDIR to still be private at cleanup time; t.Setenv's restore runs
	// in the same LIFO stack, and reordering these lines would aim it at the
	// developer's own server. This is the td-8d18de mistake, and
	// internal/testenv exists because of it.
	socket := testenv.SocketPath(tmpdir)
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })

	// Pin both inputs the decision reads, so the assertion means the same
	// thing on a CI runner with no COLORTERM as on a developer's terminal.
	t.Setenv("COLORTERM", "truecolor")
	t.Setenv("TERM", "xterm-ghostty")
	const entry = "xterm-ghostty:Tc"

	// Run twice: the second pass must be a no-op, or every shell a user opens
	// grows the override list by another copy of the same entry.
	first, err := prepareAndReadOverrides()
	if err != nil {
		t.Fatalf("PrepareServer pass 1: %v", err)
	}
	if !strings.Contains(first, entry) {
		t.Fatalf("PrepareServer did not advertise %q: %q", entry, first)
	}
	second, err := prepareAndReadOverrides()
	if err != nil {
		t.Fatalf("PrepareServer pass 2: %v", err)
	}
	if first != second {
		t.Fatalf("terminal-overrides changed across passes:\n%q\nvs\n%q", first, second)
	}
	if got := strings.Count(second, entry); got != 1 {
		t.Fatalf("%q appears %d times after two passes, want 1: %q", entry, got, second)
	}

	// Creating a session runs a full preparation pass of its own, so this is a
	// third pass and the same idempotence has to survive it.
	if err := NewSession("new-session", "-d", "-s", "probe"); err != nil {
		t.Fatalf("NewSession probe: %v", err)
	}
	third, err := exec.Command("tmux", "show-options", "-sv", "terminal-overrides").Output()
	if err != nil {
		t.Fatalf("read terminal-overrides after NewSession: %v", err)
	}
	if string(third) != first {
		t.Fatalf("NewSession changed terminal-overrides:\n%q\nvs\n%q", third, first)
	}
}

// What this deliberately does not assert: the environment a pane process
// receives. tmux 3.6 injects COLORTERM=truecolor into panes itself — verified
// against a server whose global and process environments both lack it — so a
// pane-side probe would pass whether or not this code ran. An assertion that
// cannot fail is worse than none.

// prepareAndReadOverrides runs one preparation pass and returns the resulting
// terminal-overrides value.
func prepareAndReadOverrides() (string, error) {
	if err := PrepareServer(); err != nil {
		return "", err
	}
	out, err := exec.Command("tmux", "show-options", "-sv", "terminal-overrides").Output()
	return string(out), err
}
