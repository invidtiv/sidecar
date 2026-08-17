package workspace

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/workspaceops"
)

// WorktreeAction identifies a mutating action offered by the workspace UI.
type WorktreeAction string

const (
	WorktreeActionDelete WorktreeAction = "delete"
	WorktreeActionPush   WorktreeAction = "push"
	WorktreeActionMerge  WorktreeAction = "merge"
)

// WorktreeActionRefusal returns a user-facing reason when an action is unsafe.
func WorktreeActionRefusal(wt *Worktree, action WorktreeAction) string {
	if wt == nil {
		return workspaceops.WorktreeActionRefusal(nil, workspaceops.WorktreeAction(action))
	}
	return workspaceops.WorktreeActionRefusal(&workspaceops.WorktreeActionState{Path: wt.Path, Branch: wt.Branch,
		IsMain: wt.IsMain, IsBare: wt.IsBare, IsDetached: wt.IsDetached, IsLocked: wt.IsLocked,
		IsMissing: wt.IsMissing, IsPrunable: wt.IsPrunable}, workspaceops.WorktreeAction(action))
}

type gitWorktreeState struct {
	Path     string
	Branch   string
	HEAD     string
	Bare     bool
	Detached bool
	Locked   bool
	Prunable bool
}

func readGitWorktrees(repoPath string) ([]gitWorktreeState, error) {
	return readGitWorktreesContext(context.Background(), repoPath)
}

func readGitWorktreesContext(ctx context.Context, repoPath string) ([]gitWorktreeState, error) {
	out, err := gitOutputContext(ctx, repoPath, "worktree", "list", "--porcelain")
	if err != nil {
		return nil, err
	}
	var result []gitWorktreeState
	var current *gitWorktreeState
	flush := func() {
		if current != nil {
			current.Path = canonicalGitPath(current.Path)
			result = append(result, *current)
		}
	}
	scanner := bufio.NewScanner(strings.NewReader(out))
	for scanner.Scan() {
		line := scanner.Text()
		switch {
		case strings.HasPrefix(line, "worktree "):
			flush()
			current = &gitWorktreeState{Path: strings.TrimPrefix(line, "worktree ")}
		case current == nil:
			continue
		case strings.HasPrefix(line, "HEAD "):
			current.HEAD = strings.TrimPrefix(line, "HEAD ")
		case strings.HasPrefix(line, "branch refs/heads/"):
			current.Branch = strings.TrimPrefix(line, "branch refs/heads/")
		case line == "bare":
			current.Bare = true
		case line == "detached":
			current.Detached = true
		case line == "locked" || strings.HasPrefix(line, "locked "):
			current.Locked = true
		case line == "prunable" || strings.HasPrefix(line, "prunable "):
			current.Prunable = true
		}
	}
	flush()
	return result, scanner.Err()
}

func cloneDirectMergeOperation(op *DirectMergeOperation) *DirectMergeOperation {
	if op == nil {
		return nil
	}
	clone := *op
	clone.Completed = append([]string(nil), op.Completed...)
	return &clone
}

// DirectMergeRecovery describes the safe actions available after a failed merge.
type DirectMergeRecovery string

const (
	DirectMergeRecoveryNone        DirectMergeRecovery = ""
	DirectMergeRecoveryConflict    DirectMergeRecovery = "conflict"
	DirectMergeRecoveryPushFailure DirectMergeRecovery = "push-failure"
)

// DirectMergeOperation is the immutable context and accumulated result of a merge.
type DirectMergeOperation struct {
	runContext   context.Context
	SourcePath   string
	TargetPath   string
	SourceBranch string
	TargetBranch string
	Remote       string
	SourceOID    string
	TargetOID    string // target HEAD captured during visible preflight
	PreMergeOID  string // target HEAD immediately before merge, after ff-only update
	MergeOID     string
	Completed    []string
	Recovery     DirectMergeRecovery
	GitState     string
	Err          error
	Aborted      bool
}

func preflightDirectMerge(repoPath, sourcePath, sourceBranch, targetBranch string) (*DirectMergeOperation, error) {
	return preflightDirectMergeContext(context.Background(), repoPath, sourcePath, sourceBranch, targetBranch)
}

