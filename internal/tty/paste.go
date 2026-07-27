package tty

import (
	"os/exec"
	"strings"

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

// SendPasteToTmux pastes multi-line text via tmux buffer.
// Uses load-buffer + paste-buffer which works regardless of app paste mode state.
func SendPasteToTmux(sessionName, text string) error {
	// Load text into tmux default buffer via stdin
	loadCmd := exec.Command("tmux", "load-buffer", "-")
	loadCmd.Stdin = strings.NewReader(text)
	if err := loadCmd.Run(); err != nil {
		return err
	}

	// Paste buffer into target pane
	pasteCmd := exec.Command("tmux", "paste-buffer", "-t", sessionName)
	return pasteCmd.Run()
}

// SendBracketedPasteToTmux sends text wrapped in bracketed paste sequences.
// Used when the target app has enabled bracketed paste mode.
func SendBracketedPasteToTmux(sessionName, text string) error {
	// Send bracketed paste start sequence
	if err := SendLiteralToTmux(sessionName, BracketedPasteStart); err != nil {
		return err
	}

	// Send the actual text
	if err := SendLiteralToTmux(sessionName, text); err != nil {
		return err
	}

	// Send bracketed paste end sequence
	return SendLiteralToTmux(sessionName, BracketedPasteEnd)
}

// PasteClipboardToTmuxCmd returns a tea.Cmd that pastes clipboard content to a tmux session.
// The bracketed parameter determines whether to use bracketed paste mode.
// Returns a PasteResultMsg with the result.
func PasteClipboardToTmuxCmd(scope MessageScope, sessionName string, bracketed bool) tea.Cmd {
	return func() tea.Msg {
		text, err := clipboard.ReadAll()
		if err != nil {
			return PasteResultMsg{Scope: scope, Err: err}
		}
		if text == "" {
			return PasteResultMsg{Scope: scope, Empty: true}
		}

		if bracketed {
			err = SendBracketedPasteToTmux(sessionName, text)
		} else {
			err = SendPasteToTmux(sessionName, text)
		}
		if err != nil {
			return PasteResultMsg{Scope: scope, Err: err, SessionDead: IsSessionDeadError(err)}
		}

		return PasteResultMsg{Scope: scope}
	}
}

// SendPasteInputCmd sends paste text to tmux asynchronously.
// Used for multi-character terminal input (not clipboard paste which is already async).
func SendPasteInputCmd(scope MessageScope, sessionName, text string, bracketed bool) tea.Cmd {
	return func() tea.Msg {
		err := SendPasteInput(sessionName, text, bracketed)
		if err != nil && IsSessionDeadError(err) {
			return SessionDeadMsg{Scope: scope}
		}
		return nil
	}
}

// SendPasteInput forwards paste text using the target applications current
// bracketed-paste mode.
func SendPasteInput(sessionName, text string, bracketed bool) error {
	if bracketed {
		return SendBracketedPasteToTmux(sessionName, text)
	}
	return SendPasteToTmux(sessionName, text)
}
