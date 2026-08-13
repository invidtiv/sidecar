package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"log/slog"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

// ShellManifest stores persistent shell definitions for cross-instance sync
// and reboot survival. Stored in $XDG_STATE_HOME/sidecar/projects/<slug>/shells.json.
type ShellManifest struct {
	Version int               `json:"version"`
	Shells  []ShellDefinition `json:"shells"`

	path     string     // not serialized - file path
	mu       sync.Mutex // protects concurrent access
	revision uint64     // bumped on every successful local write
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

// ShellDefinition is retained as the workspace-facing name for the shared
// persisted model. New non-interactive surfaces use shellstate.Definition
// directly rather than defining another manifest shape.
type ShellDefinition = shellstate.Definition

// manifestVersion is the current manifest format version.
const manifestVersion = 1

// LoadShellManifest loads the shell manifest from disk.
// Returns an empty manifest (not error) if file doesn't exist or is corrupted.
func LoadShellManifest(path string) (*ShellManifest, error) {
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

	// Acquire shared lock for reading
	lockFile, err := acquireManifestLock(path, false)
	if err != nil {
		slog.Debug("manifest: lock failed, returning empty", "err", err)
		return m, nil
	}
	defer releaseManifestLock(lockFile)

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return m, nil // Empty manifest is fine
		}
		slog.Warn("manifest: read failed", "err", err)
		return m, nil
	}

	if err := json.Unmarshal(data, m); err != nil {
		slog.Warn("manifest: parse failed, returning empty", "err", err)
		m.Shells = []ShellDefinition{}
	}
	m.path = path

	return m, nil
}

// Save writes the manifest to disk atomically with file locking.
func (m *ShellManifest) Save() error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.saveLocked()
}

// saveLocked replaces the whole file with this process's copy. Caller must hold
// m.mu. Reserved for the startup reconciliation, which has just computed the
// authoritative list; every single-entry edit goes through mutateLocked so it
// merges instead of clobbering.
func (m *ShellManifest) saveLocked() error {
	// The hard floor for td-8d18de. Every writer funnels through here or
	// mutateLocked, and the check runs before MkdirAll and before the lock file
	// is created so an isolated run leaves no .tmp or .lock debris in the real
	// user's tree either.
	if err := config.AssertIsolatedPath(m.path); err != nil {
		return err
	}

	// Ensure .sidecar directory exists
	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	// Acquire exclusive lock
	lockFile, err := acquireManifestLock(m.path, true)
	if err != nil {
		return err
	}
	defer releaseManifestLock(lockFile)

	return m.writeLocked()
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
	if err := config.AssertIsolatedPath(m.path); err != nil {
		return err
	}

	dir := filepath.Dir(m.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	lockFile, err := acquireManifestLock(m.path, true)
	if err != nil {
		return err
	}
	defer releaseManifestLock(lockFile)

	fresh := m.readFromDiskLocked()
	next, changed := apply(fresh)
	m.Shells = next
	if !changed {
		return nil
	}
	return m.writeLocked()
}

// readFromDiskLocked returns the definitions currently on disk, falling back to
// the in-memory copy when the file is missing or unreadable. Caller must hold
// both m.mu and the exclusive file lock.
func (m *ShellManifest) readFromDiskLocked() []ShellDefinition {
	data, err := os.ReadFile(m.path)
	if err != nil {
		if !os.IsNotExist(err) {
			slog.Warn("manifest: read-before-write failed, using in-memory copy", "err", err)
		}
		return m.Shells
	}
	var onDisk ShellManifest
	if err := json.Unmarshal(data, &onDisk); err != nil {
		slog.Warn("manifest: read-before-write parse failed, using in-memory copy", "err", err)
		return m.Shells
	}
	return onDisk.Shells
}

// writeLocked marshals and atomically replaces the file. Caller must hold m.mu,
// the exclusive file lock, and must already have asserted path isolation.
func (m *ShellManifest) writeLocked() error {
	m.Version = manifestVersion

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	tmpPath := m.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, m.path); err != nil {
		return err
	}
	m.revision++
	return nil
}

// AddShell adds a shell definition and saves.
func (m *ShellManifest) AddShell(def ShellDefinition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateLocked(func(shells []ShellDefinition) ([]ShellDefinition, bool) {
		for i, s := range shells {
			if s.TmuxName == def.TmuxName {
				shells[i] = def
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
// wrote.
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
		for _, def := range defs {
			if present[def.TmuxName] {
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

// RemoveShell removes a shell by tmuxName and saves.
func (m *ShellManifest) RemoveShell(tmuxName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.mutateLocked(func(shells []ShellDefinition) ([]ShellDefinition, bool) {
		for i, s := range shells {
			if s.TmuxName == tmuxName {
				return append(shells[:i], shells[i+1:]...), true
			}
		}
		return shells, false // Not found, nothing to remove
	})
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
		for i, s := range shells {
			if s.TmuxName == def.TmuxName {
				shells[i] = def
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
	result, err := shellstate.RenameAtPath(m.path, shellstate.RenameRequest{
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

// lockTimeout is the maximum time to wait for file lock acquisition (td-984ead)
const lockTimeout = 5 * time.Second

// lockRetryInterval is how often to retry lock acquisition
const lockRetryInterval = 10 * time.Millisecond

// acquireManifestLock acquires an advisory lock on the manifest file with timeout.
// exclusive=true for writes, false for reads.
func acquireManifestLock(path string, exclusive bool) (*os.File, error) {
	lockPath := path + ".lock"

	// Ensure directory exists for lock file
	dir := filepath.Dir(lockPath)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return nil, err
	}

	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}

	lockType := syscall.LOCK_SH | syscall.LOCK_NB
	if exclusive {
		lockType = syscall.LOCK_EX | syscall.LOCK_NB
	}

	// Try non-blocking lock with timeout (td-984ead)
	deadline := time.Now().Add(lockTimeout)
	for {
		err := syscall.Flock(int(lockFile.Fd()), lockType)
		if err == nil {
			return lockFile, nil
		}
		// EWOULDBLOCK means lock is held by another process
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = lockFile.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = lockFile.Close()
			return nil, fmt.Errorf("lock acquisition timeout after %v", lockTimeout)
		}
		time.Sleep(lockRetryInterval)
	}
}

// releaseManifestLock releases the advisory lock.
func releaseManifestLock(lockFile *os.File) {
	if lockFile == nil {
		return
	}
	_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	_ = lockFile.Close()
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
