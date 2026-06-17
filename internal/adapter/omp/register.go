package omp

import (
	"os"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/adapter"
	"github.com/marcus/sidecar/internal/adapter/piagent"
)

func init() {
	adapter.RegisterFactory(func() adapter.Adapter {
		home, _ := os.UserHomeDir()
		sessionsDir := filepath.Join(home, ".omp", "agent", "sessions")
		return piagent.NewCustom(sessionsDir, "omp", "OMP", "Ω").
			WithProjectDirFunc(projectDirPath)
	})
}

// projectDirPath implements OMP's session directory encoding.
//
// OMP v15+ uses a home-relative encoding for paths within the user's home
// directory, a tmpdir-relative encoding for paths within the temp directory,
// and falls back to the legacy absolute encoding (--path-encoded--) for
// everything else. Old sessions created before the encoding change retain
// the legacy name; OMP migrates home-relative sessions automatically but
// leaves tmp sessions in place.
//
// Encoding rules (after symlink resolution and macOS /private stripping):
//   - within home:   -<relative-path-with-slashes-as-dashes>
//   - within tmpdir: -tmp-<relative-path-with-slashes-as-dashes>  (or -tmp if cwd==tmpdir)
//   - elsewhere:     --<abs-path-with-slashes-as-dashes>--  (legacy)
func projectDirPath(sessionsDir, projectRoot string) string {
	cwd, err := filepath.Abs(projectRoot)
	if err != nil {
		cwd = projectRoot
	}
	if resolved, err := filepath.EvalSymlinks(cwd); err == nil {
		cwd = resolved
	}
	// On macOS, strip the /private prefix that EvalSymlinks adds for /tmp etc.
	// OMP does this to keep paths like /tmp stable across symlink resolution.
	if strings.HasPrefix(cwd, "/private/") {
		stripped := cwd[len("/private"):]
		if sr, err := filepath.EvalSymlinks(stripped); err == nil && sr == cwd {
			cwd = stripped
		}
	}

	home, _ := os.UserHomeDir()
	if resolved, err := filepath.EvalSymlinks(home); err == nil {
		home = resolved
	}
	tmpdir := os.TempDir() // returns /tmp on macOS (already without /private)

	switch {
	case cwd == home || strings.HasPrefix(cwd, home+string(filepath.Separator)):
		rel, _ := filepath.Rel(home, cwd)
		encoded := strings.ReplaceAll(rel, string(filepath.Separator), "-")
		if encoded == "." || encoded == "" {
			return filepath.Join(sessionsDir, "-")
		}
		return filepath.Join(sessionsDir, "-"+encoded)

	case cwd == tmpdir || strings.HasPrefix(cwd, tmpdir+string(filepath.Separator)):
		rel, _ := filepath.Rel(tmpdir, cwd)
		encoded := strings.ReplaceAll(rel, string(filepath.Separator), "-")
		if encoded == "." || encoded == "" {
			return filepath.Join(sessionsDir, "-tmp")
		}
		return filepath.Join(sessionsDir, "-tmp-"+encoded)

	default:
		// Legacy absolute encoding — also used for dirs that haven't been migrated.
		path := strings.TrimPrefix(cwd, "/")
		encoded := strings.ReplaceAll(path, "/", "-")
		return filepath.Join(sessionsDir, "--"+encoded+"--")
	}
}
