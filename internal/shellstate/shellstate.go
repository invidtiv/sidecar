// Package shellstate owns validation and persistence of Sidecar shell display names.
// It is deliberately independent of both the TUI and CLI transports.
package shellstate

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/config"
)

const MaxNameBytes = 50

// NameEnv carries the current display name into the shell's environment, and
// SessionEnv the tmux session that owns it. An agent that can read its own
// shell name can tell a default ("Shell 9") from a real one, which is what
// makes the rename instruction actionable rather than a rule to remember.
const (
	NameEnv    = "SIDECAR_SHELL_NAME"
	SessionEnv = "SIDECAR_SHELL"
)

// The managed-shell environment contract.
//
// These are published alongside NameEnv and SessionEnv when Sidecar creates a
// shell, and they are what lets a provider hook running inside that shell know
// it may report lifecycle state, and for which pane. A hook that finds
// ManagedEnv unset exits successfully and silently: outside a Sidecar shell
// there is nothing to report to and nothing to complain about.
//
// Two rules shape this list. Nothing here is a writable path — the report
// command resolves Sidecar's state directory itself, so provider input can
// never redirect where lifecycle records are written. And nothing here is
// trusted on its own: the report command re-derives the live pane and server
// from tmux and refuses a report whose claimed context does not match.
const (
	// ManagedEnv is the boolean cue, set to "1". It is the single check a hook
	// makes before doing anything at all.
	ManagedEnv = "SIDECAR_MANAGED_SHELL"

	// ServerEnv carries the tmux server's PID, which is how lifecycle records
	// are namespaced by server incarnation.
	//
	// The PID rather than tmuxserver.Incarnation.String() is deliberate, and
	// the difference is not cosmetic: that string embeds the socket ctime,
	// which tmux bumps every time the attached-client set changes. Namespacing
	// stored records by it would silently orphan every report the moment a user
	// attached or detached a client, returning a healthy pane to screen
	// fallback for no visible reason. The PID is stable for the server's
	// lifetime and new after a restart, which is exactly the property the
	// namespace needs, and it is what agentcontrol already compares.
	ServerEnv = "SIDECAR_TMUX_SERVER"

	// HostEnv carries the host that owns the pane. Reports never cross hosts; a
	// registered remote resolves its own state locally.
	HostEnv = "SIDECAR_HOST"

	// BinEnv carries the absolute path of the Sidecar binary that created the
	// shell, for provider hook formats that need a command to invoke. It exists
	// so an asset never has to guess a path or search PATH, and so nothing has
	// to shell-compose one.
	BinEnv = "SIDECAR_BIN"

	// NamespaceEnv carries the tmux socket path that identifies this host-local
	// namespace. It is an identifier for diagnostics and matching, never a
	// location anything is written to.
	NamespaceEnv = "SIDECAR_NAMESPACE"
)

// NamingInstruction is the canonical guidance for keeping a shell's display
// name useful. It lives here, next to the rules it describes, so every
// delivery channel — AGENTS.md, a harness system-prompt append, help text —
// quotes one source instead of drifting copies.
//
// The trigger is "name does not describe current work," not only the generated
// "Shell N" default — a leftover previous-task name is equally stale.
const NamingInstruction = "This terminal is a Sidecar project shell. Learn its display name from $" + NameEnv +
	" as a cue (it may be empty on older shells, or lag after a rename in this process), or run " +
	"`sidecar shell name` for the authoritative name from Sidecar's manifest. \"Shell 3\" is the unset default; " +
	"a previous task's name is equally stale. Keep the name aligned with your current task: at the start of work " +
	"and whenever context changes materially, if the name does not describe what you are doing now, run " +
	"`sidecar shell rename \"short context\"`. Update at meaningful boundaries, not every sub-step. Prefer the " +
	"outcome (\"shell rename implementation\") over the model or a transient action (\"Codex running tests\"). " +
	"These commands act only on the current Sidecar shell; never edit shells.json or rename tmux sessions directly."

type ErrorKind string

const (
	KindValidation ErrorKind = "validation"
	KindNotFound   ErrorKind = "not_found"
	KindAlready    ErrorKind = "already"
	KindAmbiguous  ErrorKind = "ambiguous"
	KindState      ErrorKind = "state"
)

