package overview

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/worktreedelete"
)

// The global browser's confirmation must be as truthful about uncommitted work
// as the project surface's, because it is the same confirmation (td-d37612).
// Both surfaces ask the same question, worktreedelete.ProbeDirtiness, when the
// modal opens — dirtiness is not carried on workspaceinventory.Workspace,
// because a `git status` per worktree per refresh cycle is exactly the kind of
// spawn AGENTS.md keeps off a polling path.
//
// The fixture is a throwaway repository under t.TempDir(). Nothing here deletes
// anything: only the confirmation is opened.

func dirtyTestRepo(t *testing.T, dirty bool) string {
	t.Helper()
	dir := t.TempDir()
	git := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v: %s", args, err, out)
		}
	}
	git("init", "-q")
	git("config", "user.email", "test@example.com")
	git("config", "user.name", "Test")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("committed\n"), 0o644); err != nil {
		t.Fatalf("seed file: %v", err)
	}
	git("add", "file.txt")
	git("commit", "-q", "-m", "seed")
	if dirty {
		if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("edited\n"), 0o644); err != nil {
			t.Fatalf("dirty the worktree: %v", err)
		}
	}
	return dir
}

// globalConfirmationView points the selected worktree at a real repository,
// opens the confirmation through the D key, lets the probe land, and returns
// what is on screen.
func globalConfirmationView(t *testing.T, dirty bool) (*Model, string) {
	t.Helper()
	repo := dirtyTestRepo(t, dirty)

	m, _ := previewModel(t)
	result := m.results["sidecar"]
	rows := result.Workspaces
	for i := range rows {
		if rows[i].ID == "a" {
			rows[i].Path = repo
			rows[i].ProjectRoot = repo
		}
	}
	result.Workspaces = rows
	m.results["sidecar"] = result
	m.projects = []Project{{Name: "sidecar", Path: repo, Key: "sidecar"}}
	m.syncBoard()
	run(t, m, m.SetWorkspacesVisible(true))
	selectWorkspace(t, m, "a")

	handled, cmd := m.WorkspacesKey(key("D"))
	if !handled || !m.DeletingWorktree() {
		t.Fatalf("D did not raise the worktree confirmation (handled=%v worktree=%v)", handled, m.DeletingWorktree())
	}
	run(t, m, cmd)

	built := m.worktreeDelete.Modal(m.width)
	if built == nil {
		t.Fatal("the armed confirmation built no modal")
	}
	return m, m.WorkspacesView(120, 32)
}

func TestGlobalDeleteWarnsAboutUncommittedChangesOnlyWhenDirty(t *testing.T) {
	m, view := globalConfirmationView(t, true)
	if got := m.worktreeDelete.Dirty; got != worktreedelete.DirtinessDirty {
		t.Fatalf("dirty worktree resolved to %v, want DirtinessDirty", got)
	}
	if !strings.Contains(view, worktreedelete.DirtyLine) {
		t.Fatalf("a dirty worktree was not warned about:\n%s", view)
	}

	m, view = globalConfirmationView(t, false)
	if got := m.worktreeDelete.Dirty; got != worktreedelete.DirtinessClean {
		t.Fatalf("clean worktree resolved to %v, want DirtinessClean", got)
	}
	if strings.Contains(view, worktreedelete.DirtyLine) {
		t.Fatalf("a clean worktree still claimed uncommitted changes would be lost:\n%s", view)
	}
	if !strings.Contains(view, worktreedelete.CleanLine) {
		t.Fatalf("a clean worktree was not told it is clean:\n%s", view)
	}
}

// A probe answer that arrives for a different target must not relabel the open
// confirmation.
func TestGlobalDeleteIgnoresAProbeForAnotherWorktree(t *testing.T) {
	m, _ := globalConfirmationView(t, false)
	m.Update(globalWorktreeDeleteProbeMsg{Path: "/somewhere/else", Dirty: worktreedelete.DirtinessDirty})
	if m.worktreeDelete.Dirty != worktreedelete.DirtinessClean {
		t.Fatalf("a probe for another worktree changed this confirmation to %v", m.worktreeDelete.Dirty)
	}
}
