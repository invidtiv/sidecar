package worktreedelete

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
)

// The confirmation's warning used to say "Uncommitted changes will be lost"
// for every worktree, which taught the user to skip past the only sentence
// standing between the keypress and lost work (td-d37612). These pin what it
// says now: the warning when git found work, a plain statement when it did
// not, and nothing reassuring before git has answered.

// tempRepo builds a throwaway git repository with one commit. Nothing here
// touches a real repository, worktree, branch, or session.
func tempRepo(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	run := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	run("init", "-q")
	run("config", "user.email", "test@example.com")
	run("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	run("add", "file.txt")
	run("commit", "-q", "-m", "seed")
	return dir
}

func renderWith(t *testing.T, dirty Dirtiness) string {
	t.Helper()
	var state State
	state.Open(Target{Name: "feature", Branch: "feature", Path: "/tmp/feature"}, false)
	state.Dirty = dirty
	built := state.Modal(80)
	if built == nil {
		t.Fatal("no confirmation was built")
	}
	return built.Render(80, 24, mouse.NewHandler())
}

func TestConfirmationWarnsOnlyWhenTheWorktreeIsDirty(t *testing.T) {
	dirtyView := renderWith(t, DirtinessDirty)
	if !strings.Contains(dirtyView, DirtyLine) {
		t.Fatalf("a dirty worktree did not get the warning:\n%s", dirtyView)
	}
	if strings.Contains(dirtyView, CleanLine) {
		t.Fatalf("a dirty worktree was also called clean:\n%s", dirtyView)
	}

	cleanView := renderWith(t, DirtinessClean)
	if strings.Contains(cleanView, DirtyLine) {
		t.Fatalf("a clean worktree still got the uncommitted-changes warning:\n%s", cleanView)
	}
	if !strings.Contains(cleanView, CleanLine) {
		t.Fatalf("a clean worktree was not told it is clean:\n%s", cleanView)
	}
}

// Before git answers, the confirmation may neither warn nor reassure.
func TestConfirmationSaysNothingDefiniteBeforeGitAnswers(t *testing.T) {
	view := renderWith(t, DirtinessUnknown)
	if strings.Contains(view, DirtyLine) || strings.Contains(view, CleanLine) {
		t.Fatalf("an unanswered confirmation claimed to know the worktree's state:\n%s", view)
	}
	if !strings.Contains(view, UnknownDirtinessLine) {
		t.Fatalf("an unanswered confirmation said nothing about uncommitted work:\n%s", view)
	}
}

// Open must not carry a previous target's answer into the next confirmation.
func TestOpenResetsDirtiness(t *testing.T) {
	var state State
	state.Open(Target{Name: "a", Branch: "a", Path: "/tmp/a"}, false)
	state.Dirty = DirtinessDirty
	state.Open(Target{Name: "b", Branch: "b", Path: "/tmp/b"}, false)
	if state.Dirty != DirtinessUnknown {
		t.Fatalf("Dirty = %v after reopening, want DirtinessUnknown", state.Dirty)
	}
}

func TestProbeDirtinessReadsGit(t *testing.T) {
	repo := tempRepo(t)
	if got := ProbeDirtiness(context.Background(), repo, false); got != DirtinessClean {
		t.Fatalf("ProbeDirtiness on a clean repo = %v, want DirtinessClean", got)
	}

	if err := os.WriteFile(filepath.Join(repo, "file.txt"), []byte("edited\n"), 0o644); err != nil {
		t.Fatalf("dirty the worktree: %v", err)
	}
	if got := ProbeDirtiness(context.Background(), repo, false); got != DirtinessDirty {
		t.Fatalf("ProbeDirtiness after a tracked edit = %v, want DirtinessDirty", got)
	}
}

// An untracked file is work a force-remove destroys, so it counts as dirty.
func TestProbeDirtinessCountsUntrackedFiles(t *testing.T) {
	repo := tempRepo(t)
	if err := os.WriteFile(filepath.Join(repo, "scratch.txt"), []byte("notes\n"), 0o644); err != nil {
		t.Fatalf("add untracked file: %v", err)
	}
	if got := ProbeDirtiness(context.Background(), repo, false); got != DirtinessDirty {
		t.Fatalf("ProbeDirtiness with an untracked file = %v, want DirtinessDirty", got)
	}
}

// Anything git cannot answer stays Unknown. Calling an unreadable worktree
// clean would be the same false reassurance in the other direction.
func TestProbeDirtinessStaysUnknownWhenGitCannotAnswer(t *testing.T) {
	if got := ProbeDirtiness(context.Background(), t.TempDir(), false); got != DirtinessUnknown {
		t.Fatalf("ProbeDirtiness outside a repository = %v, want DirtinessUnknown", got)
	}
	if got := ProbeDirtiness(context.Background(), tempRepo(t), true); got != DirtinessUnknown {
		t.Fatalf("ProbeDirtiness on a missing worktree = %v, want DirtinessUnknown", got)
	}
	if got := ProbeDirtiness(context.Background(), "", false); got != DirtinessUnknown {
		t.Fatalf("ProbeDirtiness on an empty path = %v, want DirtinessUnknown", got)
	}
}
