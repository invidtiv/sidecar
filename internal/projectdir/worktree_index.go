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
// exactly the directory the uncached lookup returns: the project resolution is
// the same findByMeta, and within a project the first record in directory
// order that matches a path wins, as it does in the linear scan.
//
// An index is a snapshot. Build one per catalog or command, never hold one
// across invocations; records written after first use are not seen. It is safe
// for concurrent use.
type WorktreeIndex struct {
	base string

	mu        sync.Mutex
	projects  map[string]projectLookup
	worktrees map[string]map[string]string
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
		project.dir, project.ok = findByMeta(filepath.Join(x.base, "projects"), projectRoot)
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
