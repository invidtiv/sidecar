package projectdir

import (
	"os"
	"path/filepath"
	"sync"

	"github.com/marcus/sidecar/internal/config"
)

// WorktreeIndex answers LookupWorktreeWithBase for many worktrees from one
// read of the state tree. A single lookup rescans every project's meta.json
// and then every worktree record of the matching project, so a caller that
// looks up each of a few hundred worktrees re-read the same files tens of
// thousands of times. The index memoizes both scans per call site and returns
// exactly the directory the uncached lookup returns: project resolution keeps
// findByMeta's root identity and exact-spelling priority, and the first record in directory
// order that matches a path wins, as it does in the linear scan.
//
// An index is a snapshot. Build one per catalog or command, never hold one
// across invocations; records written after first use are not seen. It is safe
// for concurrent use.
type WorktreeIndex struct {
	base string

	mu        sync.Mutex
	loaded    bool
	roots     []indexedProjectRoot
	projects  map[string]projectLookup
	worktrees map[string]map[string]string
}

// Canonical paths and filesystem identities are captured once per scan. In
// particular, stale registrations must not repeat EvalSymlinks' ancestor walk
// for every configured root a catalog authorizes.
type indexedProjectRoot struct {
	dir       string
	spelling  string
	canonical string
	info      os.FileInfo
}

type indexedPath struct {
	canonical string
	info      os.FileInfo
}

// pathSnapshot resolves each component once. A missing ancestor proves that
// its descendants are missing too, so stale sibling registrations share that
// answer instead of repeatedly walking all ancestors with EvalSymlinks.
type pathSnapshot map[string]indexedPath

func (paths pathSnapshot) resolve(path string) indexedPath {
	if path == "" {
		return indexedPath{}
	}
	abs, err := filepath.Abs(path)
	if err != nil {
		info, _ := os.Stat(path)
		return indexedPath{canonical: resolvedPath(path), info: info}
	}
	if cached, ok := paths[abs]; ok {
		return cached
	}
	parent := filepath.Dir(abs)
	canonical := abs
	if parent != abs {
		ancestor := paths.resolve(parent)
		canonical = filepath.Join(ancestor.canonical, filepath.Base(abs))
		if ancestor.info == nil || !ancestor.info.IsDir() {
			missing := indexedPath{canonical: canonical}
			paths[abs] = missing
			return missing
		}
	}
	info, err := os.Lstat(canonical)
	if err != nil {
		info = nil
	} else if info.Mode()&os.ModeSymlink != 0 {
		// Symlink targets may be absolute, relative or chains. Retain the
		// standard resolver's loop/broken-link behavior at this real seam.
		if target, err := filepath.EvalSymlinks(canonical); err == nil {
			canonical = filepath.Clean(target)
			info, _ = os.Stat(canonical)
		} else {
			info = nil
		}
	}
	result := indexedPath{canonical: canonical, info: info}
	paths[abs] = result
	return result
}

type projectLookup struct {
	dir string
	ok  bool
}

// NewWorktreeIndex returns an empty index over the state directory base.
func NewWorktreeIndex(base string) *WorktreeIndex {
	return &WorktreeIndex{base: base, projects: map[string]projectLookup{}, worktrees: map[string]map[string]string{}}
}

// NewStateWorktreeIndex indexes the configured state directory, the base
// LookupWorktree reads.
func NewStateWorktreeIndex() *WorktreeIndex { return NewWorktreeIndex(config.StateDir()) }

// Lookup is LookupWorktreeWithBase(index base, projectRoot, worktreePath).
func (x *WorktreeIndex) Lookup(projectRoot, worktreePath string) (string, bool) {
	normalized, normalizeErr := normalizePath(worktreePath)
	x.mu.Lock()
	defer x.mu.Unlock()
	project, seen := x.projects[projectRoot]
	if !seen {
		project = x.lookupProject(projectRoot)
		x.projects[projectRoot] = project
	}
	if !project.ok || normalizeErr != nil {
		return "", false
	}
	records, seen := x.worktrees[project.dir]
	if !seen {
		records = readWorktreeRecords(project.dir)
		x.worktrees[project.dir] = records
	}
	dir, ok := records[normalized]
	return dir, ok
}

// lookupProject runs under x.mu and follows findByMeta's ordered scan: an
// equivalent registration is a fallback, while an equivalent exact spelling
// wins even when it occurs later. Missing roots keep their lexical identity;
// existing case aliases match only when os.SameFile proves that identity.
func (x *WorktreeIndex) lookupProject(projectRoot string) projectLookup {
	if !x.loaded {
		x.loaded = true
		entries, _ := os.ReadDir(filepath.Join(x.base, "projects"))
		paths := make(pathSnapshot)
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			dir := filepath.Join(x.base, "projects", entry.Name())
			meta, err := readMeta(dir)
			if err != nil {
				continue
			}
			root, valid := registrationRoot(meta)
			if !valid {
				continue
			}
			identity := paths.resolve(root)
			x.roots = append(x.roots, indexedProjectRoot{dir: dir, spelling: meta.Path, canonical: identity.canonical, info: identity.info})
		}
	}
	if projectRoot == "" {
		return projectLookup{}
	}
	// A newly queried spelling gets fresh filesystem identity: sharing the
	// registration snapshot here could lend an old manifest through an alias
	// retargeted after another root populated the index.
	identity := make(pathSnapshot).resolve(projectRoot)
	want, info := identity.canonical, identity.info
	var equivalent projectLookup
	for _, root := range x.roots {
		matches := root.canonical == want
		if !matches && root.info != nil && info != nil && root.info.IsDir() && info.IsDir() && os.SameFile(root.info, info) {
			// Filesystem identity proves case aliases only while the anchored
			// registration still names that directory. A captured inode must
			// not lend its manifest to a directory moved to another path.
			current, err := os.Stat(root.canonical)
			matches = err == nil && current.IsDir() && os.SameFile(current, info)
		}
		if !matches {
			continue
		}
		if root.spelling == projectRoot {
			return projectLookup{dir: root.dir, ok: true}
		}
		if !equivalent.ok {
			equivalent = projectLookup{dir: root.dir, ok: true}
		}
	}
	return equivalent
}

// readWorktreeRecords maps each valid record's path to its directory, keeping
// the first record in directory order for a path, which is the one the linear
// scan in LookupWorktreeWithBase would stop at.
func readWorktreeRecords(projectDir string) map[string]string {
	records := map[string]string{}
	entries, err := os.ReadDir(filepath.Join(projectDir, "worktrees"))
	if err != nil {
		return records
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(projectDir, "worktrees", entry.Name())
		meta, err := readWorktreeMeta(dir)
		if err != nil || meta.Key != pathKey(meta.Path) {
			continue
		}
		if _, exists := records[meta.Path]; !exists {
			records[meta.Path] = dir
		}
	}
	return records
}
