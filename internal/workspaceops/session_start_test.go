package workspaceops

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/agentsession"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/tmuxenv"
)

func TestStartShellPreservesSelectedRecordAndStartsRealTerminal(t *testing.T) {
	testenv.RequireTmux(t)
	root := t.TempDir()
	dir, err := projectdir.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "shells.json")
	def := shellstate.Definition{TmuxName: "sidecar-sh-start-proof", DisplayName: "Selected inactive", WorkDir: root, Namespace: tmuxenv.Namespace(), CreatedAt: time.Now(), AgentType: "codex", SkipPerms: true}
	def.Restore = &shellstate.RestoreState{Policy: agentsession.PolicyNever}
	if err := shellstate.AddAtPath(path, def); err != nil {
		t.Fatal(err)
	}
	svc := Service{}
	got, err := svc.StartShell(context.Background(), root, def.TmuxName, tmuxenv.Namespace())
	if err != nil || got.AlreadyRunning || got.Session != def.TmuxName || !SessionExists(def.TmuxName) {
		t.Fatalf("start: %+v %v", got, err)
	}
	t.Cleanup(func() { _, _ = tmuxTest(t, "kill-session", "-t", "="+def.TmuxName) })
	rows, err := shellstate.ListAtPath(path)
	if err != nil || len(rows) != 1 || rows[0].AgentType != def.AgentType || rows[0].SkipPerms != def.SkipPerms || !rows[0].CreatedAt.Equal(def.CreatedAt) || rows[0].Restore == nil || !rows[0].Restore.Eligible || rows[0].Restore.Policy != agentsession.PolicyNever {
		t.Fatalf("record changed: %+v %v", rows, err)
	}
	got, err = svc.StartShell(context.Background(), root, def.TmuxName, tmuxenv.Namespace())
	if err != nil || !got.AlreadyRunning {
		t.Fatalf("retry: %+v %v", got, err)
	}
	if _, err := svc.StartShell(context.Background(), root, def.DisplayName, tmuxenv.Namespace()); !errors.Is(err, ErrSessionStartNotFound) {
		t.Fatalf("display name accepted: %v", err)
	}
	if _, err := svc.StartShell(context.Background(), root, def.TmuxName, "/another/socket"); !errors.Is(err, ErrSessionStartNotFound) {
		t.Fatalf("wrong server accepted: %v", err)
	}
}

func TestStartWorktreeUsesExistingCheckoutAndRetainsSessionOnRetry(t *testing.T) {
	testenv.RequireTmux(t)
	root := throwawayRepo(t)
	path := filepath.Join(filepath.Dir(root), "start-existing-feature")
	git(t, root, "worktree", "add", "-q", path, "-b", "start-existing-feature")
	svc := Service{}
	got, err := svc.StartWorktree(context.Background(), config.StateDir(), root, path)
	if err != nil || got.AlreadyRunning || got.WorkDir != CanonicalWorktreePath(path) || !SessionExists(got.Session) {
		t.Fatalf("start: %+v %v", got, err)
	}
	t.Cleanup(func() { _, _ = tmuxTest(t, "kill-session", "-t", "="+got.Session) })
	pane := PaneID(got.Session)
	retry, err := svc.StartWorktree(context.Background(), config.StateDir(), root, path)
	if err != nil || !retry.AlreadyRunning || retry.Session != got.Session || PaneID(got.Session) != pane {
		t.Fatalf("retry replaced session: %+v %v", retry, err)
	}
	states, err := ListWorktreeStates(context.Background(), root)
	if err != nil || len(states) != 2 {
		t.Fatalf("start created another checkout: %+v %v", states, err)
	}
	for _, target := range []string{root, "start-existing-feature", filepath.Join(t.TempDir(), "absent")} {
		if _, err := svc.StartWorktree(context.Background(), config.StateDir(), root, target); err == nil {
			t.Fatalf("invalid target %q accepted", target)
		}
	}
}

