package workspaceops

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// td-a66836. Deleting a worktree has to close the tmux session running in it
// before the directory goes, or whatever is in that session — an agent, most of
// the time — keeps running with a working directory that no longer exists.
// These tests are on the shared path in workspaceops rather than on either
// surface, because that is where the behaviour lives; the surface tests assert
// only that they route through here.
//
// Every tmux call below reaches the throwaway server TestMain pins. Nothing
// here may touch a real session, worktree, or branch.

func tmuxTest(t *testing.T, args ...string) (string, error) {
	t.Helper()
	out, err := exec.Command("tmux", args...).CombinedOutput()
	return strings.TrimSpace(string(out)), err
}

// startThrowawaySession makes a detached session on the isolated server and
// guarantees it is gone at the end of the test even if the code under test
// fails to kill it.
func startThrowawaySession(t *testing.T, name, workDir string) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
	if out, err := tmuxTest(t, "new-session", "-d", "-s", name, "-c", workDir); err != nil {
		t.Skipf("cannot start an isolated tmux session (%v): %s", err, out)
	}
	t.Cleanup(func() { _, _ = tmuxTest(t, "kill-session", "-t", name) })
	if !SessionExists(name) {
		t.Fatalf("session %q did not start", name)
	}
}

func TestWorktreeSessionNamesCoverBothSpellings(t *testing.T) {
	// An ordinary lowercase directory: the slug scheme and the plugin's
	// metacharacter scheme agree, so there is one name to kill.
	if got := WorktreeSessionNames("/tmp/repo/auth-refresh", ""); !reflect.DeepEqual(got, []string{"sidecar-ws-auth-refresh"}) {
		t.Fatalf("names = %v, want one canonical name", got)
	}
	// A directory the two schemes disagree about. Sessions exist under both
	// spellings in the wild — the global surface and the CLI create the first,
	// the project plugin the second — so a delete has to name both.
	got := WorktreeSessionNames("/tmp/repo/My_Feature", "")
	want := []string{"sidecar-ws-my-feature", "sidecar-ws-My_Feature"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("names = %v, want %v", got, want)
	}
	if got := WorktreeSessionNames("", ""); got != nil {
		t.Fatalf("names for no path = %v, want none", got)
	}
}

func TestDeleteWorktreeKillsTheWorktreeSession(t *testing.T) {
	root := throwawayRepo(t)
	path := filepath.Join(filepath.Dir(root), "session-holder")
	git(t, root, "worktree", "add", "-q", path, "-b", "session-holder")

	session := WorktreeSessionName(path, "")
	startThrowawaySession(t, session, path)

	if err := DeleteWorktree(context.Background(), root, path, false); err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if SessionExists(session) {
		t.Fatalf("session %q survived the delete", session)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the working directory survived: %v", err)
	}
	if got := worktreePaths(t, root); len(got) != 1 {
		t.Fatalf("git still lists %v", got)
	}
}

func TestDeleteWorktreeKillsEitherSessionSpelling(t *testing.T) {
	root := throwawayRepo(t)
	// A basename the two naming schemes disagree about, so a delete that knew
	// only one of them would leave the other session running.
	path := filepath.Join(filepath.Dir(root), "My_Feature")
	git(t, root, "worktree", "add", "-q", path, "-b", "my-feature")

	names := WorktreeSessionNames(path, "")
	if len(names) != 2 {
		t.Fatalf("names = %v, want two rival spellings", names)
	}
	for _, name := range names {
		startThrowawaySession(t, name, path)
	}

	if err := DeleteWorktree(context.Background(), root, path, false); err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	for _, name := range names {
		if SessionExists(name) {
			t.Fatalf("session %q survived the delete", name)
		}
	}
}

func TestDeleteWorktreeWithNoSessionIsNotAnError(t *testing.T) {
	root := throwawayRepo(t)
	path := filepath.Join(filepath.Dir(root), "quiet")
	git(t, root, "worktree", "add", "-q", path, "-b", "quiet")

	if SessionExists(WorktreeSessionName(path, "")) {
		t.Fatal("test precondition: the session must not exist")
	}
	if err := DeleteWorktree(context.Background(), root, path, false); err != nil {
		t.Fatalf("DeleteWorktree with no session: %v", err)
	}
	if got := worktreePaths(t, root); len(got) != 1 {
		t.Fatalf("git still lists %v", got)
	}
}

func TestDeleteMissingWorktreeStillKillsItsSession(t *testing.T) {
	root := throwawayRepo(t)
	path := filepath.Join(filepath.Dir(root), "vanished")
	git(t, root, "worktree", "add", "-q", path, "-b", "vanished")

	session := WorktreeSessionName(path, "")
	startThrowawaySession(t, session, path)
	// The directory is gone but the session that lived in it is not. This is
	// the worst version of the bug, and pruning alone would leave it running.
	if err := os.RemoveAll(path); err != nil {
		t.Fatal(err)
	}

	if err := DeleteWorktree(context.Background(), root, path, true); err != nil {
		t.Fatalf("DeleteWorktree(isMissing): %v", err)
	}
	if SessionExists(session) {
		t.Fatalf("session %q survived the prune", session)
	}
}

func TestDeleteWorktreeKillsBeforeRemovingTheDirectory(t *testing.T) {
	root := throwawayRepo(t)
	path := filepath.Join(filepath.Dir(root), "ordered")
	git(t, root, "worktree", "add", "-q", path, "-b", "ordered")

	// The ordering is the whole point of the fix, so assert it directly rather
	// than inferring it from the end state.
	directoryPresentAtKill := false
	restore := killWorktreeSessions
	killWorktreeSessions = func(ctx context.Context, p string) error {
		if p != path {
			t.Errorf("kill was aimed at %q, want %q", p, path)
		}
		_, err := os.Stat(p)
		directoryPresentAtKill = err == nil
		return nil
	}
	t.Cleanup(func() { killWorktreeSessions = restore })

	if err := DeleteWorktree(context.Background(), root, path, false); err != nil {
		t.Fatalf("DeleteWorktree: %v", err)
	}
	if !directoryPresentAtKill {
		t.Fatal("the session was killed after the directory was removed")
	}
}

func TestDeleteWorktreeStopsWhenTheSessionCannotBeKilled(t *testing.T) {
	root := throwawayRepo(t)
	path := filepath.Join(filepath.Dir(root), "stubborn")
	git(t, root, "worktree", "add", "-q", path, "-b", "stubborn")

	restore := killWorktreeSessions
	killWorktreeSessions = func(context.Context, string) error { return os.ErrPermission }
	t.Cleanup(func() { killWorktreeSessions = restore })

	if err := DeleteWorktree(context.Background(), root, path, false); err == nil {
		t.Fatal("DeleteWorktree succeeded despite a surviving session")
	}
	// Refusing is the point: the directory must not go while something is
	// still running in it.
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the working directory was removed anyway: %v", err)
	}
}

func TestKillWorktreeSessionIsSatisfiedByAnAbsentSession(t *testing.T) {
	if err := KillWorktreeSession(context.Background(), ""); err != nil {
		t.Fatalf("empty session name: %v", err)
	}
	if err := KillWorktreeSession(context.Background(), "sidecar-ws-never-existed-a66836"); err != nil {
		t.Fatalf("absent session: %v", err)
	}
}
