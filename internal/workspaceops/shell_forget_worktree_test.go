package workspaceops

import (
	"context"
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
)

// Everything here runs against the isolated tmux server and isolated state
// tree that TestMain pins (see main_test.go). No real repository, worktree,
// branch, tmux session, or shells.json is reachable from this file.

func TestPathRootedIn(t *testing.T) {
	base := t.TempDir()
	wt := filepath.Join(base, "feature")

	// A root that really exists on disk. On macOS t.TempDir() lives under
	// /var, a symlink to /private/var, so an existing directory and a
	// no-longer-existing path beneath it resolve differently unless
	// canonicalisation handles the missing tail — which is exactly the state a
	// shell's recorded subdirectory is in once its worktree has been removed.
	existing := filepath.Join(base, "existing")
	if err := os.MkdirAll(existing, 0o755); err != nil {
		t.Fatal(err)
	}

	cases := []struct {
		name string
		path string
		root string
		want bool
	}{
		{"the worktree itself", wt, wt, true},
		{"a subdirectory", filepath.Join(wt, "cmd", "app"), wt, true},
		{"trailing separator on root", filepath.Join(wt, "cmd"), wt + string(filepath.Separator), true},
		{"unclean path", filepath.Join(wt, "cmd", "..", "internal"), wt, true},

		// The near misses this rule exists for.
		{"sibling with a prefix name", filepath.Join(base, "feature-2"), wt, false},
		{"sibling with a longer prefix name", filepath.Join(base, "featureXYZ", "src"), wt, false},
		{"the parent directory", base, wt, false},
		{"a sibling entirely", filepath.Join(base, "other"), wt, false},
		{"an unrelated tree", filepath.Join(t.TempDir(), "feature"), wt, false},

		{"empty path", "", wt, false},

		{"nonexistent subdir of an existing root", filepath.Join(existing, "gone", "deeper"), existing, true},
		{"existing root, nonexistent sibling", filepath.Join(base, "existing-2", "x"), existing, false},
		{"empty root", wt, "", false},
		{"whitespace root", wt, "   ", false},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := PathRootedIn(tc.path, tc.root); got != tc.want {
				t.Errorf("PathRootedIn(%q, %q) = %v, want %v", tc.path, tc.root, got, tc.want)
			}
		})
	}
}

// TestShellsRootedInSkipsNearMisses is the selection rule that keeps a worktree
// delete from reaching outside itself. A plain prefix match would take the
// "feature-2" shell with it; a parent match would take the repo-root shell.
func TestShellsRootedInSkipsNearMisses(t *testing.T) {
	base := t.TempDir()
	wt := filepath.Join(base, "feature")

	defs := []shellstate.Definition{
		{TmuxName: "in-worktree", WorkDir: wt},
		{TmuxName: "in-subdir", WorkDir: filepath.Join(wt, "internal")},
		{TmuxName: "sibling-prefix", WorkDir: filepath.Join(base, "feature-2")},
		{TmuxName: "sibling-other", WorkDir: filepath.Join(base, "main")},
		{TmuxName: "parent", WorkDir: base},
		{TmuxName: "no-workdir", WorkDir: ""},
	}

	var got []string
	for _, def := range ShellsRootedIn(defs, wt) {
		got = append(got, def.TmuxName)
	}
	sort.Strings(got)

	want := []string{"in-subdir", "in-worktree"}
	if len(got) != len(want) {
		t.Fatalf("selected %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("selected %v, want %v", got, want)
		}
	}
}

// TestShellsRootedInIgnoresUnknownWorkDir states the conservative choice
// explicitly: a pre-td-4819be entry that does not record where it lives is not
// evidence that it lives here, so it is left for the liveness reaper.
func TestShellsRootedInIgnoresUnknownWorkDir(t *testing.T) {
	wt := t.TempDir()
	defs := []shellstate.Definition{{TmuxName: "legacy", WorkDir: ""}}
	if got := ShellsRootedIn(defs, wt); len(got) != 0 {
		t.Fatalf("an entry with no recorded WorkDir was selected: %v", got)
	}
	if got := ShellsRootedIn(defs, ""); len(got) != 0 {
		t.Fatalf("an empty root selected %v; it must select nothing", got)
	}
}

// shellManifestPath returns the isolated manifest for a throwaway project.
func shellManifestPath(t *testing.T, projectRoot string) string {
	t.Helper()
	dir, err := projectdir.Resolve(projectRoot)
	if err != nil {
		t.Fatalf("resolve project dir: %v", err)
	}
	return filepath.Join(dir, "shells.json")
}

// recordShell writes one manifest row through shellstate, never by touching
// shells.json directly.
func recordShell(t *testing.T, projectRoot, tmuxName, displayName, workDir string) {
	t.Helper()
	err := shellstate.AddAtPath(shellManifestPath(t, projectRoot), shellstate.Definition{
		TmuxName:    tmuxName,
		DisplayName: displayName,
		CreatedAt:   time.Now(),
		WorkDir:     workDir,
	})
	if err != nil {
		t.Fatalf("record shell %s: %v", tmuxName, err)
	}
}

func manifestNames(t *testing.T, projectRoot string) []string {
	t.Helper()
	defs, err := shellstate.ListAtPath(shellManifestPath(t, projectRoot))
	if err != nil {
		t.Fatalf("list shells: %v", err)
	}
	var names []string
	for _, def := range defs {
		names = append(names, def.TmuxName)
	}
	sort.Strings(names)
	return names
}

