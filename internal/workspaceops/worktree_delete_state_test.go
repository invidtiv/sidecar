package workspaceops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
)

func TestConfirmedDeleteRevalidatesIdentityAfterTeardown(t *testing.T) {
	root := throwawayRepo(t)
	path := filepath.Join(filepath.Dir(root), "topic")
	git(t, root, "worktree", "add", "-q", "-b", "topic", path)
	ctx := context.Background()
	state, err := WorktreeDeleteState(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	head := git(t, path, "rev-parse", "HEAD")
	previous := killWorktreeSessions
	t.Cleanup(func() { killWorktreeSessions = previous })
	killWorktreeSessions = func(context.Context, string) error { git(t, path, "branch", "-m", "replacement"); return nil }
	err = DeleteWorktree(ctx, WorktreeRemoval{RepoPath: root, Path: path, Branch: "topic", ExpectedOID: head, ExpectedDeleteState: state, Force: true})
	var identity *WorktreeIdentityError
	if !errors.As(err, &identity) {
		t.Fatalf("replacement checkout was not refused: %v", err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("replacement worktree lost: %v", err)
	}
}
