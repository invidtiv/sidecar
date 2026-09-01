package gitstatus

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestInitWithHostIDDoesNotOpenLocalTree(t *testing.T) {
	root := t.TempDir()
	run := exec.Command("git", "init")
	run.Dir = root
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("git init: %s: %v", out, err)
	}
	if err := os.WriteFile(filepath.Join(root, "tracked.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	p := New()
	ctx := &plugin.Context{WorkDir: root, ProjectRoot: root, HostID: "aerie"}
	if err := p.Init(ctx); err != nil {
		t.Fatal(err)
	}
	if p.tree != nil || p.hasRepo {
		t.Fatalf("opened local repo: tree=%v hasRepo=%v", p.tree != nil, p.hasRepo)
	}
	if cmd := p.Start(); cmd != nil {
		t.Fatal("Start probed the twin git root")
	}
	view := p.View(80, 20)
	if !strings.Contains(view, "aerie") {
		t.Errorf("view = %q, want the host named", view)
	}
	if p.inNoRepoMode() {
		t.Fatal("remote bind must not present the local no-repo init path")
	}
}

func TestUpdatePluginFocusedMsgWithHostIDDoesNotLoadCommits(t *testing.T) {
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: t.TempDir(), HostID: "aerie"}); err != nil {
		t.Fatal(err)
	}
	p.historyLoader = func(workDir string, limit int) ([]*Commit, *PushStatus, error) {
		t.Errorf("git history loader ran against %q", workDir)
		return []*Commit{{Hash: "deadbeef", Subject: "should not load"}}, &PushStatus{}, nil
	}

	_, cmd := p.Update(app.PluginFocusedMsg{})
	if cmd != nil {
		msg := cmd()
		if loaded, ok := msg.(RecentCommitsLoadedMsg); ok && len(loaded.Commits) > 0 {
			t.Fatalf("PluginFocusedMsg loaded commits: %+v", loaded.Commits)
		}
		t.Fatalf("PluginFocusedMsg returned cmd producing %#v", msg)
	}
	if p.recentCommits != nil {
		t.Fatalf("recentCommits = %+v, want none", p.recentCommits)
	}
}

func TestUpdateMovementKeyWithHostIDDoesNotPanic(t *testing.T) {
	p := New()
	if err := p.Init(&plugin.Context{WorkDir: t.TempDir(), HostID: "aerie"}); err != nil {
		t.Fatal(err)
	}
	_, _ = p.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
	_, _ = p.Update(tea.KeyPressMsg{Code: tea.KeyDown})
}
