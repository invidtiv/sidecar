package workspaceops

import (
	"context"
	"fmt"
	"os"
	"os/exec"
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
func DeleteCreatedWorktree(ctx context.Context, plan *WorktreePlan, record *WorktreeRecord) error {
	if plan == nil || record == nil || record.HEADOID == "" {
		return fmt.Errorf("creation identity is incomplete")
	}
	head, err := gitOutput(ctx, record.Path, "rev-parse", "HEAD")
	if err != nil || head != record.HEADOID {
		return fmt.Errorf("created worktree HEAD changed; refusing delete")
	}
	status, err := gitOutput(ctx, record.Path, "status", "--porcelain=v1", "--untracked-files=normal")
	if err != nil {
		return err
	}
	if status != "" {
		return fmt.Errorf("created worktree is dirty; refusing delete")
	}
	cmd := exec.CommandContext(ctx, "git", "worktree", "remove", record.Path)
	cmd.Dir = plan.SourceWorktree
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("remove created worktree: %s: %w", strings.TrimSpace(string(out)), err)
	}
	if _, err := gitOutput(ctx, plan.SourceWorktree, "branch", "-d", record.Branch); err != nil {
		return fmt.Errorf("delete created branch: %w", err)
	}
	return nil
}