func preflightDirectMergeContext(ctx context.Context, repoPath, sourcePath, sourceBranch, targetBranch string) (*DirectMergeOperation, error) {
	worktrees, err := readGitWorktreesContext(ctx, repoPath)
	if err != nil {
		return nil, fmt.Errorf("inventory worktrees: %w", err)
	}
	sourcePath = canonicalGitPath(sourcePath)
	var source, target *gitWorktreeState
	for i := range worktrees {
		wt := &worktrees[i]
		if wt.Path == sourcePath {
			source = wt
		}
		if wt.Branch == targetBranch {
			if target != nil {
				return nil, fmt.Errorf("target branch %q is checked out more than once", targetBranch)
			}
			target = wt
		}
	}
	if source == nil {
		return nil, fmt.Errorf("source worktree %q is not registered", sourcePath)
	}
	if source.Branch != sourceBranch || source.Detached || source.Bare || source.Locked || source.Prunable {
		return nil, fmt.Errorf("source worktree is not a safe checkout of %q", sourceBranch)
	}
	if target == nil {
		return nil, fmt.Errorf("target branch %q must be checked out in a worktree", targetBranch)
	}
	if target.Path == source.Path {
		return nil, fmt.Errorf("source and target resolve to the same worktree")
	}
	if target.Detached || target.Bare || target.Locked || target.Prunable {
		return nil, fmt.Errorf("target worktree is not safe to update")
	}
	for _, checkout := range []*gitWorktreeState{source, target} {
		if err := requireCleanContext(ctx, checkout.Path); err != nil {
			return nil, err
		}
		if state := gitOperationStateContext(ctx, checkout.Path); state != "clean" {
			return nil, fmt.Errorf("worktree %q has a Git operation in progress: %s", checkout.Path, state)
		}
	}
	sourceOID, err := gitOutputContext(ctx, source.Path, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("resolve source HEAD: %w", err)
	}
	sourceRefOID, err := gitOutputContext(ctx, repoPath, "rev-parse", "refs/heads/"+sourceBranch)
	if err != nil || sourceRefOID != sourceOID {
		return nil, fmt.Errorf("source branch moved or does not match its checkout")
	}
	targetOID, err := gitOutputContext(ctx, target.Path, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("resolve target HEAD: %w", err)
	}
	targetRefOID, err := gitOutputContext(ctx, repoPath, "rev-parse", "refs/heads/"+targetBranch)
	if err != nil || targetRefOID != targetOID {
		return nil, fmt.Errorf("target branch moved or does not match its checkout")
	}
	remote, err := resolveBranchRemoteContext(ctx, repoPath, targetBranch)
	if err != nil {
		return nil, err
	}
	if _, err := gitOutputContext(ctx, repoPath, "ls-remote", "--exit-code", "--heads", remote, "refs/heads/"+targetBranch); err != nil {
		return nil, fmt.Errorf("remote %q does not expose target branch %q: %w", remote, targetBranch, err)
	}
	return &DirectMergeOperation{
		SourcePath: source.Path, TargetPath: target.Path,
		SourceBranch: sourceBranch, TargetBranch: targetBranch, Remote: remote,
		SourceOID: sourceOID, TargetOID: targetOID,
		Completed: []string{"preflight"},
	}, nil
}

func runDirectMerge(op *DirectMergeOperation) *DirectMergeOperation {
	return runDirectMergeWithHooks(context.Background(), op, nil, nil)
}

func runDirectMergeContext(ctx context.Context, op *DirectMergeOperation) *DirectMergeOperation {
	return runDirectMergeWithHooks(ctx, op, nil, nil)
}

func runDirectMergeWithBeforeMerge(op *DirectMergeOperation, beforeMerge func()) *DirectMergeOperation {
	return runDirectMergeWithHooks(context.Background(), op, nil, beforeMerge)
}

func runDirectMergeWithBeforePull(op *DirectMergeOperation, beforePull func()) *DirectMergeOperation {
	return runDirectMergeWithHooks(context.Background(), op, beforePull, nil)
}

