package projectdir

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestResolveWithBase_NewProject(t *testing.T) {
	base := t.TempDir()
	projectRoot := "/Users/alice/Projects/myapp"

	dir, err := resolveWithBase(base, projectRoot)
	if err != nil {
		t.Fatalf("resolveWithBase: %v", err)
	}

	// Directory should exist.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatalf("expected directory to exist: %s", dir)
	}

	// Slug should be "myapp".
	expectedSlug := "myapp"
	if filepath.Base(dir) != expectedSlug {
		t.Errorf("directory slug = %q, want %q", filepath.Base(dir), expectedSlug)
	}

	// meta.json should contain the project path.
	metaPath := filepath.Join(dir, "meta.json")
	data, err := os.ReadFile(metaPath)
	if err != nil {
		t.Fatalf("reading meta.json: %v", err)
	}

	var meta projectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parsing meta.json: %v", err)
	}
	if meta.Path != projectRoot {
		t.Errorf("meta.Path = %q, want %q", meta.Path, projectRoot)
	}
}

func TestWorktreeDirWithBase_LegacyMigrationIsAdditiveAndAmbiguousCollisionRefused(t *testing.T) {
	base := t.TempDir()
	repo := filepath.Join(t.TempDir(), "repo")
	runGit(t, "", "init", "-b", "main", repo)
	runGit(t, repo, "config", "user.email", "test@example.com")
	runGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, repo, "add", "README")
	runGit(t, repo, "commit", "-m", "init")
	wt := filepath.Join(t.TempDir(), "feature", "auth")
	runGit(t, repo, "worktree", "add", "-b", "feature/auth", wt)

	projectDir, err := resolveWithBase(base, repo)
	if err != nil {
		t.Fatal(err)
	}
	legacy := filepath.Join(projectDir, "worktrees", "auth")
	if err := os.MkdirAll(legacy, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy, "task"), []byte("td-legacy"), 0644); err != nil {
		t.Fatal(err)
	}
	migrated, err := worktreeDirWithBase(base, repo, wt)
	if err != nil {
		t.Fatalf("unambiguous migration: %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(migrated, "task")); err != nil || string(got) != "td-legacy" {
		t.Fatalf("migrated task = %q, %v", got, err)
	}
	if _, err := os.Stat(filepath.Join(legacy, "task")); err != nil {
		t.Fatalf("legacy source was modified: %v", err)
	}

	other := filepath.Join(t.TempDir(), "fix", "auth")
	runGit(t, repo, "worktree", "add", "-b", "fix/auth", other)
	base2 := t.TempDir()
	projectDir2, err := resolveWithBase(base2, repo)
	if err != nil {
		t.Fatal(err)
	}
	legacy2 := filepath.Join(projectDir2, "worktrees", "auth")
	if err := os.MkdirAll(legacy2, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(legacy2, "pr"), []byte("legacy"), 0644); err != nil {
		t.Fatal(err)
	}
	if _, err := worktreeDirWithBase(base2, repo, wt); err == nil || !strings.Contains(err.Error(), "ambiguous legacy") {
		t.Fatalf("ambiguous migration error = %v", err)
	}
	if got, err := os.ReadFile(filepath.Join(legacy2, "pr")); err != nil || string(got) != "legacy" {
		t.Fatalf("ambiguous legacy state changed: %q, %v", got, err)
	}
}

func runGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}

func TestResolveWithBase_ExistingProject(t *testing.T) {
	base := t.TempDir()
	projectRoot := "/Users/bob/code/webapp"

	// First resolve creates the directory.
	dir1, err := resolveWithBase(base, projectRoot)
	if err != nil {
		t.Fatalf("first resolve: %v", err)
	}

	// Second resolve should return the same directory.
	dir2, err := resolveWithBase(base, projectRoot)
	if err != nil {
		t.Fatalf("second resolve: %v", err)
	}

	if dir1 != dir2 {
		t.Errorf("second resolve returned %q, want %q", dir2, dir1)
	}
}

func TestResolveWithBase_SlugCollision(t *testing.T) {
	base := t.TempDir()
	projectA := "/Users/alice/work/myapp"
	projectB := "/Users/alice/personal/myapp"

	dirA, err := resolveWithBase(base, projectA)
	if err != nil {
		t.Fatalf("resolve project A: %v", err)
	}

	dirB, err := resolveWithBase(base, projectB)
	if err != nil {
		t.Fatalf("resolve project B: %v", err)
	}

	if dirA == dirB {
		t.Errorf("collision: both projects resolved to %q", dirA)
	}

	// First should be "myapp", second should be "myapp-2".
	if filepath.Base(dirA) != "myapp" {
		t.Errorf("project A slug = %q, want %q", filepath.Base(dirA), "myapp")
	}
	if filepath.Base(dirB) != "myapp-2" {
		t.Errorf("project B slug = %q, want %q", filepath.Base(dirB), "myapp-2")
	}

	// Both should have correct meta.json.
	checkMeta(t, dirA, projectA)
	checkMeta(t, dirB, projectB)
}

