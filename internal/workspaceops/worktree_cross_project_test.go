package workspaceops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
)

// TestMain isolates both state and tmux; all sessions here are throwaway.
func TestDeleteWorktreeClosesCrossProjectShells(t *testing.T) {
	if !TmuxInstalled() {
		t.Skip("tmux not installed")
	}
	root := throwawayRepo(t)
	other := t.TempDir()
	base := t.TempDir()
	wt := filepath.Join(base, "feature")
	git(t, root, "worktree", "add", "-q", "-b", "feature", wt)
	keep := filepath.Join(base, "feature-2")
	if err := os.MkdirAll(keep, 0o755); err != nil {
		t.Fatal(err)
	}
	startThrowawaySession(t, "sidecar-sh-cross-doomed", wt)
	startThrowawaySession(t, "sidecar-sh-cross-keep", keep)
	recordShell(t, root, "sidecar-sh-owner-doomed", "Owner", wt)
	recordShell(t, other, "sidecar-sh-cross-doomed", "Cross project", filepath.Join(wt, "missing", "nested"))
	recordShell(t, other, "sidecar-sh-cross-keep", "Sibling", keep)
	recordShell(t, other, "sidecar-sh-cross-parent", "Parent", base)
	recordShell(t, other, "sidecar-sh-cross-legacy", "Legacy", "")

	if err := DeleteWorktree(context.Background(), WorktreeRemoval{RepoPath: root, ProjectRoot: root, Path: wt, Force: true}); err != nil {
		t.Fatal(err)
	}
	if SessionExists("sidecar-sh-cross-doomed") {
		t.Error("cross-project shell survived with a deleted cwd")
	}
	if !SessionExists("sidecar-sh-cross-keep") {
		t.Error("unrelated sibling shell was closed")
	}
	assertNames(t, manifestNames(t, root), nil)
	assertNames(t, manifestNames(t, other), []string{"sidecar-sh-cross-keep", "sidecar-sh-cross-legacy", "sidecar-sh-cross-parent"})
}