func runDirectMergeWithHooks(ctx context.Context, op *DirectMergeOperation, beforePull, beforeMerge func()) *DirectMergeOperation {
	if op == nil {
		return &DirectMergeOperation{Err: fmt.Errorf("missing direct merge operation")}
	}
	op.runContext = ctx
	if err := revalidateDirectMergeContext(ctx, op); err != nil {
		return failDirectMerge(op, err, DirectMergeRecoveryNone)
	}
	if _, err := gitOutputContext(ctx, op.TargetPath, "fetch", op.Remote, op.TargetBranch); err != nil {
		return failDirectMerge(op, fmt.Errorf("fetch %s: %w", op.Remote, err), DirectMergeRecoveryNone)
	}
	op.Completed = append(op.Completed, "fetch")
	if beforePull != nil {
		beforePull()
	}
	if err := requireCleanContext(ctx, op.TargetPath); err != nil {
		return failDirectMerge(op, fmt.Errorf("target changed before fast-forward: %w", err), DirectMergeRecoveryNone)
	}
	if state := gitOperationStateContext(ctx, op.TargetPath); state != "clean" {
		return failDirectMerge(op, fmt.Errorf("target started a Git operation before fast-forward: %s", state), DirectMergeRecoveryNone)
	}
	if err := requireCheckoutIdentityContext(ctx, op.TargetPath, op.TargetBranch, op.TargetOID); err != nil {
		return failDirectMerge(op, fmt.Errorf("target checkout changed before fast-forward: %w", err), DirectMergeRecoveryNone)
	}
	if _, err := gitOutputContext(ctx, op.TargetPath, "pull", "--ff-only", op.Remote, op.TargetBranch); err != nil {
		return failDirectMerge(op, fmt.Errorf("fast-forward target: %w", err), DirectMergeRecoveryNone)
	}
	op.Completed = append(op.Completed, "fast-forward target")
	postPullOID, err := gitOutputContext(ctx, op.TargetPath, "rev-parse", "HEAD")
	if err != nil {
		return failDirectMerge(op, fmt.Errorf("pin post-pull target HEAD: %w", err), DirectMergeRecoveryNone)
	}
	if postPullOID == "" {
		return failDirectMerge(op, fmt.Errorf("pin post-pull target HEAD: empty OID"), DirectMergeRecoveryNone)
	}
	op.PreMergeOID = postPullOID
	if err := requireCleanContext(ctx, op.TargetPath); err != nil {
		return failDirectMerge(op, err, DirectMergeRecoveryNone)
	}
	if beforeMerge != nil {
		beforeMerge()
	}
	if err := requireCleanContext(ctx, op.SourcePath); err != nil {
		return failDirectMerge(op, fmt.Errorf("source changed after review: %w", err), DirectMergeRecoveryNone)
	}
	if state := gitOperationStateContext(ctx, op.SourcePath); state != "clean" {
		return failDirectMerge(op, fmt.Errorf("source started a Git operation after review: %s", state), DirectMergeRecoveryNone)
	}
	if err := requireCheckoutIdentityContext(ctx, op.SourcePath, op.SourceBranch, op.SourceOID); err != nil {
		return failDirectMerge(op, fmt.Errorf("source checkout changed before merge: %w", err), DirectMergeRecoveryNone)
	}
	if err := requireCheckoutIdentityContext(ctx, op.TargetPath, op.TargetBranch, op.PreMergeOID); err != nil {
		return failDirectMerge(op, fmt.Errorf("target checkout changed before merge: %w", err), DirectMergeRecoveryNone)
	}
	message := fmt.Sprintf("Merge branch '%s'", op.SourceBranch)
	if _, err := gitOutputContext(ctx, op.TargetPath, "merge", "--no-ff", op.SourceOID, "-m", message); err != nil {
		if gitOperationStateContext(ctx, op.TargetPath) == "merge" {
			return failDirectMerge(op, fmt.Errorf("merge conflict: %w", err), DirectMergeRecoveryConflict)
		}
		return failDirectMerge(op, fmt.Errorf("merge: %w", err), DirectMergeRecoveryNone)
	}
	op.Completed = append(op.Completed, "merge")
	op.MergeOID, _ = gitOutputContext(ctx, op.TargetPath, "rev-parse", "HEAD")
	return pushDirectMergeContext(ctx, op)
}

func continueDirectMerge(op *DirectMergeOperation) *DirectMergeOperation {
	return continueDirectMergeContext(context.Background(), op)
}

