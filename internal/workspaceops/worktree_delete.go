package workspaceops

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"sync"
)

// This file is the presentation-neutral worktree deletion path. Every surface
// that can delete a worktree — the project workspace and the global Workspaces
// browser — executes through it, so the two cannot drift in what "delete" does
// to git.

// DeleteWorktree removes the worktree at path. A worktree whose directory has
// already gone is pruned from git's metadata instead.
func DeleteWorktree(ctx context.Context, workDir, path string, isMissing bool) error {
	if isMissing {
		return PruneWorktrees(ctx, workDir)
	}

	// First try without force.
	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", path)
	cmd.Dir = workDir
	if err := cmd.Run(); err == nil {
		return nil
	}

	cmd = exec.CommandContext(ctx, "git", "worktree", "remove", "--force", path)
	cmd.Dir = workDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree remove: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// PruneWorktrees drops git's records of worktrees whose directories are gone.
func PruneWorktrees(ctx context.Context, workDir string) error {
	cmd := exec.CommandContext(ctx, "git", "worktree", "prune")
	cmd.Dir = workDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("git worktree prune: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// DeleteLocalBranch deletes a local branch, refusing the repository's default
// branch outright.
func DeleteLocalBranch(ctx context.Context, workDir, branch string) error {
	if IsDefaultBranch(ctx, workDir, branch) {
		return fmt.Errorf("refusing to delete main branch %q", branch)
	}
	cmd := exec.CommandContext(ctx, "git", "branch", "-d", branch)
	cmd.Dir = workDir
	if err := cmd.Run(); err == nil {
		return nil
	}

	cmd = exec.CommandContext(ctx, "git", "branch", "-D", branch)
	cmd.Dir = workDir
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("delete branch: %s: %w", strings.TrimSpace(string(output)), err)
	}
	return nil
}

// DeleteRemoteBranch deletes origin's copy of a branch. A branch the remote has
// already dropped (GitHub's auto-delete, for instance) is not an error.
func DeleteRemoteBranch(ctx context.Context, workDir, branch string) error {
	if IsDefaultBranch(ctx, workDir, branch) {
		return fmt.Errorf("refusing to delete remote main branch %q", branch)
	}
	cmd := exec.CommandContext(ctx, "git", "push", "origin", "--delete", branch)
	cmd.Dir = workDir
	output, err := cmd.CombinedOutput()
	if err != nil {
		text := string(output)
		if strings.Contains(text, "remote ref does not exist") ||
			strings.Contains(text, "unable to delete") ||
			strings.Contains(text, "couldn't find remote ref") {
			return nil
		}
		return fmt.Errorf("delete remote branch: %s", strings.TrimSpace(text))
	}
	return nil
}

// RemoteBranchExists reports whether origin carries the branch.
func RemoteBranchExists(ctx context.Context, workDir, branch string) bool {
	cmd := exec.CommandContext(ctx, "git", "ls-remote", "--heads", "origin", branch)
	cmd.Dir = workDir
	output, err := cmd.Output()
	if err != nil {
		return false
	}
	return len(strings.TrimSpace(string(output))) > 0
}

var (
	defaultBranchCache   = make(map[string]string)
	defaultBranchCacheMu sync.RWMutex
)

// IsDefaultBranch reports whether branch is the repository's primary branch.
func IsDefaultBranch(ctx context.Context, workDir, branch string) bool {
	if branch == "" {
		return false
	}
	return branch == DefaultBranch(ctx, workDir)
}

// DefaultBranch detects a repository's default branch: origin's HEAD when it is
// known, then the conventional names, then "main". Answers are cached per
// working directory.
func DefaultBranch(ctx context.Context, workDir string) string {
	if ctx.Err() != nil {
		return ""
	}
	defaultBranchCacheMu.RLock()
	if branch, ok := defaultBranchCache[workDir]; ok {
		defaultBranchCacheMu.RUnlock()
		return branch
	}
	defaultBranchCacheMu.RUnlock()

	cmd := exec.CommandContext(ctx, "git", "symbolic-ref", "refs/remotes/origin/HEAD")
	cmd.Dir = workDir
	if output, err := cmd.Output(); err == nil {
		ref := strings.TrimSpace(string(output))
		if branch, found := strings.CutPrefix(ref, "refs/remotes/origin/"); found {
			cacheDefaultBranch(workDir, branch)
			return branch
		}
	}
	if ctx.Err() != nil {
		return ""
	}

	for _, branch := range []string{"main", "master"} {
		if ctx.Err() != nil {
			return ""
		}
		cmd := exec.CommandContext(ctx, "git", "rev-parse", "--verify", branch)
		cmd.Dir = workDir
		if err := cmd.Run(); err == nil {
			cacheDefaultBranch(workDir, branch)
			return branch
		}
	}

	if ctx.Err() != nil {
		return ""
	}
	cacheDefaultBranch(workDir, "main")
	return "main"
}

func cacheDefaultBranch(workDir, branch string) {
	defaultBranchCacheMu.Lock()
	defaultBranchCache[workDir] = branch
	defaultBranchCacheMu.Unlock()
}
