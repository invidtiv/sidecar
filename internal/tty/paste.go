package tty

import (
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/atotto/clipboard"
)

// IsPasteInput detects if a key message is actually a paste operation.
//
// In bubbletea v2, true bracketed pastes arrive as a separate tea.PasteMsg
// (handled in Update), so this heuristic only catches terminals that deliver
// multi-rune text as a single KeyPressMsg without bracketed paste. It returns
// true if the typed text contains newlines or is suspiciously long for typing.
func IsPasteInput(msg tea.KeyPressMsg) bool {
	runes := []rune(msg.Text)
	if len(runes) <= 1 {
		return false
	}
	// Treat as paste if contains newline or is suspiciously long for typing
	return strings.Contains(msg.Text, "\n") || len(runes) > 10
}

// pasteBufferSeq disambiguates two pastes started inside the same nanosecond.
var pasteBufferSeq atomic.Uint64

// newPasteBufferName returns a buffer name no other paste can be using.
//
// tmux buffers are server-global: every client and every Sidecar pane attached
// to the same server shares them, so the unnamed buffer is a race waiting to
// happen. The pid scopes the name to this Sidecar process (a tmux server and
// its clients are always on one host, so pids are unambiguous), the timestamp
// scopes it within the process's lifetime across restarts and pid reuse, and
// the counter separates pastes issued in the same nanosecond. Only characters
// tmux accepts unquoted in a buffer name are used.
func newPasteBufferName() string {
	return fmt.Sprintf("sidecar-paste-%d-%d-%d", os.Getpid(), time.Now().UnixNano(), pasteBufferSeq.Add(1))
}

// SendPasteToTmux pastes text into a pane through a uniquely named tmux buffer.
//
// tmux, not Sidecar, decides whether the paste is bracketed: `paste-buffer -p`
// emits the bracketed-paste control codes only when the pane's application has
// actually requested that mode. That state is not exposed as a tmux format and
// cannot be reconstructed from a capture, so any bracketing Sidecar did itself
// would be a guess. `-d` drops the buffer once it has been pasted, and `-t`
// names the pane explicitly.
//
// `-r` is deliberately NOT passed, although the plan for this work specified it.
// It disables tmux's default LF→CR translation, and a raw-mode program that has
// not requested bracketed paste reads CR — not LF — as "submit this line". With
// `-r` a multi-line paste into such a program (measured: the pane received
// `41 0a 42` instead of `41 0d 42`) stops submitting its lines. Line-ending
// behavior therefore stays exactly as it has always been; `-p` is the part that
// actually matters. Pinned in both directions by
// TestSendPasteToTmuxBracketedAndPlain.
func SendPasteToTmux(sessionName, text string) error {
	return sendPasteToTmuxSocket("", sessionName, text)
}

// sendPasteToTmuxSocket is SendPasteToTmux against an explicit tmux socket.
// An empty socket means the ambient server; tests pass a throwaway socket so
// they never touch it.
func sendPasteToTmuxSocket(socket, sessionName, text string) error {
	buffer := newPasteBufferName()
	tmux := func(args ...string) *exec.Cmd {
		if socket != "" {
			args = append([]string{"-S", socket}, args...)
		}
		cmd := exec.Command("tmux", args...) //nolint:gosec
		if socket != "" {
			// An explicit socket already overrides server selection, but TMUX is
			// scrubbed anyway so a suite running inside tmux can never resolve any
			// part of the command against the developer's live default server.
			// Matches newProcessControlChannelForSocket.
			cmd.Env = append(os.Environ(), "TMUX=")
		}
		return cmd
	}

	loadCmd := tmux("load-buffer", "-b", buffer, "-")
	loadCmd.Stdin = strings.NewReader(text)
	if output, err := loadCmd.CombinedOutput(); err != nil {
		if len(output) > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
		}
		return err
	}

	output, err := tmux("paste-buffer", "-p", "-d", "-b", buffer, "-t", sessionName).CombinedOutput()
	if err != nil {
		// -d only deletes on the success path. A pane that died between the two
		// commands would otherwise leave the named buffer on the user's server
		// forever, once per failed paste.
		_ = tmux("delete-buffer", "-b", buffer).Run()
		if len(output) > 0 {
			return fmt.Errorf("%w: %s", err, strings.TrimSpace(string(output)))
		}
		return err
	}
	return nil
}

// PasteClipboardToTmuxCmd returns a tea.Cmd that pastes clipboard content to a
// tmux session. Whether the paste is bracketed is tmux's call, not the caller's.
// Returns a PasteResultMsg with the result.
func PasteClipboardToTmuxCmd(scope MessageScope, sessionName string) tea.Cmd {
	return func() tea.Msg {
		text, err := clipboard.ReadAll()
		if err != nil {
			return PasteResultMsg{Scope: scope, Err: err}
		}
		if text == "" {
			return PasteResultMsg{Scope: scope, Empty: true}
		}

		if err = SendPasteToTmux(sessionName, text); err != nil {
			return PasteResultMsg{Scope: scope, Err: err, SessionDead: IsSessionDeadError(err)}
		}

		return PasteResultMsg{Scope: scope}
	}
}

// SendPasteInputCmd sends paste text to tmux asynchronously.
// Used for multi-character terminal input (not clipboard paste which is already async).
func SendPasteInputCmd(scope MessageScope, sessionName, text string) tea.Cmd {
	return func() tea.Msg {
		err := SendPasteInput(sessionName, text)
		if err != nil && IsSessionDeadError(err) {
			return SessionDeadMsg{Scope: scope}
		}
		return nil
	}
}

// SendPasteInput forwards paste text to the pane, letting tmux apply the
// target application's current bracketed-paste mode.
func SendPasteInput(sessionName, text string) error {
	return SendPasteToTmux(sessionName, text)
}
