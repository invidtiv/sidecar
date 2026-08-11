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

// NamingInstruction is the canonical guidance for keeping a shell's display
// name useful. It lives here, next to the rules it describes, so every
// delivery channel — AGENTS.md, a harness system-prompt append, help text —
// quotes one source instead of drifting copies.
const NamingInstruction = "This terminal is a Sidecar project shell. Its display name is in $" + NameEnv +
	"; a name like \"Shell 3\" is an unset default. Run `sidecar shell rename \"short context\"` when you begin a " +
	"materially different task or the current name is stale — at meaningful context boundaries, not for every " +
	"sub-step. Prefer the outcome (\"shell rename implementation\") over the model or a transient action " +
	"(\"Codex running tests\"). This renames only the current Sidecar shell; never edit shells.json or rename tmux " +
	"sessions directly."

type ErrorKind string

const (
	KindValidation ErrorKind = "validation"
	KindNotFound   ErrorKind = "not_found"
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
}

type manifest struct {
	Version int          `json:"version"`
	Shells  []Definition `json:"shells"`
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

// RenameCurrent searches registered project manifests without creating state.
func RenameCurrent(stateDir string, req RenameRequest) (RenameResult, error) {
	name, err := NormalizeName(req.Name)
	if err != nil {
		return RenameResult{}, err
	}
	req.Name = name
	entries, err := os.ReadDir(filepath.Join(stateDir, "projects"))
	if err != nil {
		if os.IsNotExist(err) {
			return RenameResult{}, notFound()
		}
		return RenameResult{}, &Error{Kind: KindState, Msg: "read registered Sidecar projects", Err: err}
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
			return RenameResult{}, &Error{Kind: KindState, Msg: "read registered shell manifest", Err: readErr}
		}
		for _, shell := range m.Shells {
			if shell.TmuxName == req.TmuxName && sameNamespace(shell.Namespace, req.Namespace) {
				paths = append(paths, path)
			}
		}
	}
	if len(paths) == 0 {
		return RenameResult{}, notFound()
	}
	if len(paths) > 1 {
		return RenameResult{}, &Error{Kind: KindAmbiguous, Msg: "current shell matches multiple Sidecar project manifests; refusing ambiguous rename"}
	}
	return RenameAtPath(paths[0], req)
}

func notFound() error {
	return &Error{Kind: KindNotFound, Msg: "current tmux session is not a registered Sidecar project shell; run this command from the shell you want to rename"}
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
	m.Shells[match].DisplayName = req.Name
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