type Error struct {
	Kind ErrorKind
	Msg  string
	Err  error
}

func (e *Error) Error() string {
	if e.Err != nil {
		return e.Msg + ": " + e.Err.Error()
	}
	return e.Msg
}
func (e *Error) Unwrap() error { return e.Err }

func IsValidation(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Kind == KindValidation
}

func IsNotFound(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Kind == KindNotFound
}

func IsAlready(err error) bool {
	var target *Error
	return errors.As(err, &target) && target.Kind == KindAlready
}

type Identity struct {
	TmuxName  string
	Namespace string
}

type LookupResult struct {
	Shell string `json:"shell"`
	Name  string `json:"name"`
}

type OriginInfo struct {
	TmuxName    string `json:"tmuxName"`
	Namespace   string `json:"namespace"`
	ProjectKey  string `json:"projectKey"`
	WorkDir     string `json:"workDir"`
	DisplayName string `json:"displayName"`
}

type RenameRequest struct {
	TmuxName  string
	Namespace string
	Name      string
}

type RenameResult struct {
	Shell   string `json:"shell"`
	OldName string `json:"oldName"`
	Name    string `json:"name"`
	Changed bool   `json:"changed"`
}

// AddAtPath records one shell definition with the same locked, atomic
// read-before-write semantics used by rename. It is the state-free persistence
// boundary used by project and cross-project shell hosts.
func AddAtPath(path string, def Definition) error {
	if strings.TrimSpace(def.TmuxName) == "" {
		return &Error{Kind: KindValidation, Msg: "shell session name is required"}
	}
	if _, err := NormalizeName(def.DisplayName); err != nil {
		return err
	}
	return mutateManifest(path, func(m *manifest) error {
		for i := range m.Shells {
			if m.Shells[i].TmuxName == def.TmuxName && sameNamespace(m.Shells[i].Namespace, def.Namespace) {
				m.Shells[i] = def
				m.Tombstones = dropTombstone(m.Tombstones, Identity{TmuxName: def.TmuxName, Namespace: def.Namespace})
				return nil
			}
			if m.Shells[i].DisplayName == def.DisplayName {
				return &Error{Kind: KindValidation, Msg: "name is already in use in this project"}
			}
		}
		m.Shells = append(m.Shells, def)
		m.Tombstones = dropTombstone(m.Tombstones, Identity{TmuxName: def.TmuxName, Namespace: def.Namespace})
		return nil
	})
}

// ListAtPath returns every shell definition recorded in the manifest at path.
//
// It exists so callers that need to decide *which* shells to act on — the
// worktree delete, which must forget the shells rooted in the directory it is
// about to remove — can read the manifest through this package instead of
// unmarshalling shells.json themselves. A missing manifest is an empty
// project, not an error.
//
// The result is a copy; mutating it does not affect the file. Removal is still
// one exact Identity at a time, so a caller that lists and then removes must
// tolerate the manifest changing in between (RemoveAtPath treats an entry that
// is already gone as success).
func ListAtPath(path string) ([]Definition, error) {
	m, err := readManifest(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &Error{Kind: KindState, Msg: "read shell manifest", Err: err}
	}
	out := make([]Definition, len(m.Shells))
	copy(out, m.Shells)
	return out, nil
}

// RemoveAtPath forgets one exact shell identity. A missing entry is already
// the requested state and therefore succeeds. The definition is moved to
// tombstones rather than dropped, so RestoreAtPath can put it back.
func RemoveAtPath(path string, id Identity) error {
	return mutateManifestRemoving(path, func(m *manifest) error {
		return tombstoneIdentity(m, id, time.Time{})
	})
}

// ErrShellChanged reports that the entry on disk is not the one the caller
// looked at, so the removal was refused.
var ErrShellChanged = errors.New("shell entry was replaced since it was observed")

