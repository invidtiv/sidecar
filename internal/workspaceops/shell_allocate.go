package workspaceops

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"github.com/marcus/sidecar/internal/tty"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
)

// ShellCreateError gives every surface a named, actionable creation failure.
type ShellCreateError struct {
	Code    string
	Message string
	Err     error
}

func (e *ShellCreateError) Error() string { return e.Code + ": " + e.Message }
func (e *ShellCreateError) Unwrap() error { return e.Err }

// ShellEditor retains a loaded projection's recovery policy and revision fence,
// while shellstate owns the cross-process transaction. Never reenter the store
// from the callback.
type ShellEditor interface {
	EditShells(func(*shellstate.Snapshot) (bool, error)) error
}

func (s Service) createAllocatedShell(spec ManagedShellSpec) (ShellResult, error) {
	var result ShellResult
	dir, err := projectdir.Resolve(spec.ProjectRoot)
	if err != nil {
		return result, shellCreateFailure("shell_state", "Resolve the owning project and retry", err)
	}
	edit := func(apply func(*shellstate.Snapshot) (bool, error)) error {
		_, _, err := shellstate.EditAtPath(filepath.Join(dir, "shells.json"), nil, false, apply)
		return err
	}
	if s.Shells != nil {
		editor, ok := s.Shells.(ShellEditor)
		if !ok {
			return result, shellCreateFailure("shell_state", "The shell adapter must support atomic allocation", nil)
		}
		edit = editor.EditShells
	}
	created := false
	err = edit(func(snapshot *shellstate.Snapshot) (bool, error) {
		// Keep tmux work below shellstate's five-second contention timeout.
		// The budget starts after acquiring the file lock, not while waiting for it.
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		inventory := append([]shellstate.Definition(nil), snapshot.Shells...)
		for _, tomb := range snapshot.Tombstones {
			inventory = append(inventory, tomb.Definition)
		}
		custom := strings.TrimSpace(spec.DisplayName)
		if custom != "" {
			normalized, err := shellstate.NormalizeName(custom)
			if err != nil {
				return false, shellCreateFailure("shell_name_invalid", "Choose a valid shell display name: "+err.Error(), err)
			}
			custom = normalized
			for _, def := range snapshot.Shells {
				if def.DisplayName == custom {
					return false, shellCreateFailure("shell_name_in_use", fmt.Sprintf("Shell name %q is already in use; choose another name", custom), nil)
				}
			}
		}
		for {
			display, session := ShellNames(spec.ProjectRoot, inventory)
			inventory = append(inventory, shellstate.Definition{TmuxName: session})
			if custom != "" {
				display = custom
			} else {
				used := false
				for _, def := range snapshot.Shells {
					if def.DisplayName == display {
						used = true
						break
					}
				}
				if used {
					continue
				}
			}
			// Unrecorded sessions and other projects with the same basename also
			// occupy this server's names. A fresh create must never adopt them.
			fresh := spec.ShellSpec
			fresh.SessionName, fresh.DisplayName, fresh.RequireNew = session, display, true
			var createErr error
			result, created, createErr = createFreshShell(ctx, fresh)
			if errors.Is(createErr, errShellSessionCollision) {
				continue
			}
			if createErr != nil {
				return false, shellCreateFailure("shell_create_failed", "Check tmux and the working directory, then retry: "+createErr.Error(), createErr)
			}
			now := time.Now().UTC()
			def := shellstate.Definition{TmuxName: session, DisplayName: display, Namespace: tmuxenv.Namespace(), CreatedAt: now, WorkDir: spec.WorkDir, AgentType: spec.AgentType, SkipPerms: spec.SkipPerms}
			if server := tmuxserver.Combine(tmuxserver.Socket(), serverPIDContext(ctx)).ServerID(); server != "" {
				def.Restore = &shellstate.RestoreState{Eligible: true, LastSeenServer: server, LastSeenAliveAt: now}
			}
			if err := ctx.Err(); err != nil {
				return false, shellCreateFailure("shell_create_failed", "Shell creation deadline exceeded; check tmux and retry", err)
			}
			snapshot.Shells = append(snapshot.Shells, def)
			result.DisplayName = display
			return true, nil
		}
	})
	if err != nil {
		if created || result.allocationToken != "" {
			cleanupAllocatedShell(result)
		}
		var named *ShellCreateError
		if errors.As(err, &named) {
			return result, err
		}
		return result, shellCreateFailure("shell_state", "Check the shell manifest and its directory permissions, then retry: "+err.Error(), err)
	}
	return result, nil
}

