package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uiapi"
)

// Fixture development must not reuse live discovery or paired-origin authority.
// Check before config loading, listener binding, or writing any API files.
func checkAPIFixtureIsolation(stateDir string) error {
	if os.Getenv(config.IsolationEnv) != "1" {
		return fmt.Errorf("--fixtures requires %s=1, XDG_STATE_HOME and -config pointing at a temporary tree; see scripts/ui-api-fixture-proof.sh", config.IsolationEnv)
	}
	paths := []string{stateDir, config.ConfigPath(), uiapi.Dir(stateDir)}
	for _, name := range []string{"origins.json", "serve.lock", "endpoint.json", "api.sock", "tailnet.sock", "layouts"} {
		paths = append(paths, filepath.Join(uiapi.Dir(stateDir), name))
	}
	for _, path := range paths {
		if err := config.AssertIsolatedPath(path); err != nil {
			return err
		}
		resolved, err := fixtureResolvedPath(path)
		if err != nil {
			return err
		}
		if err := config.AssertIsolatedPath(resolved); err != nil {
			return err
		}
		for _, root := range []string{config.RealUserStateDir(), config.RealUserConfigDir()} {
			canonicalRoot, err := fixtureResolvedPath(root)
			if err != nil {
				return err
			}
			rel, err := filepath.Rel(canonicalRoot, resolved)
			if err == nil && rel != ".." && !strings.HasPrefix(rel, ".."+string(os.PathSeparator)) {
				return fmt.Errorf("%s resolves inside real Sidecar state/config; export XDG_STATE_HOME and pass -config to a temporary tree", path)
			}
		}
	}
	return nil
}

// Resolve existing ancestors too: a new API directory beneath a symlink can
// reach real state even though EvalSymlinks on the full path reports ENOENT.
func fixtureResolvedPath(path string) (string, error) {
	path, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	resolved, err := filepath.EvalSymlinks(path)
	if err == nil {
		return resolved, nil
	}
	if !os.IsNotExist(err) {
		return "", err
	}
	// A dangling symlink is unsafe; do not treat it as a missing leaf.
	if info, statErr := os.Lstat(path); statErr == nil && info.Mode()&os.ModeSymlink != 0 {
		return "", fmt.Errorf("fixture path %s is a dangling symlink", path)
	}
	parent := filepath.Dir(path)
	if parent == path {
		return "", err
	}
	resolved, err = fixtureResolvedPath(parent)
	if err != nil {
		return "", err
	}
	return filepath.Join(resolved, filepath.Base(path)), nil
}
