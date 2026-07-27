package tty

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
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
	printf '%s\n' "${TMUX_FAKE_HISTORY:-2000}"
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

	if err := NewSession("new-session", "-d", "-s", "test-session"); err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	calls := fakeTmuxCalls(t, logPath)
	want := []string{
		"start-server ; set-option -s exit-empty off",
		"show-options -gv history-limit",
		"set-option -g history-limit 10000",
		"new-session -d -s test-session",
	}
	if strings.Join(calls, "\n") != strings.Join(want, "\n") {
		t.Fatalf("tmux call order:\n%s\nwant:\n%s", strings.Join(calls, "\n"), strings.Join(want, "\n"))
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
