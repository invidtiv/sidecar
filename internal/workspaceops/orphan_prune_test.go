package workspaceops

import (
	"context"
	"errors"
	"testing"
)

func stubOrphanTmux(t *testing.T, path string, alive bool) *[]string {
	t.Helper()
	var killed []string
	oldPath, oldKill, oldForget := sessionStartPath, killSessionExact, forgetShellsInWorktree
	sessionStartPath = func(context.Context, string) (string, bool) { return path, alive }
	killSessionExact = func(_ context.Context, session string) error {
		killed = append(killed, session)
		return nil
	}
	forgetShellsInWorktree = func(string, string) error { return nil }
	t.Cleanup(func() { sessionStartPath, killSessionExact, forgetShellsInWorktree = oldPath, oldKill, oldForget })
	return &killed
}

// The plan and the kill are separate moments, and a worktree created in
// between can take the same session name. The session is re-read and left
// alone when it no longer starts where it was planned.
func TestPruneOrphanedSessionRefusesASessionThatMoved(t *testing.T) {
	root := t.TempDir() + "/repo-foo"
	killed := stubOrphanTmux(t, "/elsewhere/repo-foo", true)
	_, err := PruneOrphanedWorktreeSession(t.Context(), OrphanedSessionPrune{
		Root: root, Session: "sidecar-ws-repo-foo", SessionPath: root,
	})
	if !errors.Is(err, ErrOrphanChanged) {
		t.Fatalf("err = %v, want ErrOrphanChanged", err)
	}
	if len(*killed) != 0 {
		t.Fatalf("killed %v after the session moved", *killed)
	}
}

func TestPruneOrphanedSessionClosesTheSessionItPlanned(t *testing.T) {
	root := t.TempDir() + "/repo-foo"
	killed := stubOrphanTmux(t, root, true)
	gone, err := PruneOrphanedWorktreeSession(t.Context(), OrphanedSessionPrune{
		Root: root, Session: "sidecar-ws-repo-foo", SessionPath: root,
	})
	if err != nil || gone {
		t.Fatalf("gone %v err %v", gone, err)
	}
	if len(*killed) != 1 || (*killed)[0] != "sidecar-ws-repo-foo" {
		t.Fatalf("killed = %v", *killed)
	}
}

func TestPruneOrphanedSessionAlreadyGoneIsSuccess(t *testing.T) {
	killed := stubOrphanTmux(t, "", false)
	gone, err := PruneOrphanedWorktreeSession(t.Context(), OrphanedSessionPrune{
		Root: "/code/repo-foo", Session: "sidecar-ws-repo-foo", SessionPath: "/code/repo-foo",
	})
	if err != nil || !gone || len(*killed) != 0 {
		t.Fatalf("gone %v err %v killed %v", gone, err, *killed)
	}
}

func TestPruneOrphanedSessionRefusesNonWorktreeSessions(t *testing.T) {
	killed := stubOrphanTmux(t, "/code/repo-foo", true)
	for _, session := range []string{"", "sidecar-sh-repo-1", "probe"} {
		if _, err := PruneOrphanedWorktreeSession(t.Context(), OrphanedSessionPrune{Root: "/code/repo-foo", Session: session, SessionPath: "/code/repo-foo"}); err == nil {
			t.Errorf("session %q was accepted", session)
		}
	}
	if len(*killed) != 0 {
		t.Fatalf("killed %v", *killed)
	}
}
