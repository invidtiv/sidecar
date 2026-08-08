package workspace

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"log/slog"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

// ShellManifest stores persistent shell definitions for cross-instance sync
// and reboot survival. Stored in ~/.config/sidecar/projects/<slug>/shells.json.
type ShellManifest struct {
	Version int               `json:"version"`
	Shells  []ShellDefinition `json:"shells"`

	path string     // not serialized - file path
	mu   sync.Mutex // protects concurrent access
}

// ShellDefinition contains all info needed to recreate a shell session.
type ShellDefinition struct {
	TmuxName    string `json:"tmuxName"`
	DisplayName string `json:"displayName"`
	// Namespace identifies the tmux server that owns this session
	// (tmuxenv.Namespace). Empty means a pre-td-8d18de entry of unknown
	// origin: it is never pruned, because this instance cannot prove the
	// session was ever visible to it, and it is stamped the first time the
	// entry is seen live locally. Legacy entries whose session is really gone
	// therefore surface as orphans rather than vanishing, and clear on an
	// explicit kill.
	Namespace string    `json:"namespace,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	AgentType string    `json:"agentType,omitempty"`
	SkipPerms bool      `json:"skipPerms,omitempty"`
}

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

// saveLocked writes the manifest to disk. Caller must hold m.mu.
func (m *ShellManifest) saveLocked() error {
	// The hard floor for td-8d18de. Every writer (Save, AddShell, EnsureShells,
	// RemoveShell, UpdateShell) funnels through here, and the check runs before
	// MkdirAll and before the lock file is created so an isolated run leaves no
	// .tmp or .lock debris in the real user's tree either.
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

	// Ensure version is set
	m.Version = manifestVersion

	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return err
	}

	// Atomic write: temp file + rename
	tmpPath := m.path + ".tmp"
	if err := os.WriteFile(tmpPath, data, 0644); err != nil {
		return err
	}

	return os.Rename(tmpPath, m.path)
}

// AddShell adds a shell definition and saves.
func (m *ShellManifest) AddShell(def ShellDefinition) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	// Check for duplicate
	for i, s := range m.Shells {
		if s.TmuxName == def.TmuxName {
			// Update existing
			m.Shells[i] = def
			return m.saveLocked()
		}
	}
	m.Shells = append(m.Shells, def)
	return m.saveLocked()
}

// EnsureShells adds any definitions the manifest is missing and saves once.
// Existing entries are left untouched. Returns true when the file changed.
//
// This is the additive counterpart to AddShell: it heals a manifest another
// instance narrowed (td-8d18de) without ever overwriting what that instance
// wrote.
func (m *ShellManifest) EnsureShells(defs []ShellDefinition) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()

	present := make(map[string]bool, len(m.Shells))
	for _, s := range m.Shells {
		present[s.TmuxName] = true
	}

	changed := false
	for _, def := range defs {
		if present[def.TmuxName] {
			continue
		}
		m.Shells = append(m.Shells, def)
		present[def.TmuxName] = true
		changed = true
	}
	if !changed {
		return false, nil
	}
	return true, m.saveLocked()
}

// RemoveShell removes a shell by tmuxName and saves.
func (m *ShellManifest) RemoveShell(tmuxName string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, s := range m.Shells {
		if s.TmuxName == tmuxName {
			m.Shells = append(m.Shells[:i], m.Shells[i+1:]...)
			return m.saveLocked()
		}
	}
	return nil // Not found, nothing to remove
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
	for i, s := range m.Shells {
		if s.TmuxName == def.TmuxName {
			m.Shells[i] = def
			return m.saveLocked()
		}
	}
	// Not found - add it
	m.Shells = append(m.Shells, def)
	return m.saveLocked()
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
	}
}

// definitionToAgentType converts a string agent type to AgentType.
func definitionToAgentType(s string) AgentType {
	if s == "" {
		return AgentNone
	}
	return AgentType(s)
}
