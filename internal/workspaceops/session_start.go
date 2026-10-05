package workspaceops

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxserver"
)

var ErrSessionStartNotFound = errors.New("no recorded shell or worktree matches the target")

// SessionStartResult retains the selected identity when an inactive terminal is started.
type SessionStartResult struct {
	Session, DisplayName, WorkDir string
	AlreadyRunning                bool
}

// StartShell starts only an exact, existing managed record on the caller's server.
// It retains agent metadata and never resumes a previous conversation.
func (Service) StartShell(ctx context.Context, projectRoot, session, namespace string) (SessionStartResult, error) {
	dir, err := projectdir.Resolve(projectRoot)
	if err != nil {
		return SessionStartResult{}, err
	}
	path := filepath.Join(dir, "shells.json")
	var result SessionStartResult
	var allocated ShellResult
	_, _, err = shellstate.EditAtPath(path, nil, false, func(snapshot *shellstate.Snapshot) (bool, error) {
		// Creation and ownership checks share the manifest lock with deletion.
		// Bound tmux work below the store's five-second contention timeout.
		startCtx, cancel := context.WithTimeout(ctx, 2*time.Second)
		defer cancel()
		for i, def := range snapshot.Shells {
			if def.TmuxName != session || (def.Namespace != "" && canonicalSocket(def.Namespace) != canonicalSocket(namespace)) {
				continue
			}
			workDir := def.WorkDir
			if workDir == "" {
				workDir = projectRoot
			}
			result = SessionStartResult{Session: session, DisplayName: def.DisplayName, WorkDir: workDir}
			if allocationCommand(startCtx, "has-session", "-t", "="+session).Run() == nil {
				if err := sessionStartsAt(startCtx, session, workDir); err != nil {
					return false, err
				}
				result.AlreadyRunning = true
				return false, nil
			}
			if err := startDirectoryExists(workDir); err != nil {
				return false, err
			}
			var err error
			allocated, _, err = createFreshShell(startCtx, ShellSpec{SessionName: session, DisplayName: def.DisplayName, WorkDir: workDir})
			if err != nil {
				return false, err
			}
			if server := tmuxserver.Combine(tmuxserver.Socket(), serverPIDContext(startCtx)).ServerID(); server != "" {
				freshRestore := &shellstate.RestoreState{Eligible: true, LastSeenServer: server, LastSeenAliveAt: time.Now().UTC()}
				if def.Restore != nil {
					freshRestore.Policy = def.Restore.Policy
				}
				def.Restore = freshRestore
				if def.Agent != nil && def.Agent.Candidate != nil {
					agent := *def.Agent
					agent.Candidate = nil
					def.Agent = &agent
				}
			}
			if err := startCtx.Err(); err != nil {
				return false, err
			}
			snapshot.Shells[i] = def
			return true, nil
		}
		return false, ErrSessionStartNotFound
	})
	if err != nil {
		cleanupAllocatedShell(allocated)
	}
	return result, err
}

// StartWorktree starts a session in an existing linked checkout, without creating
// a branch or running its setup pipeline again. The path is the catalog identity.
func (s Service) StartWorktree(ctx context.Context, stateDir, projectRoot, target string) (SessionStartResult, error) {
	projectRoot = CanonicalWorktreePath(projectRoot)
	if !filepath.IsAbs(target) {
		return SessionStartResult{}, fmt.Errorf("worktree start requires an absolute worktree path")
	}
	states, err := ListWorktreeStates(ctx, projectRoot)
	if err != nil {
		return SessionStartResult{}, err
	}
	path := CanonicalWorktreePath(target)
	main := CanonicalWorktreePath(MainWorktreePath(ctx, projectRoot))
	for _, state := range states {
		if state.Path != path {
			continue
		}
		if path == main || state.Bare || state.Prunable {
			return SessionStartResult{}, fmt.Errorf("only an existing linked worktree can start a session")
		}
		if err := startDirectoryExists(path); err != nil {
			return SessionStartResult{}, err
		}
		if _, err := projectdir.WorktreeDirWithBase(stateDir, projectRoot, path); err != nil {
			return SessionStartResult{}, err
		}
		name, err := LookupWorktreeDisplayName(stateDir, projectRoot, path)
		if err != nil {
			return SessionStartResult{}, err
		}
		result := SessionStartResult{Session: WorktreeSessionName(path, name), DisplayName: name, WorkDir: path}
		for _, session := range WorktreeSessionNames(path, name) {
			if !SessionExists(session) {
				continue
			}
			// Equal basenames across repositories can collide. Never attach the
			// selected checkout to a session belonging to another directory.
			if err := sessionStartsAt(ctx, session, path); err != nil {
				return result, err
			}
			result.Session, result.AlreadyRunning = session, true
			return result, nil
		}
		_, err = s.LaunchWorktree(ctx, AgentLaunchSpec{SessionName: result.Session, WorkDir: path, DisplayName: name, Env: BuildEnvOverrides(main), RequireNew: true})
		return result, err
	}
	return SessionStartResult{}, ErrSessionStartNotFound
}

func canonicalSocket(path string) string {
	return filepath.Join(CanonicalWorktreePath(filepath.Dir(path)), filepath.Base(path))
}

func startDirectoryExists(path string) error {
	info, err := os.Stat(path)
	if err != nil {
		return fmt.Errorf("terminal working directory is unavailable: %w", err)
	}
	if !info.IsDir() {
		return fmt.Errorf("terminal working directory is not a directory")
	}
	return nil
}

// session_path remains the session's original directory when its shell changes cwd.
func sessionStartsAt(ctx context.Context, session, path string) error {
	out, err := allocationCommand(ctx, "display-message", "-p", "-t", "="+session+":", "#{session_path}").Output()
	if err != nil || CanonicalWorktreePath(strings.TrimSpace(string(out))) != CanonicalWorktreePath(path) {
		return fmt.Errorf("session %q belongs to another directory or cannot be verified", session)
	}
	return nil
}