func shellCreateFailure(code, message string, err error) error {
	return &ShellCreateError{Code: code, Message: message, Err: err}
}

const allocationTokenEnv = "SIDECAR_CREATE_TOKEN"

var errShellSessionCollision = errors.New("shell session name is occupied")

// allocationCommand bounds pipe draining too: a tmux wrapper or child must not
// hold the cross-process file lock merely by retaining an output descriptor.
func allocationCommand(ctx context.Context, args ...string) *exec.Cmd {
	cmd := exec.CommandContext(ctx, "tmux", args...)
	if _, bounded := ctx.Deadline(); bounded {
		cmd.WaitDelay = 100 * time.Millisecond
	}
	return cmd
}

// createFreshShell is the bounded fresh-create path. Explicit reconnect and
// cold restore continue to use CreateShell, including their recovery policy.
// The bool records confirmed creation even if a later PID/pane probe times out.
func createFreshShell(ctx context.Context, spec ShellSpec) (ShellResult, bool, error) {
	result := ShellResult{SessionName: spec.SessionName}
	if err := allocationCommand(ctx, "has-session", "-t", "="+spec.SessionName).Run(); err == nil {
		return result, false, errShellSessionCollision
	}
	if err := ctx.Err(); err != nil {
		return result, false, err
	}
	env := managedShellEnvContext(ctx, spec.SessionName)
	args := []string{"new-session", "-d", "-s", spec.SessionName, "-c", spec.WorkDir}
	if spec.Cols > 0 && spec.Rows > 0 {
		args = append(args, "-x", strconv.Itoa(spec.Cols), "-y", strconv.Itoa(spec.Rows))
	}
	withEnv := append(append([]string(nil), args...), "-e", shellstate.SessionEnv+"="+spec.SessionName, "-e", shellstate.NameEnv+"="+spec.DisplayName)
	for k, v := range env {
		withEnv = append(withEnv, "-e", k+"="+v)
	}
	result.allocationToken = rand.Text()
	withEnv = append(withEnv, "-e", allocationTokenEnv+"="+result.allocationToken)
	err := tty.NewSessionContext(ctx, withEnv...)
	if err != nil && strings.Contains(err.Error(), "duplicate session") {
		return result, false, errShellSessionCollision
	}
	if err != nil && ctx.Err() == nil {
		// Retry with only the ownership cue if the full identity environment
		// was rejected. Supported tmux versions (3.4+) accept -e.
		err = tty.NewSessionContext(ctx, append(args, "-e", allocationTokenEnv+"="+result.allocationToken)...)
		if err == nil {
			env[shellstate.SessionEnv], env[shellstate.NameEnv] = spec.SessionName, spec.DisplayName
			for k, v := range env {
				_ = allocationCommand(ctx, "set-environment", "-t", spec.SessionName, k, v).Run()
			}
		}
	}
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		if strings.Contains(err.Error(), "duplicate session") {
			err = errShellSessionCollision
		}
		return result, false, err
	}
	out, _ := allocationCommand(ctx, "list-panes", "-t", "="+spec.SessionName, "-F", "#{pane_id}").Output()
	result.PaneID = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
	return result, true, ctx.Err()
}

// Only the allocation carrying our private ownership token may be rolled back.
func cleanupAllocatedShell(result ShellResult) {
	if result.allocationToken == "" {
		return
	}
	cleanupCtx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	output, err := allocationCommand(cleanupCtx, "show-environment", "-t", "="+result.SessionName, allocationTokenEnv).Output()
	owned := err == nil && strings.TrimSpace(string(output)) == allocationTokenEnv+"="+result.allocationToken
	if owned {
		_ = allocationCommand(cleanupCtx, "kill-session", "-t", "="+result.SessionName).Run()
	}
}
