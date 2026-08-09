package workspace

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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
		return "No worktree is selected"
	}
	if wt.IsMain {
		return fmt.Sprintf("%s is unavailable for the main worktree", action)
	}
	if wt.IsBare {
		return fmt.Sprintf("%s is unavailable for a bare worktree", action)
	}
	if wt.IsDetached || wt.Branch == "" || wt.Branch == "(detached)" {
		return fmt.Sprintf("%s requires a checked-out branch", action)
	}
	if wt.IsLocked {
		return fmt.Sprintf("%s is unavailable while the worktree is locked", action)
	}
	if wt.IsMissing {
		return fmt.Sprintf("%s is unavailable because the worktree path is missing", action)
	}
	if info, err := os.Stat(wt.Path); err != nil || !info.IsDir() {
		return fmt.Sprintf("%s is unavailable because the worktree path is missing", action)
	}
	return ""
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
	out, err := gitOutput(repoPath, "worktree", "list", "--porcelain")
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

// DirectMergeRecovery describes the safe actions available after a failed merge.
type DirectMergeRecovery string

const (
	DirectMergeRecoveryNone        DirectMergeRecovery = ""
	DirectMergeRecoveryConflict    DirectMergeRecovery = "conflict"
	DirectMergeRecoveryPushFailure DirectMergeRecovery = "push-failure"
)

// DirectMergeOperation is the immutable context and accumulated result of a merge.
type DirectMergeOperation struct {
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
	worktrees, err := readGitWorktrees(repoPath)
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
		if err := requireClean(checkout.Path); err != nil {
			return nil, err
		}
		if state := gitOperationState(checkout.Path); state != "clean" {
			return nil, fmt.Errorf("worktree %q has a Git operation in progress: %s", checkout.Path, state)
		}
	}
	sourceOID, err := gitOutput(source.Path, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("resolve source HEAD: %w", err)
	}
	sourceRefOID, err := gitOutput(repoPath, "rev-parse", "refs/heads/"+sourceBranch)
	if err != nil || sourceRefOID != sourceOID {
		return nil, fmt.Errorf("source branch moved or does not match its checkout")
	}
	targetOID, err := gitOutput(target.Path, "rev-parse", "HEAD")
	if err != nil {
		return nil, fmt.Errorf("resolve target HEAD: %w", err)
	}
	targetRefOID, err := gitOutput(repoPath, "rev-parse", "refs/heads/"+targetBranch)
	if err != nil || targetRefOID != targetOID {
		return nil, fmt.Errorf("target branch moved or does not match its checkout")
	}
	remote, err := resolveBranchRemote(repoPath, targetBranch)
	if err != nil {
		return nil, err
	}
	if _, err := gitOutput(repoPath, "ls-remote", "--exit-code", "--heads", remote, "refs/heads/"+targetBranch); err != nil {
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
	if op == nil {
		return &DirectMergeOperation{Err: fmt.Errorf("missing direct merge operation")}
	}
	if err := revalidateDirectMerge(op); err != nil {
		return failDirectMerge(op, err, DirectMergeRecoveryNone)
	}
	if _, err := gitOutput(op.TargetPath, "fetch", op.Remote, op.TargetBranch); err != nil {
		return failDirectMerge(op, fmt.Errorf("fetch %s: %w", op.Remote, err), DirectMergeRecoveryNone)
	}
	op.Completed = append(op.Completed, "fetch")
	if _, err := gitOutput(op.TargetPath, "pull", "--ff-only", op.Remote, op.TargetBranch); err != nil {
		return failDirectMerge(op, fmt.Errorf("fast-forward target: %w", err), DirectMergeRecoveryNone)
	}
	op.Completed = append(op.Completed, "fast-forward target")
	if err := requireClean(op.TargetPath); err != nil {
		return failDirectMerge(op, err, DirectMergeRecoveryNone)
	}
	if err := requireClean(op.SourcePath); err != nil {
		return failDirectMerge(op, fmt.Errorf("source changed after review: %w", err), DirectMergeRecoveryNone)
	}
	if state := gitOperationState(op.SourcePath); state != "clean" {
		return failDirectMerge(op, fmt.Errorf("source started a Git operation after review: %s", state), DirectMergeRecoveryNone)
	}
	if err := requireCheckoutIdentity(op.SourcePath, op.SourceBranch, op.SourceOID); err != nil {
		return failDirectMerge(op, fmt.Errorf("source checkout changed before merge: %w", err), DirectMergeRecoveryNone)
	}
	if err := requireCheckoutIdentity(op.TargetPath, op.TargetBranch, ""); err != nil {
		return failDirectMerge(op, fmt.Errorf("target checkout changed before merge: %w", err), DirectMergeRecoveryNone)
	}
	op.PreMergeOID, _ = gitOutput(op.TargetPath, "rev-parse", "HEAD")
	message := fmt.Sprintf("Merge branch '%s'", op.SourceBranch)
	if _, err := gitOutput(op.TargetPath, "merge", "--no-ff", op.SourceOID, "-m", message); err != nil {
		if gitOperationState(op.TargetPath) == "merge" {
			return failDirectMerge(op, fmt.Errorf("merge conflict: %w", err), DirectMergeRecoveryConflict)
		}
		return failDirectMerge(op, fmt.Errorf("merge: %w", err), DirectMergeRecoveryNone)
	}
	op.Completed = append(op.Completed, "merge")
	op.MergeOID, _ = gitOutput(op.TargetPath, "rev-parse", "HEAD")
	return pushDirectMerge(op)
}

