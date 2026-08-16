package workspaceops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// Every repository here is created fresh under t.TempDir(). Nothing in this
// file may reach a real checkout, worktree, branch, or remote.

func git(t *testing.T, dir string, args ...string) string {
	t.Helper()
	full := append([]string{
		"-c", "user.email=test@example.invalid",
		"-c", "user.name=Test",
		"-c", "commit.gpgsign=false",
		"-c", "init.defaultBranch=main",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

// throwawayRepo is a repository with one commit on main, living only for this
// test.
func throwawayRepo(t *testing.T) string {
	t.Helper()
	root := filepath.Join(t.TempDir(), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, root, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "README")
	git(t, root, "commit", "-q", "-m", "initial")
	return root
}

func worktreePaths(t *testing.T, root string) []string {
	t.Helper()
	var paths []string
	for _, line := range strings.Split(git(t, root, "worktree", "list", "--porcelain"), "\n") {
		if path, ok := strings.CutPrefix(line, "worktree "); ok {
			paths = append(paths, path)
		}
	}
	return paths
}

func TestDeleteWorktreeRemovesEvenWithUncommittedChanges(t *testing.T) {
	root := throwawayRepo(t)
	path := filepath.Join(filepath.Dir(root), "feature")
	git(t, root, "worktree", "add", "-q", path, "-b", "feature")

	// A dirty worktree makes plain `git worktree remove` fail; the fallback
	// forces it.
	if err := os.WriteFile(filepath.Join(path, "dirty"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	if err := DeleteWorktree(context.Background(), root, path, false); err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the working directory survived: %v", err)
	}
	if got := worktreePaths(t, root); len(got) != 1 {
		t.Fatalf("git still lists %v", got)
	}
}

func TestDeleteWorktreePrunesOneWhoseDirectoryIsGone(t *testing.T) {
	root := throwawayRepo(t)
	path := filepath.Join(filepath.Dir(root), "gone")
	git(t, root, "worktree", "add", "-q", path, "-b", "gone")
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	if err := DeleteWorktree(context.Background(), root, path, true); err != nil {
		t.Fatalf("DeleteWorktree(isMissing): %v", err)
	}
	if got := worktreePaths(t, root); len(got) != 1 {
		t.Fatalf("the prunable record survived: %v", got)
	}
}

func TestDeleteLocalBranchRefusesTheDefaultBranchAndForcesTheRest(t *testing.T) {
	root := throwawayRepo(t)
	git(t, root, "branch", "unmerged")
	// Give the branch a commit main does not have, so the safe delete fails
	// and only the forced one can succeed.
	git(t, root, "checkout", "-q", "unmerged")
	if err := os.WriteFile(filepath.Join(root, "extra"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", "extra")
	git(t, root, "commit", "-q", "-m", "extra")
	git(t, root, "checkout", "-q", "main")

	if err := DeleteLocalBranch(context.Background(), root, "main"); err == nil {
		t.Fatal("the default branch was deleted")
	} else if !strings.Contains(err.Error(), "refusing to delete main branch") {
		t.Fatalf("refusal read %q", err)
	}
	if !strings.Contains(git(t, root, "branch", "--format=%(refname:short)"), "main") {
		t.Fatal("main is gone")
	}

	if err := DeleteLocalBranch(context.Background(), root, "unmerged"); err != nil {
		t.Fatalf("DeleteLocalBranch: %v", err)
	}
	if strings.Contains(git(t, root, "branch", "--format=%(refname:short)"), "unmerged") {
		t.Fatal("the unmerged branch survived")
	}
}

func TestDeleteLocalBranchReportsAFailureItCannotForce(t *testing.T) {
	root := throwawayRepo(t)
	err := DeleteLocalBranch(context.Background(), root, "never-existed")
	if err == nil {
		t.Fatal("deleting a branch that does not exist reported success")
	}
	if !strings.Contains(err.Error(), "delete branch") {
		t.Fatalf("error read %q", err)
	}
}

// origin is a bare repository on disk. Nothing here talks to a network remote.
func withLocalOrigin(t *testing.T, root string) string {
	t.Helper()
	origin := filepath.Join(t.TempDir(), "origin.git")
	git(t, filepath.Dir(origin), "init", "-q", "--bare", "-b", "main", origin)
	git(t, root, "remote", "add", "origin", origin)
	git(t, root, "push", "-q", "-u", "origin", "main")
	return origin
}

func TestRemoteBranchDeletionIsIdempotentAgainstAnAlreadyGoneBranch(t *testing.T) {
	root := throwawayRepo(t)
	withLocalOrigin(t, root)
	git(t, root, "branch", "topic")
	git(t, root, "push", "-q", "origin", "topic")

	ctx := context.Background()
	if !RemoteBranchExists(ctx, root, "topic") {
		t.Fatal("the pushed branch is not reported on origin")
	}
	if RemoteBranchExists(ctx, root, "absent") {
		t.Fatal("a branch nobody pushed is reported on origin")
	}

	if err := DeleteRemoteBranch(ctx, root, "topic"); err != nil {
		t.Fatalf("DeleteRemoteBranch: %v", err)
	}
	if RemoteBranchExists(ctx, root, "topic") {
		t.Fatal("the remote branch survived")
	}

	// Deleting it again is not an error: the branch is already gone, which is
	// the outcome the caller asked for. Note the tolerated-output match is
	// deliberately broad (it also swallows a push refused with "unable to
	// delete"); that behaviour is unchanged from where this code used to live.
	if err := DeleteRemoteBranch(ctx, root, "topic"); err != nil {
		t.Fatalf("second delete reported %v, want the already-gone branch tolerated", err)
	}
}

func TestDeleteRemoteBranchRefusesTheDefaultBranch(t *testing.T) {
	root := throwawayRepo(t)
	withLocalOrigin(t, root)
	if err := DeleteRemoteBranch(context.Background(), root, "main"); err == nil {
		t.Fatal("origin's default branch was deleted")
	}
	if !RemoteBranchExists(context.Background(), root, "main") {
		t.Fatal("main is gone from origin")
	}
}

func TestDefaultBranchPrefersOriginsHeadAndFallsBackToConvention(t *testing.T) {
	ctx := context.Background()

	// No remote: the conventional name that exists wins.
	local := throwawayRepo(t)
	if got := DefaultBranch(ctx, local); got != "main" {
		t.Fatalf("DefaultBranch(no remote) = %q, want main", got)
	}
	if !IsDefaultBranch(ctx, local, "main") || IsDefaultBranch(ctx, local, "topic") || IsDefaultBranch(ctx, local, "") {
		t.Fatal("IsDefaultBranch disagrees with DefaultBranch")
	}

	// With origin's HEAD recorded, that answer wins over convention.
	withHead := throwawayRepo(t)
	git(t, withHead, "checkout", "-q", "-b", "trunk")
	withLocalOrigin(t, withHead)
	git(t, withHead, "push", "-q", "-u", "origin", "trunk")
	git(t, withHead, "remote", "set-head", "origin", "trunk")
	if got := DefaultBranch(ctx, withHead); got != "trunk" {
		t.Fatalf("DefaultBranch(origin HEAD=trunk) = %q, want trunk", got)
	}
}

func TestDefaultBranchObservedRecordsEverySpawn(t *testing.T) {
	root := throwawayRepo(t)
	spawns := 0
	// A repository with no origin HEAD needs the fallback probes, so more than
	// one process runs and each must be recorded (the startup trace counts
	// them).
	if got := DefaultBranchObserved(context.Background(), root, func() { spawns++ }); got != "main" {
		t.Fatalf("DefaultBranchObserved = %q", got)
	}
	if spawns < 2 {
		t.Fatalf("recorded %d spawns, want one per git process", spawns)
	}
}

func TestPruneWorktreesReportsGitFailures(t *testing.T) {
	notARepo := t.TempDir()
	if err := PruneWorktrees(context.Background(), notARepo); err == nil {
		t.Fatal("pruning outside a repository reported success")
	}
}
