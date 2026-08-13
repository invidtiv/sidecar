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

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/plugins/gitstatus"
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

func TestPinnedDiffSnapshotIgnoresMovingBaseRef(t *testing.T) {
	dir := authoritativeRepo(t)
	baseOID := strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "main"))
	headOID := strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "HEAD"))
	// Move the human-readable base name onto the first feature commit after
	// inventory. A ref-based load would now report only one unique commit.
	runGitOutput(t, dir, "branch", "-f", "main", headOID+"~1")
	s, err := loadDiffSnapshotPinned(context.Background(), dir, "main", baseOID, headOID)
	if err != nil {
		t.Fatal(err)
	}
	if len(s.Commits) != 2 {
		t.Fatalf("pinned commits = %d, want 2 after base ref moved", len(s.Commits))
	}
	if s.MergeBase != baseOID {
		t.Fatalf("merge base = %s, want pinned %s", s.MergeBase, baseOID)
	}
}

func TestLoadDiffCapturesPinnedStrategyAcrossRefresh(t *testing.T) {
	dir := authoritativeRepo(t)
	baseOID := strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "main"))
	headOID := strings.TrimSpace(runGitOutput(t, dir, "rev-parse", "HEAD"))
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}

	binDir := t.TempDir()
	marker, release := filepath.Join(t.TempDir(), "git-started"), filepath.Join(t.TempDir(), "git-release")
	wrapper := filepath.Join(binDir, "git")
	script := fmt.Sprintf("#!/bin/sh\ntouch \"$SIDECAR_TEST_MARKER\"\nwhile [ ! -f \"$SIDECAR_TEST_RELEASE\" ]; do sleep 0.01; done\nexec %q \"$@\"\n", realGit)
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SIDECAR_TEST_MARKER", marker)
	t.Setenv("SIDECAR_TEST_RELEASE", release)

	wt := &Worktree{Key: "feature", RepoKey: "old-repo", Name: "feature", Path: dir,
		Branch: "feature", BaseBranch: "main", BaseOID: baseOID, HEADOID: headOID}
	p := New()
	p.ctx = &plugin.Context{Epoch: 17, WorkDir: dir, ProjectRoot: dir}
	p.operationCtx, p.operationCancel = context.WithCancel(context.Background())
	p.repoSnapshot = &RepoSnapshot{Key: "old-repo", Worktrees: []WorktreeSnapshot{{Key: wt.Key, BaseRef: "main", BaseOID: baseOID, HEADOID: headOID}}}
	p.worktrees, p.selectedIdx = []*Worktree{wt}, 0
	cmd := p.loadDiff(wt)
	done := make(chan tea.Msg, 1)
	go func() { done <- cmd() }()
	waitForFile(t, marker)

	// Clear the plugin snapshot through the real Update path and move the
	// display ref while the command is delayed. The command must retain its
	// already-captured pinned strategy and OIDs.
	p.refreshOperationID = "refresh-clear"
	p.update(RefreshDoneMsg{OperationScope: OperationScope{Epoch: 17, OperationID: "refresh-clear"}, Worktrees: []*Worktree{wt}})
	realCmd := exec.Command(realGit, "branch", "-f", "main", headOID+"~1")
	realCmd.Dir = dir
	if out, err := realCmd.CombinedOutput(); err != nil {
		t.Fatalf("move base: %s: %v", out, err)
	}
	if err := os.WriteFile(release, []byte("go"), 0o644); err != nil {
		t.Fatal(err)
	}

	var result tea.Msg
	select {
	case result = <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("delayed diff did not finish")
	}
	loaded, ok := result.(DiffLoadedMsg)
	if !ok {
		t.Fatalf("result = %T: %+v", result, result)
	}
	if len(loaded.Snapshot.Commits) != 2 {
		t.Fatalf("captured strategy returned %d commits, want 2", len(loaded.Snapshot.Commits))
	}

	// A subsequent repository swap makes the old operation scope stale; its
	// otherwise-valid result must not replace current diff state.
	p.refreshOperationID = "refresh-new"
	p.update(RefreshDoneMsg{OperationScope: OperationScope{Epoch: 17, OperationID: "refresh-new"},
		Snapshot: &RepoSnapshot{Key: "new-repo"}, Worktrees: []*Worktree{{Key: "new", RepoKey: "new-repo", Path: dir}}})
	p.diffError = "current"
	p.update(loaded)
	if p.diffError != "current" {
		t.Fatal("stale delayed diff result was applied after repository swap")
	}
}

func runGitOutput(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %s: %v", args, out, err)
	}
	return string(out)
}

