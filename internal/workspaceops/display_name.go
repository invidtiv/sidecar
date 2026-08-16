package workspaceops

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
)

const worktreeDisplayNameFile = "display-name"

// WorktreeDisplayNameResult describes the shared outcome used by both the TUI
// rename action and the agent-facing CLI.
type WorktreeDisplayNameResult struct {
	OldName string `json:"oldName"`
	Name    string `json:"name"`
	Changed bool   `json:"changed"`
}

// LookupWorktreeDisplayName reads the registered Sidecar worktree identity
// without creating state. Worktrees without an explicit name use their path
// basename, matching the human workspace list.
func LookupWorktreeDisplayName(stateDir, projectRoot, worktreePath string) (string, error) {
	dir, ok := projectdir.LookupWorktreeWithBase(stateDir, projectRoot, worktreePath)
	if !ok {
		return "", fmt.Errorf("current tmux session is not a registered Sidecar worktree agent")
	}
	return worktreeDisplayNameAt(dir, projectRoot, worktreePath)
}

func worktreeDisplayNameAt(dir, projectRoot, worktreePath string) (string, error) {
	data, err := os.ReadFile(filepath.Join(dir, worktreeDisplayNameFile))
	if err != nil && !os.IsNotExist(err) {
		return "", fmt.Errorf("read worktree display name: %w", err)
	}
	if name := strings.TrimSpace(string(data)); name != "" {
		return name, nil
	}
	if filepath.Clean(worktreePath) != filepath.Clean(projectRoot) {
		if rel, relErr := filepath.Rel(filepath.Dir(projectRoot), worktreePath); relErr == nil && rel != "" {
			return rel, nil
		}
	}
	return filepath.Base(filepath.Clean(worktreePath)), nil
}

// RenameWorktreeDisplayName validates and durably persists the presentation
// name Sidecar owns. It does not rename the Git branch, worktree, or tmux
// session identity.
func RenameWorktreeDisplayName(ctx context.Context, stateDir, projectRoot, worktreePath, name string) (WorktreeDisplayNameResult, error) {
	name, err := shellstate.NormalizeName(name)
	if err != nil {
		return WorktreeDisplayNameResult{}, err
	}
	dir, ok := projectdir.LookupWorktreeWithBase(stateDir, projectRoot, worktreePath)
	if !ok {
		dir, err = projectdir.WorktreeDirWithBase(stateDir, projectRoot, worktreePath)
		if err != nil {
			return WorktreeDisplayNameResult{}, fmt.Errorf("resolve worktree display-name state: %w", err)
		}
	}
	oldName, err := worktreeDisplayNameAt(dir, projectRoot, worktreePath)
	if err != nil {
		return WorktreeDisplayNameResult{}, err
	}
	result := WorktreeDisplayNameResult{OldName: oldName, Name: name, Changed: oldName != name}
	if !result.Changed {
		return result, nil
	}
	if err := ctx.Err(); err != nil {
		return WorktreeDisplayNameResult{}, err
	}
	if err := WriteDurableFile(filepath.Join(dir, worktreeDisplayNameFile), []byte(name+"\n"), 0o644); err != nil {
		return WorktreeDisplayNameResult{}, fmt.Errorf("write worktree display name: %w", err)
	}
	return result, nil
}

// WorktreeRoot returns the Git worktree root containing path.
func WorktreeRoot(ctx context.Context, path string) string {
	out, err := gitOutput(ctx, path, "--no-optional-locks", "rev-parse", "--show-toplevel")
	if err != nil {
		return ""
	}
	return filepath.Clean(strings.TrimSpace(out))
}
