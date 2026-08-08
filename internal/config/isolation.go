package config

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
)

// IsolationEnv is the promise a harness makes: this process must never read or
// write the real user's Sidecar state. Set it to "1".
//
// The promise exists because tmux isolation and state isolation are separate
// axes (td-8d18de). A proof run can hold a private tmux socket and still share
// ~/.local/state/sidecar with the developer's live Sidecar, in which case it
// rewrites a manifest that instance is watching. Asserting isolation turns
// every path that resolves back into the real tree into a hard error instead of
// a silent overwrite.
const IsolationEnv = "SIDECAR_ISOLATED_STATE"

// RealUserStateDir returns the state directory an ordinary run would use,
// ignoring XDG_STATE_HOME and any test override: $HOME/.local/state/sidecar.
//
// It deliberately derives the root from $HOME rather than from the pre-override
// value of XDG_STATE_HOME. A harness launches Sidecar as a subprocess with the
// override already in its environment, so the process has no way to observe
// what XDG_STATE_HOME was before isolation was applied. $HOME is the one anchor
// that is still visible from inside, and it is what the XDG default expands to.
func RealUserStateDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, ".local", "state", "sidecar")
}

// RealUserConfigDir returns $HOME/.config/sidecar, likewise unoverridable.
func RealUserConfigDir() string {
	home, err := os.UserHomeDir()
	if err != nil {
		return ""
	}
	return filepath.Join(home, configDir)
}

// AllowRealStateEnv opts a process out of the automatic isolation that applies
// to every `go test` binary. Set it to "1" only in a test that deliberately
// proves the ordinary, unisolated path still works — and only with HOME already
// pointed at a temp dir.
const AllowRealStateEnv = "SIDECAR_ALLOW_REAL_STATE"

// IsolationAsserted reports whether this process claims isolated state.
//
// A test binary asserts it by default. Isolation used to be opt-in, which meant
// the guard was silently inert in every package that had not thought about it,
// and `go test ./...` went on creating directories in the developer's real
// ~/.local/state/sidecar/projects tree — the same tree that holds the shell
// manifest td-8d18de destroyed. Defaulting to on makes forgetting to isolate a
// loud failure instead of a silent write.
func IsolationAsserted() bool {
	if os.Getenv(IsolationEnv) == "1" || testStateDir != "" {
		return true
	}
	return testing.Testing() && os.Getenv(AllowRealStateEnv) != "1"
}

// AssertIsolatedPath returns an error when isolation is asserted and path
// resolves inside the real user state or config tree. It returns nil otherwise:
// an ordinary run is never blocked from writing its own real files.
func AssertIsolatedPath(path string) error {
	if path == "" || !IsolationAsserted() {
		return nil
	}
	for _, root := range []string{RealUserStateDir(), RealUserConfigDir()} {
		if !pathWithin(path, root) {
			continue
		}
		return fmt.Errorf(
			"%s=1 asserts isolated state, but %s resolves inside the real user directory %s; "+
				"export XDG_STATE_HOME and pass -config to a temp dir",
			IsolationEnv, path, root)
	}
	return nil
}

// CheckStateIsolation validates the paths this process actually resolved.
func CheckStateIsolation() error {
	if err := AssertIsolatedPath(StateDir()); err != nil {
		return err
	}
	return AssertIsolatedPath(ConfigPath())
}

// pathWithin reports whether path is root itself or lives beneath it.
// The separator on the prefix keeps sibling names such as
// ~/.local/state/sidecar-other from matching ~/.local/state/sidecar.
func pathWithin(path, root string) bool {
	if root == "" {
		return false
	}
	path = cleanAbs(path)
	root = cleanAbs(root)
	if path == root {
		return true
	}
	return len(path) > len(root) &&
		path[:len(root)] == root &&
		path[len(root)] == os.PathSeparator
}

func cleanAbs(path string) string {
	if abs, err := filepath.Abs(path); err == nil {
		return filepath.Clean(abs)
	}
	return filepath.Clean(path)
}
