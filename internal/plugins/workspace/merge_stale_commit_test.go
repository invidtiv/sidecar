package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
)

// The commit-for-merge modal's counts come from a cached status snapshot. If an
// agent commits between the snapshot and the user pressing Commit, git reports
// "nothing to commit" and the modal used to dead-end on that error. The clean
// tree must instead pass through as success so the merge workflow continues.
func TestStageAllAndCommitTreatsAlreadyCleanTreeAsSuccess(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")

	p := New()
	p.ctx = &plugin.Context{Epoch: 1}
	p.operationCtx = context.Background()
	wt := &Worktree{Key: "wt", Name: "wt", Path: dir}

	msg := p.stageAllAndCommit(wt, "Agent chips")().(MergeCommitDoneMsg)
	if msg.Err != nil {
		t.Fatalf("clean tree returned error: %v", msg.Err)
	}
	if !msg.NothingToCommit {
		t.Fatal("expected NothingToCommit on an already-clean tree")
	}

	// A dirty tree still commits normally.
	if err := os.WriteFile(filepath.Join(dir, "b.txt"), []byte("y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	msg = p.stageAllAndCommit(wt, "Agent chips")().(MergeCommitDoneMsg)
	if msg.Err != nil || msg.NothingToCommit || msg.CommitHash == "" {
		t.Fatalf("dirty tree commit = %+v", msg)
	}
}

// Pressing m must not trust a stale dirty snapshot. After the files have been
// committed, merge proceeds without the commit-for-merge modal, and the shared
// snapshot is replaced with the live result.
func TestMergeStartAppliesFreshStatusAndSkipsStaleCommitModal(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")

	p := New()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: dir}
	p.operationCtx = context.Background()
	wt := &Worktree{
		Key: "wt", Name: "wt", Path: dir, Branch: "main",
		Changes: &WorktreeChanges{State: LoadStateReady, Unstaged: []string{"styles.go", "themes.go"}},
	}
	p.worktrees = []*Worktree{wt}

	check := p.startMergeWorkflow(wt)().(UncommittedChangesCheckMsg)
	if check.Err != nil || check.HasChanges {
		t.Fatalf("fresh check = %+v", check)
	}

	_, cmd := p.update(check)
	if wt.Changes == nil || wt.Changes.State != LoadStateClean {
		t.Fatalf("shared snapshot not refreshed: %+v", wt.Changes)
	}
	if p.viewMode != ViewModeMerge {
		t.Fatalf("viewMode = %v, want merge (no commit modal)", p.viewMode)
	}
	if p.mergeCommitState != nil {
		t.Fatalf("commit-for-merge modal opened from stale dirty snapshot: %+v", p.mergeCommitState)
	}
	if p.mergeState == nil || p.mergeState.Worktree != wt {
		t.Fatalf("merge state = %+v", p.mergeState)
	}
	if cmd == nil {
		t.Fatal("expected merge workflow to continue after a clean check")
	}
}

func TestMergeStartShowsCommitModalForLiveDirtyTree(t *testing.T) {
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
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	run("add", ".")
	run("commit", "-m", "base")
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	p := New()
	p.ctx = &plugin.Context{Epoch: 1, WorkDir: dir}
	p.operationCtx = context.Background()
	wt := &Worktree{
		Key: "wt", Name: "wt", Path: dir, Branch: "main",
		Changes: &WorktreeChanges{State: LoadStateClean},
	}
	p.worktrees = []*Worktree{wt}

	check := p.startMergeWorkflow(wt)().(UncommittedChangesCheckMsg)
	if check.Err != nil || !check.HasChanges || check.ModifiedCount != 1 {
		t.Fatalf("fresh check = %+v", check)
	}

	p.update(check)
	if p.viewMode != ViewModeCommitForMerge {
		t.Fatalf("viewMode = %v, want commit-for-merge", p.viewMode)
	}
	if p.mergeCommitState == nil || p.mergeCommitState.ModifiedCount != 1 {
		t.Fatalf("mergeCommitState = %+v", p.mergeCommitState)
	}
	if wt.Changes == nil || !containsPath(wt.Changes.Unstaged, "a.txt") {
		t.Fatalf("shared snapshot not refreshed: %+v", wt.Changes)
	}
}