// RemoveIfUnchangedAtPath forgets one shell only when the entry on disk is
// still the incarnation the caller observed.
//
// An auto-close decides a shell is dead, and then takes a moment to confirm it.
// A shell can be created under the same tmux name inside that moment — the
// global browser's create writes a fresh definition — and deleting the entry
// then would delete a live shell's identity (td-6a4100). The comparison runs
// inside the same exclusive lock the creating write takes, so the two orderings
// are the only two possible and both are correct: create-then-remove is
// refused, remove-then-create leaves the new entry alone.
//
// A zero observedAt means the caller has no incarnation to check and accepts an
// unconditional removal.
func RemoveIfUnchangedAtPath(path string, id Identity, observedAt time.Time) error {
	return mutateManifestRemoving(path, func(m *manifest) error {
		return tombstoneIdentity(m, id, observedAt)
	})
}

// RestoreAtPath moves a forgotten definition from tombstones back onto shells.
// Display name, agent type, skip-perms, and workdir are left intact.
//
// A live record is already in that state (KindAlready). An identity that is
// in neither list is KindNotFound.
func RestoreAtPath(path string, id Identity) (Definition, error) {
	var restored Definition
	err := mutateManifest(path, func(m *manifest) error {
		for i := range m.Shells {
			if m.Shells[i].TmuxName == id.TmuxName && sameNamespace(m.Shells[i].Namespace, id.Namespace) {
				restored = m.Shells[i]
				return &Error{Kind: KindAlready, Msg: "shell record is already live"}
			}
		}
		for i := range m.Tombstones {
			if m.Tombstones[i].TmuxName != id.TmuxName || !sameNamespace(m.Tombstones[i].Namespace, id.Namespace) {
				continue
			}
			restored = m.Tombstones[i].Definition
			m.Shells = append(m.Shells, restored)
			m.Tombstones = append(m.Tombstones[:i], m.Tombstones[i+1:]...)
			return nil
		}
		return &Error{Kind: KindNotFound, Msg: "forgotten shell record not found"}
	})
	return restored, err
}

// ListTombstonesAtPath returns forgotten shell definitions still in the
// manifest. A missing file is an empty list, not an error.
//
// Records past the retention window are filtered out even though the next
// write is what physically removes them, so a caller never offers a restore
// that the write behind it would refuse.
func ListTombstonesAtPath(path string) ([]Tombstone, error) {
	m, err := readManifest(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, nil
		}
		return nil, &Error{Kind: KindState, Msg: "read shell manifest", Err: err}
	}
	live := expireTombstonesNow(m.Tombstones)
	out := make([]Tombstone, len(live))
	copy(out, live)
	return out, nil
}

func tombstoneIdentity(m *manifest, id Identity, observedAt time.Time) error {
	for i := range m.Shells {
		if m.Shells[i].TmuxName != id.TmuxName || !sameNamespace(m.Shells[i].Namespace, id.Namespace) {
			continue
		}
		if !observedAt.IsZero() && m.Shells[i].CreatedAt.After(observedAt) {
			return ErrShellChanged
		}
		m.Tombstones = appendTombstone(m.Tombstones, m.Shells[i], time.Now().UTC())
		m.Shells = append(m.Shells[:i], m.Shells[i+1:]...)
		return nil
	}
	return nil
}

func appendTombstone(tombs []Tombstone, def Definition, at time.Time) []Tombstone {
	stone := Tombstone{Definition: def, DeletedAt: at}
	for i := range tombs {
		if tombs[i].TmuxName == def.TmuxName && sameNamespace(tombs[i].Namespace, def.Namespace) {
			tombs[i] = stone
			return tombs
		}
	}
	return append(tombs, stone)
}

func dropTombstone(tombs []Tombstone, id Identity) []Tombstone {
	for i := range tombs {
		if tombs[i].TmuxName == id.TmuxName && sameNamespace(tombs[i].Namespace, id.Namespace) {
			return append(tombs[:i], tombs[i+1:]...)
		}
	}
	return tombs
}

func mutateManifest(path string, apply func(*manifest) error) error {
	return mutateManifestLive(path, false, apply)
}

func mutateManifestRemoving(path string, apply func(*manifest) error) error {
	return mutateManifestLive(path, true, apply)
}

