package projectdir

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

// The index returns exactly what LookupWorktreeWithBase returns for every
// query shape: registered worktrees, a symlinked spelling, a path recorded
// twice (first record in directory order wins), a record whose key does not
// match its path, an unregistered worktree and an unknown project.
func TestWorktreeIndexMatchesLinearLookup(t *testing.T) {
	base := t.TempDir()
	work := t.TempDir()
	project := filepath.Join(work, "repo")
	worktreeA := filepath.Join(work, "repo-a")
	worktreeB := filepath.Join(work, "repo-b")
	for _, dir := range []string{project, worktreeA, worktreeB} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(work, "alias-a")
	if err := os.Symlink(worktreeA, alias); err != nil {
		t.Fatal(err)
	}
	for _, wt := range []string{worktreeA, worktreeB} {
		if _, err := WorktreeDirWithBase(base, project, wt); err != nil {
			t.Fatal(err)
		}
	}
	projectDir, ok := findByMeta(filepath.Join(base, "projects"), project)
	if !ok {
		t.Fatal("project not registered")
	}
	normalizedA, _ := normalizePath(worktreeA)
	normalizedB, _ := normalizePath(worktreeB)
	writeRecord := func(name, path, key string) {
		dir := filepath.Join(projectDir, "worktrees", name)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(worktreeMeta{Path: path, Key: key})
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// Sorts before the real record, so the linear scan stops here first.
	writeRecord("0-duplicate-a", normalizedA, pathKey(normalizedA))
	// A wrong key never matches.
	writeRecord("0-bad-key-b", normalizedB, "not-the-key")

	index := NewWorktreeIndex(base)
	queries := []struct{ root, path string }{
		{project, worktreeA}, {project, worktreeB}, {project, alias},
		{project, filepath.Join(work, "unregistered")}, {filepath.Join(work, "elsewhere"), worktreeA},
	}
	for _, q := range queries {
		wantDir, wantOK := LookupWorktreeWithBase(base, q.root, q.path)
		for range 2 { // cold, then memoized
			gotDir, gotOK := index.Lookup(q.root, q.path)
			if gotDir != wantDir || gotOK != wantOK {
				t.Fatalf("Lookup(%q, %q) = %q, %v; linear = %q, %v", q.root, q.path, gotDir, gotOK, wantDir, wantOK)
			}
		}
	}
	if dir, _ := index.Lookup(project, worktreeA); filepath.Base(dir) != "0-duplicate-a" {
		t.Fatalf("first record in directory order should win, got %q", dir)
	}
}
