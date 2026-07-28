package tty

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
)

// HistoryLimit is the minimum scrollback retained for sidecar-managed panes.
const HistoryLimit = 10000

var tmuxSessionMu sync.Mutex

// PrepareServer configures tmux before any sidecar session is created. Raising
// the global default before new-session is the only behavior that works on
// older tmux releases where history-limit changes do not affect existing panes.
// Existing user settings above the sidecar minimum are preserved.
func PrepareServer() error {
	tmuxSessionMu.Lock()
	defer tmuxSessionMu.Unlock()
	return prepareServer()
}

func prepareServer() error {
	// Keep the server alive between session creations and configure both
	// options in the same invocation so an empty new server cannot exit in
	// the gap between commands.
	if err := exec.Command("tmux",
		"start-server", ";",
		"set-option", "-s", "exit-empty", "off",
	).Run(); err != nil {
		return fmt.Errorf("prepare tmux server: %w", err)
	}

	output, err := exec.Command("tmux", "show-options", "-gv", "history-limit").Output()
	if err != nil {
		return fmt.Errorf("read tmux history-limit: %w", err)
	}
	current, err := strconv.Atoi(strings.TrimSpace(string(output)))
	if err != nil {
		return fmt.Errorf("parse tmux history-limit: %w", err)
	}
	if current < HistoryLimit {
		if err := exec.Command("tmux", "set-option", "-g", "history-limit", strconv.Itoa(HistoryLimit)).Run(); err != nil {
			return fmt.Errorf("set tmux history-limit: %w", err)
		}
	}
	return nil
}

// NewSession creates a tmux session only after the server-wide history default
// is known to be configured. The mutex keeps every sidecar new-session path
// ordered behind preparation and makes a failed preparation retryable.
func NewSession(args ...string) error {
	tmuxSessionMu.Lock()
	defer tmuxSessionMu.Unlock()
	if err := prepareServer(); err != nil {
		return err
	}
	return exec.Command("tmux", args...).Run()
}

// IsSessionDeadError checks if an error indicates the tmux session/pane is gone.
func IsSessionDeadError(err error) bool {
	if err == nil {
		return false
	}
	errStr := err.Error()
	return strings.Contains(errStr, "can't find pane") ||
		strings.Contains(errStr, "no such session") ||
		strings.Contains(errStr, "session not found") ||
		strings.Contains(errStr, "pane not found")
}

// SendKeyToTmux sends a key to a tmux pane using send-keys.
// Uses the tmux key name syntax (e.g., "Enter", "C-c", "Up").
func SendKeyToTmux(sessionName, key string) error {
	return runTmuxWithError("send-keys", "-t", sessionName, key)
}

// SendLiteralToTmux sends literal text to a tmux pane using send-keys -l.
// This prevents tmux from interpreting special key names.
func SendLiteralToTmux(sessionName, text string) error {
	// tmux treats bare ; in argv as a command separator, so a literal
	// semicolon never reaches send-keys. Fall back to hex encoding (-H)
	// which bypasses tmux's command parser entirely.
	if strings.Contains(text, ";") {
		args := []string{"send-keys", "-t", sessionName, "-H"}
		for _, b := range []byte(text) {
			args = append(args, fmt.Sprintf("%02x", b))
		}
		return runTmuxWithError(args...)
	}
	return runTmuxWithError("send-keys", "-l", "-t", sessionName, text)
}

func runTmuxWithError(args ...string) error {
	output, err := exec.Command("tmux", args...).CombinedOutput()
	if err != nil && len(output) > 0 {
		return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
	}
	return err
}

// SendKeysCmd sends keys to tmux asynchronously.
//
// The batch is queued at call time (see send_queue.go) so keystrokes reach tmux
// in the order Update handled them, not the order Bubble Tea happens to schedule
// their goroutines in. Call this from Update, not from inside another tea.Cmd.
// Returns SessionDeadMsg if the session has ended.
func SendKeysCmd(scope MessageScope, sessionName string, keys ...KeySpec) tea.Cmd {
	done := SendKeysOrdered(sessionName, keys...)
	return func() tea.Msg {
		if err := <-done; err != nil && IsSessionDeadError(err) {
			return SessionDeadMsg{Scope: scope}
		}
		return nil
	}
}