// mutateManifestLive is the shells.json writer. identityRemoval is true only
// for RemoveAtPath and RemoveIfUnchangedAtPath.
func mutateManifestLive(path string, identityRemoval bool, apply func(*manifest) error) error {
	if err := config.AssertIsolatedPath(path); err != nil {
		return &Error{Kind: KindState, Msg: "refusing shell manifest path", Err: err}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return &Error{Kind: KindState, Msg: "create shell manifest directory", Err: err}
	}
	lock, err := acquireLock(path)
	if err != nil {
		return &Error{Kind: KindState, Msg: "lock shell manifest", Err: err}
	}
	defer releaseLock(lock)
	m := manifest{Version: CurrentVersion}
	if current, readErr := readManifest(path); readErr == nil {
		m = current
	} else if !os.IsNotExist(readErr) {
		return &Error{Kind: KindState, Msg: "read shell manifest", Err: readErr}
	}
	if err := CheckWritableVersion(m.Version); err != nil {
		return err
	}
	// Expiry runs before apply so that the mutation, and anything it decides
	// from the tombstone list, sees the same set the readers do.
	m.Tombstones = expireTombstonesNow(m.Tombstones)
	before := len(m.Shells)
	if err := apply(&m); err != nil {
		return err
	}
	ObserveLiveCountWrite(path, before, len(m.Shells), identityRemoval)
	m.Version = CurrentVersion
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return &Error{Kind: KindState, Msg: "encode shell manifest", Err: err}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return &Error{Kind: KindState, Msg: "write shell manifest", Err: err}
	}
	if err := os.Rename(tmp, path); err != nil {
		return &Error{Kind: KindState, Msg: "replace shell manifest", Err: err}
	}
	return nil
}

// Definition contains all information needed to recreate a shell session.
type Definition struct {
	TmuxName    string `json:"tmuxName"`
	DisplayName string `json:"displayName"`
	// Namespace is the resolved tmux socket path. Empty identifies a legacy
	// entry that a headless rename must never claim as a side effect.
	Namespace string    `json:"namespace,omitempty"`
	CreatedAt time.Time `json:"createdAt"`
	AgentType string    `json:"agentType,omitempty"`
	SkipPerms bool      `json:"skipPerms,omitempty"`
	// WorkDir is the parent worktree path this shell was created in. Empty on
	// pre-td-4819be manifests; callers infer or treat empty as current workDir.
	WorkDir string `json:"workDir,omitempty"`
}

// Tombstone is a forgotten shell definition kept so RestoreAtPath can move it
// back, for as long as TombstoneRetention says.
//
// It arrived with schema version 2. An older binary — one that predates
// CurrentVersion being read at all — ignores the field on read and drops the
// key on its next write. That degradation is accepted and unavoidable in that
// direction; what version 2 buys is the other direction, where this build
// refuses to rewrite a manifest whose version it does not understand rather
// than dropping the fields it could not parse (see CheckWritableVersion).
type Tombstone struct {
	Definition
	DeletedAt time.Time `json:"deletedAt"`
}

type manifest struct {
	// Version is the schema version of the file on disk. Every writer checks
	// it with CheckWritableVersion before rewriting, and stamps CurrentVersion
	// on the way out, which is how a v1 file upgrades in place.
	Version int          `json:"version"`
	Shells  []Definition `json:"shells"`
	// Tombstones is omitted by older binaries; see Tombstone.
	Tombstones []Tombstone `json:"tombstones,omitempty"`
}

func NormalizeName(name string) (string, error) {
	if !utf8.ValidString(name) {
		return "", &Error{Kind: KindValidation, Msg: "name must be valid UTF-8"}
	}
	for _, r := range name {
		if unicode.IsControl(r) {
			return "", &Error{Kind: KindValidation, Msg: "name cannot contain control characters"}
		}
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return "", &Error{Kind: KindValidation, Msg: "name cannot be empty"}
	}
	if len(name) > MaxNameBytes {
		return "", &Error{Kind: KindValidation, Msg: fmt.Sprintf("name is too long (maximum %d bytes)", MaxNameBytes)}
	}
	return name, nil
}