func continueDirectMergeContext(ctx context.Context, op *DirectMergeOperation) *DirectMergeOperation {
	if op == nil || op.Recovery != DirectMergeRecoveryConflict {
		return failDirectMerge(op, fmt.Errorf("no conflicted merge is available to continue"), DirectMergeRecoveryNone)
	}
	op.runContext = ctx
	if err := revalidateConflictRecoveryContext(ctx, op); err != nil {
		return failDirectMerge(op, err, DirectMergeRecoveryConflict)
	}
	if unmerged, _ := gitOutputContext(ctx, op.TargetPath, "diff", "--name-only", "--diff-filter=U"); unmerged != "" {
		return failDirectMerge(op, fmt.Errorf("resolve all conflicts before continuing: %s", strings.ReplaceAll(unmerged, "\n", ", ")), DirectMergeRecoveryConflict)
	}
	cmd := exec.CommandContext(ctx, "git", "-c", "core.editor=true", "merge", "--continue")
	cmd.Dir = op.TargetPath
	if out, err := cmd.CombinedOutput(); err != nil {
		return failDirectMerge(op, fmt.Errorf("continue merge: %s: %w", strings.TrimSpace(string(out)), err), DirectMergeRecoveryConflict)
	}
	op.Completed = appendUnique(op.Completed, "merge")
	op.MergeOID, _ = gitOutputContext(ctx, op.TargetPath, "rev-parse", "HEAD")
	op.Recovery, op.Err = DirectMergeRecoveryNone, nil
	return pushDirectMergeContext(ctx, op)
}

func abortDirectMerge(op *DirectMergeOperation) *DirectMergeOperation {
	return abortDirectMergeContext(context.Background(), op)
}

func abortDirectMergeContext(ctx context.Context, op *DirectMergeOperation) *DirectMergeOperation {
	if op == nil || op.Recovery != DirectMergeRecoveryConflict {
		return failDirectMerge(op, fmt.Errorf("no conflicted merge is available to abort"), DirectMergeRecoveryNone)
	}
	op.runContext = ctx
	if err := revalidateConflictRecoveryContext(ctx, op); err != nil {
		return failDirectMerge(op, err, DirectMergeRecoveryConflict)
	}
	if _, err := gitOutputContext(ctx, op.TargetPath, "merge", "--abort"); err != nil {
		return failDirectMerge(op, fmt.Errorf("abort merge: %w", err), DirectMergeRecoveryConflict)
	}
	head, _ := gitOutputContext(ctx, op.TargetPath, "rev-parse", "HEAD")
	if head != op.PreMergeOID {
		return failDirectMerge(op, fmt.Errorf("merge aborted but target HEAD is %s, expected %s", head, op.PreMergeOID), DirectMergeRecoveryNone)
	}
	op.Recovery, op.Err = DirectMergeRecoveryNone, nil
	op.Aborted = true
	op.GitState = currentGitStateContext(ctx, op.TargetPath)
	return op
}

func retryDirectMergePush(op *DirectMergeOperation) *DirectMergeOperation {
	return retryDirectMergePushContext(context.Background(), op)
}

func retryDirectMergePushContext(ctx context.Context, op *DirectMergeOperation) *DirectMergeOperation {
	if op == nil || op.Recovery != DirectMergeRecoveryPushFailure {
		return failDirectMerge(op, fmt.Errorf("no failed push is available to retry"), DirectMergeRecoveryNone)
	}
	op.runContext = ctx
	if state := gitOperationStateContext(ctx, op.TargetPath); state != "clean" {
		return failDirectMerge(op, fmt.Errorf("cannot retry push during Git operation: %s", state), DirectMergeRecoveryPushFailure)
	}
	if err := requireCleanContext(ctx, op.TargetPath); err != nil {
		return failDirectMerge(op, err, DirectMergeRecoveryPushFailure)
	}
	if err := requireCheckoutIdentityContext(ctx, op.TargetPath, op.TargetBranch, op.MergeOID); err != nil {
		return failDirectMerge(op, fmt.Errorf("target checkout changed since the failed push: %w", err), DirectMergeRecoveryPushFailure)
	}
	return pushDirectMergeContext(ctx, op)
}

