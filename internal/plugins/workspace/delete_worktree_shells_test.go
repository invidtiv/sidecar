package workspace

import (
	"os"
	"path/filepath"
	"sort"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
)

// td-f017b9. Deleting a worktree used to leave the shells that lived in it in
// shells.json, naming a directory that no longer existed. The reaper will not
// clean those up — it never observed their sessions alive — so they persisted
// until removed by hand.
//
// The rule and its near-miss cases are tested in
// internal/workspaceops/shell_forget_worktree_test.go. What this file says is
// that the *project surface* reaches that rule, which is the half of parity
// this package owns; internal/overview/delete_worktree_test.go says the same
// for the global browser.
//
// TestMain pins this package to a throwaway tmux server and a throwaway state
// tree, and the repository is created fresh under t.TempDir().

func projectShellNames(t *testing.T, projectRoot string) []string {
	t.Helper()
	dir, err := projectdir.Resolve(projectRoot)
	if err != nil {
		t.Fatalf("resolve project dir: %v", err)
	}
	defs, err := shellstate.ListAtPath(filepath.Join(dir, "shells.json"))
	if err != nil {
		t.Fatalf("list shells: %v", err)
	}
	var names []string
	for _, def := range defs {
		names = append(names, def.TmuxName)
	}
	sort.Strings(names)
	return names
}

func recordProjectShell(t *testing.T, projectRoot, tmuxName, displayName, workDir string) {
	t.Helper()
	dir, err := projectdir.Resolve(projectRoot)
	if err != nil {
		t.Fatalf("resolve project dir: %v", err)
	}
	err = shellstate.AddAtPath(filepath.Join(dir, "shells.json"), shellstate.Definition{
		TmuxName: tmuxName, DisplayName: displayName, CreatedAt: time.Now(), WorkDir: workDir,
	})
	if err != nil {
		t.Fatalf("record shell %s: %v", tmuxName, err)
	}
}

func TestProjectDeleteForgetsShellsRootedInTheWorktree(t *testing.T) {
	root, path := throwawayRepoWithWorktree(t, "feature")

	// A sibling worktree whose name has the deleted one as a prefix. A
	// path-prefix match would sweep its shell up too.
	sibling := filepath.Join(filepath.Dir(path), "feature-2")
	gitIn(t, root, "worktree", "add", "-q", sibling, "-b", "feature-2")

	recordProjectShell(t, root, "sidecar-sh-in-worktree", "Inside", path)
	recordProjectShell(t, root, "sidecar-sh-nested", "Nested", filepath.Join(path, "internal"))
	recordProjectShell(t, root, "sidecar-sh-sibling", "Sibling", sibling)
	recordProjectShell(t, root, "sidecar-sh-repo", "Repo root", root)

	wt := &Worktree{Name: "feature", Path: path, Branch: "feature"}
	p := &Plugin{
		worktrees:             []*Worktree{wt},
		selectedIdx:           0,
		deleteConfirmWorktree: wt,
		managedSessions:       map[string]bool{},
		ctx:                   &plugin.Context{WorkDir: root, ProjectRoot: root},
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
		t.Fatalf("delete reported %v", msg.Err)
	}
	if _, err := os.Stat(path); !os.IsNotExist(err) {
		t.Fatalf("the working directory survived: %v", err)
	}

	got := projectShellNames(t, root)
	want := []string{"sidecar-sh-repo", "sidecar-sh-sibling"}
	if len(got) != len(want) {
		t.Fatalf("manifest holds %v, want %v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("manifest holds %v, want %v", got, want)
		}
	}
}
