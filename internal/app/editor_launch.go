package app

import (
	"errors"
	"os"
	"os/exec"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
	"golang.org/x/term"
)

// launchEditor suspends Sidecar into the user's editor. The editor is spawned
// through the login + interactive shell so the profile runs — see
// internal/tty/editor_launch.go for why, and for why the path can never become
// shell text. Nothing here runs before the first frame: it is reached only from
// a plugin's OpenFileMsg, which is a keypress.
func (m *Model) launchEditor(msg plugin.OpenFileMsg) tea.Cmd {
	argv, viaProfile := tty.EditorArgv(msg.Editor, msg.LineNo, msg.Path)
	var fallback []string
	if viaProfile {
		fallback = tty.DirectEditorArgv(msg.Editor, msg.LineNo, msg.Path)
	}
	return execEditorCmd(argv, fallback)
}

// execEditorCmd hands one argv to Bubble Tea's process runner and carries the
// fallback along so the answer can decide whether to retry.
func execEditorCmd(argv, fallback []string) tea.Cmd {
	if len(argv) == 0 {
		return nil
	}
	c := exec.Command(argv[0], argv[1:]...)
	termState, _ := term.GetState(int(os.Stdout.Fd()))
	return tea.ExecProcess(c, func(err error) tea.Msg {
		if termState != nil {
			_ = term.Restore(int(os.Stdout.Fd()), termState)
		}
		return EditorReturnedMsg{Err: err, Fallback: fallback}
	})
}

// editorFallbackCmd retries the direct exec when the profile-loading shell is
// what failed. An editor that started and exited badly is the user's own
// business and is reported, not retried.
func editorFallbackCmd(msg EditorReturnedMsg) tea.Cmd {
	if msg.Err == nil || len(msg.Fallback) == 0 || !shellLaunchFailed(msg.Err) {
		return nil
	}
	return execEditorCmd(msg.Fallback, nil)
}

// shellLaunchFailed reports that the wrapper shell, rather than the editor,
// is what went wrong: the shell binary would not start, or it exited with the
// POSIX "could not execute" / "not found" statuses without handing the
// terminal to anything.
func shellLaunchFailed(err error) bool {
	var execErr *exec.Error
	if errors.As(err, &execErr) {
		return true
	}
	var pathErr *os.PathError
	if errors.As(err, &pathErr) {
		return true
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		switch exitErr.ExitCode() {
		case 126, 127:
			return true
		}
	}
	return false
}