func continueDirectMerge(op *DirectMergeOperation) *DirectMergeOperation {
	if op == nil || op.Recovery != DirectMergeRecoveryConflict {
		return failDirectMerge(op, fmt.Errorf("no conflicted merge is available to continue"), DirectMergeRecoveryNone)
	}
	if gitOperationState(op.TargetPath) != "merge" {
		return failDirectMerge(op, fmt.Errorf("the target no longer has the expected merge in progress"), DirectMergeRecoveryNone)
	}
	if unmerged, _ := gitOutput(op.TargetPath, "diff", "--name-only", "--diff-filter=U"); unmerged != "" {
		return failDirectMerge(op, fmt.Errorf("resolve all conflicts before continuing: %s", strings.ReplaceAll(unmerged, "\n", ", ")), DirectMergeRecoveryConflict)
	}
	cmd := exec.Command("git", "-c", "core.editor=true", "merge", "--continue")
	cmd.Dir = op.TargetPath
	if out, err := cmd.CombinedOutput(); err != nil {
		return failDirectMerge(op, fmt.Errorf("continue merge: %s: %w", strings.TrimSpace(string(out)), err), DirectMergeRecoveryConflict)
	}
	op.Completed = appendUnique(op.Completed, "merge")
	op.MergeOID, _ = gitOutput(op.TargetPath, "rev-parse", "HEAD")
	op.Recovery, op.Err = DirectMergeRecoveryNone, nil
	return pushDirectMerge(op)
}

func abortDirectMerge(op *DirectMergeOperation) *DirectMergeOperation {
	if op == nil || op.Recovery != DirectMergeRecoveryConflict {
		return failDirectMerge(op, fmt.Errorf("no conflicted merge is available to abort"), DirectMergeRecoveryNone)
	}
	if _, err := gitOutput(op.TargetPath, "merge", "--abort"); err != nil {
		return failDirectMerge(op, fmt.Errorf("abort merge: %w", err), DirectMergeRecoveryConflict)
	}
	head, _ := gitOutput(op.TargetPath, "rev-parse", "HEAD")
	if head != op.PreMergeOID {
		return failDirectMerge(op, fmt.Errorf("merge aborted but target HEAD is %s, expected %s", head, op.PreMergeOID), DirectMergeRecoveryNone)
	}
	op.Recovery, op.Err = DirectMergeRecoveryNone, nil
	op.Aborted = true
	op.GitState = currentGitState(op.TargetPath)
	return op
}

func retryDirectMergePush(op *DirectMergeOperation) *DirectMergeOperation {
	if op == nil || op.Recovery != DirectMergeRecoveryPushFailure {
		return failDirectMerge(op, fmt.Errorf("no failed push is available to retry"), DirectMergeRecoveryNone)
	}
	if state := gitOperationState(op.TargetPath); state != "clean" {
		return failDirectMerge(op, fmt.Errorf("cannot retry push during Git operation: %s", state), DirectMergeRecoveryPushFailure)
	}
	if err := requireClean(op.TargetPath); err != nil {
		return failDirectMerge(op, err, DirectMergeRecoveryPushFailure)
	}
	head, _ := gitOutput(op.TargetPath, "rev-parse", "HEAD")
	if head != op.MergeOID {
		return failDirectMerge(op, fmt.Errorf("target HEAD changed since the failed push"), DirectMergeRecoveryPushFailure)
	}
	return pushDirectMerge(op)
}

