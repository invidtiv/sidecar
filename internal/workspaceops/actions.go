package workspaceops

import (
	"fmt"
	"os"
)

type WorktreeAction string

const (
	WorktreeActionDelete WorktreeAction = "delete"
	WorktreeActionPush   WorktreeAction = "push"
	WorktreeActionMerge  WorktreeAction = "merge"
)

// WorktreeActionState is the shared, presentation-neutral refusal input.
type WorktreeActionState struct {
	Path, Branch                         string
	IsMain, IsBare, IsDetached, IsLocked bool
	IsMissing, IsPrunable                bool
	// TrustPath lets an inventory-backed presentation avoid a filesystem call
	// on every frame; activation still performs its own current validation.
	TrustPath bool
}

func WorktreeActionRefusal(wt *WorktreeActionState, action WorktreeAction) string {
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
	if wt.IsPrunable {
		return fmt.Sprintf("%s is unavailable because the worktree record is prunable", action)
	}
	if !wt.TrustPath {
		if info, err := os.Stat(wt.Path); err != nil || !info.IsDir() {
			return fmt.Sprintf("%s is unavailable because the worktree path is missing", action)
		}
	}
	return ""
}