func TestPorcelainRenamePreservesSourceAndDestination(t *testing.T) {
	for _, tc := range []struct{ name, xy string }{
		{"staged rename", "R "}, {"unstaged rename", " R"}, {"staged copy", "C "},
	} {
		t.Run(tc.name, func(t *testing.T) {
			changes := &WorktreeChanges{}
			parsePorcelainStatus([]byte(tc.xy+" renamed \"file\".txt\x00shared\nfile.txt\x00"), changes)
			if !containsPath(changes.Dirty, "renamed \"file\".txt") || !containsPath(changes.Dirty, "shared\nfile.txt") {
				t.Fatalf("dirty paths = %q", changes.Dirty)
			}
		})
	}
}

func containsPath(paths []string, want string) bool {
	for _, p := range paths {
		if p == want {
			return true
		}
	}
	return false
}

func TestRenameSourceOverlapsOtherWorktreeDirtyPath(t *testing.T) {
	worktrees := []*Worktree{
		{Key: "rename", Changes: &WorktreeChanges{Dirty: []string{"renamed.txt", "shared.txt"}}},
		{Key: "modify", Changes: &WorktreeChanges{Dirty: []string{"shared.txt"}}},
	}
	conflicts := detectConflictsFromChanges(worktrees)
	if len(conflicts) != 1 || !containsPath(conflicts[0].Files, "shared.txt") {
		t.Fatalf("overlaps = %+v", conflicts)
	}
}

func TestCollectedStatusPreservesStagedAndUnstagedRenameIdentities(t *testing.T) {
	for _, staged := range []bool{true, false} {
		name := "unstaged"
		if staged {
			name = "staged"
		}
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			runGitOutput(t, dir, "init", "-b", "main")
			runGitOutput(t, dir, "config", "user.name", "Sidecar Test")
			runGitOutput(t, dir, "config", "user.email", "sidecar@example.test")
			if err := os.WriteFile(filepath.Join(dir, "shared.txt"), []byte("shared\n"), 0o644); err != nil {
				t.Fatal(err)
			}
			runGitOutput(t, dir, "add", ".")
			runGitOutput(t, dir, "commit", "-m", "base")
			runGitOutput(t, dir, "mv", "shared.txt", "renamed.txt")
			if !staged {
				runGitOutput(t, dir, "reset", "HEAD")
			}
			changes, _ := collectWorktreeChanges(context.Background(), dir, nil)
			if changes.Err != nil {
				t.Fatal(changes.Err)
			}
			if !containsPath(changes.Dirty, "shared.txt") || !containsPath(changes.Dirty, "renamed.txt") {
				t.Fatalf("dirty = %q", changes.Dirty)
			}
		})
	}
}

func TestUntrackedCandidateCapBoundsAllLstatAttempts(t *testing.T) {
	dir := authoritativeRepo(t)
	for i := 0; i < maxUntrackedFiles+30; i++ {
		if err := os.Symlink("missing-target", filepath.Join(dir, fmt.Sprintf("link-%03d", i))); err != nil {
			t.Fatal(err)
		}
	}
	for _, tc := range []struct {
		name string
		stat func(string) (os.FileInfo, error)
	}{
		{"symlinks", os.Lstat},
		{"missing races", func(string) (os.FileInfo, error) { return nil, os.ErrNotExist }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			calls := 0
			_, meta, err := getUntrackedFileDiffsWithLstat(context.Background(), dir, func(path string) (os.FileInfo, error) { calls++; return tc.stat(path) })
			if err != nil {
				t.Fatal(err)
			}
			if calls != maxUntrackedFiles {
				t.Fatalf("Lstat calls = %d, want cap %d", calls, maxUntrackedFiles)
			}
			if !meta.Truncated || meta.Omitted < 30 {
				t.Fatalf("meta = %+v, want disclosed truncation", meta)
			}
		})
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
			maximum := loadRefreshChanges(context.Background(), worktrees, maxRefreshConcurrency, &processes)
			duration := time.Since(started)
			if got, want := processes.Load(), int64(count*2); got != want {
				t.Fatalf("processes = %d, want %d", got, want)
			}
			if duration > 15*time.Second {
				t.Fatalf("refresh duration %s exceeds recorded 15s local budget", duration)
			}
			if maximum < 1 || maximum > maxRefreshConcurrency {
				t.Fatalf("max concurrency = %d, want 1..%d", maximum, maxRefreshConcurrency)
			}
			for _, wt := range worktrees {
				if wt.Changes == nil || wt.Changes.Err != nil {
					t.Fatalf("missing status result: %+v", wt.Changes)
				}
			}
		})
	}
}

