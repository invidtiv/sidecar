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
	"syscall"
	"time"

	"github.com/marcus/sidecar/internal/config"
)

// projectMeta is stored as meta.json inside each project slug directory.
type projectMeta struct {
	Path string `json:"path"`
	// ResolvedRoot pins the alias target observed at registration. Legacy entries
	// gain it on their next creating Resolve, while read-only lookup stays read-only.
	ResolvedRoot string `json:"resolved_root,omitempty"`
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

// LookupAll resolves many project roots in one pass over projects/.
//
// Lookup rescans and re-reads every registered project's meta.json on each
// call, so resolving N roots with it costs N full scans — and a long-lived
// install accumulates hundreds of registered projects. Callers that need the
// whole configured set at once use this instead: one ReadDir, one meta.json
// read per candidate, regardless of how many roots are asked for.
//
// Roots with no registered directory are absent from the result rather than
// mapped to an empty string, so a caller can tell "never registered" from
// "registered at the empty path".
func LookupAll(projectRoots []string) map[string]string {
	return LookupAllWithBase(config.StateDir(), projectRoots)
}

// LookupAllWithBase is the testable form of LookupAll. base is the Sidecar
// state directory containing projects/.
func LookupAllWithBase(base string, projectRoots []string) map[string]string {
	if len(projectRoots) == 0 {
		return nil
	}
	// Both sides are compared canonically, because the two strings come from
	// different places and only agree by luck. A configured path is whatever the
	// user or a config file wrote; meta.Path was canonicalised when the project
	// was registered. On macOS a project under /tmp registers as /private/tmp,
	// and a raw string match then reports a project that plainly exists as never
	// registered.
	//
	// That is not a hypothetical. A remote host whose project was configured by
	// one path and registered under another answered "no project state directory
	// exists on this host yet" while `sidecar shell list` was returning that
	// project's shells, and the freshness watch over shells.json silently never
	// registered — the fix it exists to deliver simply absent, with nothing
	// saying so. Any symlinked project root reaches the same place.
	//
	// The result stays keyed by the caller's own root string: callers look their
	// configured path back up, and handing them a canonical key they never used
	// would trade this bug for a lookup miss.
	wanted := make(map[string]string, len(projectRoots))
	var wantedOrder []string
	for _, root := range projectRoots {
		if root == "" {
			continue
		}
		key := resolvedPath(root)
		// First configured root wins, so two entries naming one directory
		// resolve deterministically rather than by map order.
		if _, seen := wanted[key]; !seen {
			wanted[key] = root
			wantedOrder = append(wantedOrder, key)
		}
	}
	entries, err := os.ReadDir(filepath.Join(base, "projects"))
	if err != nil {
		return nil
	}
	found := make(map[string]string, len(wanted))
	for _, e := range entries {
		if len(found) == len(wanted) {
			break
		}
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(base, "projects", e.Name())
		meta, err := readMeta(dir)
		if err != nil {
			continue
		}
		registered, valid := registrationRoot(meta)
		if !valid {
			continue
		}
		key := resolvedPath(registered)
		root, want := wanted[key]
		if !want {
			// EvalSymlinks does not normalize case on case-insensitive volumes.
			// Probe filesystem identity only for case variants, keeping unrelated
			// registrations on the single-pass text comparison path.
			for _, candidate := range wantedOrder {
				if strings.EqualFold(candidate, key) && sameProjectRoot(candidate, key) {
					root, want = wanted[candidate], true
					break
				}
			}
		}
		if !want {
			continue
		}
		// First registration wins, matching findByMeta's scan order semantics.
		if _, seen := found[root]; !seen {
			found[root] = dir
		}
	}
	return found
}

// LookupWorktree returns the already-registered data directory for worktreePath
// without creating or migrating state. It is intended for read-only inventory
// consumers such as diagnostics and the cross-project overview.
func LookupWorktree(projectRoot, worktreePath string) (string, bool) {
	return LookupWorktreeWithBase(config.StateDir(), projectRoot, worktreePath)
}

// LookupWorktreeWithBase is the read-only, testable form of LookupWorktree.
// base is the Sidecar state directory containing projects/.
func LookupWorktreeWithBase(base, projectRoot, worktreePath string) (string, bool) {
	projectDir, ok := findByMeta(filepath.Join(base, "projects"), projectRoot)
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
		defer func() { _ = in.Close() }()
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
	// Registration must publish the directory and metadata as one allocation.
	// Otherwise another process sees an incomplete slot and gives the same
	// project a second state directory, bypassing its shell manifest lock.
	if err := os.MkdirAll(projectsDir, 0755); err != nil {
		return "", fmt.Errorf("create project registry: %w", err)
	}
	lock, err := os.OpenFile(projectsDir+".lock", os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		return "", fmt.Errorf("open project registry lock: %w", err)
	}
	defer func() { _ = lock.Close() }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
		if err == nil {
			break
		}
		if err != syscall.EWOULDBLOCK && err != syscall.EAGAIN {
			return "", fmt.Errorf("lock project registry: %w", err)
		}
		if time.Now().After(deadline) {
			return "", fmt.Errorf("lock project registry: acquisition timeout after 5s; retry registration")
		}
		time.Sleep(10 * time.Millisecond)
	}
	defer func() { _ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN) }()

	// Scan existing project directories for a matching path.
	if dir, found := findByMeta(projectsDir, projectRoot); found {
		meta, err := readMeta(dir)
		if err != nil {
			return "", err
		}
		if meta.ResolvedRoot == "" {
			meta.ResolvedRoot = resolvedPath(meta.Path)
			if err := writeProjectMeta(dir, meta); err != nil {
				return "", err
			}
		}
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
		if registered, valid := registrationRoot(meta); valid && sameProjectRoot(registered, projectRoot) {
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

// EquivalentProject is one registry entry for a directory.
type EquivalentProject struct {
	// Dir is the project's state directory.
	Dir string
	// Registered is the registered spelling while it still names the root,
	// otherwise the pinned root. Callers that go
	// on to Resolve must pass it, not their own spelling, or Resolve creates a
	// second project.
	Registered string
}

// LookupEquivalent finds every registry entry for projectRoot, including ones
// registered under another spelling of the same directory: a symlinked
// checkout, or macOS's /var for /private/var. It never creates anything.
//
// All of them, not the first: one checkout registered under two spellings has
// two manifests, and a caller that picked one by directory order could miss
// the records it was looking for.
func LookupEquivalent(projectRoot string) []EquivalentProject {
	want := resolvedPath(projectRoot)
	if want == "" {
		return nil
	}
	projectsDir := filepath.Join(config.StateDir(), "projects")
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return nil
	}
	var matches []EquivalentProject
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(projectsDir, e.Name())
		meta, err := readMeta(dir)
		if err != nil || meta.Path == "" {
			continue
		}
		if registered, valid := registrationRoot(meta); valid && sameProjectRoot(registered, projectRoot) {
			spelling := meta.Path
			if !sameProjectRoot(spelling, registered) {
				spelling = registered
			}
			matches = append(matches, EquivalentProject{Dir: dir, Registered: spelling})
		}
	}
	return matches
}

