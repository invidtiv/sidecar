package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/worktreedelete"
)

// The delete confirmation's "Uncommitted changes will be lost" must describe
// this worktree, not every worktree (td-d37612). The global Workspaces browser
// proves the same thing over its own flow in internal/overview.
//
// Everything here runs against a throwaway repository under t.TempDir(); no
// real repository, worktree, branch, or tmux session is touched, and nothing is
// deleted — only the confirmation is opened.

// dirtyTestRepo builds a throwaway repository with one commit, dirtied on
// request by editing the committed file.
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

// runCmds executes a command the way the Bubble Tea loop does, flattening
// batches, and delivers every message back to the plugin.
func runCmds(t *testing.T, p *Plugin, cmd tea.Cmd) {
	t.Helper()
	if cmd == nil {
		return
	}
	msg := cmd()
	if msg == nil {
		return
	}
	if batch, ok := msg.(tea.BatchMsg); ok {
		for _, sub := range batch {
			runCmds(t, p, sub)
		}
		return
	}
	_, next := p.Update(msg)
	runCmds(t, p, next)
}

func dirtyDeletePlugin(t *testing.T, dirty bool) *Plugin {
	t.Helper()
	repo := dirtyTestRepo(t, dirty)
	wt := &Worktree{Key: "feature", Name: "feature", Path: repo, Branch: "feature-branch"}
	p := &Plugin{
		viewMode:     ViewModeList,
		mouseHandler: mouse.NewHandler(),
		width:        120,
		height:       32,
		sidebarWidth: 40,
		ctx:          &plugin.Context{WorkDir: repo, Epoch: 1},
	}
	p.selection.Clear()
	p.worktrees = []*Worktree{wt}
	p.selectedIdx = 0
	return p
}

// confirmationView opens the delete confirmation through the real key path,
// lets the asynchronous answers land, and returns what is on screen.
func confirmationView(t *testing.T, p *Plugin) string {
	t.Helper()
	runCmds(t, p, p.handleListKeys(tea.KeyPressMsg{Code: 'D', Text: "D"}))
	if p.viewMode != ViewModeConfirmDelete {
		t.Fatalf("D did not arm the worktree confirmation: viewMode=%v", p.viewMode)
	}
	built := p.deleteConfirm.Modal(p.width)
	if built == nil {
		t.Fatal("the armed confirmation built no modal")
	}
	return built.Render(p.width, 24, mouse.NewHandler())
}

func TestProjectDeleteWarnsAboutUncommittedChangesOnlyWhenDirty(t *testing.T) {
	dirty := dirtyDeletePlugin(t, true)
	view := confirmationView(t, dirty)
	if p := dirty.deleteConfirm.Dirty; p != worktreedelete.DirtinessDirty {
		t.Fatalf("dirty worktree resolved to %v, want DirtinessDirty", p)
	}
	if !strings.Contains(view, worktreedelete.DirtyLine) {
		t.Fatalf("a dirty worktree was not warned about:\n%s", view)
	}

	clean := dirtyDeletePlugin(t, false)
	view = confirmationView(t, clean)
	if p := clean.deleteConfirm.Dirty; p != worktreedelete.DirtinessClean {
		t.Fatalf("clean worktree resolved to %v, want DirtinessClean", p)
	}
	if strings.Contains(view, worktreedelete.DirtyLine) {
		t.Fatalf("a clean worktree still claimed uncommitted changes would be lost:\n%s", view)
	}
	if !strings.Contains(view, worktreedelete.CleanLine) {
		t.Fatalf("a clean worktree was not told it is clean:\n%s", view)
	}
}

// A late answer about a different worktree must not relabel the open
// confirmation.
func TestProjectDeleteIgnoresADirtinessAnswerForAnotherWorktree(t *testing.T) {
	p := dirtyDeletePlugin(t, false)
	confirmationView(t, p)
	p.Update(WorktreeDirtyCheckedMsg{Path: "/somewhere/else", Dirty: worktreedelete.DirtinessDirty})
	if p.deleteConfirm.Dirty != worktreedelete.DirtinessClean {
		t.Fatalf("an answer about another worktree changed this confirmation to %v", p.deleteConfirm.Dirty)
	}
}
