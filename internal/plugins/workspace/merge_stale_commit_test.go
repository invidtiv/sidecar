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
