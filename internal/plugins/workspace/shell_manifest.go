package workspace

import (
	"strings"
	"sync"
	"time"

	"log/slog"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// ShellManifest stores persistent shell definitions for cross-instance sync
// and reboot survival. Stored in $XDG_STATE_HOME/sidecar/projects/<slug>/shells.json.
type ShellManifest struct {
	// Version is the schema version of the file this handle last read. Writes
	// check it (shellstate.CheckWritableVersion) and refuse a manifest from a
	// newer Sidecar rather than marshalling this narrower struct over it.
	Version int               `json:"version"`
	Shells  []ShellDefinition `json:"shells"`
	// Tombstones holds forgotten definitions so restore can put them back,
	// for as long as shellstate.TombstoneRetention says. An older binary —
	// one from before the version field was read — ignores this field and
	// drops the key on write; see shellstate.Tombstone for why that direction
	// is the one that cannot be defended.
	Tombstones []shellstate.Tombstone `json:"tombstones,omitempty"`

	path     string     // not serialized - file path
	mu       sync.Mutex // protects concurrent access
	revision uint64     // bumped on every successful local write
}

// Snapshot returns a coherent projection for readers outside the manifest
// mutex. The slices must be copied: background service writes and rename can
// replace or edit them while the TUI is rebuilding its sidebar. Manifest edits
// replace nested Agent/Restore values rather than modifying them in place.
func (m *ShellManifest) Snapshot() shellstate.Snapshot {
	if m == nil {
		return shellstate.Snapshot{}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return shellstate.Snapshot{
		Version:    m.Version,
		Shells:     append([]ShellDefinition(nil), m.Shells...),
		Tombstones: append([]shellstate.Tombstone(nil), m.Tombstones...),
	}
}

// Revision counts the successful writes this process has made through this
// manifest object. A reconciliation that started before a local delete and
// lands after it would resurrect the deleted shell, so callers stamp the
// revision they observed and discard (or re-run) a sync whose base moved
// underneath them (td-8d18de).
func (m *ShellManifest) Revision() uint64 {
	if m == nil {
		return 0
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.revision
}

// NoteExternalMutation fences snapshots taken before a successful mutation
// through workspaceops. Its durable write may already be a no-op when the UI
// reconciles it, but that does not make an older in-flight snapshot current.
func (m *ShellManifest) NoteExternalMutation() {
	if m == nil {
		return
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.revision++
}

// ShellDefinition is retained as the workspace-facing name for the shared
// persisted model. New non-interactive surfaces use shellstate.Definition
// directly rather than defining another manifest shape.
type ShellDefinition = shellstate.Definition

// manifestVersion is the schema version this build writes. The number lives in
// shellstate, next to the guard that reads it, so the two surfaces cannot
// disagree about what "current" means.
const manifestVersion = shellstate.CurrentVersion

// errNoManifestPath is returned by every manifest operation whose handle was
// built without a file to act on. It is a refusal rather than a fallback: there
// is no sensible default location for a project's shells.json, and inventing a
// relative one silently writes state next to whatever directory the process
// happens to be in.
var errNoManifestPath = shellstate.ErrNoManifestPath

// LoadShellManifest loads the shell manifest from disk.
// Returns an empty manifest (not error) if file doesn't exist or is corrupted.
func LoadShellManifest(path string) (*ShellManifest, error) {
	if strings.TrimSpace(path) == "" {
		return nil, errNoManifestPath
	}
	// A process that claims isolated state must not even observe the real
	// user's manifest: reading it is how an isolated instance would come to
	// believe those shells are its own (td-8d18de).
	if err := config.AssertIsolatedPath(path); err != nil {
		return nil, err
	}

	m := &ShellManifest{
		Version: manifestVersion,
		Shells:  []ShellDefinition{},
		path:    path,
	}

	snapshot, err := shellstate.SnapshotAtPath(path)
	if err != nil {
		slog.Warn("manifest: read failed, returning empty", "err", err)
		return m, nil
	}
	m.Version, m.Shells, m.Tombstones = snapshot.Version, snapshot.Shells, snapshot.Tombstones

	return m, nil
}

// mutateLocked applies a single-entry edit against the manifest as it exists on
// disk *right now*, not against this process's possibly-stale snapshot.
//
// Every writer used to marshal its in-memory copy over the whole file. Between
// loading that copy and writing it, a sibling instance can have renamed a shell
// or recorded a new one; the blind rewrite silently reverted it (td-8d18de).
// Re-reading inside the exclusive lock makes each edit a merge: we change the
// one entry we mean to change and preserve everything else the file has gained.
//
// apply receives the fresh definitions and returns the new list plus whether
// anything actually changed. Caller must hold m.mu.
func (m *ShellManifest) mutateLocked(apply func([]ShellDefinition) ([]ShellDefinition, bool)) error {
	return m.mutateLockedKind(false, apply)
}

// mutateLockedKind projects shellstate's authoritative snapshot back into this
// compatibility handle. All locking, version guards and atomic disk writes live
// in shellstate. The fallback preserves the plugin's recovery behavior when its
// previously loaded file is missing or unreadable. Caller must hold m.mu.
func (m *ShellManifest) mutateLockedKind(identityRemoval bool, apply func([]ShellDefinition) ([]ShellDefinition, bool)) error {
	fallback := shellstate.Snapshot{Version: manifestVersion, Shells: m.Shells, Tombstones: m.Tombstones}
	snapshot, changed, err := shellstate.EditAtPath(m.path, &fallback, identityRemoval, func(fresh *shellstate.Snapshot) (bool, error) {
		m.Tombstones = fresh.Tombstones
		next, changed := apply(fresh.Shells)
		fresh.Shells, fresh.Tombstones = next, m.Tombstones
		return changed, nil
	})
	if err != nil {
		return err
	}
	m.Version, m.Shells, m.Tombstones = snapshot.Version, snapshot.Shells, snapshot.Tombstones
	if changed {
		m.revision++
	}
	return nil
}

// AddShell adds a shell definition and saves.
func (m *ShellManifest) AddShell(def ShellDefinition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateLocked(func(shells []ShellDefinition) ([]ShellDefinition, bool) {
		m.Tombstones = dropWorkspaceTombstone(m.Tombstones, def.TmuxName)
		for i, s := range shells {
			if s.TmuxName == def.TmuxName {
				// Carry the schema fields the workspace projection does not model. A
				// wholesale replacement here would drop the v3 session binding.
				shells[i] = shellstate.CarryForward(s, def)
				return shells, true
			}
		}
		return append(shells, def), true
	})
}

// EnsureShells adds any definitions the manifest is missing and saves once.
// Existing entries are left untouched. Returns true when the file changed.
//
// This is the additive counterpart to AddShell: it heals a manifest another
// instance narrowed (td-8d18de) without ever overwriting what that instance
// wrote. A name currently in tombstones is an explicit forget, not a missing
// definition — it is not added back, and the tombstone is not dropped.
func (m *ShellManifest) EnsureShells(defs []ShellDefinition) (bool, error) {
	if len(defs) == 0 {
		return false, nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()

	changed := false
	err := m.mutateLocked(func(shells []ShellDefinition) ([]ShellDefinition, bool) {
		present := make(map[string]bool, len(shells))
		for _, s := range shells {
			present[s.TmuxName] = true
		}
		forgotten := tombstoneTmuxNames(m.Tombstones)
		for _, def := range defs {
			if present[def.TmuxName] || forgotten[def.TmuxName] {
				continue
			}
			shells = append(shells, def)
			present[def.TmuxName] = true
			changed = true
		}
		return shells, changed
	})
	return changed, err
}

// MarkRestoreEligible records that one shell is running under this tmux server.
//
// It writes only on a transition — a record already marked eligible under the
// same server is left completely alone, timestamp included — so the marker costs
// one write per shell per tmux-server lifetime rather than one per observation.
func (m *ShellManifest) MarkRestoreEligible(tmuxName, serverID string, now time.Time) error {
	if tmuxName == "" || serverID == "" {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateLocked(func(shells []ShellDefinition) ([]ShellDefinition, bool) {
		for i, s := range shells {
			if s.TmuxName != tmuxName {
				continue
			}
			if s.Restore != nil && s.Restore.Eligible && s.Restore.LastSeenServer == serverID {
				return shells, false
			}
			next := shellstate.RestoreState{}
			if s.Restore != nil {
				next = *s.Restore
			}
			next.Eligible = true
			next.LastSeenServer = serverID
			next.LastSeenAliveAt = now
			next.ServerLostAt = time.Time{}
			next.LastAgentActivity = ""
			if s.Agent != nil && s.Agent.Candidate != nil {
				agent := *s.Agent
				agent.Candidate = nil
				s.Agent = &agent
			}
			s.Restore = &next
			shells[i] = s
			return shells, true
		}
		return shells, false
	})
}

// ReapShell retires a shell whose tmux session is gone, and declines to delete
// the record when it was the tmux server that went away rather than the shell.
//
// This surface reaps per shell, driven by one capture failing, and it has no
// equivalent of the global browser's empty-listing guard — there is no listing
// here to be empty. That makes it the path most exposed to the failure this work
// exists to fix: when a server dies, every shell's capture fails at once, and a
// per-shell "this one is dead" conclusion, applied N times, empties the file.
//
// The rule is the same one shellstate.ForgetOrPreserveAtPath applies for the
// other surface, and runs through the shared shellstate writer. A record
// whose last-confirmed server is not the one running now, or that is being judged with no server
// running at all, is preserved and marked as a cold-restore candidate. Only a
// shell that vanished inside a server that is still up is tombstoned, because
// that is a terminal someone closed.
func (m *ShellManifest) ReapShell(tmuxName string, server shellstate.ServerState, activityEvidence ...string) (shellstate.ReapOutcome, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	outcome := shellstate.ReapAbsent
	err := m.mutateLockedKind(true, func(shells []ShellDefinition) ([]ShellDefinition, bool) {
		for i, s := range shells {
			if s.TmuxName != tmuxName {
				continue
			}
			lastSeen := ""
			if s.Restore != nil {
				lastSeen = s.Restore.LastSeenServer
			}
			// The same decision table shellstate.ForgetOrPreserveAtPath applies,
			// retained here for the plugin compatibility projection. Tombstoning
			// needs positive evidence the server is alive and this shell is gone from it; marking eligible needs positive
			// evidence the server died or was replaced; anything else defers.
			switch {
			case !server.Known(), lastSeen == "" && server.Running():
				outcome = shellstate.ReapDeferred
				return shells, false
			case !server.Running(), lastSeen != server.ID():
				outcome = shellstate.ReapPreserved
				evidence := ""
				if len(activityEvidence) > 0 {
					evidence = activityEvidence[0]
				}
				next, changed := shellstate.RecordServerLoss(s, time.Now().UTC(), evidence)
				shells[i] = next
				return shells, changed
			}
			outcome = shellstate.ReapTombstoned
			m.Tombstones = appendWorkspaceTombstone(m.Tombstones, s)
			return append(shells[:i], shells[i+1:]...), true
		}
		return shells, false
	})
	return outcome, err
}

// RemoveShell moves a shell by tmuxName into tombstones and saves.
func (m *ShellManifest) RemoveShell(tmuxName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateLockedKind(true, func(shells []ShellDefinition) ([]ShellDefinition, bool) {
		for i, s := range shells {
			if s.TmuxName == tmuxName {
				m.Tombstones = appendWorkspaceTombstone(m.Tombstones, s)
				return append(shells[:i], shells[i+1:]...), true
			}
		}
		return shells, false // Not found, nothing to remove
	})
}

func appendWorkspaceTombstone(tombs []shellstate.Tombstone, def ShellDefinition) []shellstate.Tombstone {
	stone := shellstate.Tombstone{Definition: def, DeletedAt: time.Now().UTC()}
	for i := range tombs {
		if tombs[i].TmuxName == def.TmuxName {
			tombs[i] = stone
			return tombs
		}
	}
	return append(tombs, stone)
}

func dropWorkspaceTombstone(tombs []shellstate.Tombstone, tmuxName string) []shellstate.Tombstone {
	for i := range tombs {
		if tombs[i].TmuxName == tmuxName {
			return append(tombs[:i], tombs[i+1:]...)
		}
	}
	return tombs
}

// tombstoneTmuxNames is the "which names are forgotten" question, asked by the
// startup reconcile, the merge, and EnsureShells. Expired records are dropped
// here rather than only at the writer boundary, so a name whose retention
// window has passed is adoptable again from the next read, not from the next
// write.
func tombstoneTmuxNames(tombs []shellstate.Tombstone) map[string]bool {
	tombs = shellstate.ExpireTombstones(tombs, time.Now().UTC(), shellstate.TombstoneRetention())
	out := make(map[string]bool, len(tombs))
	for _, stone := range tombs {
		if stone.TmuxName != "" {
			out[stone.TmuxName] = true
		}
	}
	return out
}

// FindShell returns a shell definition by tmuxName, or nil if not found.
func (m *ShellManifest) FindShell(tmuxName string) *ShellDefinition {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Shells {
		if m.Shells[i].TmuxName == tmuxName {
			return &m.Shells[i]
		}
	}
	return nil
}

// UpdateShell updates an existing shell definition and saves.
func (m *ShellManifest) UpdateShell(def ShellDefinition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateLocked(func(shells []ShellDefinition) ([]ShellDefinition, bool) {
		m.Tombstones = dropWorkspaceTombstone(m.Tombstones, def.TmuxName)
		for i, s := range shells {
			if s.TmuxName == def.TmuxName {
				// Same rule as AddShell, and the one that matters most: this is
				// the path a revived shell takes, so replacing wholesale here
				// destroyed the binding at the cold-restore moment.
				shells[i] = shellstate.CarryForward(s, def)
				return shells, true
			}
		}
		// Not found - add it
		return append(shells, def), true
	})
}

// RenameShell routes display-name validation and persistence through the same
// application boundary as the agent-facing CLI.
func (m *ShellManifest) RenameShell(tmuxName, namespace, name string) (shellstate.RenameResult, error) {
	result, err := (workspaceops.Service{}).RenameShell(m.path, shellstate.RenameRequest{
		TmuxName: tmuxName, Namespace: namespace, Name: name,
	})
	if err != nil {
		return shellstate.RenameResult{}, err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range m.Shells {
		if m.Shells[i].TmuxName == tmuxName && m.Shells[i].Namespace == namespace {
			m.Shells[i].DisplayName = result.Name
			break
		}
	}
	if result.Changed {
		m.revision++
	}
	return result, nil
}

// Path returns the manifest file path.
func (m *ShellManifest) Path() string {
	return m.path
}

// shellToDefinition converts a ShellSession to a ShellDefinition for storage.
func shellToDefinition(shell *ShellSession) ShellDefinition {
	agentType := ""
	if shell.ChosenAgent != AgentNone {
		agentType = string(shell.ChosenAgent)
	}
	return ShellDefinition{
		TmuxName:    shell.TmuxName,
		DisplayName: shell.Name,
		Namespace:   tmuxenv.Namespace(),
		CreatedAt:   shell.CreatedAt,
		AgentType:   agentType,
		SkipPerms:   shell.SkipPerms,
		WorkDir:     shell.WorkDir,
	}
}

// BackfillWorkDirs writes inferred parent worktree paths onto definitions that
// still lack WorkDir. Existing non-empty values are left untouched.
func (m *ShellManifest) BackfillWorkDirs(byTmux map[string]string) error {
	if m == nil || len(byTmux) == 0 {
		return nil
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateLocked(func(shells []ShellDefinition) ([]ShellDefinition, bool) {
		changed := false
		for i, s := range shells {
			if strings.TrimSpace(s.WorkDir) != "" {
				continue
			}
			dir := strings.TrimSpace(byTmux[s.TmuxName])
			if dir == "" {
				continue
			}
			shells[i].WorkDir = dir
			changed = true
		}
		return shells, changed
	})
}

// definitionToAgentType converts a string agent type to AgentType.
func definitionToAgentType(s string) AgentType {
	if s == "" {
		return AgentNone
	}
	return AgentType(s)
}
