package workspaceinventory

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// Git's porcelain state markers decide whether an action may run against a
// worktree at all. The catalog has to carry them, or a presentation surface
// built on it cannot refuse what the project surface refuses (td-2af16d).

func TestParseWorktreesCarriesGitsStateMarkers(t *testing.T) {
	text := strings.Join([]string{
		"worktree /repo",
		"HEAD abc",
		"branch refs/heads/main",
		"",
		"worktree /repo-locked",
		"HEAD abc",
		"branch refs/heads/locked-branch",
		"locked being used by another tool",
		"",
		"worktree /repo-gone",
		"HEAD abc",
		"branch refs/heads/gone",
		"prunable gitdir file points to non-existent location",
		"",
		"worktree /repo-detached",
		"HEAD abc",
		"detached",
		"",
		"worktree /repo-bare",
		"bare",
		"",
	}, "\n")

	got := parseWorktrees(text)
	if len(got) != 5 {
		t.Fatalf("parsed %d worktrees, want 5: %#v", len(got), got)
	}
	if got[0].Locked || got[0].Prunable || got[0].Detached || got[0].Bare || got[0].Branch != "main" {
		t.Fatalf("the plain worktree parsed as %#v", got[0])
	}
	if !got[1].Locked {
		t.Fatalf("a locked worktree with a reason parsed as %#v", got[1])
	}
	if !got[2].Prunable {
		t.Fatalf("a prunable worktree parsed as %#v", got[2])
	}
	if !got[3].Detached || got[3].Branch != "" {
		t.Fatalf("a detached worktree parsed as %#v", got[3])
	}
	if !got[4].Bare {
		t.Fatalf("a bare worktree parsed as %#v", got[4])
	}
}

func TestCollectedWorktreesCarryLockedPrunableAndMissingState(t *testing.T) {
	root := t.TempDir()
	present := filepath.Join(root, "present")
	locked := filepath.Join(root, "locked")
	if err := os.MkdirAll(present, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(locked, 0o755); err != nil {
		t.Fatal(err)
	}
	gone := filepath.Join(root, "gone")

	porcelain := strings.Join([]string{
		"worktree " + root,
		"branch refs/heads/main",
		"",
		"worktree " + present,
		"branch refs/heads/present",
		"",
		"worktree " + locked,
		"branch refs/heads/locked-branch",
		"locked in use",
		"",
		"worktree " + gone,
		"branch refs/heads/gone",
		"prunable gitdir file points to non-existent location",
		"",
	}, "\n")

	collector := Collector{Runner: &fakeRunner{git: map[string]string{root: porcelain}}}
	result := collector.CollectProjectInventory(context.Background(), "p", root)
	if result.Err != nil {
		t.Fatalf("collect: %v", result.Err)
	}

	byName := map[string]Workspace{}
	for _, workspace := range result.Workspaces {
		byName[workspace.Name] = workspace
	}

	if w := byName["locked"]; !w.IsLocked || w.IsMissing || w.IsPrunable {
		t.Fatalf("the locked worktree collected as %+v", w)
	}
	if w := byName["gone"]; !w.IsPrunable || !w.IsMissing {
		t.Fatalf("the prunable worktree collected as %+v", w)
	}
	if w := byName["present"]; w.IsLocked || w.IsMissing || w.IsPrunable || w.IsBare || w.IsDetached {
		t.Fatalf("an ordinary worktree collected state it does not have: %+v", w)
	}
}

// A directory that has vanished without git noticing yet is still missing.
func TestAWorktreeDirectoryThatVanishedIsMissingBeforeGitCallsItPrunable(t *testing.T) {
	root := t.TempDir()
	vanished := filepath.Join(root, "vanished")
	porcelain := strings.Join([]string{
		"worktree " + root,
		"branch refs/heads/main",
		"",
		"worktree " + vanished,
		"branch refs/heads/vanished",
		"",
	}, "\n")

	collector := Collector{Runner: &fakeRunner{git: map[string]string{root: porcelain}}}
	result := collector.CollectProjectInventory(context.Background(), "p", root)
	for _, workspace := range result.Workspaces {
		if workspace.Name != "vanished" {
			continue
		}
		if !workspace.IsMissing || workspace.IsPrunable {
			t.Fatalf("a vanished directory collected as %+v", workspace)
		}
		return
	}
	t.Fatal("the vanished worktree was not collected at all")
}
