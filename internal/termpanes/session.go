package termpanes

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceops"
)

const SessionPrefix = "sidecar-tp-"

const (
	CapMessage        = "Two live terminals at a time; close one first"
	CapDisabledReason = "Two terminals are already on screen — close one first"
)

// SessionName derives the stable tmux name used by a terminal split on every
// host. selector is the owning workspace or shell's durable tmux identity.
func SessionName(selector string) string {
	selector = workspaceops.SanitizeSessionName(strings.TrimSpace(selector))
	if selector == "" {
		return ""
	}
	return SessionPrefix + selector
}

// EnsureSession reuses a split session when it exists, otherwise creates it.
func EnsureSession(session, workDir string) (string, error) {
	if pane := workspaceops.PaneID(session); pane != "" {
		return pane, nil
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		return "", fmt.Errorf("tmux not installed")
	}
	if err := tty.NewSession("new-session", "-d", "-s", session, "-c", workDir); err != nil {
		return "", fmt.Errorf("create terminal panel session: %w", err)
	}
	return workspaceops.PaneID(session), nil
}

// TmuxRunner runs one tmux argv on another machine and returns stdout.
type TmuxRunner func(args ...string) ([]byte, error)

// EnsureRemoteSession is the host-side equivalent of EnsureSession: reuse the
// split session when tmux already has it, otherwise `new-session -d` there.
func EnsureRemoteSession(run TmuxRunner, session, workDir string) (string, error) {
	session = strings.TrimSpace(session)
	if session == "" {
		return "", fmt.Errorf("terminal split session is required")
	}
	if pane := remotePaneID(run, session); pane != "" {
		return pane, nil
	}
	if _, err := run("new-session", "-d", "-s", session, "-c", workDir); err != nil {
		if pane := remotePaneID(run, session); pane != "" {
			return pane, nil
		}
		return "", fmt.Errorf("create terminal panel session: %w", err)
	}
	pane := remotePaneID(run, session)
	if pane == "" {
		return "", fmt.Errorf("create terminal panel session: no pane")
	}
	return pane, nil
}

func remotePaneID(run TmuxRunner, session string) string {
	out, err := run("list-panes", "-t", session, "-F", "#{pane_id}")
	if err != nil {
		return ""
	}
	paneID := strings.TrimSpace(string(out))
	if idx := strings.Index(paneID, "\n"); idx > 0 {
		paneID = paneID[:idx]
	}
	return paneID
}

type CloseMode uint8

const (
	CloseHide CloseMode = iota
	CloseExplicit
	CloseSessionEnded
)

// CloseNeedsConfirm reports whether a split close must ask about a running
// process rather than closing its login shell directly.
func CloseNeedsConfirm(currentCommand, shellCommand string) bool {
	current := baseCommand(currentCommand)
	if current == "" {
		return false
	}
	shell := baseCommand(shellCommand)
	return shell == "" || !strings.EqualFold(current, shell)
}

// CloseEvidence is the minimum tmux state needed to decide whether a terminal
// split may close immediately or needs a running-process confirmation.
type CloseEvidence struct {
	CurrentCommand string
	ShellCommand   string
}

// ProbeClose asks only the owned split session what is currently running.
func ProbeClose(session string) (CloseEvidence, error) {
	session = strings.TrimSpace(session)
	if !strings.HasPrefix(session, SessionPrefix) {
		return CloseEvidence{}, fmt.Errorf("terminal split session is unavailable")
	}
	out, err := exec.Command("tmux", "display-message", "-p", "-t", session, "#{pane_current_command}").Output()
	return CloseEvidence{
		CurrentCommand: strings.TrimSpace(string(out)),
		ShellCommand:   filepath.Base(strings.TrimSpace(os.Getenv("SHELL"))),
	}, err
}

func baseCommand(command string) string {
	command = strings.TrimSpace(command)
	if idx := strings.LastIndexByte(command, '/'); idx >= 0 {
		command = command[idx+1:]
	}
	return strings.TrimPrefix(command, "-")
}

// KillSession ends only a session created for a terminal split.
func KillSession(session string) tea.Cmd {
	session = strings.TrimSpace(session)
	if !strings.HasPrefix(session, SessionPrefix) {
		return nil
	}
	return func() tea.Msg {
		_ = exec.Command("tmux", "kill-session", "-t", session).Run()
		return nil
	}
}