func TestResolveWithBase_FindsExistingByMeta(t *testing.T) {
	base := t.TempDir()
	projectRoot := "/Users/carol/code/api"

	// Pre-create the directory with a different slug name (simulating
	// a collision scenario where this project got slug "api-2").
	projectsDir := filepath.Join(base, "projects")
	slugDir := filepath.Join(projectsDir, "api-2")
	if err := os.MkdirAll(slugDir, 0755); err != nil {
		t.Fatal(err)
	}
	meta := projectMeta{Path: projectRoot}
	data, _ := json.Marshal(meta)
	if err := os.WriteFile(filepath.Join(slugDir, "meta.json"), data, 0644); err != nil {
		t.Fatal(err)
	}

	// Resolve should find the existing directory by scanning meta.json files.
	dir, err := resolveWithBase(base, projectRoot)
	if err != nil {
		t.Fatalf("resolveWithBase: %v", err)
	}

	if dir != slugDir {
		t.Errorf("resolved to %q, want %q (existing dir with matching meta)", dir, slugDir)
	}
}

func TestWorktreeDirWithBase(t *testing.T) {
	base := t.TempDir()
	projectRoot := "/Users/dave/Projects/repo"
	worktreePath := "/Users/dave/Projects/repo-feature"

	dir, err := worktreeDirWithBase(base, projectRoot, worktreePath)
	if err != nil {
		t.Fatalf("worktreeDirWithBase: %v", err)
	}

	// Should be a subdirectory of the project dir.
	projectDir, err := resolveWithBase(base, projectRoot)
	if err != nil {
		t.Fatal(err)
	}

	rel, err := filepath.Rel(projectDir, dir)
	if err != nil {
		t.Fatalf("Rel: %v", err)
	}

	if !strings.HasPrefix(rel, filepath.Join("worktrees", "repo-feature-")) {
		t.Errorf("worktree relative path = %q, want collision-safe repo-feature slug", rel)
	}

	// Directory should exist.
	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatalf("expected worktree directory to exist: %s", dir)
	}
	meta, err := readWorktreeMeta(dir)
	if err != nil {
		t.Fatalf("read worktree meta: %v", err)
	}
	if meta.Path != filepath.Clean(worktreePath) {
		t.Fatalf("meta path = %q, want %q", meta.Path, filepath.Clean(worktreePath))
	}
}

func TestWorktreeDirWithBase_SameBasenameIndependentAcrossRestart(t *testing.T) {
	base := t.TempDir()
	projectRoot := filepath.Join(t.TempDir(), "repo")
	paths := []string{filepath.Join(t.TempDir(), "feature", "auth"), filepath.Join(t.TempDir(), "fix", "auth")}

	dirs := make([]string, len(paths))
	for i, path := range paths {
		dir, err := worktreeDirWithBase(base, projectRoot, path)
		if err != nil {
			t.Fatal(err)
		}
		dirs[i] = dir
		if err := os.WriteFile(filepath.Join(dir, "task"), []byte(fmt.Sprintf("td-%d", i)), 0644); err != nil {
			t.Fatal(err)
		}
	}
	if dirs[0] == dirs[1] {
		t.Fatalf("same-basename worktrees collided at %s", dirs[0])
	}
	for i, path := range paths {
		dir, err := worktreeDirWithBase(base, projectRoot, path)
		if err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(filepath.Join(dir, "task"))
		if err != nil || string(got) != fmt.Sprintf("td-%d", i) {
			t.Fatalf("restart metadata for %q = %q, %v", path, got, err)
		}
	}
}

func TestSanitizeSlug(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"myapp", "myapp"},
		{"my-app", "my-app"},
		{"my_app", "my_app"},
		{"My.App", "My.App"},
		{"", "_"},
		{".", "_"},
		{"..", "_"},
		{"foo/bar", "foobar"},
		{"foo\\bar", "foobar"},
		{"a b c", "a b c"},
		{"...hidden", "...hidden"},
		{string([]byte{0x00, 0x01}), "_"},
	}

	for _, tc := range tests {
		got := sanitizeSlug(tc.input)
		if got != tc.want {
			t.Errorf("sanitizeSlug(%q) = %q, want %q", tc.input, got, tc.want)
		}
	}
}

func TestResolveWithBase_MultipleCollisions(t *testing.T) {
	base := t.TempDir()

	// Create 3 projects all with basename "app".
	projects := []string{
		"/a/app",
		"/b/app",
		"/c/app",
	}
	expectedSlugs := []string{"app", "app-2", "app-3"}

	dirs := make([]string, len(projects))
	for i, p := range projects {
		d, err := resolveWithBase(base, p)
		if err != nil {
			t.Fatalf("resolve %q: %v", p, err)
		}
		dirs[i] = d
	}

	for i, d := range dirs {
		if filepath.Base(d) != expectedSlugs[i] {
			t.Errorf("project %q: slug = %q, want %q", projects[i], filepath.Base(d), expectedSlugs[i])
		}
	}

	// All dirs should be unique.
	seen := make(map[string]bool)
	for _, d := range dirs {
		if seen[d] {
			t.Errorf("duplicate directory: %s", d)
		}
		seen[d] = true
	}
}

