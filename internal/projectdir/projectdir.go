// Package projectdir resolves project-specific state directories under
// $XDG_STATE_HOME/sidecar/projects/<slug>/ (defaults to
// ~/.local/state/sidecar/projects/<slug>/). Each project root gets a
// unique slug-named directory containing a meta.json that maps back to
// the original project path.
package projectdir

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/config"
)

// projectMeta is stored as meta.json inside each project slug directory.
type projectMeta struct {
	Path string `json:"path"`
}

// worktreeMeta makes the collision-safe directory name inspectable and permits
// state to be recovered without relying on the slug allocation algorithm.
type worktreeMeta struct {
	Path string `json:"path"`
	Key  string `json:"key"`
}

// Resolve returns the data directory for the given project root path.
// It creates the directory (with meta.json) if it does not already exist.
// On subsequent calls with the same projectRoot, the existing directory is
// returned.
func Resolve(projectRoot string) (string, error) {
	base := config.StateDir()
	return resolveWithBase(base, projectRoot)
}

// Lookup returns the already-registered data directory for a project root
// without creating anything. Diagnostics use it: a function whose job is to
// report what this process will write to must not itself write.
func Lookup(projectRoot string) (string, bool) {
	return findByMeta(filepath.Join(config.StateDir(), "projects"), projectRoot)
}

// LookupWorktree returns the already-registered data directory for worktreePath
// without creating or migrating state. It is intended for read-only inventory
// consumers such as diagnostics and the cross-project overview.
func LookupWorktree(projectRoot, worktreePath string) (string, bool) {
	projectDir, ok := Lookup(projectRoot)
	if !ok {
		return "", false
	}
	normalized, err := normalizePath(worktreePath)
	if err != nil {
		return "", false
	}
	entries, err := os.ReadDir(filepath.Join(projectDir, "worktrees"))
	if err != nil {
		return "", false
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(projectDir, "worktrees", entry.Name())
		meta, err := readWorktreeMeta(dir)
		if err == nil && meta.Path == normalized && meta.Key == pathKey(normalized) {
			return dir, true
		}
	}
	return "", false
}

// WorktreeDir returns the worktree-specific data directory for a project.
// The directory is created if it does not exist.
func WorktreeDir(projectRoot, worktreePath string) (string, error) {
	return WorktreeDirContext(context.Background(), projectRoot, worktreePath)
}

// WorktreeDirContext resolves worktree state while allowing legacy Git
// inventory to be cancelled with its owning lifecycle operation.
func WorktreeDirContext(ctx context.Context, projectRoot, worktreePath string) (string, error) {
	base := config.StateDir()
	return worktreeDirWithBaseContext(ctx, base, projectRoot, worktreePath)
}

// WorktreeDirWithBase is the exported, testable form of WorktreeDir.
// base overrides the state directory (e.g. a temp dir in tests).
func WorktreeDirWithBase(base, projectRoot, worktreePath string) (string, error) {
	return worktreeDirWithBase(base, projectRoot, worktreePath)
}

// ResolveWithBase is the exported, testable form of Resolve.
// base overrides the state directory (e.g. a temp dir in tests).
func ResolveWithBase(base, projectRoot string) (string, error) {
	return resolveWithBase(base, projectRoot)
}

// worktreeDirWithBase is the testable core of WorktreeDir.
func worktreeDirWithBase(base, projectRoot, worktreePath string) (string, error) {
	return worktreeDirWithBaseContext(context.Background(), base, projectRoot, worktreePath)
}

