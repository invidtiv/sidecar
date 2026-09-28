package workspaceops

import (
	"context"
	"errors"
	"testing"
)

type orphanStub struct {
	state       orphanSessionState
	alive       bool
	rootMissing bool
	killed      []string
}

func stubOrphanTmux(t *testing.T, stub *orphanStub) {
	t.Helper()
	oldRead, oldKill, oldForget, oldMissing := readOrphanSession, killSessionByID, forgetShellsInWorktree, rootStillMissing
	readOrphanSession = func(context.Context, string) (orphanSessionState, bool) { return stub.state, stub.alive }
	killSessionByID = func(_ context.Context, id string) error {
		stub.killed = append(stub.killed, id)
		return nil
	}
	forgetShellsInWorktree = func(string, string) error { return nil }
	rootStillMissing = func(string) bool { return stub.rootMissing }
	t.Cleanup(func() {
		readOrphanSession, killSessionByID, forgetShellsInWorktree, rootStillMissing = oldRead, oldKill, oldForget, oldMissing
	})
}

const orphanRoot = "/nonexistent-sidecar-test/repo-foo"

func planned() OrphanedSessionPrune {
	return OrphanedSessionPrune{Root: orphanRoot, Session: "sidecar-ws-repo-foo", SessionPath: orphanRoot}
}

func TestPruneOrphanedSessionClosesTheSessionItPlannedByID(t *testing.T) {
	stub := &orphanStub{alive: true, rootMissing: true, state: orphanSessionState{ID: "$7", Path: orphanRoot, PanePaths: []string{orphanRoot}}}
	stubOrphanTmux(t, stub)
	gone, err := PruneOrphanedWorktreeSession(t.Context(), planned())
	if err != nil || gone {
		t.Fatalf("gone %v err %v", gone, err)
	}
	if len(stub.killed) != 1 || stub.killed[0] != "$7" {
		t.Fatalf("killed = %v, want the session id", stub.killed)
	}
}

// The plan and the kill are separate moments. Each of these is something that
// can change in between, and each must leave the session alone.
func TestPruneOrphanedSessionRevalidatesBeforeTheKill(t *testing.T) {
	existing := t.TempDir()
	cases := map[string]*orphanStub{
		"root re-created":       {alive: true, rootMissing: false, state: orphanSessionState{ID: "$7", Path: orphanRoot}},
		"name reused elsewhere": {alive: true, rootMissing: true, state: orphanSessionState{ID: "$7", Path: "/elsewhere/repo-foo"}},
		"pane followed a move":  {alive: true, rootMissing: true, state: orphanSessionState{ID: "$7", Path: orphanRoot, PanePaths: []string{orphanRoot, existing}}},
	}
	for name, stub := range cases {
		t.Run(name, func(t *testing.T) {
			stubOrphanTmux(t, stub)
			_, err := PruneOrphanedWorktreeSession(t.Context(), planned())
			if !errors.Is(err, ErrOrphanChanged) {
				t.Fatalf("err = %v, want ErrOrphanChanged", err)
			}
			if len(stub.killed) != 0 {
				t.Fatalf("killed %v", stub.killed)
			}
		})
	}
}

func TestPruneOrphanedSessionAlreadyGoneIsSuccess(t *testing.T) {
	stub := &orphanStub{alive: false, rootMissing: true}
	stubOrphanTmux(t, stub)
	gone, err := PruneOrphanedWorktreeSession(t.Context(), planned())
	if err != nil || !gone || len(stub.killed) != 0 {
		t.Fatalf("gone %v err %v killed %v", gone, err, stub.killed)
	}
}

func TestPruneOrphanedSessionRefusesNonWorktreeSessions(t *testing.T) {
	stub := &orphanStub{alive: true, rootMissing: true, state: orphanSessionState{ID: "$7", Path: orphanRoot}}
	stubOrphanTmux(t, stub)
	for _, session := range []string{"", "sidecar-sh-repo-1", "probe"} {
		req := planned()
		req.Session = session
		if _, err := PruneOrphanedWorktreeSession(t.Context(), req); err == nil {
			t.Errorf("session %q was accepted", session)
		}
	}
	if len(stub.killed) != 0 {
		t.Fatalf("killed %v", stub.killed)
	}
}