// checkMeta verifies the meta.json in dir matches the expected path.
func checkMeta(t *testing.T, dir, expectedPath string) {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "meta.json"))
	if err != nil {
		t.Fatalf("reading meta.json in %s: %v", dir, err)
	}
	var meta projectMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		t.Fatalf("parsing meta.json in %s: %v", dir, err)
	}
	if meta.Path != expectedPath {
		t.Errorf("meta.Path in %s = %q, want %q", dir, meta.Path, expectedPath)
	}
}

// LookupAll exists so a caller needing the whole configured set pays one
// directory scan instead of one per root. It must agree with the Lookup it
// replaces on every case that matters — a difference here would silently point
// a caller at the wrong project's state.
func TestLookupAllWithBase_AgreesWithFindByMeta(t *testing.T) {
	base := t.TempDir()
	projectsDir := filepath.Join(base, "projects")
	register := func(slug, root string) {
		t.Helper()
		dir := filepath.Join(projectsDir, slug)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		data, _ := json.Marshal(projectMeta{Path: root})
		if err := os.WriteFile(filepath.Join(dir, "meta.json"), data, 0644); err != nil {
			t.Fatal(err)
		}
	}

	alpha := "/Users/erin/code/alpha"
	beta := "/Users/erin/code/beta"
	register("alpha", alpha)
	register("beta-7", beta)
	// Noise the scan has to skip: a registration for a root nobody asked about,
	// a directory with no meta.json, and a plain file.
	register("gamma", "/Users/erin/code/gamma")
	if err := os.MkdirAll(filepath.Join(projectsDir, "orphan"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(projectsDir, "stray.json"), []byte("{}"), 0644); err != nil {
		t.Fatal(err)
	}

	missing := "/Users/erin/code/never-opened"
	found := LookupAllWithBase(base, []string{alpha, beta, missing})
	if len(found) != 2 {
		t.Fatalf("resolved %d roots, want 2: %#v", len(found), found)
	}
	if _, ok := found[missing]; ok {
		t.Fatal("an unregistered root must be absent, not mapped to an empty path")
	}
	for _, root := range []string{alpha, beta} {
		want, ok := findByMeta(projectsDir, root)
		if !ok {
			t.Fatalf("findByMeta could not resolve %s", root)
		}
		if found[root] != want {
			t.Errorf("LookupAllWithBase(%s) = %q, findByMeta = %q", root, found[root], want)
		}
	}

	// Two directories claiming one root: both must pick the same winner, or the
	// batched form would watch a different manifest than the rest of the app
	// reads. ReadDir is sorted, so first-registered-by-name wins in both.
	register("alpha-duplicate", alpha)
	if got, want := LookupAllWithBase(base, []string{alpha})[alpha], mustFind(t, projectsDir, alpha); got != want {
		t.Errorf("duplicate meta: LookupAllWithBase = %q, findByMeta = %q", got, want)
	}

	if got := LookupAllWithBase(base, nil); got != nil {
		t.Errorf("no roots = %#v, want nil", got)
	}
	if got := LookupAllWithBase(filepath.Join(base, "absent"), []string{alpha}); len(got) != 0 {
		t.Errorf("missing projects dir = %#v, want empty", got)
	}
}

func mustFind(t *testing.T, projectsDir, root string) string {
	t.Helper()
	dir, ok := findByMeta(projectsDir, root)
	if !ok {
		t.Fatalf("findByMeta could not resolve %s", root)
	}
	return dir
}

// TestLookupAllMatchesASymlinkedProjectRoot pins the canonical comparison.
//
// A configured path and the meta.Path written at registration come from
// different places and agree only by luck. Driven against a real host, a
// project configured as /tmp/... and registered as /private/tmp/... reported
// itself unregistered while its shells were being listed, which silently
// disabled the shells.json freshness watch that reads this.
func TestLookupAllMatchesASymlinkedProjectRoot(t *testing.T) {
	base := t.TempDir()
	real := filepath.Join(base, "real-project")
	if err := os.MkdirAll(real, 0o755); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(base, "linked-project")
	if err := os.Symlink(real, link); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}

	state := filepath.Join(base, "state")
	dir := filepath.Join(state, "projects", "p1")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	// Registered under the resolved path, as registration canonicalises.
	if err := os.WriteFile(filepath.Join(dir, "meta.json"),
		[]byte(`{"path":`+strconv.Quote(real)+`}`), 0o644); err != nil {
		t.Fatal(err)
	}

	// Asked for by the symlinked path, as a config file would carry it.
	got := LookupAllWithBase(state, []string{link})
	if got[link] != dir {
		t.Fatalf("a project registered at its resolved path was not found via its symlinked path: got %v, want %s -> %s", got, link, dir)
	}
}