func worktreeDirWithBaseContext(ctx context.Context, base, projectRoot, worktreePath string) (string, error) {
	if err := ctx.Err(); err != nil {
		return "", err
	}
	projectDir, err := resolveWithBase(base, projectRoot)
	if err != nil {
		return "", err
	}

	normalized, err := normalizePath(worktreePath)
	if err != nil {
		return "", fmt.Errorf("normalize worktree path: %w", err)
	}
	worktreesDir := filepath.Join(projectDir, "worktrees")
	key := pathKey(normalized)
	wtSlug := sanitizeSlug(filepath.Base(normalized)) + "-" + key[:12]
	dir := filepath.Join(worktreesDir, wtSlug)

	if meta, readErr := readWorktreeMeta(dir); readErr == nil {
		if meta.Path != normalized || meta.Key != key {
			return "", fmt.Errorf("worktree state identity mismatch in %s", dir)
		}
		return dir, nil
	} else if _, statErr := os.Stat(dir); statErr == nil {
		return "", fmt.Errorf("worktree state directory %s exists without valid meta.json", dir)
	} else if !os.IsNotExist(statErr) {
		return "", fmt.Errorf("stat worktree dir: %w", statErr)
	}

	// Older Sidecar releases used only the basename. Copy that state on first
	// unambiguous access, keeping the source intact until the new mapping has
	// been verified by a later read. Two registered worktrees with the same
	// basename are deliberately refused: choosing either would silently assign
	// task/PR/agent metadata to the wrong checkout.
	legacyDir := filepath.Join(worktreesDir, sanitizeSlug(filepath.Base(normalized)))
	if info, statErr := os.Stat(legacyDir); statErr == nil && info.IsDir() {
		ambiguous, inventoryErr := legacyWorktreeAmbiguousContext(ctx, projectRoot, normalized)
		if inventoryErr != nil {
			return "", fmt.Errorf("cannot safely migrate legacy worktree state %s: %w", legacyDir, inventoryErr)
		}
		if ambiguous {
			return "", fmt.Errorf("ambiguous legacy worktree state %s: multiple registered worktrees share basename %q", legacyDir, filepath.Base(normalized))
		}
		if err := copyDirContext(ctx, legacyDir, dir); err != nil {
			return "", fmt.Errorf("migrate legacy worktree state: %w", err)
		}
	}

	if err := ctx.Err(); err != nil {
		return "", err
	}
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating worktree dir: %w", err)
	}
	data, err := json.Marshal(worktreeMeta{Path: normalized, Key: key})
	if err != nil {
		return "", fmt.Errorf("marshaling worktree meta: %w", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0644); err != nil {
		return "", fmt.Errorf("writing worktree meta.json: %w", err)
	}
	return dir, nil
}

// WorktreeKey is stable across restarts and independent of presentation names.
func WorktreeKey(worktreePath string) (string, error) {
	normalized, err := normalizePath(worktreePath)
	if err != nil {
		return "", err
	}
	return pathKey(normalized), nil
}

func normalizePath(path string) (string, error) {
	abs, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	abs = filepath.Clean(abs)
	if resolved, err := filepath.EvalSymlinks(abs); err == nil {
		abs = filepath.Clean(resolved)
	}
	return abs, nil
}

func pathKey(path string) string {
	sum := sha256.Sum256([]byte(path))
	return fmt.Sprintf("%x", sum[:])
}

func readWorktreeMeta(dir string) (worktreeMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return worktreeMeta{}, err
	}
	var meta worktreeMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return worktreeMeta{}, err
	}
	if meta.Path == "" || meta.Key == "" {
		return worktreeMeta{}, fmt.Errorf("incomplete worktree metadata")
	}
	return meta, nil
}

func legacyWorktreeAmbiguous(projectRoot, target string) (bool, error) {
	return legacyWorktreeAmbiguousContext(context.Background(), projectRoot, target)
}

func legacyWorktreeAmbiguousContext(ctx context.Context, projectRoot, target string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", projectRoot, "worktree", "list", "--porcelain")
	out, err := cmd.Output()
	if err != nil {
		return false, err
	}
	basename := filepath.Base(target)
	matches := 0
	for _, line := range strings.Split(string(out), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok && filepath.Base(filepath.Clean(path)) == basename {
			matches++
		}
	}
	return matches > 1, nil
}

func copyDir(src, dst string) error {
	return copyDirContext(context.Background(), src, dst)
}

func copyDirContext(ctx context.Context, src, dst string) error {
	return filepath.WalkDir(src, func(path string, entry os.DirEntry, walkErr error) error {
		if err := ctx.Err(); err != nil {
			return err
		}
		if walkErr != nil {
			return walkErr
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		target := filepath.Join(dst, rel)
		if entry.IsDir() {
			info, err := entry.Info()
			if err != nil {
				return err
			}
			return os.MkdirAll(target, info.Mode().Perm())
		}
		in, err := os.Open(path)
		if err != nil {
			return err
		}
		defer in.Close()
		info, err := entry.Info()
		if err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, info.Mode().Perm())
		if err != nil {
			return err
		}
		_, copyErr := io.Copy(out, in)
		closeErr := out.Close()
		if copyErr != nil {
			return copyErr
		}
		return closeErr
	})
}