func assertNames(t *testing.T, got, want []string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("manifest holds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manifest holds %v, want %v", got, want)
		}
	}
}

// TestForgetShellsInWorktreeForgetsOnlyRootedShells is td-f017b9's core claim,
// exercised end-to-end against a real (isolated) manifest.
func TestForgetShellsInWorktreeForgetsOnlyRootedShells(t *testing.T) {
	base := t.TempDir()
	projectRoot := filepath.Join(base, "repo")
	wt := filepath.Join(base, "feature")

	recordShell(t, projectRoot, "sidecar-sh-inside", "Shell 1", wt)
	recordShell(t, projectRoot, "sidecar-sh-nested", "Shell 2", filepath.Join(wt, "internal"))
	recordShell(t, projectRoot, "sidecar-sh-sibling", "Shell 3", filepath.Join(base, "feature-2"))
	recordShell(t, projectRoot, "sidecar-sh-parent", "Shell 4", projectRoot)
	recordShell(t, projectRoot, "sidecar-sh-legacy", "Shell 5", "")

	if err := ForgetShellsInWorktree(projectRoot, wt); err != nil {
		t.Fatalf("ForgetShellsInWorktree: %v", err)
	}

	assertNames(t, manifestNames(t, projectRoot), []string{
		"sidecar-sh-legacy", "sidecar-sh-parent", "sidecar-sh-sibling",
	})
}

// TestForgetShellsInWorktreeKillsTheirSessions pins the decision to close the
// tmux sessions, not merely drop the rows: a shell whose working directory has
// been deleted is orphaned exactly as the worktree session was, and the row is
// the last record that it exists.
func TestForgetShellsInWorktreeKillsTheirSessions(t *testing.T) {
	if !TmuxInstalled() {
		t.Skip("tmux not installed")
	}
	base := t.TempDir()
	projectRoot := filepath.Join(base, "repo")
	wt := filepath.Join(base, "feature")
	keep := filepath.Join(base, "feature-2")
	for _, dir := range []string{projectRoot, wt, keep} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}

	// startThrowawaySession registers its own cleanup on the isolated server.
	startThrowawaySession(t, "sidecar-sh-doomed", wt)
	startThrowawaySession(t, "sidecar-sh-survivor", keep)
	recordShell(t, projectRoot, "sidecar-sh-doomed", "Doomed", wt)
	recordShell(t, projectRoot, "sidecar-sh-survivor", "Survivor", keep)

	if err := ForgetShellsInWorktree(projectRoot, wt); err != nil {
		t.Fatalf("ForgetShellsInWorktree: %v", err)
	}

	if SessionExists("sidecar-sh-doomed") {
		t.Error("the shell rooted in the deleted worktree is still running in a directory that is going away")
	}
	if !SessionExists("sidecar-sh-survivor") {
		t.Error("a shell in a sibling directory was killed by the worktree delete")
	}
	assertNames(t, manifestNames(t, projectRoot), []string{"sidecar-sh-survivor"})
}

// TestDeleteWorktreeRemovesTheShellsAndTheWorktreeInThatOrder covers the one
// shared operation every caller reaches: after it, neither the worktree nor its
// manifest rows remain. The shell step is inside DeleteWorktree rather than
// beside it, so no caller can remove a worktree without it.
func TestDeleteWorktreeRemovesTheShellsAndTheWorktreeInThatOrder(t *testing.T) {
	root := throwawayRepo(t)
	wt := filepath.Join(t.TempDir(), "feature")
	git(t, root, "worktree", "add", "-q", "-b", "feature", wt)

	recordShell(t, root, "sidecar-sh-in-wt", "In worktree", wt)
	recordShell(t, root, "sidecar-sh-in-repo", "In repo", root)

	// The shells are torn down before the git removal, so a shell is never
	// left running in a directory that has already gone.
	var order []string
	restore := deleteManagedShellForForget
	deleteManagedShellForForget = func(projectRoot, sessionName, namespace string) error {
		if _, err := os.Stat(wt); err != nil {
			t.Errorf("%s was closed after the worktree directory was removed", sessionName)
		}
		order = append(order, sessionName)
		return restore(projectRoot, sessionName, namespace)
	}
	t.Cleanup(func() { deleteManagedShellForForget = restore })

	err := DeleteWorktree(context.Background(), WorktreeRemoval{
		RepoPath: root, ProjectRoot: root, Path: wt, Branch: "feature", Force: true,
	})
	if err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}

	if len(order) != 1 || order[0] != "sidecar-sh-in-wt" {
		t.Errorf("closed %v, want only [sidecar-sh-in-wt]", order)
	}
	for _, path := range worktreePaths(t, root) {
		if path == wt {
			t.Fatalf("worktree %s survived the delete", wt)
		}
	}
	assertNames(t, manifestNames(t, root), []string{"sidecar-sh-in-repo"})
}

// A caller that has no owning project must not have the delete fail, and must
// not have anything forgotten.
func TestDeleteWorktreeIsInertWithoutAProjectRoot(t *testing.T) {
	root := throwawayRepo(t)
	wt := filepath.Join(t.TempDir(), "feature")
	git(t, root, "worktree", "add", "-q", "-b", "feature", wt)

	err := DeleteWorktree(context.Background(), WorktreeRemoval{
		RepoPath: root, Path: wt, Branch: "feature", Force: true,
	})
	if err != nil {
		t.Fatalf("DeleteWorktree with no project root: %v", err)
	}
}