// LookupCurrent returns the display name of the current Sidecar project shell
// from the registered manifest. It does not create state or depend on process
// environment cues, so it works for shells created before SIDECAR_SHELL_NAME
// injection existed.
func LookupCurrent(stateDir string, id Identity) (LookupResult, error) {
	path, err := locateCurrentManifest(stateDir, id)
	if err != nil {
		return LookupResult{}, err
	}
	m, err := readManifest(path)
	if err != nil {
		return LookupResult{}, &Error{Kind: KindState, Msg: "read shell manifest", Err: err}
	}
	match := -1
	for i, shell := range m.Shells {
		if shell.TmuxName == id.TmuxName && sameNamespace(shell.Namespace, id.Namespace) {
			if match >= 0 {
				return LookupResult{}, &Error{Kind: KindAmbiguous, Msg: "current shell appears more than once in its manifest; refusing ambiguous match"}
			}
			match = i
		}
	}
	if match < 0 {
		return LookupResult{}, notFound()
	}
	return LookupResult{Shell: id.TmuxName, Name: m.Shells[match].DisplayName}, nil
}

// LookupOrigin returns origin identity and workspace root for the current shell.
func LookupOrigin(stateDir string, id Identity) (OriginInfo, error) {
	entries, err := os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil {
		if os.IsNotExist(err) {
			return OriginInfo{}, notFound()
		}
		return OriginInfo{}, &Error{Kind: KindState, Msg: "read registered Sidecar projects", Err: err}
	}
	var matches []struct {
		projectKey string
		dir        string
		shell      Definition
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(stateDir, "projects", entry.Name())
		path := filepath.Join(dir, "shells.json")
		m, readErr := readManifest(path)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return OriginInfo{}, &Error{Kind: KindState, Msg: "read registered shell manifest", Err: readErr}
		}
		for _, shell := range m.Shells {
			if shell.TmuxName == id.TmuxName && sameNamespace(shell.Namespace, id.Namespace) {
				matches = append(matches, struct {
					projectKey string
					dir        string
					shell      Definition
				}{
					projectKey: entry.Name(),
					dir:        dir,
					shell:      shell,
				})
			}
		}
	}
	if len(matches) == 0 {
		return OriginInfo{}, notFound()
	}
	if len(matches) > 1 {
		return OriginInfo{}, &Error{Kind: KindAmbiguous, Msg: "current shell matches multiple Sidecar project manifests; refusing ambiguous match"}
	}
	m := matches[0]
	workDir := m.shell.WorkDir
	if workDir == "" {
		metaPath := filepath.Join(m.dir, "meta.json")
		if data, err := os.ReadFile(metaPath); err == nil {
			var meta struct {
				Path string `json:"path"`
			}
			if err := json.Unmarshal(data, &meta); err == nil && meta.Path != "" {
				workDir = meta.Path
			}
		}
	}
	return OriginInfo{
		TmuxName:    id.TmuxName,
		Namespace:   id.Namespace,
		ProjectKey:  m.projectKey,
		WorkDir:     workDir,
		DisplayName: m.shell.DisplayName,
	}, nil
}

// RenameCurrent searches registered project manifests without creating state.
func RenameCurrent(stateDir string, req RenameRequest) (RenameResult, error) {
	name, err := NormalizeName(req.Name)
	if err != nil {
		return RenameResult{}, err
	}
	req.Name = name
	path, err := locateCurrentManifest(stateDir, Identity{TmuxName: req.TmuxName, Namespace: req.Namespace})
	if err != nil {
		return RenameResult{}, err
	}
	return RenameAtPath(path, req)
}

// locateCurrentManifest finds the single registered shells.json that owns the
// caller's tmux session. It fails closed on ambiguity or unreadable inventory.
func locateCurrentManifest(stateDir string, id Identity) (string, error) {
	entries, err := os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil {
		if os.IsNotExist(err) {
			return "", notFound()
		}
		return "", &Error{Kind: KindState, Msg: "read registered Sidecar projects", Err: err}
	}
	var paths []string
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		path := filepath.Join(stateDir, "projects", entry.Name(), "shells.json")
		m, readErr := readManifest(path)
		if readErr != nil {
			if os.IsNotExist(readErr) {
				continue
			}
			return "", &Error{Kind: KindState, Msg: "read registered shell manifest", Err: readErr}
		}
		for _, shell := range m.Shells {
			if shell.TmuxName == id.TmuxName && sameNamespace(shell.Namespace, id.Namespace) {
				paths = append(paths, path)
				break
			}
		}
	}
	if len(paths) == 0 {
		return "", notFound()
	}
	if len(paths) > 1 {
		return "", &Error{Kind: KindAmbiguous, Msg: "current shell matches multiple Sidecar project manifests; refusing ambiguous match"}
	}
	return paths[0], nil
}