// resolveWithBase is the testable core of Resolve. It uses base as the
// sidecar state directory (e.g. ~/.local/state/sidecar) instead of
// deriving it from config.StateDir().
func resolveWithBase(base, projectRoot string) (string, error) {
	// Slug allocation is order-dependent and creating: an unisolated test that
	// claims projects/<basename> for a temp path pushes the developer's real
	// project onto <basename>-2, which is a different directory and therefore
	// an empty shell manifest — the "my shells vanished" symptom of td-8d18de
	// by another route. Refuse the real tree whenever isolation is asserted.
	if err := config.AssertIsolatedPath(base); err != nil {
		return "", err
	}

	projectsDir := filepath.Join(base, "projects")

	// Scan existing project directories for a matching path.
	if dir, found := findByMeta(projectsDir, projectRoot); found {
		return dir, nil
	}

	slug := sanitizeSlug(filepath.Base(projectRoot))

	// Try slug, then slug-2, slug-3, ..., slug-99.
	for i := 1; i <= 99; i++ {
		candidate := slug
		if i > 1 {
			candidate = fmt.Sprintf("%s-%d", slug, i)
		}
		dir := filepath.Join(projectsDir, candidate)

		_, err := os.Stat(dir)
		if os.IsNotExist(err) {
			// Slot is free -- claim it.
			return createProjectDir(dir, projectRoot)
		}
		if err != nil {
			return "", fmt.Errorf("stat %s: %w", dir, err)
		}

		// Directory exists. Check if it belongs to the same project.
		meta, readErr := readMeta(dir)
		if readErr != nil {
			// Corrupt or missing meta -- skip to next candidate.
			continue
		}
		if meta.Path == projectRoot {
			return dir, nil
		}
		// Different project owns this slug -- try next suffix.
	}

	return "", fmt.Errorf("could not allocate slug for %q (tried 99 suffixes)", projectRoot)
}

// sanitizeSlug removes characters that are problematic in directory names.
func sanitizeSlug(s string) string {
	// Remove forward and back slashes.
	s = strings.ReplaceAll(s, "/", "")
	s = strings.ReplaceAll(s, "\\", "")

	// Remove control characters (0x00-0x1F).
	var b strings.Builder
	for _, r := range s {
		if r >= 0x20 {
			b.WriteRune(r)
		}
	}
	s = b.String()

	// Replace empty, ".", ".." with underscore.
	if s == "" || s == "." || s == ".." {
		return "_"
	}
	return s
}

// findByMeta scans all subdirectories in projectsDir looking for one
// whose meta.json path matches projectRoot. Returns the directory path
// and true if found.
func findByMeta(projectsDir, projectRoot string) (string, bool) {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(projectsDir, e.Name())
		meta, err := readMeta(dir)
		if err != nil {
			continue
		}
		if meta.Path == projectRoot {
			return dir, true
		}
	}
	return "", false
}

// readMeta reads and parses the meta.json in the given directory.
func readMeta(dir string) (projectMeta, error) {
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		return projectMeta{}, err
	}
	var meta projectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return projectMeta{}, err
	}
	return meta, nil
}

// createProjectDir creates the slug directory and writes its meta.json.
func createProjectDir(dir, projectRoot string) (string, error) {
	if err := os.MkdirAll(dir, 0755); err != nil {
		return "", fmt.Errorf("creating project dir: %w", err)
	}

	meta := projectMeta{Path: projectRoot}
	data, err := json.Marshal(meta)
	if err != nil {
		return "", fmt.Errorf("marshaling meta: %w", err)
	}

	metaPath := filepath.Join(dir, "meta.json")
	if err := os.WriteFile(metaPath, data, 0644); err != nil {
		return "", fmt.Errorf("writing meta.json: %w", err)
	}

	return dir, nil
}