func TestDeleteWorktreeReportsCrossProjectCloseFailure(t *testing.T) {
	root := throwawayRepo(t)
	other := t.TempDir()
	wt := filepath.Join(t.TempDir(), "feature")
	git(t, root, "worktree", "add", "-q", "-b", "feature", wt)
	recordShell(t, other, "sidecar-sh-cross-failure", "Failure", wt)
	recordShell(t, other, "sidecar-sh-cross-success", "Success", wt)
	old := deleteManagedShellForForget
	deleteManagedShellForForget = func(project, session, namespace string, observedAt time.Time) error {
		if _, err := os.Stat(wt); err != nil {
			t.Error("shell close attempted after directory deletion")
		}
		if session == "sidecar-sh-cross-failure" {
			return errors.New("simulated close failure")
		}
		return old(project, session, namespace, observedAt)
	}
	t.Cleanup(func() { deleteManagedShellForForget = old })
	err := DeleteWorktree(context.Background(), WorktreeRemoval{RepoPath: root, ProjectRoot: root, Path: wt, Force: true})
	var warning *WorktreeRemovedWarning
	if !errors.As(err, &warning) || !strings.Contains(err.Error(), "sidecar-sh-cross-failure") {
		t.Fatalf("expected identified cross-project teardown warning, got %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("checkout survived: %v", err)
	}
	assertNames(t, manifestNames(t, other), []string{"sidecar-sh-cross-failure"})
}

func TestDeleteWorktreePreservesCrossProjectShellsOnIdentityRefusal(t *testing.T) {
	root := throwawayRepo(t)
	other := t.TempDir()
	wt := filepath.Join(t.TempDir(), "feature")
	git(t, root, "worktree", "add", "-q", "-b", "feature", wt)
	recordShell(t, other, "sidecar-sh-cross-pinned", "Pinned", wt)
	err := DeleteWorktree(context.Background(), WorktreeRemoval{RepoPath: root, ProjectRoot: root, Path: wt, Branch: "different", ExpectedOID: git(t, wt, "rev-parse", "HEAD"), Force: true})
	if err == nil {
		t.Fatal("changed identity did not refuse deletion")
	}
	assertNames(t, manifestNames(t, other), []string{"sidecar-sh-cross-pinned"})
}

func TestDeleteWorktreeRetainsForeignNamespaceShell(t *testing.T) {
	if !TmuxInstalled() {
		t.Skip("tmux not installed")
	}
	root := throwawayRepo(t)
	other := t.TempDir()
	wt := filepath.Join(t.TempDir(), "feature")
	git(t, root, "worktree", "add", "-q", "-b", "feature", wt)
	name := "sidecar-sh-cross-foreign"
	startThrowawaySession(t, name, root)
	if err := shellstate.AddAtPath(shellManifestPath(t, other), shellstate.Definition{TmuxName: name, DisplayName: "Foreign", Namespace: filepath.Join(t.TempDir(), "different-tmux"), WorkDir: wt}); err != nil {
		t.Fatal(err)
	}
	err := DeleteWorktree(context.Background(), WorktreeRemoval{RepoPath: root, ProjectRoot: root, Path: wt, Force: true})
	var warning *WorktreeRemovedWarning
	if !errors.As(err, &warning) || !strings.Contains(err.Error(), name) {
		t.Fatalf("expected foreign namespace warning, got %v", err)
	}
	if !SessionExists(name) {
		t.Fatal("same-named session on current server was closed")
	}
	assertNames(t, manifestNames(t, other), []string{name})
}

func TestForgetShellsInWorktreePreservesReplacement(t *testing.T) {
	if !TmuxInstalled() {
		t.Skip("tmux not installed")
	}
	other := t.TempDir()
	wt := t.TempDir()
	name := "sidecar-sh-cross-replacement"
	startThrowawaySession(t, name, other)
	recordShell(t, other, name, "Original", wt)
	old := deleteManagedShellForForget
	deleteManagedShellForForget = func(dir, session, namespace string, observedAt time.Time) error {
		path := filepath.Join(dir, "shells.json")
		if err := shellstate.RemoveAtPath(path, shellstate.Identity{TmuxName: session, Namespace: namespace}); err != nil {
			t.Fatal(err)
		}
		if err := shellstate.AddAtPath(path, shellstate.Definition{TmuxName: session, Namespace: namespace, DisplayName: "Replacement", WorkDir: other, CreatedAt: observedAt.Add(time.Hour)}); err != nil {
			t.Fatal(err)
		}
		return old(dir, session, namespace, observedAt)
	}
	t.Cleanup(func() { deleteManagedShellForForget = old })
	if err := ForgetShellsInWorktree(other, wt); !errors.Is(err, shellstate.ErrShellChanged) {
		t.Fatalf("expected replacement refusal, got %v", err)
	}
	if !SessionExists(name) {
		t.Fatal("replacement session was closed")
	}
	assertNames(t, manifestNames(t, other), []string{name})
}

func TestForgetShellsInWorktreeReconcilesDuplicateRegistrations(t *testing.T) {
	// Older installs could register one project twice. Teardown must write the
	// exact inventoried manifest rather than Resolve selecting the first alias.
	oldStateDir := config.StateDir()
	config.SetTestStateDir(t.TempDir())
	t.Cleanup(func() { config.SetTestStateDir(oldStateDir) })
	root := t.TempDir()
	wt := t.TempDir()
	first, err := projectdir.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	second := filepath.Join(filepath.Dir(first), "duplicate")
	if err := os.MkdirAll(second, 0o755); err != nil {
		t.Fatal(err)
	}
	meta, err := os.ReadFile(filepath.Join(first, "meta.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(second, "meta.json"), meta, 0o644); err != nil {
		t.Fatal(err)
	}
	for i, dir := range []string{first, second} {
		name := []string{"sidecar-sh-duplicate-a", "sidecar-sh-duplicate-b"}[i]
		if err := shellstate.AddAtPath(filepath.Join(dir, "shells.json"), shellstate.Definition{TmuxName: name, DisplayName: name, WorkDir: wt, CreatedAt: time.Now()}); err != nil {
			t.Fatal(err)
		}
	}
	if err := ForgetShellsInWorktree(root, wt); err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{first, second} {
		defs, err := shellstate.ListAtPath(filepath.Join(dir, "shells.json"))
		if err != nil || len(defs) != 0 {
			t.Fatalf("manifest %s was skipped: %+v, %v", dir, defs, err)
		}
	}
}

func TestDeleteWorktreeReportsIncompleteShellInventory(t *testing.T) {
	for _, corruption := range []string{"metadata", "manifest", "rootless metadata"} {
		t.Run(corruption, func(t *testing.T) {
			oldStateDir := config.StateDir()
			config.SetTestStateDir(t.TempDir())
			t.Cleanup(func() { config.SetTestStateDir(oldStateDir) })
			root := throwawayRepo(t)
			wt := filepath.Join(t.TempDir(), "feature")
			git(t, root, "worktree", "add", "-q", "-b", "feature", wt)
			other := t.TempDir()
			manifest := shellManifestPath(t, other)
			target := manifest
			data := []byte("invalid JSON")
			if corruption != "manifest" {
				target = filepath.Join(filepath.Dir(manifest), "meta.json")
			}
			if corruption == "rootless metadata" {
				data = []byte(`{"path":""}`)
			}
			if err := os.WriteFile(target, data, 0o644); err != nil {
				t.Fatal(err)
			}
			err := DeleteWorktree(context.Background(), WorktreeRemoval{RepoPath: root, ProjectRoot: root, Path: wt, Force: true})
			var warning *WorktreeRemovedWarning
			if !errors.As(err, &warning) {
				t.Fatalf("incomplete inventory was silent: %v", err)
			}
			if _, err := os.Stat(wt); !os.IsNotExist(err) {
				t.Fatalf("checkout survived: %v", err)
			}
		})
	}
}