func notFound() error {
	return &Error{Kind: KindNotFound, Msg: "current tmux session is not a registered Sidecar project shell; run this command from a Sidecar project shell"}
}

// RenameAtPath performs a locked read-before-write mutation of one manifest.
func RenameAtPath(path string, req RenameRequest) (RenameResult, error) {
	name, err := NormalizeName(req.Name)
	if err != nil {
		return RenameResult{}, err
	}
	req.Name = name
	if err := config.AssertIsolatedPath(path); err != nil {
		return RenameResult{}, &Error{Kind: KindState, Msg: "refusing shell manifest path", Err: err}
	}
	lock, err := acquireLock(path)
	if err != nil {
		return RenameResult{}, &Error{Kind: KindState, Msg: "lock shell manifest", Err: err}
	}
	defer releaseLock(lock)
	m, err := readManifest(path)
	if err != nil {
		return RenameResult{}, &Error{Kind: KindState, Msg: "read shell manifest", Err: err}
	}
	if err := CheckWritableVersion(m.Version); err != nil {
		return RenameResult{}, err
	}
	match := -1
	for i, shell := range m.Shells {
		if shell.TmuxName == req.TmuxName && sameNamespace(shell.Namespace, req.Namespace) {
			if match >= 0 {
				return RenameResult{}, &Error{Kind: KindAmbiguous, Msg: "current shell appears more than once in its manifest; refusing ambiguous rename"}
			}
			match = i
		}
	}
	if match < 0 {
		return RenameResult{}, notFound()
	}
	for i, shell := range m.Shells {
		if i != match && shell.DisplayName == req.Name {
			return RenameResult{}, &Error{Kind: KindValidation, Msg: "name is already in use in this project"}
		}
	}
	result := RenameResult{Shell: req.TmuxName, OldName: m.Shells[match].DisplayName, Name: req.Name}
	if result.OldName == result.Name {
		return result, nil
	}
	before := len(m.Shells)
	m.Shells[match].DisplayName = req.Name
	// Every write sweeps, including this one: that is what keeps the file
	// bounded without a sweeper that has to know where manifests live.
	m.Tombstones = expireTombstonesNow(m.Tombstones)
	m.Version = CurrentVersion
	ObserveLiveCountWrite(path, before, len(m.Shells), false)
	data, err := json.MarshalIndent(m, "", "  ")
	if err != nil {
		return RenameResult{}, &Error{Kind: KindState, Msg: "encode shell manifest", Err: err}
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, data, 0644); err != nil {
		return RenameResult{}, &Error{Kind: KindState, Msg: "write shell manifest", Err: err}
	}
	if err := os.Rename(tmp, path); err != nil {
		return RenameResult{}, &Error{Kind: KindState, Msg: "replace shell manifest", Err: err}
	}
	result.Changed = true
	return result, nil
}

func sameNamespace(a, b string) bool {
	if a == "" || b == "" {
		return a == b
	}
	return canonicalPath(a) == canonicalPath(b)
}

func canonicalPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return path
}

func readManifest(path string) (manifest, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return manifest{}, err
	}
	var m manifest
	if err := json.Unmarshal(data, &m); err != nil {
		return manifest{}, err
	}
	return m, nil
}

const lockTimeout = 5 * time.Second

func acquireLock(path string) (*os.File, error) {
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return nil, err
	}
	deadline := time.Now().Add(lockTimeout)
	for {
		err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			return lock, nil
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			_ = lock.Close()
			return nil, err
		}
		if time.Now().After(deadline) {
			_ = lock.Close()
			return nil, fmt.Errorf("lock acquisition timeout after %v", lockTimeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
func releaseLock(lock *os.File) { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN); _ = lock.Close() }
