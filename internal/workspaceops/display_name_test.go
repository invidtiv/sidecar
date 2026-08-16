package workspaceops

import (
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/projectdir"
)

func TestLookupWorktreeDisplayNameMatchesWorkspaceFallback(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "sidecar")
	root := filepath.Join(t.TempDir(), "sidecar")
	worktree := filepath.Join(filepath.Dir(root), "experiments", "pane-handles")
	if _, err := projectdir.WorktreeDirWithBase(stateDir, root, worktree); err != nil {
		t.Fatal(err)
	}

	got, err := LookupWorktreeDisplayName(stateDir, root, worktree)
	if err != nil {
		t.Fatal(err)
	}
	if want := filepath.Join("experiments", "pane-handles"); got != want {
		t.Fatalf("fallback display name = %q, want %q", got, want)
	}
}

func TestRenameWorktreeDisplayNamePersistsSharedValue(t *testing.T) {
	stateDir := filepath.Join(t.TempDir(), "sidecar")
	root := filepath.Join(t.TempDir(), "sidecar")
	if _, err := projectdir.WorktreeDirWithBase(stateDir, root, root); err != nil {
		t.Fatal(err)
	}

	result, err := RenameWorktreeDisplayName(t.Context(), stateDir, root, root, "  Pane polish  ")
	if err != nil {
		t.Fatal(err)
	}
	if !result.Changed || result.OldName != "sidecar" || result.Name != "Pane polish" {
		t.Fatalf("rename result = %+v", result)
	}
	if got, err := LookupWorktreeDisplayName(stateDir, root, root); err != nil || got != "Pane polish" {
		t.Fatalf("persisted display name = %q, %v", got, err)
	}
}