func pushDirectMerge(op *DirectMergeOperation) *DirectMergeOperation {
	refspec := "HEAD:refs/heads/" + op.TargetBranch
	if _, err := gitOutput(op.TargetPath, "push", op.Remote, refspec); err != nil {
		return failDirectMerge(op, fmt.Errorf("push %s %s: %w", op.Remote, op.TargetBranch, err), DirectMergeRecoveryPushFailure)
	}
	op.Completed = appendUnique(op.Completed, "push")
	op.Recovery, op.Err = DirectMergeRecoveryNone, nil
	op.GitState = currentGitState(op.TargetPath)
	return op
}

func revalidateDirectMerge(op *DirectMergeOperation) error {
	checks := []struct{ path, branch, want string }{
		{op.SourcePath, op.SourceBranch, op.SourceOID},
		{op.TargetPath, op.TargetBranch, op.TargetOID},
	}
	for _, check := range checks {
		if state := gitOperationState(check.path); state != "clean" {
			return fmt.Errorf("worktree %q has a Git operation in progress: %s", check.path, state)
		}
		if err := requireClean(check.path); err != nil {
			return err
		}
		if err := requireCheckoutIdentity(check.path, check.branch, check.want); err != nil {
			return err
		}
	}
	return nil
}

func requireCheckoutIdentity(path, branch, oid string) error {
	currentBranch, err := gitOutput(path, "symbolic-ref", "--quiet", "--short", "HEAD")
	if err != nil || currentBranch != branch {
		return fmt.Errorf("worktree %q checks out %q, expected %q", path, currentBranch, branch)
	}
	worktrees, err := readGitWorktrees(path)
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
		head, err := gitOutput(path, "rev-parse", "HEAD")
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
		op.GitState = currentGitState(op.TargetPath)
	}
	return op
}