func TestRefreshConcurrencyFailureAndCancellation(t *testing.T) {
	t.Run("failure", func(t *testing.T) {
		worktrees := make([]*Worktree, 20)
		for i := range worktrees {
			worktrees[i] = &Worktree{Key: fmt.Sprint(i), Path: filepath.Join(t.TempDir(), "missing")}
		}
		var processes atomic.Int64
		maximum := loadRefreshChanges(context.Background(), worktrees, maxRefreshConcurrency, &processes)
		if maximum > maxRefreshConcurrency {
			t.Fatalf("max concurrency = %d", maximum)
		}
		if processes.Load() != int64(len(worktrees)) {
			t.Fatalf("failed status processes = %d, want %d", processes.Load(), len(worktrees))
		}
		for _, wt := range worktrees {
			if wt.Changes == nil || wt.Changes.Err == nil {
				t.Fatal("failure result missing")
			}
		}
	})
	t.Run("cancelled", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		worktrees := make([]*Worktree, 50)
		for i := range worktrees {
			worktrees[i] = &Worktree{Key: fmt.Sprint(i), Path: t.TempDir()}
		}
		var processes atomic.Int64
		done := make(chan int, 1)
		go func() { done <- loadRefreshChanges(ctx, worktrees, maxRefreshConcurrency, &processes) }()
		var maximum int
		select {
		case maximum = <-done:
		case <-time.After(2 * time.Second):
			t.Fatal("cancelled refresh workers leaked")
		}
		if maximum != 0 || processes.Load() != 0 {
			t.Fatalf("cancelled metrics max=%d processes=%d", maximum, processes.Load())
		}
		for _, wt := range worktrees {
			if wt.Changes == nil || wt.Changes.Err == nil {
				t.Fatal("cancelled result missing")
			}
		}
	})
}

func TestStaleRefreshResultCannotReplaceCurrentStatus(t *testing.T) {
	current := &Worktree{Key: "current", Changes: &WorktreeChanges{State: LoadStateReady, Dirty: []string{"keep"}}}
	p := New()
	p.ctx = &plugin.Context{Epoch: 9}
	p.worktrees = []*Worktree{current}
	p.refreshOperationID = "current-op"
	p.update(RefreshDoneMsg{OperationScope: OperationScope{Epoch: 9, OperationID: "stale-op"},
		Worktrees: []*Worktree{{Key: "stale"}}})
	if len(p.worktrees) != 1 || p.worktrees[0] != current || !containsPath(p.worktrees[0].Changes.Dirty, "keep") {
		t.Fatalf("stale refresh replaced current state: %+v", p.worktrees)
	}
}