func pushDirectMergeContext(ctx context.Context, op *DirectMergeOperation) *DirectMergeOperation {
	if op == nil {
		return failDirectMerge(nil, fmt.Errorf("missing direct merge operation"), DirectMergeRecoveryNone)
	}
	op.runContext = ctx
	if op.MergeOID == "" {
		return failDirectMerge(op, fmt.Errorf("merge result OID is not pinned"), DirectMergeRecoveryPushFailure)
	}
	if state := gitOperationStateContext(ctx, op.TargetPath); state != "clean" {
		return failDirectMerge(op, fmt.Errorf("cannot push during Git operation: %s", state), DirectMergeRecoveryPushFailure)
	}
	if err := requireCleanContext(ctx, op.TargetPath); err != nil {
		return failDirectMerge(op, err, DirectMergeRecoveryPushFailure)
	}
	if err := requireCheckoutIdentityContext(ctx, op.TargetPath, op.TargetBranch, op.MergeOID); err != nil {
		return failDirectMerge(op, fmt.Errorf("target checkout changed before push: %w", err), DirectMergeRecoveryPushFailure)
	}
	refspec := "HEAD:refs/heads/" + op.TargetBranch
	if _, err := gitOutputContext(ctx, op.TargetPath, "push", op.Remote, refspec); err != nil {
		return failDirectMerge(op, fmt.Errorf("push %s %s: %w", op.Remote, op.TargetBranch, err), DirectMergeRecoveryPushFailure)
	}
	op.Completed = appendUnique(op.Completed, "push")
	op.Recovery, op.Err = DirectMergeRecoveryNone, nil
	op.GitState = currentGitStateContext(ctx, op.TargetPath)
	return op
}

func revalidateConflictRecoveryContext(ctx context.Context, op *DirectMergeOperation) error {
	if op.SourceOID == "" || op.PreMergeOID == "" {
		return fmt.Errorf("conflict recovery identity is incomplete")
	}
	if err := requireCheckoutIdentityContext(ctx, op.TargetPath, op.TargetBranch, op.PreMergeOID); err != nil {
		return fmt.Errorf("refusing recovery because target checkout changed: %w", err)
	}
	if state := gitOperationStateContext(ctx, op.TargetPath); state != "merge" {
		return fmt.Errorf("the target no longer has the expected merge in progress")
	}
	mergeHead, err := gitOutputContext(ctx, op.TargetPath, "rev-parse", "MERGE_HEAD")
	if err != nil || mergeHead != op.SourceOID {
		return fmt.Errorf("refusing recovery because MERGE_HEAD changed: got %q, expected %q", mergeHead, op.SourceOID)
	}
	return nil
}

func revalidateDirectMergeContext(ctx context.Context, op *DirectMergeOperation) error {
	checks := []struct{ path, branch, want string }{
		{op.SourcePath, op.SourceBranch, op.SourceOID},
		{op.TargetPath, op.TargetBranch, op.TargetOID},
	}
	for _, check := range checks {
		if state := gitOperationStateContext(ctx, check.path); state != "clean" {
			return fmt.Errorf("worktree %q has a Git operation in progress: %s", check.path, state)
		}
		if err := requireCleanContext(ctx, check.path); err != nil {
			return err
		}
		if err := requireCheckoutIdentityContext(ctx, check.path, check.branch, check.want); err != nil {
			return err
		}
	}
	return nil
}

