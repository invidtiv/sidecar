package workspaceops

import (
	"context"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
)

// This file is the presentation-neutral worktree deletion path. Every surface
// that can delete a worktree — the project workspace and the global Workspaces
// browser — executes through it, so the two cannot drift in what "delete" does
// to git, or in what it does to the worktree's tmux session.

// WorktreeSessionPrefix names the tmux sessions Sidecar runs a worktree's agent
// in.
const WorktreeSessionPrefix = "sidecar-ws-"

// SanitizeSessionName strips the characters tmux gives meaning to in a target.
// It is the project plugin's rule, kept here so the shared delete path can
// resolve a session that plugin created.
func SanitizeSessionName(name string) string {
	name = strings.ReplaceAll(name, ".", "-")
	name = strings.ReplaceAll(name, ":", "-")
	name = strings.ReplaceAll(name, "/", "-")
	return name
}

// WorktreeSessionNames lists every tmux session name Sidecar may have started a
// worktree's agent under, most-canonical first and never empty of meaning.
//
// There are two spellings in live use and this is not the place to unify them:
// WorktreeSessionName above lowercases and slugifies (it names the sessions the
// global surface and the CLI create), while the project plugin only replaces
// tmux's metacharacters. For an ordinary lowercase directory the two agree and
// this returns one name; for `My_Feature` they do not, and a delete that knew
// only one spelling would leave the other session running — the exact bug this
// path exists to prevent. Killing both is safe: both are Sidecar-owned
// `sidecar-ws-` names derived from this one directory.
func WorktreeSessionNames(path, name string) []string {
	if strings.TrimSpace(path) == "" {
		return nil
	}
	candidates := []string{
		WorktreeSessionName(path, name),
		WorktreeSessionPrefix + SanitizeSessionName(filepath.Base(path)),
	}
	var out []string
	for _, candidate := range candidates {
		if candidate == "" || candidate == WorktreeSessionPrefix {
			continue
		}
		if !slices.Contains(out, candidate) {
			out = append(out, candidate)
		}
	}
	return out
}

// KillWorktreeSession closes a worktree's tmux session, if one is running.
//
// A session tmux has already lost is success: the requested state is reached.
// A session that is still there after the kill is a hard failure, because the
// only reason to call this is that the directory underneath it is about to be
// removed — see DeleteWorktree.
func KillWorktreeSession(ctx context.Context, sessionName string) error {
	if sessionName == "" {
		return nil
	}
	cmd := exec.CommandContext(ctx, "tmux", "kill-session", "-t", sessionName)
	output, err := cmd.CombinedOutput()
	if err == nil || !SessionExists(sessionName) {
		return nil
	}
	return fmt.Errorf("close worktree session %s: %s: %w", sessionName, strings.TrimSpace(string(output)), err)
}

// killWorktreeSessions is indirected so tests can exercise the delete ordering
// without a tmux server.
var killWorktreeSessions = func(ctx context.Context, path string) error {
	for _, session := range WorktreeSessionNames(path, "") {
		if err := KillWorktreeSession(ctx, session); err != nil {
			return err
		}
	}
	return nil
}

// DeleteWorktree closes the worktree's tmux session and then removes the
// worktree at path. A worktree whose directory has already gone is pruned from
// git's metadata instead.
//
// The session teardown lives here, ahead of the git work, because the ordering
// is the point: removing the directory first leaves whatever is running in the
// session — an agent, most of the time — alive in a working directory that no
// longer exists (td-a66836). Putting it on the shared path is what stops one
// surface having it and the other not; neither caller can opt out or spell the
// session name differently, because neither one supplies it.
//
// Interaction with internal/shellliveness: none, deliberately. That subsystem
// reaps *shells* — sidecar-sh-* sessions recorded in shells.json — and both of
// its bindings skip anything that is not a KindShell workspace. A worktree
// session is in neither the manifest nor a liveness tracker, so a kill here
// cannot be mistaken for a suspicious disappearance and cannot race the reaper.
func DeleteWorktree(ctx context.Context, workDir, path string, isMissing bool) error {
	if err := killWorktreeSessions(ctx, path); err != nil {
		return err
	}

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
	return DefaultBranchObserved(ctx, workDir, nil)
}

// DefaultBranchObserved is DefaultBranch with a hook called immediately before
// each git process it spawns. Hosts that count subprocesses (the startup trace)
// pass their recorder here so detection is not under-reported as one spawn.
func DefaultBranchObserved(ctx context.Context, workDir string, onSpawn func()) string {
	if ctx.Err() != nil {
		return ""
	}
	spawn := func() {
		if onSpawn != nil {
			onSpawn()
		}
	}
	defaultBranchCacheMu.RLock()
	if branch, ok := defaultBranchCache[workDir]; ok {
		defaultBranchCacheMu.RUnlock()
		return branch
	}
	defaultBranchCacheMu.RUnlock()

	spawn()
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
		spawn()
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