func TestMergeDirtyCheckUsesSharedStatus(t *testing.T) {
	binDir := t.TempDir()
	marker := filepath.Join(t.TempDir(), "unexpected-git")
	git := filepath.Join(binDir, "git")
	if err := os.WriteFile(git, []byte("#!/bin/sh\ntouch \"$SIDECAR_TEST_MARKER\"\nexit 99\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("SIDECAR_TEST_MARKER", marker)
	p := New()
	p.ctx = &plugin.Context{Epoch: 1}
	p.operationCtx = context.Background()
	wt := &Worktree{Key: "wt", Name: "wt", Changes: &WorktreeChanges{State: LoadStateReady,
		Staged: []string{"a"}, Unstaged: []string{"b"}, Untracked: []string{"c"}}}
	msg := p.checkUncommittedChanges(wt)().(UncommittedChangesCheckMsg)
	if msg.Err != nil || !msg.HasChanges || msg.StagedCount != 1 || msg.ModifiedCount != 1 || msg.UntrackedCount != 1 {
		t.Fatalf("msg = %+v", msg)
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatal("merge dirty gating spawned duplicate git status")
	}
	wt.Changes = &WorktreeChanges{State: LoadStateError, Err: os.ErrPermission}
	msg = p.checkUncommittedChanges(wt)().(UncommittedChangesCheckMsg)
	if msg.Err == nil || !strings.Contains(msg.Err.Error(), "shared git status") {
		t.Fatalf("error msg = %+v", msg)
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

// A worktree whose tree is clean but whose branch carries commits must still
// list those commits in the default Working Tree scope — otherwise the branch's
// entire body of work is invisible until the user cycles the scope.
func TestCleanWorktreeStillListsBranchCommitsInWorkingTreeScope(t *testing.T) {
	dir := authoritativeRepo(t)
	for _, f := range []string{"staged.txt", "untracked.txt", "binary.dat", "oversized.txt"} {
		if err := os.Remove(filepath.Join(dir, f)); err != nil {
			t.Fatal(err)
		}
	}
	runGitOutput(t, dir, "reset", "--hard", "HEAD")

	s, err := loadDiffSnapshot(context.Background(), dir, "main")
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(s.WorkingTree) != "" {
		t.Fatalf("working tree should be clean, got %q", s.WorkingTree)
	}

	p := &Plugin{diffSnapshot: s, diffScope: DiffScopeWorkingTree}
	p.applyDiffScope()

	if len(p.commitStatusList) != 2 {
		t.Errorf("commits in working-tree scope = %d, want 2", len(p.commitStatusList))
	}
	if n := p.diffTabFileCount(); n != 0 {
		t.Errorf("clean working tree produced %d phantom file entries, want 0", n)
	}
}

const sampleWorkingTreeDiff = `diff --git a/foo.go b/foo.go
--- a/foo.go
+++ b/foo.go
@@ -1 +1,2 @@
 line
+added
`

func testDiffPlugin(t *testing.T) *Plugin {
	t.Helper()
	p := New()
	p.ctx = &plugin.Context{Epoch: 1}
	p.worktrees = []*Worktree{{Key: "wt", Name: "wt", Path: t.TempDir()}}
	p.selectedIdx = 0
	return p
}

func commitDetailHashFromCmd(t *testing.T, cmd tea.Cmd) (string, bool) {
	t.Helper()
	if cmd == nil {
		return "", false
	}
	msg := cmd()
	if batch, ok := msg.(tea.BatchMsg); ok {
		var hash string
		found := false
		for _, child := range batch {
			if h, ok := commitDetailHashFromCmd(t, child); ok {
				if found {
					t.Fatalf("multiple CommitDetailLoadedMsg in batch")
				}
				hash, found = h, true
			}
		}
		return hash, found
	}
	loaded, ok := msg.(CommitDetailLoadedMsg)
	if !ok {
		return "", false
	}
	return loaded.CommitHash, true
}

func TestDiffLoadedMsgLoadsFirstCommitWithoutCursorMove(t *testing.T) {
	p := testDiffPlugin(t)
	p.diffScope = DiffScopeWorkingTree
	p.diffTabCursor = 0

	_, cmd := p.update(DiffLoadedMsg{
		OperationScope: OperationScope{Epoch: 1, WorktreeKey: "wt"},
		WorkspaceName:  "wt",
		Snapshot: &DiffSnapshot{
			State: LoadStateReady,
			Commits: []CommitStatusInfo{
				{Hash: "aaa1111", Subject: "first"},
				{Hash: "bbb2222", Subject: "second"},
			},
		},
	})

	if p.diffTabCursor != 0 {
		t.Fatalf("cursor = %d, want 0", p.diffTabCursor)
	}
	if n := p.diffTabFileCount(); n != 0 {
		t.Fatalf("file count = %d, want 0 so cursor sits on first commit", n)
	}
	hash, ok := commitDetailHashFromCmd(t, cmd)
	if !ok {
		t.Fatal("applying snapshot with cursor on first commit did not issue loadCommitDetail")
	}
	if hash != "aaa1111" {
		t.Fatalf("loaded hash = %q, want first commit aaa1111", hash)
	}

	// Moving to another commit and back still loads, even without another snapshot.
	p.diffTabCursor = 1
	hash, ok = commitDetailHashFromCmd(t, p.onDiffTabCursorChanged(0))
	if !ok || hash != "bbb2222" {
		t.Fatalf("move to second commit: hash=%q ok=%v, want bbb2222", hash, ok)
	}
	p.diffTabCursor = 0
	hash, ok = commitDetailHashFromCmd(t, p.onDiffTabCursorChanged(1))
	if !ok || hash != "aaa1111" {
		t.Fatalf("move back to first commit: hash=%q ok=%v, want aaa1111", hash, ok)
	}
}

func TestDiffLoadedMsgDoesNotLoadCommitWhenCursorOnFile(t *testing.T) {
	p := testDiffPlugin(t)
	p.diffScope = DiffScopeWorkingTree

	_, cmd := p.update(DiffLoadedMsg{
		OperationScope: OperationScope{Epoch: 1, WorktreeKey: "wt"},
		WorkspaceName:  "wt",
		Snapshot: &DiffSnapshot{
			State:       LoadStateReady,
			WorkingTree: sampleWorkingTreeDiff,
			Commits:     []CommitStatusInfo{{Hash: "aaa1111", Subject: "first"}},
		},
	})

	if p.diffTabCursor != 0 {
		t.Fatalf("cursor = %d, want 0", p.diffTabCursor)
	}
	if n := p.diffTabFileCount(); n == 0 {
		t.Fatal("expected working-tree files so cursor sits on a file")
	}
	if hash, ok := commitDetailHashFromCmd(t, cmd); ok {
		t.Fatalf("file-under-cursor issued loadCommitDetail for %q", hash)
	}
}

const (
	testCommitShortHash = "aaa1111"
	testCommitFullHash  = "aaa1111bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
)

func TestLoadSelectedDiffTabCommitSkipsAlreadyLoadedHash(t *testing.T) {
	p := testDiffPlugin(t)
	p.commitStatusList = []CommitStatusInfo{{Hash: testCommitShortHash, Subject: "first"}}
	p.diffTabCursor = 0
	p.commitDetail = &gitstatus.Commit{Hash: testCommitFullHash, ShortHash: testCommitShortHash}
	p.commitFileCursor = 2

	if cmd := p.loadSelectedDiffTabCommit(); cmd != nil {
		t.Fatal("already-loaded commit under cursor should not refetch")
	}
	if p.commitFileCursor != 2 {
		t.Fatalf("skip reset commitFileCursor to %d, want 2", p.commitFileCursor)
	}

	// ShortHash can be empty; list %h is still a prefix of detail %H.
	p.commitDetail = &gitstatus.Commit{Hash: testCommitFullHash}
	if cmd := p.loadSelectedDiffTabCommit(); cmd != nil {
		t.Fatal("full-hash prefix of list short hash should skip")
	}

	// A later cursor move onto a different commit still loads.
	p.commitStatusList = append(p.commitStatusList, CommitStatusInfo{Hash: "bbb2222", Subject: "second"})
	p.diffTabCursor = 1
	hash, ok := commitDetailHashFromCmd(t, p.onDiffTabCursorChanged(0))
	if !ok || hash != "bbb2222" {
		t.Fatalf("move after skip: hash=%q ok=%v, want bbb2222", hash, ok)
	}
}

func TestDiffLoadedMsgPreservesCommitFileCursor(t *testing.T) {
	p := testDiffPlugin(t)
	p.diffScope = DiffScopeWorkingTree
	p.diffTabCursor = 0
	p.commitDetail = &gitstatus.Commit{
		Hash:      testCommitFullHash,
		ShortHash: testCommitShortHash,
		Files:     []gitstatus.CommitFile{{Path: "a.go"}, {Path: "b.go"}, {Path: "c.go"}},
	}
	p.commitFileCursor = 2

	_, cmd := p.update(DiffLoadedMsg{
		OperationScope: OperationScope{Epoch: 1, WorktreeKey: "wt"},
		WorkspaceName:  "wt",
		Snapshot: &DiffSnapshot{
			State:   LoadStateReady,
			Commits: []CommitStatusInfo{{Hash: testCommitShortHash, Subject: "first"}},
		},
	})

	if p.commitDetail == nil || p.commitDetail.Hash != testCommitFullHash {
		t.Fatal("refresh cleared commitDetail for the already-loaded commit")
	}
	if p.commitFileCursor != 2 {
		t.Fatalf("commitFileCursor = %d, want 2 after DiffLoadedMsg", p.commitFileCursor)
	}
	if hash, ok := commitDetailHashFromCmd(t, cmd); ok {
		t.Fatalf("refresh issued loadCommitDetail for %q", hash)
	}
}

func TestCycleDiffScopeLoadsFirstCommit(t *testing.T) {
	p := testDiffPlugin(t)
	p.diffSnapshot = &DiffSnapshot{
		State:       LoadStateReady,
		WorkingTree: sampleWorkingTreeDiff,
		Commits: []CommitStatusInfo{
			{Hash: "aaa1111", Subject: "first"},
		},
	}
	p.diffScope = DiffScopeWorkingTree
	p.applyDiffScope()
	if p.diffTabFileCount() == 0 {
		t.Fatal("working-tree scope should list files before cycling")
	}

	hash, ok := commitDetailHashFromCmd(t, p.cycleDiffScope())
	if !ok || hash != "aaa1111" {
		t.Fatalf("cycle to commits: hash=%q ok=%v, want aaa1111", hash, ok)
	}
	if p.diffScope != DiffScopeCommits {
		t.Fatalf("scope = %v, want DiffScopeCommits", p.diffScope)
	}
}