func resolveBranchRemote(repoPath, branch string) (string, error) {
	if remote, err := gitOutput(repoPath, "config", "--get", "branch."+branch+".remote"); err == nil && remote != "" && remote != "." {
		return remote, nil
	}
	remotes, err := gitOutput(repoPath, "remote")
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

func requireClean(path string) error {
	status, err := gitOutput(path, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return fmt.Errorf("inspect %q: %w", path, err)
	}
	if status != "" {
		return fmt.Errorf("worktree %q is dirty", path)
	}
	return nil
}

func gitOperationState(path string) string {
	states := []struct{ name, key string }{
		{"merge", "MERGE_HEAD"}, {"rebase", "rebase-merge"}, {"rebase", "rebase-apply"}, {"cherry-pick", "CHERRY_PICK_HEAD"},
	}
	for _, state := range states {
		gitPath, err := gitOutput(path, "rev-parse", "--git-path", state.key)
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

func currentGitState(path string) string {
	head, _ := gitOutput(path, "rev-parse", "--short", "HEAD")
	branch, _ := gitOutput(path, "branch", "--show-current")
	status, _ := gitOutput(path, "status", "--short")
	state := fmt.Sprintf("target %s at %s; operation: %s", branch, head, gitOperationState(path))
	if status == "" {
		return state + "; working tree clean"
	}
	return state + "; status:\n" + status
}

func gitOutput(path string, args ...string) (string, error) {
	cmd := exec.Command("git", args...)
	cmd.Dir = path
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text == "" {
			return "", err
		}
		return "", fmt.Errorf("%s: %w", text, err)
	}
	return text, nil
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
	result := BaseUpdateResult{Branch: branch}
	if remote == "" {
		var err error
		remote, err = resolveBranchRemote(repoPath, branch)
		if err != nil {
			result.Err = err
			return result
		}
	}
	if _, err := gitOutput(repoPath, "fetch", remote, branch); err != nil {
		result.Err = fmt.Errorf("fetch %s %s: %w", remote, branch, err)
		return result
	}
	result.Fetched = true
	worktrees, err := readGitWorktrees(repoPath)
	if err != nil {
		result.Err = err
		return result
	}
	for _, wt := range worktrees {
		if wt.Branch == branch && !wt.Bare && !wt.Detached && !wt.Locked && !wt.Prunable {
			result.TargetPath = wt.Path
			break
		}
	}
	if result.TargetPath == "" {
		result.LeftUnchanged = true
		result.Message = fmt.Sprintf("Fetched %s/%s; local %s was intentionally left unchanged because it is not safely checked out", remote, branch, branch)
		return result
	}
	if err := requireClean(result.TargetPath); err != nil {
		result.LeftUnchanged = true
		result.Message = fmt.Sprintf("Fetched %s/%s; local %s was intentionally left unchanged", remote, branch, branch)
		result.Err = err
		return result
	}
	if state := gitOperationState(result.TargetPath); state != "clean" {
		result.LeftUnchanged = true
		result.Message = fmt.Sprintf("Fetched %s/%s; local %s was intentionally left unchanged", remote, branch, branch)
		result.Err = fmt.Errorf("target has a Git operation in progress: %s", state)
		return result
	}
	if _, err := gitOutput(result.TargetPath, "pull", "--ff-only", remote, branch); err != nil {
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
	RepoPath          string
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
}

func runCleanupPlan(plan CleanupPlan) *CleanupResults {
	results := &CleanupResults{}
	// Revalidate the selected identity before deleting anything.
	worktrees, err := readGitWorktrees(plan.RepoPath)
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
		remoteOID, err := remoteBranchOID(plan.RepoPath, plan.BranchRemote, plan.Branch)
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
	if plan.DeleteWorktree {
		if err := doDeleteWorktree(plan.RepoPath, plan.WorktreePath, false); err != nil {
			results.Errors = append(results.Errors, fmt.Sprintf("Workspace: %v", err))
		} else {
			results.LocalWorktreeDeleted = true
		}
	}
	if plan.DeleteBranch {
		if !results.LocalWorktreeDeleted && plan.DeleteWorktree {
			results.Errors = append(results.Errors, "Branch: skipped because worktree deletion failed")
		} else if err := deleteBranchSafe(plan.RepoPath, plan.Branch); err != nil {
			results.Errors = append(results.Errors, fmt.Sprintf("Branch: %v", err))
		} else {
			results.LocalBranchDeleted = true
		}
	}
	if plan.DeleteRemote {
		if err := deleteRemoteBranchFrom(plan.RepoPath, plan.BranchRemote, plan.Branch, plan.ExpectedRemoteOID); err != nil {
			results.Errors = append(results.Errors, fmt.Sprintf("Remote branch: %v", err))
		} else {
			results.RemoteBranchDeleted = true
		}
	}
	if plan.UpdateBase {
		update := updateCheckedOutBase(plan.RepoPath, plan.BaseBranch, plan.BaseRemote)
		results.PullAttempted = true
		results.PullSuccess = update.Updated || (update.Fetched && update.LeftUnchanged && update.Err == nil)
		results.PullError = update.Err
		results.PullMessage = update.Message
	}
	return results
}

func deleteBranchSafe(repoPath, branch string) error {
	if isMainBranch(repoPath, branch) {
		return fmt.Errorf("refusing to delete main branch %q", branch)
	}
	_, err := gitOutput(repoPath, "branch", "-d", branch)
	return err
}

func deleteRemoteBranchFrom(repoPath, remote, branch, expectedOID string) error {
	if isMainBranch(repoPath, branch) {
		return fmt.Errorf("refusing to delete remote main branch %q", branch)
	}
	if remote == "" {
		var err error
		remote, err = resolveBranchRemote(repoPath, branch)
		if err != nil {
			return err
		}
	}
	lease := "--force-with-lease=refs/heads/" + branch + ":" + expectedOID
	_, err := gitOutput(repoPath, "push", lease, remote, "--delete", branch)
	if err != nil && (strings.Contains(err.Error(), "remote ref does not exist") || strings.Contains(err.Error(), "couldn't find remote ref")) {
		return nil
	}
	return err
}

func remoteBranchOID(repoPath, remote, branch string) (string, error) {
	if remote == "" {
		return "", fmt.Errorf("remote is not resolved for branch %q", branch)
	}
	out, err := gitOutput(repoPath, "ls-remote", "--heads", remote, "refs/heads/"+branch)
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
