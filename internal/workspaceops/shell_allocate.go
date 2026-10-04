package workspaceops

import (
	"errors"
	"fmt"
	"os/exec"
	"path/filepath"
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
			result, createErr = CreateShell(fresh)
			if errors.Is(createErr, errShellSessionCollision) {
				continue
			}
			if createErr != nil {
				return false, shellCreateFailure("shell_create_failed", "Check tmux and the working directory, then retry: "+createErr.Error(), createErr)
			}
			created = true
			now := time.Now().UTC()
			def := shellstate.Definition{TmuxName: session, DisplayName: display, Namespace: tmuxenv.Namespace(), CreatedAt: now, WorkDir: spec.WorkDir, AgentType: spec.AgentType, SkipPerms: spec.SkipPerms}
			if server := tmuxserver.Combine(tmuxserver.Socket(), ServerPID()).ServerID(); server != "" {
				def.Restore = &shellstate.RestoreState{Eligible: true, LastSeenServer: server, LastSeenAliveAt: now}
			}
			snapshot.Shells = append(snapshot.Shells, def)
			result.DisplayName = display
			return true, nil
		}
	})
	if err != nil {
		if created {
			_ = exec.Command("tmux", "kill-session", "-t", "="+result.SessionName).Run()
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

var errShellSessionCollision = errors.New("shell session name is occupied")
