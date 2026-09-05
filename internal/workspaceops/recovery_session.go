package workspaceops

import (
	"fmt"
	"path/filepath"
	"strings"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/tmuxserver"
)

// RecoverySessionsFile is deliberately separate from shells.json. Worktree
// sessions and terminal splits already have UI identities in workspace state;
// putting them in shells.json would manufacture duplicate shell rows.
const RecoverySessionsFile = "recovery-sessions.json"

func RecoverySessionsPath(stateDir string) string {
	return filepath.Join(stateDir, RecoverySessionsFile)
}

// RecordRecoverableSession gives a Sidecar-owned non-shell tmux session the
// same prior-live eligibility marker managed shells receive at creation.
func RecordRecoverableSession(session, workDir, displayName, agentType string) error {
	return recordRecoverableSession(session, workDir, displayName, agentType, false)
}

func recordRecoverableSession(session, workDir, displayName, agentType string, preserveIncident bool) error {
	session = strings.TrimSpace(session)
	if !strings.HasPrefix(session, WorktreeSessionPrefix) && !strings.HasPrefix(session, "sidecar-tp-") {
		return fmt.Errorf("record recovery session: %q is not a Sidecar worktree or terminal split", session)
	}
	if strings.TrimSpace(workDir) == "" {
		return fmt.Errorf("record recovery session %s: worktree path is required", session)
	}
	if strings.TrimSpace(displayName) == "" {
		displayName = filepath.Base(filepath.Clean(workDir))
	}
	path := RecoverySessionsPath(config.StateDir())
	namespace := tmuxenv.Namespace()
	id := shellstate.Identity{TmuxName: session, Namespace: namespace}
	defs, err := shellstate.ListAtPath(path)
	if err == nil {
		for _, def := range defs {
			if def.TmuxName == id.TmuxName && def.Namespace == id.Namespace {
				// A cold-restore executor has already persisted any inferred
				// candidate before recreating this tmux session. Its NoteLive
				// callback re-stamps the new server after the prefill decision;
				// observing here would clear that candidate before it can be read.
				if preserveIncident && def.Restore != nil && !def.Restore.ServerLostAt.IsZero() {
					return nil
				}
				if server := tmuxserver.Combine(tmuxserver.Socket(), ServerPID()).ServerID(); server != "" {
					_, err = shellstate.ObserveLiveAtPath(path, server, []shellstate.Identity{id}, time.Now().UTC())
				}
				return err
			}
		}
	}
	if err != nil {
		return err
	}
	now := time.Now().UTC()
	def := shellstate.Definition{
		TmuxName: session, DisplayName: displayName, Namespace: namespace,
		CreatedAt: now, AgentType: agentType, WorkDir: workDir,
	}
	if server := tmuxserver.Combine(tmuxserver.Socket(), ServerPID()).ServerID(); server != "" {
		def.Restore = &shellstate.RestoreState{Eligible: true, LastSeenServer: server, LastSeenAliveAt: now}
	}
	return shellstate.AddAtPath(path, def)
}

// ForgetRecoverableSession removes the supplemental identity after an explicit
// close. A missing record is already the requested state.
func ForgetRecoverableSession(session string) error {
	return shellstate.RemoveAtPath(RecoverySessionsPath(config.StateDir()), shellstate.Identity{
		TmuxName: strings.TrimSpace(session), Namespace: tmuxenv.Namespace(),
	})
}
