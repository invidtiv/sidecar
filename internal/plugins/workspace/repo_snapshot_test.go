package workspace

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/plugin"
)

func TestBuildRepoSnapshotCarriesStableIndependentIdentity(t *testing.T) {
	repo := filepath.Join(t.TempDir(), "repo")
	runSnapshotGit(t, "", "init", "-b", "main", repo)
	runSnapshotGit(t, repo, "config", "user.email", "test@example.com")
	runSnapshotGit(t, repo, "config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(repo, "README"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	runSnapshotGit(t, repo, "add", "README")
	runSnapshotGit(t, repo, "commit", "-m", "init")
	remote := filepath.Join(t.TempDir(), "origin.git")
	runSnapshotGit(t, "", "init", "--bare", remote)
	runSnapshotGit(t, repo, "remote", "add", "origin", remote)
	runSnapshotGit(t, repo, "push", "-u", "origin", "main")
	root := t.TempDir()
	feature := filepath.Join(root, "feature", "auth")
	fix := filepath.Join(root, "fix", "auth")
	runSnapshotGit(t, repo, "worktree", "add", "-b", "feature/auth", feature)
	runSnapshotGit(t, repo, "worktree", "add", "-b", "fix/auth", fix)

	snapshot, err := BuildRepoSnapshot(context.Background(), feature)
	if err != nil {
		t.Fatal(err)
	}
	if snapshot.Key == "" || snapshot.CanonicalCommonDir == "" || snapshot.CanonicalRoot != canonicalGitPath(repo) {
		t.Fatalf("incomplete repository identity: %+v", snapshot)
	}
	if len(snapshot.Worktrees) != 3 {
		t.Fatalf("worktrees = %d, want 3", len(snapshot.Worktrees))
	}
	seen := map[string]WorktreeSnapshot{}
	for _, wt := range snapshot.Worktrees {
		if wt.Key == "" || wt.RepoKey != snapshot.Key || wt.HEADOID == "" {
			t.Fatalf("incomplete worktree snapshot: %+v", wt)
		}
		if _, duplicate := seen[wt.Key]; duplicate {
			t.Fatalf("duplicate stable key %q", wt.Key)
		}
		seen[wt.Key] = wt
	}
	if snapshot.CheckedOut["feature/auth"] == snapshot.CheckedOut["fix/auth"] {
		t.Fatal("same-basename worktrees share checked-out identity")
	}
	for _, wt := range snapshot.Worktrees {
		if wt.Branch == "main" && (wt.Remote != "origin" || wt.Upstream != "origin/main") {
			t.Fatalf("main remote identity = remote %q upstream %q", wt.Remote, wt.Upstream)
		}
	}

	restarted, err := BuildRepoSnapshot(context.Background(), fix)
	if err != nil {
		t.Fatal(err)
	}
	if restarted.Key != snapshot.Key || restarted.CheckedOut["feature/auth"] != snapshot.CheckedOut["feature/auth"] {
		t.Fatal("snapshot identity changed across worktree/restart context")
	}
}

func TestScopedResultRejectedAfterSwitch(t *testing.T) {
	p := New()
	p.ctx = &plugin.Context{Epoch: 8}
	p.repoSnapshot = &RepoSnapshot{Key: "repo-new"}
	p.worktrees = []*Worktree{{Key: "new-wt", RepoKey: "repo-new", Name: "auth"}}
	stale := OperationScope{Epoch: 7, OperationID: "7-1", RepoKey: "repo-old", WorktreeKey: "old-wt"}
	if p.scopeMatches(stale) {
		t.Fatal("stale switched-project result was accepted")
	}
	current := OperationScope{Epoch: 8, OperationID: "8-1", RepoKey: "repo-new", WorktreeKey: "new-wt"}
	if !p.scopeMatches(current) {
		t.Fatal("current operation result was rejected")
	}
	p.activeLifecycleOperationID = "8-live"
	current.Lifecycle = true
	if p.scopeMatches(current) {
		t.Fatal("result from replaced lifecycle operation was accepted")
	}
}

func TestInitCancelsPriorOperationsAndResetsLifecycleState(t *testing.T) {
	p := New()
	if err := p.Init(&plugin.Context{Epoch: 1}); err != nil {
		t.Fatal(err)
	}
	oldCtx := p.operationCtx
	p.mergeState = &MergeWorkflowState{Worktree: &Worktree{Name: "old"}}
	p.linkingWorktree = &Worktree{Name: "old"}
	p.fetchPRLoading = true
	if err := p.Init(&plugin.Context{Epoch: 2}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-oldCtx.Done():
	default:
		t.Fatal("reinit did not cancel prior subprocess context")
	}
	if p.mergeState != nil || p.linkingWorktree != nil || p.fetchPRLoading {
		t.Fatal("reinit retained lifecycle/modal state")
	}
}

func TestDelayedPROperationIsCancelledAndCannotMutateSwitchedProject(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "started")
	gh := filepath.Join(binDir, "gh")
	if err := os.WriteFile(gh, []byte("#!/bin/sh\ntouch \"$SIDECAR_TEST_MARKER\"\nexec sleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SIDECAR_TEST_MARKER", marker)

	p := New()
	if err := p.Init(&plugin.Context{Epoch: 3, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	cmd := p.fetchPRList()
	result := make(chan FetchPRListMsg, 1)
	go func() { result <- cmd().(FetchPRListMsg) }()
	deadline := time.Now().Add(2 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("delayed gh command did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	if err := p.Init(&plugin.Context{Epoch: 4, WorkDir: t.TempDir(), ProjectRoot: t.TempDir()}); err != nil {
		t.Fatal(err)
	}
	select {
	case msg := <-result:
		p.fetchPRItems = []PRListItem{{Number: 99}}
		p.update(msg)
		if len(p.fetchPRItems) != 1 || p.fetchPRItems[0].Number != 99 {
			t.Fatal("stale cancelled PR result mutated the switched project")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("cancelled gh subprocess did not return promptly")
	}
}

func runSnapshotGit(t *testing.T, dir string, args ...string) {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
}