func TestStartWorktreeRefusesSessionFromAnotherDirectory(t *testing.T) {
	testenv.RequireTmux(t)
	root := throwawayRepo(t)
	path := filepath.Join(filepath.Dir(root), "start-collision")
	git(t, root, "worktree", "add", "-q", path, "-b", "start-collision")
	session := WorktreeSessionName(path, "")
	startThrowawaySession(t, session, t.TempDir())
	if _, err := (Service{}).StartWorktree(context.Background(), config.StateDir(), root, path); err == nil {
		t.Fatal("unrelated session accepted")
	}
	if !SessionExists(session) {
		t.Fatal("collision was destroyed")
	}
}

func TestStartShellRefusesMissingWorkingDirectory(t *testing.T) {
	root := t.TempDir()
	dir, err := projectdir.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	missing := filepath.Join(root, "removed")
	def := shellstate.Definition{TmuxName: "sidecar-sh-missing-start", DisplayName: "Missing", WorkDir: missing, Namespace: tmuxenv.Namespace()}
	if err := shellstate.AddAtPath(filepath.Join(dir, "shells.json"), def); err != nil {
		t.Fatal(err)
	}
	if _, err := (Service{}).StartShell(context.Background(), root, def.TmuxName, tmuxenv.Namespace()); err == nil {
		t.Fatal("missing workdir accepted")
	}
	if _, err := os.Stat(missing); !os.IsNotExist(err) {
		t.Fatal("start created a removed working directory")
	}
}

func TestStartShellRefusesUnrelatedOccupantOfRecordedName(t *testing.T) {
	testenv.RequireTmux(t)
	root := t.TempDir()
	dir, err := projectdir.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	def := shellstate.Definition{TmuxName: "sidecar-sh-start-collision", DisplayName: "Inactive shell", WorkDir: root, Namespace: tmuxenv.Namespace()}
	if err := shellstate.AddAtPath(filepath.Join(dir, "shells.json"), def); err != nil {
		t.Fatal(err)
	}
	startThrowawaySession(t, def.TmuxName, t.TempDir())
	pane := PaneID(def.TmuxName)
	if _, err := (Service{}).StartShell(context.Background(), root, def.TmuxName, tmuxenv.Namespace()); err == nil {
		t.Fatal("unrelated occupant accepted")
	}
	if PaneID(def.TmuxName) != pane {
		t.Fatal("unrelated occupant replaced")
	}
}

func TestStartShellWaitsForConcurrentRecordRemoval(t *testing.T) {
	testenv.RequireTmux(t)
	root := t.TempDir()
	dir, err := projectdir.Resolve(root)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "shells.json")
	def := shellstate.Definition{TmuxName: "sidecar-sh-start-deleted", DisplayName: "Deleted shell", WorkDir: root, Namespace: tmuxenv.Namespace()}
	if err := shellstate.AddAtPath(path, def); err != nil {
		t.Fatal(err)
	}
	locked, release := make(chan struct{}), make(chan struct{})
	removed := make(chan error, 1)
	go func() {
		_, _, err := shellstate.EditAtPath(path, nil, true, func(snapshot *shellstate.Snapshot) (bool, error) {
			close(locked)
			<-release
			snapshot.Shells = nil
			return true, nil
		})
		removed <- err
	}()
	<-locked
	started := make(chan error, 1)
	go func() {
		_, err := (Service{}).StartShell(context.Background(), root, def.TmuxName, tmuxenv.Namespace())
		started <- err
	}()
	// Give the start an opportunity to read the old bytes; it must wait for
	// the writer instead of launching a terminal from that stale definition.
	select {
	case err := <-started:
		close(release)
		t.Fatalf("start escaped the manifest transaction: %v", err)
	case <-time.After(100 * time.Millisecond):
	}
	close(release)
	if err := <-removed; err != nil {
		t.Fatal(err)
	}
	if err := <-started; !errors.Is(err, ErrSessionStartNotFound) {
		t.Fatalf("removed record launched a terminal: %v", err)
	}
	if SessionExists(def.TmuxName) {
		t.Fatal("removed record has a running terminal")
	}
}
