package projectdir

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
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

func writeIndexedProject(t *testing.T, base, name, spelling, resolved, worktree string) string {
	t.Helper()
	dir := filepath.Join(base, "projects", name)
	if err := os.MkdirAll(filepath.Join(dir, "worktrees", "record"), 0755); err != nil {
		t.Fatal(err)
	}
	meta, _ := json.Marshal(projectMeta{Path: spelling, ResolvedRoot: resolved})
	if err := os.WriteFile(filepath.Join(dir, "meta.json"), meta, 0600); err != nil {
		t.Fatal(err)
	}
	path, err := normalizePath(worktree)
	if err != nil {
		t.Fatal(err)
	}
	record, _ := json.Marshal(worktreeMeta{Path: path, Key: pathKey(path)})
	if err := os.WriteFile(filepath.Join(dir, "worktrees", "record", "meta.json"), record, 0600); err != nil {
		t.Fatal(err)
	}
	return filepath.Join(dir, "worktrees", "record")
}

func TestWorktreeIndexPreservesExactSpellingPriorityAcrossAliases(t *testing.T) {
	base, work := t.TempDir(), t.TempDir()
	project := filepath.Join(work, "repo")
	if err := os.Mkdir(project, 0755); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(work, "alias")
	if err := os.Symlink(project, alias); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(work, "worktree")
	first := writeIndexedProject(t, base, "a-first-alias", alias, resolvedPath(project), wt)
	exact := writeIndexedProject(t, base, "z-exact-root", project, resolvedPath(project), wt)
	index := NewWorktreeIndex(base)
	for _, query := range []struct{ root, want string }{{project, exact}, {alias, first}} {
		got, ok := index.Lookup(query.root, wt)
		linear, linearOK := LookupWorktreeWithBase(base, query.root, wt)
		if !ok || got != query.want || got != linear || ok != linearOK {
			t.Fatalf("root %q: index=%q,%v linear=%q,%v want=%q", query.root, got, ok, linear, linearOK, query.want)
		}
	}
}

func TestWorktreeIndexRetargetedAliasKeepsOriginalRegistration(t *testing.T) {
	base, work := t.TempDir(), t.TempDir()
	first, second := filepath.Join(work, "first"), filepath.Join(work, "second")
	for _, root := range []string{first, second} {
		if err := os.Mkdir(root, 0755); err != nil {
			t.Fatal(err)
		}
	}
	alias := filepath.Join(work, "alias")
	if err := os.Symlink(first, alias); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(work, "worktree")
	registered := writeIndexedProject(t, base, "original", alias, resolvedPath(first), wt)
	if err := os.Remove(alias); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(second, alias); err != nil {
		t.Fatal(err)
	}
	index := NewWorktreeIndex(base)
	for _, root := range []string{first, second, alias} {
		got, ok := index.Lookup(root, wt)
		want, wantOK := LookupWorktreeWithBase(base, root, wt)
		if got != want || ok != wantOK || root == first && (!ok || got != registered) || root != first && ok {
			t.Fatalf("retargeted root %q: index=%q,%v linear=%q,%v", root, got, ok, want, wantOK)
		}
	}
	if err := os.Remove(first); err != nil {
		t.Fatal(err)
	}
	index = NewWorktreeIndex(base)
	if got, ok := index.Lookup(first, wt); !ok || got != registered {
		t.Fatalf("deleted original registration lost lexical identity: %q,%v", got, ok)
	}
}

func TestWorktreeIndexCaseAliasesRequireFilesystemIdentity(t *testing.T) {
	base, work := t.TempDir(), t.TempDir()
	root := filepath.Join(work, "MiXeD-Root")
	if err := os.Mkdir(root, 0755); err != nil {
		t.Fatal(err)
	}
	wt := filepath.Join(work, "worktree")
	writeIndexedProject(t, base, "registration", root, resolvedPath(root), wt)
	alias := filepath.Join(work, strings.ToLower(filepath.Base(root)))
	for _, path := range []string{root, alias} {
		got, ok := NewWorktreeIndex(base).Lookup(path, wt)
		want, wantOK := LookupWorktreeWithBase(base, path, wt)
		if got != want || ok != wantOK {
			t.Fatalf("case variant %q: index=%q,%v linear=%q,%v", path, got, ok, want, wantOK)
		}
	}
}

func TestWorktreeIndexProjectMetadataIsOneInvocationSnapshot(t *testing.T) {
	base, work := t.TempDir(), t.TempDir()
	wt := filepath.Join(work, "worktree")
	first, second := filepath.Join(work, "first"), filepath.Join(work, "second")
	writeIndexedProject(t, base, "first", first, "", wt)
	index := NewWorktreeIndex(base)
	if _, ok := index.Lookup(first, wt); !ok {
		t.Fatal("initial registration missing")
	}
	added := writeIndexedProject(t, base, "second", second, "", wt)
	if got, ok := index.Lookup(second, wt); ok {
		t.Fatalf("existing snapshot read a newly written project: %q", got)
	}
	if got, ok := NewWorktreeIndex(base).Lookup(second, wt); !ok || got != added {
		t.Fatalf("new invocation missed new metadata: %q,%v", got, ok)
	}
}
