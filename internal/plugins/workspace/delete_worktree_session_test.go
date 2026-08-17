package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/workspaceops"
)

// td-a66836. The project surface has always closed a worktree's tmux session
// before removing the directory; the fix moved the kill itself onto the shared
// workspaceops path so the global surface gets it too. This test is what says
// the move did not lose it here.
//
// TestMain pins this package to a throwaway tmux server and a throwaway state
// tree. The repository is created fresh under t.TempDir(). Nothing below can
// reach a real session, worktree, or branch.

func gitIn(t *testing.T, dir string, args ...string) {
	t.Helper()
	full := append([]string{
		"-c", "user.email=test@example.invalid",
		"-c", "user.name=Test",
		"-c", "commit.gpgsign=false",
		"-c", "init.defaultBranch=main",
	}, args...)
	cmd := exec.Command("git", full...)
	cmd.Dir = dir
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_TERMINAL_PROMPT=0")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git %v in %s: %v: %s", args, dir, err, out)
	}
}

// throwawayRepoWithWorktree returns a fresh repository and one worktree of it.
func throwawayRepoWithWorktree(t *testing.T, name string) (root, worktree string) {
	t.Helper()
	base := t.TempDir()
	root = filepath.Join(base, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(root, "README"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	gitIn(t, root, "add", "README")
	gitIn(t, root, "commit", "-q", "-m", "initial")
	worktree = filepath.Join(base, name)
	gitIn(t, root, "worktree", "add", "-q", worktree, "-b", name)
	return root, worktree
}

func TestProjectDeleteStillKillsTheWorktreeSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	root, path := throwawayRepoWithWorktree(t, "session-holder")
	wt := &Worktree{Name: "session-holder", Path: path, Branch: "session-holder"}

	session := worktreeTmuxSession(wt)
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", path).CombinedOutput(); err != nil {
		t.Skipf("cannot start an isolated tmux session (%v): %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })
	if !workspaceops.SessionExists(session) {
		t.Fatalf("session %q did not start", session)
	}

	p := &Plugin{
		worktrees:             []*Worktree{wt},
		selectedIdx:           0,
		deleteConfirmWorktree: wt,
		managedSessions:       map[string]bool{session: true},
		ctx:                   &plugin.Context{WorkDir: root},
	}
	globalPaneCache.setAll(map[string]string{session: "some output"})

	cmd := p.executeDelete()
	if cmd == nil {
		t.Fatal("executeDelete produced no work")
	}
	// The surface's own state is dropped synchronously, as it always was.
	if p.managedSessions[session] {
		t.Fatal("the managed-session record survived")
	}
	if _, ok := globalPaneCache.get(session); ok {
		t.Fatal("the cached pane output survived")
	}

	msg, ok := cmd().(DeleteDoneMsg)
	if !ok {
		t.Fatalf("delete produced %#v", cmd())
	}
	if msg.Err != nil {
		t.Fatalf("delete reported %v", msg.Err)
	}
	if workspaceops.SessionExists(session) {
		t.Fatalf("session %q survived the delete", session)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the working directory survived: %v", err)
	}
}

func TestProjectDeleteOfAWorktreeWithNoSessionDoesNotError(t *testing.T) {
	root, path := throwawayRepoWithWorktree(t, "quiet")
	wt := &Worktree{Name: "quiet", Path: path, Branch: "quiet"}
	if workspaceops.SessionExists(worktreeTmuxSession(wt)) {
		t.Fatal("test precondition: the session must not exist")
	}

	p := &Plugin{
		worktrees:             []*Worktree{wt},
		selectedIdx:           0,
		deleteConfirmWorktree: wt,
		managedSessions:       map[string]bool{},
		ctx:                   &plugin.Context{WorkDir: root},
	}
	cmd := p.executeDelete()
	if cmd == nil {
		t.Fatal("executeDelete produced no work")
	}
	msg, ok := cmd().(DeleteDoneMsg)
	if !ok {
		t.Fatalf("delete produced %#v", cmd())
	}
	if msg.Err != nil {
		t.Fatalf("delete of a session-less worktree reported %v", msg.Err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the working directory survived: %v", err)
	}
}

// The delete path must be able to find the session this plugin creates. The two
// naming schemes in the codebase disagree for some directory names, so the
// shared resolver lists both spellings rather than picking one.
func TestSharedDeleteResolvesThisPluginsSessionName(t *testing.T) {
	for _, name := range []string{"auth-refresh", "My_Feature", "fix.v2"} {
		wt := &Worktree{Name: "display name", Path: filepath.Join("/tmp/repo", name)}
		want := worktreeTmuxSession(wt)
		got := workspaceops.WorktreeSessionNames(wt.Path, "")
		found := false
		for _, candidate := range got {
			if candidate == want {
				found = true
			}
		}
		if !found {
			t.Fatalf("shared delete would not kill %q for %s (it names %s)", want, name, strings.Join(got, ", "))
		}
	}
}

// td-3df472. Post-merge cleanup used to kill the worktree's tmux session after
// runCleanupPlan had already removed the directory, which left whatever was
// running in that session alive with a deleted working directory. The fix is
// not a re-ordering here: cleanup now removes the worktree through
// workspaceops.DeleteWorktree, where the kill lives ahead of the git work and
// no caller can get the order wrong. These tests say cleanup still reaches it.
func TestMergeCleanupClosesTheWorktreeSessionThroughTheSharedPath(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	r := newLifecycleRepo(t)
	expectedOID := mustGit(t, r.feature, "rev-parse", "HEAD")

	session := worktreeTmuxSession(&Worktree{Name: "feature", Path: r.feature})
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", r.feature).CombinedOutput(); err != nil {
		t.Skipf("cannot start an isolated tmux session (%v): %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })
	if !workspaceops.SessionExists(session) {
		t.Fatalf("session %q did not start", session)
	}

	results := runCleanupPlan(CleanupPlan{
		RepoPath: r.main, WorktreePath: r.feature, Branch: "feature", ExpectedOID: expectedOID,
		DeleteWorktree: true,
	})
	if len(results.Errors) > 0 {
		t.Fatalf("cleanup reported %v", results.Errors)
	}
	if !results.LocalWorktreeDeleted {
		t.Fatal("cleanup did not remove the worktree")
	}
	if workspaceops.SessionExists(session) {
		t.Fatalf("session %q survived the cleanup", session)
	}
	if _, err := os.Stat(r.feature); !os.IsNotExist(err) {
		t.Fatalf("the working directory survived: %v", err)
	}
}

// The cleanup path states no force, so it keeps the refusal it has always had —
// and, because the checks run before the kill, a refusal costs no session.
func TestMergeCleanupRefusesADirtyWorktreeWithoutKillingItsSession(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	r := newLifecycleRepo(t)
	expectedOID := mustGit(t, r.feature, "rev-parse", "HEAD")
	mustWrite(t, filepath.Join(r.feature, "valuable.txt"), "irreplaceable untracked work\n")

	session := worktreeTmuxSession(&Worktree{Name: "feature", Path: r.feature})
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", session, "-c", r.feature).CombinedOutput(); err != nil {
		t.Skipf("cannot start an isolated tmux session (%v): %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", session).Run() })

	results := runCleanupPlan(CleanupPlan{
		RepoPath: r.main, WorktreePath: r.feature, Branch: "feature", ExpectedOID: expectedOID,
		DeleteWorktree: true, DeleteBranch: true,
	})
	if len(results.Errors) == 0 || !strings.Contains(strings.Join(results.Errors, "\n"), "dirty") {
		t.Fatalf("cleanup result = %+v, want a dirty refusal", results)
	}
	if results.LocalWorktreeDeleted || results.LocalBranchDeleted {
		t.Fatalf("a refused cleanup deleted something: %+v", results)
	}
	if !workspaceops.SessionExists(session) {
		t.Fatalf("the refused cleanup killed session %q anyway", session)
	}
}