// SendKeys sends keys to tmux synchronously and preserves their order.
func SendKeys(sessionName string, keys ...KeySpec) error {
	for _, k := range keys {
		var err error
		if k.Literal {
			err = SendLiteralToTmux(sessionName, k.Value)
		} else {
			err = SendKeyToTmux(sessionName, k.Value)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// ResizeTmuxPane resizes a tmux window/pane to the specified dimensions.
// resize-window works for detached sessions; resize-pane is a fallback.
func ResizeTmuxPane(paneID string, width, height int) {
	if width <= 0 && height <= 0 {
		return
	}

	args := []string{"resize-window", "-t", paneID}
	if width > 0 {
		args = append(args, "-x", strconv.Itoa(width))
	}
	if height > 0 {
		args = append(args, "-y", strconv.Itoa(height))
	}
	cmd := exec.Command("tmux", args...)
	if err := cmd.Run(); err == nil {
		return
	}

	// Fallback for older tmux or attached clients that reject resize-window.
	args = []string{"resize-pane", "-t", paneID}
	if width > 0 {
		args = append(args, "-x", strconv.Itoa(width))
	}
	if height > 0 {
		args = append(args, "-y", strconv.Itoa(height))
	}
	_ = exec.Command("tmux", args...).Run()
}

// SetWindowSizeManual sets the tmux window-size option to "manual" for a session.
// This prevents tmux from auto-constraining window size based on attached clients,
// allowing resize-window commands to stick reliably.
func SetWindowSizeManual(sessionName string) {
	_ = exec.Command("tmux", "set-option", "-t", sessionName, "window-size", "manual").Run()
}

// QueryPaneSize queries the current size of a tmux pane.
func QueryPaneSize(target string) (width, height int, ok bool) {
	if target == "" {
		return 0, 0, false
	}

	cmd := exec.Command("tmux", "display-message", "-t", target, "-p", "#{pane_width},#{pane_height}")
	output, err := cmd.Output()
	if err != nil {
		return 0, 0, false
	}

	parts := strings.Split(strings.TrimSpace(string(output)), ",")
	if len(parts) < 2 {
		return 0, 0, false
	}

	width, _ = strconv.Atoi(parts[0])
	height, _ = strconv.Atoi(parts[1])
	return width, height, true
}

// SGR wheel button codes. Wheel notches are reported as button presses in the
// 64+ range and have no matching release event.
const (
	SGRWheelUp   = 64
	SGRWheelDown = 65
)

// SendSGRWheel sends notches wheel reports to a tmux pane. col and row are
// 1-indexed. A wheel notch has no release event, so only the press form is
// emitted — sending a release would look like a stray button-4/5 release to the
// application.
func SendSGRWheel(sessionName string, up bool, col, row, notches int) error {
	button := SGRWheelDown
	if up {
		button = SGRWheelUp
	}
	for range notches {
		if err := SendSGRMouse(sessionName, button, col, row, false); err != nil {
			return err
		}
	}
	return nil
}

// SendSGRMouse sends an SGR mouse event to a tmux pane.
// button is the mouse button (0=left, 1=middle, 2=right).
// col and row are 1-indexed coordinates.
// release indicates if this is a button release event.
func SendSGRMouse(sessionName string, button, col, row int, release bool) error {
	if col <= 0 || row <= 0 {
		return nil
	}
	suffix := "M"
	if release {
		suffix = "m"
	}
	seq := fmt.Sprintf("\x1b[<%d;%d;%d%s", button, col, row, suffix)
	return SendLiteralToTmux(sessionName, seq)
}

// CapturePaneOutput captures the current output of a tmux pane.
// Uses capture-pane with -p flag to print to stdout and -e to preserve
// ANSI escape sequences (colors, styles).
// The scrollback parameter controls how many lines of history to capture.
func CapturePaneOutput(target string, scrollback int) (string, error) {
	args := []string{"capture-pane", "-p", "-e", "-t", target}
	if scrollback > 0 {
		args = append(args, "-S", fmt.Sprintf("-%d", scrollback))
	}
	cmd := exec.Command("tmux", args...)
	output, err := cmd.Output()
	if err != nil {
		return "", err
	}
	return string(output), nil
}
