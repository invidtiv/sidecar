package workspaceops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/projectdir"
)

// PersistWorktreeIdentity records the minimal Sidecar metadata shared by every
// host after Git creates a worktree.
func PersistWorktreeIdentity(ctx context.Context, plan *WorktreePlan) []SetupOutcome {
	if plan == nil {
		return []SetupOutcome{{Kind: "identity", Action: "persist identity", Required: true, Err: fmt.Errorf("missing plan")}}
	}
	dir, err := projectdir.WorktreeDirContext(ctx, plan.MainWorktree, plan.Path)
	if err != nil {
		return []SetupOutcome{{Kind: "identity", Action: "resolve worktree state", Required: true, Err: err}}
	}
	write := func(kind, action, file, value string, required bool) SetupOutcome {
		if err := ctx.Err(); err != nil {
			return SetupOutcome{Kind: kind, Action: action, Required: required, Err: err}
		}
		path := filepath.Join(dir, file)
		if strings.TrimSpace(value) == "" {
			_ = os.Remove(path)
			return SetupOutcome{Kind: kind, Action: action, Required: required}
		}
		return SetupOutcome{Kind: kind, Action: action, Required: required, Err: WriteDurableFile(path, []byte(value+"\n"), 0644)}
	}
	outcomes := []SetupOutcome{
		write("identity", "base metadata", "base", strings.TrimPrefix(plan.SourceRef, "refs/heads/"), true),
		write("identity", "display name", "display-name", plan.DisplayName, true),
		write("agent-metadata", "agent metadata", "agent", plan.AgentType, true),
	}
	if plan.TaskID != "" {
		outcomes = append(outcomes, write("task-link", "task link "+plan.TaskID, "task", plan.TaskID, true))
	}
	return outcomes
}

// DeleteCreatedWorktree removes only the exact clean identity returned by the
// creation core, then deletes its local branch. It has no force fallback.
//
// It runs the shared removal path (DeleteWorktree) rather than its own git
// commands, which is what stops a rollback from orphaning the session a host
// may already have launched into the new worktree: the kill lives inside that
// path, ahead of the removal, for every caller.
func DeleteCreatedWorktree(ctx context.Context, plan *WorktreePlan, record *WorktreeRecord) error {
	if plan == nil || record == nil || record.HEADOID == "" {
		return fmt.Errorf("creation identity is incomplete")
	}
	if err := DeleteWorktree(ctx, WorktreeRemoval{
		RepoPath:    plan.SourceWorktree,
		ProjectRoot: plan.MainWorktree,
		Path:        record.Path,
		Branch:      record.Branch,
		ExpectedOID: record.HEADOID,
	}); err != nil {
		return fmt.Errorf("remove created worktree: %w", err)
	}
	if err := DeleteLocalBranch(ctx, BranchDeletion{
		RepoPath:    plan.SourceWorktree,
		Branch:      record.Branch,
		ExpectedOID: record.HEADOID,
	}); err != nil {
		return fmt.Errorf("delete created branch: %w", err)
	}
	return nil
}
