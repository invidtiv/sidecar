package cli

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/uiapi"
	"github.com/marcus/sidecar/internal/workspaceops"
	"github.com/marcus/sidecar/internal/workspacewire"
)

func TestWorkspaceStartsInactiveShellAndExistingWorktree(t *testing.T) {
	testenv.RequireTmux(t)
	stateDir, root := targetProject(t)
	backend := &mobileBackend{env: Env{StateDir: stateDir}}
	start := func(operation, target string) workspacewire.SessionStarted {
		t.Helper()
		data, exit, err := backend.WorkspaceOperation(context.Background(), "demo", uiapi.WorkspaceCommand{Operation: operation, Target: target})
		var doc workspacewire.SessionStarted
		if err != nil || exit != 0 || json.Unmarshal(data, &doc) != nil || doc.Status != "started" || doc.Project != "demo" || !workspaceops.SessionExists(doc.Shell.Session) {
			t.Fatalf("%s: exit=%d err=%v body=%s", operation, exit, err, data)
		}
		t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "="+doc.Shell.Session).Run() })
		return doc
	}
	shell := start("shells/start", "sidecar-sh-demo-2")
	if shell.Shell.Session != "sidecar-sh-demo-2" || shell.Shell.WorkDir != root {
		t.Fatalf("selected shell changed: %+v", shell)
	}
	git := func(args ...string) {
		t.Helper()
		command := exec.Command("git", args...)
		command.Dir = root
		if out, err := command.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %s %v", args, out, err)
		}
	}
	git("init", "-q", "-b", "main")
	git("-c", "user.name=Test", "-c", "user.email=test@example.com", "commit", "--allow-empty", "-q", "-m", "initial")
	worktree := filepath.Join(filepath.Dir(root), "inactive-worktree")
	git("worktree", "add", "-q", worktree, "-b", "inactive-worktree")
	doc := start("worktrees/start", worktree)
	if doc.Shell.WorkDir != worktree {
		t.Fatalf("wrong worktree: %+v", doc)
	}
	if data, exit, err := backend.WorkspaceOperation(context.Background(), "demo", uiapi.WorkspaceCommand{Operation: "worktrees/start", Target: root}); exit == 0 || err == nil {
		t.Fatalf("main checkout accepted: %s %d %v", data, exit, err)
	}
}

func TestWorkspaceStartExactTargetsNeverFallBackToDisplayNames(t *testing.T) {
	for _, operation := range []string{"shells/start", "worktrees/start"} {
		args, _, err := workspaceCommandArgs("demo", uiapi.WorkspaceCommand{Operation: operation, Target: "-session"})
		if err != nil || args[len(args)-2] != "--" || args[len(args)-1] != "-session" {
			t.Fatalf("%s target not protected: %v %v", operation, args, err)
		}
	}
	stateDir, _ := targetProject(t)
	b := &mobileBackend{env: Env{StateDir: stateDir}}
	if _, exit, err := b.WorkspaceOperation(context.Background(), "demo", uiapi.WorkspaceCommand{Operation: "shells/start", Target: "two"}); exit != 3 || err == nil {
		t.Fatalf("display name accepted: %d %v", exit, err)
	}
}