func requireCheckoutIdentityContext(ctx context.Context, path, branch, oid string) error {
	currentBranch, err := gitOutputContext(ctx, path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || currentBranch != branch {
		return fmt.Errorf("worktree %q checks out %q, expected %q", path, currentBranch, branch)
	}
	worktrees, err := readGitWorktreesContext(ctx, path)
	if err != nil {
		return fmt.Errorf("refresh worktree inventory: %w", err)
	}
	found := false
	for _, wt := range worktrees {
		if wt.Path == canonicalGitPath(path) && wt.Branch == branch && !wt.Bare && !wt.Detached && !wt.Locked && !wt.Prunable {
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("worktree %q is no longer the safe checkout of %q", path, branch)
	}
	if oid != "" {
		head, err := gitOutputContext(ctx, path, "rev-parse", "HEAD")
		if err != nil || head != oid {
			return fmt.Errorf("HEAD changed after review in %q", path)
		}
	}
	return nil
}

func failDirectMerge(op *DirectMergeOperation, err error, recovery DirectMergeRecovery) *DirectMergeOperation {
	if op == nil {
		op = &DirectMergeOperation{}
	}
	op.Err, op.Recovery = err, recovery
	if op.TargetPath != "" {
		ctx := op.runContext
		if ctx == nil {
			ctx = context.Background()
		}
		op.GitState = currentGitStateContext(ctx, op.TargetPath)
	}
	return op
}

func resolveBranchRemoteContext(ctx context.Context, repoPath, branch string) (string, error) {
	if remote, err := gitOutputContext(ctx, repoPath, "config", "--get", "branch."+branch+".remote"); err == nil && remote != "" && remote != "." {
		return remote, nil
	}
	remotes, err := gitOutputContext(ctx, repoPath, "remote")
	if err != nil {
		return "", fmt.Errorf("list remotes: %w", err)
	}
	items := strings.Fields(remotes)
	if len(items) == 1 {
		return items[0], nil
	}
	if len(items) == 0 {
		return "", fmt.Errorf("no remote is configured for target branch %q", branch)
	}
	return "", fmt.Errorf("target branch %q has no remote and repository has multiple remotes", branch)
}

func requireCleanContext(ctx context.Context, path string) error {
	status, err := gitOutputContext(ctx, path, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	if status != "" {
		return fmt.Errorf("worktree %q is dirty", path)
	}
	return nil
}

func gitOperationState(path string) string {
	return gitOperationStateContext(context.Background(), path)
}

func gitOperationStateContext(ctx context.Context, path string) string {
	states := []struct{ name, key string }{
		{"merge", "MERGE_HEAD"}, {"rebase", "rebase-merge"}, {"rebase", "rebase-apply"}, {"cherry-pick", "CHERRY_PICK_HEAD"},
	}
	for _, state := range states {
		gitPath, err := gitOutputContext(ctx, path, "rev-parse", "--git-path", state.key)
		if err != nil {
			continue
		}
		if !filepath.IsAbs(gitPath) {
			gitPath = filepath.Join(path, gitPath)
		}
		if _, err := os.Stat(gitPath); err == nil {
			return state.name
		}
	}
	return "clean"
}

func currentGitStateContext(ctx context.Context, path string) string {
	head, _ := gitOutputContext(ctx, path, "rev-parse", "--short", "HEAD")
	branch, _ := gitOutputContext(ctx, path, "branch", "--show-current")
	status, _ := gitOutputContext(ctx, path, "status", "--short")
	state := fmt.Sprintf("target %s at %s; operation: %s", branch, head, gitOperationStateContext(ctx, path))
	if status == "" {
		return state + "; working tree clean"
	}
	return state + "; status:\n" + status
}

func appendUnique(items []string, item string) []string {
	for _, existing := range items {
		if existing == item {
			return items
		}
	}
	return append(items, item)
}

// BaseUpdateResult describes a safe post-merge base refresh.
type BaseUpdateResult struct {
	Branch        string
	TargetPath    string
	Fetched       bool
	Updated       bool
	LeftUnchanged bool
	Message       string
	Err           error
}

func updateCheckedOutBase(repoPath, branch, remote string) BaseUpdateResult {
	return updateCheckedOutBaseContext(context.Background(), repoPath, branch, remote)
}

func updateCheckedOutBaseContext(ctx context.Context, repoPath, branch, remote string) BaseUpdateResult {
	return updateCheckedOutBaseWithBeforePullContext(ctx, repoPath, branch, remote, nil)
}

func updateCheckedOutBaseWithBeforePull(repoPath, branch, remote string, beforePull func()) BaseUpdateResult {
	return updateCheckedOutBaseWithBeforePullContext(context.Background(), repoPath, branch, remote, beforePull)
}

func updateCheckedOutBaseWithBeforePullContext(ctx context.Context, repoPath, branch, remote string, beforePull func()) BaseUpdateResult {
	result := BaseUpdateResult{Branch: branch}
	if remote == "" {
		var err error
		remote, err = resolveBranchRemoteContext(ctx, repoPath, branch)
		if err != nil {
			result.Err = err
			return result
		}
	}
	if _, err := gitOutputContext(ctx, repoPath, "fetch", remote, branch); err != nil {
		result.Err = fmt.Errorf("fetch %s %s: %w", remote, branch, err)
		return result
	}
	result.Fetched = true
	worktrees, err := readGitWorktreesContext(ctx, repoPath)
	if err != nil {
		result.Err = err
		return result
	}
	var targetOID string
	for _, wt := range worktrees {
		if wt.Branch == branch && !wt.Bare && !wt.Detached && !wt.Locked && !wt.Prunable {
			result.TargetPath = wt.Path
			targetOID = wt.HEAD
			break
		}
	}
	if result.TargetPath == "" {
		result.LeftUnchanged = true
		result.Message = fmt.Sprintf("Fetched %s/%s; local %s was intentionally left unchanged because it is not safely checked out", remote, branch, branch)
		return result
	}
	if err := requireCleanContext(ctx, result.TargetPath); err != nil {
		result.LeftUnchanged = true
		result.Message = fmt.Sprintf("Fetched %s/%s; local %s was intentionally left unchanged", remote, branch, branch)
		result.Err = err
		return result
	}
	if state := gitOperationStateContext(ctx, result.TargetPath); state != "clean" {
		result.LeftUnchanged = true
		result.Message = fmt.Sprintf("Fetched %s/%s; local %s was intentionally left unchanged", remote, branch, branch)
		result.Err = fmt.Errorf("target has a Git operation in progress: %s", state)
		return result
	}
	if targetOID == "" {
		result.LeftUnchanged = true
		result.Err = fmt.Errorf("target checkout has no pinned HEAD OID")
		return result
	}
	if beforePull != nil {
		beforePull()
	}
	if err := requireCleanContext(ctx, result.TargetPath); err != nil {
		result.LeftUnchanged = true
		result.Err = fmt.Errorf("target changed before pull: %w", err)
		return result
	}
	if state := gitOperationStateContext(ctx, result.TargetPath); state != "clean" {
		result.LeftUnchanged = true
		result.Err = fmt.Errorf("target started a Git operation before pull: %s", state)
		return result
	}
	if err := requireCheckoutIdentityContext(ctx, result.TargetPath, branch, targetOID); err != nil {
		result.LeftUnchanged = true
		result.Message = fmt.Sprintf("Fetched %s/%s; local %s was intentionally left unchanged", remote, branch, branch)
		result.Err = fmt.Errorf("target checkout changed before pull: %w", err)
		return result
	}
	if _, err := gitOutputContext(ctx, result.TargetPath, "pull", "--ff-only", remote, branch); err != nil {
		result.LeftUnchanged = true
		result.Err = fmt.Errorf("fast-forward %s: %w", branch, err)
		return result
	}
	result.Updated = true
	result.Message = fmt.Sprintf("Updated %s in %s with pull --ff-only", branch, result.TargetPath)
	return result
}

// CleanupPlan is captured before cleanup starts and executed in order.
type CleanupPlan struct {
	RepoPath string
	// ProjectRoot is the owning project, whose manifest records the shells
	// rooted in the worktree. It is not RepoPath, which is only a surviving
	// checkout the git commands run from.
	ProjectRoot       string
	WorktreePath      string
	Branch            string
	ExpectedOID       string
	BranchRemote      string
	ExpectedRemoteOID string
	BaseRemote        string
	BaseBranch        string
	DeleteWorktree    bool
	DeleteBranch      bool
	DeleteRemote      bool
	UpdateBase        bool
	PRIdentity        *PRIdentity
	ReviewedOID       string
	ForceDeleteBranch bool
}

func runCleanupPlan(plan CleanupPlan) *CleanupResults {
	return runCleanupPlanContext(context.Background(), plan)
}

func runCleanupPlanContext(ctx context.Context, plan CleanupPlan) *CleanupResults {
	results := &CleanupResults{}
	if plan.PRIdentity != nil {
		forceRequired, err := validateMergedPRForCleanupContext(ctx, plan.WorktreePath, plan.ReviewedOID, plan.BaseBranch, *plan.PRIdentity)
		if err != nil {
			results.Errors = append(results.Errors, "Pull request: "+err.Error())
			return results
		}
		if plan.ForceDeleteBranch && !forceRequired {
			results.Errors = append(results.Errors, "Branch: force deletion was selected but is not required by the validated merge")
			return results
		}
	}
	// Revalidate the selected identity before deleting anything.
	worktrees, err := readGitWorktreesContext(ctx, plan.RepoPath)
	if err != nil {
		results.Errors = append(results.Errors, "Inventory: "+err.Error())
		return results
	}
	found := false
	for _, wt := range worktrees {
		if canonicalGitPath(wt.Path) == canonicalGitPath(plan.WorktreePath) && wt.Branch == plan.Branch {
			found = true
			if plan.ExpectedOID != "" && wt.HEAD != plan.ExpectedOID {
				results.Errors = append(results.Errors, "Workspace: HEAD changed since cleanup was confirmed")
				return results
			}
			break
		}
	}
	if !found && plan.DeleteWorktree {
		results.Errors = append(results.Errors, "Workspace: selected worktree identity changed")
		return results
	}
	if plan.DeleteRemote {
		remoteOID, err := remoteBranchOIDContext(ctx, plan.RepoPath, plan.BranchRemote, plan.Branch)
		if err != nil {
			results.Errors = append(results.Errors, "Remote branch: "+err.Error())
			return results
		}
		if remoteOID != plan.ExpectedRemoteOID {
			results.Errors = append(results.Errors, fmt.Sprintf("Remote branch: %s/%s changed since cleanup was confirmed", plan.BranchRemote, plan.Branch))
			return results
		}
		if remoteOID == "" {
			results.RemoteBranchDeleted = true
			plan.DeleteRemote = false
		}
	}
	// Every command below runs from RepoPath, which must be a surviving checkout.
	//
	// The removal is the shared workspaceops path, stating no force: nobody
	// confirmed a destructive action here, so a worktree that has acquired
	// uncommitted work since the merge was reviewed is a reason to stop. It is
	// also what closes the session before the directory goes (td-3df472); this
	// path must not kill anything itself.
	if plan.DeleteWorktree {
		if err := workspaceops.DeleteWorktree(ctx, workspaceops.WorktreeRemoval{
			RepoPath: plan.RepoPath, ProjectRoot: plan.ProjectRoot,
			Path: plan.WorktreePath, Branch: plan.Branch, ExpectedOID: plan.ExpectedOID,
		}); err != nil {
			results.Errors = append(results.Errors, fmt.Sprintf("Workspace: %v", err))
			return results
		} else {
			results.LocalWorktreeDeleted = true
		}
	}
	if plan.DeleteBranch {
		if !results.LocalWorktreeDeleted && plan.DeleteWorktree {
			results.Errors = append(results.Errors, "Branch: skipped because worktree deletion failed")
		} else if err := deleteBranchAfterMergeContext(ctx, plan.RepoPath, plan.Branch, plan.ExpectedOID, plan.ForceDeleteBranch); err != nil {
			results.Errors = append(results.Errors, fmt.Sprintf("Branch: %v", err))
		} else {
			results.LocalBranchDeleted = true
		}
	}
	if plan.DeleteRemote {
		if err := deleteRemoteBranchFromContext(ctx, plan.RepoPath, plan.BranchRemote, plan.Branch, plan.ExpectedRemoteOID); err != nil {
			results.Errors = append(results.Errors, fmt.Sprintf("Remote branch: %v", err))
		} else {
			results.RemoteBranchDeleted = true
		}
	}
	if plan.UpdateBase {
		update := updateCheckedOutBaseContext(ctx, plan.RepoPath, plan.BaseBranch, plan.BaseRemote)
		results.PullAttempted = true
		results.PullSuccess = update.Updated || (update.Fetched && update.LeftUnchanged && update.Err == nil)
		results.PullError = update.Err
		results.PullMessage = update.Message
	}
	return results
}

func deleteBranchAfterMergeContext(ctx context.Context, repoPath, branch, expectedOID string, force bool) error {
	return workspaceops.DeleteLocalBranch(ctx, workspaceops.BranchDeletion{
		RepoPath: repoPath, Branch: branch, ExpectedOID: expectedOID, Force: force,
	})
}

func deleteRemoteBranchFromContext(ctx context.Context, repoPath, remote, branch, expectedOID string) error {
	if remote == "" {
		var err error
		remote, err = resolveBranchRemoteContext(ctx, repoPath, branch)
		if err != nil {
			return err
		}
	}
	return workspaceops.DeleteRemoteBranch(ctx, workspaceops.BranchDeletion{
		RepoPath: repoPath, Branch: branch, Remote: remote, ExpectedOID: expectedOID,
	})
}

func remoteBranchOID(repoPath, remote, branch string) (string, error) {
	return remoteBranchOIDContext(context.Background(), repoPath, remote, branch)
}

func remoteBranchOIDContext(ctx context.Context, repoPath, remote, branch string) (string, error) {
	if remote == "" {
		return "", fmt.Errorf("remote is not resolved for branch %q", branch)
	}
	out, err := gitOutputContext(ctx, repoPath, "ls-remote", "--heads", remote, "refs/heads/"+branch)
	if err != nil {
		return "", fmt.Errorf("inspect %s/%s: %w", remote, branch, err)
	}
	if out == "" {
		return "", nil
	}
	fields := strings.Fields(out)
	if len(fields) < 2 || fields[1] != "refs/heads/"+branch {
		return "", fmt.Errorf("unexpected ls-remote result for %s/%s", remote, branch)
	}
	return fields[0], nil
}

func canonicalGitPath(path string) string {
	path = filepath.Clean(path)
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		return filepath.Clean(resolved)
	}
	return path
}

func shortOID(oid string) string {
	if len(oid) > 12 {
		return oid[:12]
	}
	return oid
}
