package workspaceops

import (
	"fmt"
	"os/exec"
	"strconv"
	"strings"

	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tty"
)

// Creating a shell is the smallest thing either Workspaces surface does, and it
// is the first one the global browser needs. It is here rather than in the
// project plugin because nothing about it is project-plugin shaped: given a
// directory, a session name, a display name, and a pane size, it makes a
// detached tmux session and reports what it made.
//
// The caller supplies the names. Both are project-scoped decisions — the next
// free "Shell N", a session name that does not collide — and the plugin and a
// global host resolve them from different places. Passing them in is what lets
// one implementation serve both.

// ShellSpec is everything creating a shell needs. Every field is resolved by
// the caller; nothing here is discovered.
type ShellSpec struct {
	// WorkDir is the directory the session starts in.
	WorkDir string
	// SessionName is the tmux session identity, already checked for collisions
	// by whoever owns the naming scheme.
	SessionName string
	// DisplayName is what a human sees. It is published to the session
	// environment as a cue; the manifest remains the authority.
	DisplayName string
	// Cols and Rows size the pane at creation. Without them tmux uses its
	// default 80x24 and anything started before the follow-up resize — an
	// editor especially — lays itself out for 24 rows. Zero means let tmux
	// decide.
	Cols, Rows int
}

// ShellResult reports what was created. PaneID is empty when tmux created the
// session but did not report a pane, which is survivable: the session exists
// and the pane can be resolved again later.
type ShellResult struct {
	SessionName string
	PaneID      string
}

// TmuxInstalled reports whether a tmux binary is on PATH. Creating a shell
// without one fails in a way worth refusing early and explaining.
func TmuxInstalled() bool {
	_, err := exec.LookPath("tmux")
	return err == nil
}

// SessionExists reports whether a tmux session of that name is already running.
func SessionExists(name string) bool {
	if name == "" {
		return false
	}
	return exec.Command("tmux", "has-session", "-t", name).Run() == nil
}

// PaneID returns a session's first pane identifier, or "" if tmux cannot say.
// Callers that cache do so around this; there is one tmux call, here.
func PaneID(sessionName string) string {
	if sessionName == "" {
		return ""
	}
	output, err := exec.Command("tmux", "list-panes", "-t", sessionName, "-F", "#{pane_id}").Output()
	if err != nil {
		return ""
	}
	paneID := strings.TrimSpace(string(output))
	if idx := strings.Index(paneID, "\n"); idx > 0 {
		paneID = paneID[:idx]
	}
	return paneID
}

// CreateShell makes the detached session described by spec.
//
// An existing session of that name is treated as success rather than an error:
// the caller asked for a session by that name and there is one. That is what
// makes a retry after an interrupted create safe.
func CreateShell(spec ShellSpec) (ShellResult, error) {
	result := ShellResult{SessionName: spec.SessionName}
	if spec.SessionName == "" {
		return result, fmt.Errorf("shell session name is required")
	}
	if SessionExists(spec.SessionName) {
		result.PaneID = PaneID(spec.SessionName)
		return result, nil
	}

	args := []string{
		"new-session",
		"-d",
		"-s", spec.SessionName,
		"-c", spec.WorkDir,
	}
	if spec.Cols > 0 && spec.Rows > 0 {
		args = append(args, "-x", strconv.Itoa(spec.Cols), "-y", strconv.Itoa(spec.Rows))
	}
	if err := NewSessionWithIdentity(args, spec.SessionName, spec.DisplayName); err != nil {
		return result, fmt.Errorf("create shell session: %w", err)
	}
	result.PaneID = PaneID(spec.SessionName)
	return result, nil
}

// NewSessionWithIdentity creates the session carrying its identity
// environment, falling back to a plain create if this tmux rejects
// new-session -e (added in tmux 3.2). Losing the environment cue is a degraded
// shell; failing to create the session is a broken one, and the fallback still
// publishes the values to the session environment for any pane opened later.
//
// It is exported for the creation paths that build their own new-session
// arguments rather than going through CreateShell.
func NewSessionWithIdentity(args []string, sessionName, displayName string) error {
	withEnv := append(append([]string(nil), args...), ShellEnvArgs(sessionName, displayName)...)
	if err := tty.NewSession(withEnv...); err == nil {
		return nil
	}
	if err := tty.NewSession(args...); err != nil {
		return err
	}
	SetShellEnv(sessionName, displayName)
	return nil
}

// ShellEnvArgs is the new-session flag pair that publishes a shell's identity
// into its own environment, so a pane can name itself without asking Sidecar.
func ShellEnvArgs(sessionName, displayName string) []string {
	return []string{
		"-e", shellstate.SessionEnv + "=" + sessionName,
		"-e", shellstate.NameEnv + "=" + displayName,
	}
}

// SetShellEnv publishes the display name to the tmux session environment.
// Panes already running keep the value they were created with — the manifest
// and `sidecar shell rename` remain the authority, this is only the cue.
func SetShellEnv(sessionName, displayName string) {
	if sessionName == "" {
		return
	}
	_ = tty.SetSessionEnv(sessionName, shellstate.SessionEnv, sessionName)
	_ = tty.SetSessionEnv(sessionName, shellstate.NameEnv, displayName)
}
