package workspace

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/plugin"
)

func authoritativeRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s: %v", args, out, err)
		}
	}
	run("init", "-b", "main")
	run("config", "user.name", "Sidecar Test")
	run("config", "user.email", "sidecar@example.test")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	run("switch", "-c", "feature")
	for i := 1; i <= 2; i++ {
		if err := os.WriteFile(filepath.Join(dir, fmt.Sprintf("commit-%d.txt", i)), []byte("committed\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run("add", ".")
		run("commit", "-m", fmt.Sprintf("feature %d", i))
	}
	if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", "staged.txt")
	if err := os.WriteFile(filepath.Join(dir, "tracked.txt"), []byte("base\nunstaged\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("untracked\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "binary.dat"), []byte{0, 1, 2, 3}, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "oversized.txt"), make([]byte, maxUntrackedFileSize+1), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestLoadDiffSnapshotSeparatesAllThreeViewsAndBoundsUntracked(t *testing.T) {
	dir := authoritativeRepo(t)
	s, err := loadDiffSnapshot(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Commits) != 2 {
		t.Fatalf("commits = %d, want 2", len(s.Commits))
	}
	for _, want := range []string{"staged.txt", "tracked.txt", "untracked.txt", "binary.dat", "oversized.txt"} {
		if !strings.Contains(s.WorkingTree, want) {
			t.Errorf("working diff missing %q", want)
		}
	}
	if !strings.Contains(s.WorkingTree, "Binary files") {
		t.Error("binary untracked file is not identified")
	}
	if !strings.Contains(s.WorkingTree, "File too large to display") || !s.Truncated {
		t.Error("oversized untracked file is not disclosed as truncated")
	}
	if !strings.Contains(s.AggregateCommitted, "commit-1.txt") || !strings.Contains(s.AggregateCommitted, "commit-2.txt") {
		t.Error("aggregate committed section does not cover all branch commits")
	}
	if strings.Contains(s.AggregateCommitted, "staged.txt") {
		t.Error("aggregate committed section contains uncommitted changes")
	}
	if !strings.Contains(s.AggregateUncommitted, "staged.txt") {
		t.Error("aggregate uncommitted section missing staged changes")
	}
}

func TestMissingWorktreeRendersErrorNotClean(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "gone")
	_, err := loadDiffSnapshot(context.Background(), missing, "main")
	if err == nil {
		t.Fatal("missing worktree unexpectedly loaded")
	}
	p := New()
	p.worktrees = []*Worktree{{Key: "missing-key", Name: "same", Path: missing}}
	p.selectedIdx = 0
	p.diffState = LoadStateError
	p.diffError = err.Error()
	p.ctx = &plugin.Context{}
	got := p.renderDiffContent(80, 10)
	if !strings.Contains(got, "Error loading diff") || strings.Contains(got, "No changes") {
		t.Fatalf("render = %q", got)
	}
}

func TestStatsRouteByStableKeyForNestedSameBasename(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 7}
	p.worktrees = []*Worktree{
		{Key: "feature-auth", Name: "feature/auth", Path: "/repos/feature/auth"},
		{Key: "fix-auth", Name: "fix/auth", Path: "/repos/fix/auth"},
	}
	msg := StatsLoadedMsg{OperationScope: OperationScope{Epoch: 7, WorktreeKey: "fix-auth"}, WorkspaceName: "auth", Stats: &GitStats{Additions: 9}}
	p.update(msg)
	if p.worktrees[0].Stats != nil {
		t.Fatal("stats routed to same-basename sibling")
	}
	if p.worktrees[1].Stats == nil || p.worktrees[1].Stats.Additions != 9 {
		t.Fatal("stats did not route by stable key")
	}
}

func TestRefreshStatusProcessAndLatencyBudgets(t *testing.T) {
	dir := authoritativeRepo(t)
	for _, count := range []int{1, 10, 50} {
		t.Run(fmt.Sprintf("worktrees_%d", count), func(t *testing.T) {
			worktrees := make([]*Worktree, count)
			for i := range worktrees {
				worktrees[i] = &Worktree{Key: fmt.Sprintf("k-%d", i), Path: dir}
			}
			var processes atomic.Int64
			started := time.Now()
			loadRefreshChanges(context.Background(), worktrees, maxRefreshConcurrency, &processes)
			duration := time.Since(started)
			if got, want := processes.Load(), int64(count*2); got != want {
				t.Fatalf("processes = %d, want %d", got, want)
			}
			if duration > 15*time.Second {
				t.Fatalf("refresh duration %s exceeds recorded 15s local budget", duration)
			}
			for _, wt := range worktrees {
				if wt.Changes == nil || wt.Changes.Err != nil {
					t.Fatalf("missing status result: %+v", wt.Changes)
				}
			}
		})
	}
}

func TestCellWidthSafeWorktreeName(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{}
	wt := &Worktree{Key: "wide", Name: "猫猫猫猫猫猫猫猫", Path: t.TempDir(), CreatedAt: time.Now(), UpdatedAt: time.Now()}
	for _, line := range strings.Split(p.renderWorktreeItem(wt, false, 18), "\n") {
		if width := ansi.StringWidth(line); width > 18 {
			t.Fatalf("rendered width = %d > 18: %q", width, line)
		}
	}
}