func resolvedPath(path string) string {
	if path == "" {
		return ""
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	// A removed checkout still has a durable lexical identity. Resolve the
	// existing ancestors so /var and /private/var do not diverge after a move.
	parent, suffix := filepath.Dir(path), filepath.Base(path)
	for parent != filepath.Dir(parent) {
		if resolved, err := filepath.EvalSymlinks(parent); err == nil {
			return filepath.Join(resolved, suffix)
		}
		suffix = filepath.Join(filepath.Base(parent), suffix)
		parent = filepath.Dir(parent)
	}
	return filepath.Clean(path)
}

// sameProjectRoot preserves lexical identity for missing registrations and
// recognizes case aliases only when the filesystem proves they are the same
// directory. Lowercasing would merge distinct roots on case-sensitive volumes.
func sameProjectRoot(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	if resolvedPath(a) == resolvedPath(b) {
		return true
	}
	first, err := os.Stat(a)
	if err != nil {
		return false
	}
	second, err := os.Stat(b)
	return err == nil && first.IsDir() && second.IsDir() && os.SameFile(first, second)
}

// registrationRoot keeps state anchored to its original root even when its
// alias is removed or repointed. It must not lend that manifest to the new
// checkout. Paths, rather than inode
// numbers, remain the durable identity, so normal process restarts are portable.
func registrationRoot(meta projectMeta) (string, bool) {
	if meta.ResolvedRoot == "" {
		return meta.Path, meta.Path != ""
	}
	return meta.ResolvedRoot, true
}

// findByMeta scans all subdirectories in projectsDir looking for one
// whose meta.json path matches projectRoot. Returns the directory path
// and true if found.
func findByMeta(projectsDir, projectRoot string) (string, bool) {
	entries, err := os.ReadDir(projectsDir)
	if err != nil {
		return "", false
	}
	equivalent := ""
	for _, e := range entries {
		if !e.IsDir() {
			continue
		}
		dir := filepath.Join(projectsDir, e.Name())
		meta, err := readMeta(dir)
		if err != nil {
			continue
		}
		registered, valid := registrationRoot(meta)
		if !valid || !sameProjectRoot(registered, projectRoot) {
			continue
		}
		if meta.Path == projectRoot {
			return dir, true
		}
		if equivalent == "" {
			equivalent = dir
		}
	}
	// Keep the exact spelling's priority for old split registrations, but
	// never allocate another manifest for an alias of a registered root.
	return equivalent, equivalent != ""
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

	meta := projectMeta{Path: projectRoot, ResolvedRoot: resolvedPath(projectRoot)}
	if err := writeProjectMeta(dir, meta); err != nil {
		return "", err
	}
	return dir, nil
}

// Write atomically because inventories read metadata without the allocation lock.
func writeProjectMeta(dir string, meta projectMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshaling meta: %w", err)
	}
	file, err := os.CreateTemp(dir, ".meta-*")
	if err != nil {
		return fmt.Errorf("creating meta.json: %w", err)
	}
	defer func() { _ = os.Remove(file.Name()) }()
	if err := file.Chmod(0644); err != nil {
		_ = file.Close()
		return err
	}
	_, writeErr := file.Write(data)
	closeErr := file.Close()
	if writeErr != nil {
		return writeErr
	}
	if closeErr != nil {
		return closeErr
	}
	if err := os.Rename(file.Name(), filepath.Join(dir, "meta.json")); err != nil {
		return fmt.Errorf("writing meta.json: %w", err)
	}
	return nil
}
