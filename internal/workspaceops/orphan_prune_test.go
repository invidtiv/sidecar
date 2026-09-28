package workspaceops

import (
	"context"
	"errors"
	"os/exec"
	"testing"
)

type orphanStub struct {
	state       orphanSessionState
	alive       bool
	rootMissing bool
	readErr     error
	killed      []string
}

func stubOrphanTmux(t *testing.T, stub *orphanStub) {
	t.Helper()
	oldRead, oldKill, oldForget, oldMissing := readOrphanSession, killSessionByID, forgetShellsInWorktree, rootStillMissing
	readOrphanSession = func(context.Context, string) (orphanSessionState, error) {
		if stub.readErr != nil {
			return orphanSessionState{}, stub.readErr
		}
		if !stub.alive {
			return orphanSessionState{}, errOrphanSessionGone
		}
		return stub.state, nil
	}
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

// A read that failed for any other reason is not evidence the session is gone,
// and must not drop its restore record.
func TestPruneOrphanedSessionUnreadableIsAFailureNotGone(t *testing.T) {
	stub := &orphanStub{rootMissing: true, readErr: errors.New("tmux: context deadline exceeded")}
	stubOrphanTmux(t, stub)
	gone, err := PruneOrphanedWorktreeSession(t.Context(), planned())
	if gone || err == nil || errors.Is(err, ErrOrphanChanged) {
		t.Fatalf("gone %v err %v, want a plain failure", gone, err)
	}
	if len(stub.killed) != 0 {
		t.Fatalf("killed %v", stub.killed)
	}
}

// Against this package's private tmux server: a missing name is "gone", and a
// name that is a prefix of a live session is not that session. tmux resolves
// `list-panes -s -t =pro` to `probe`; only `=pro:` is exact.
func TestReadOrphanSessionIsExactAndRecognisesGone(t *testing.T) {
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux is not installed")
	}
	const live = "sidecar-ws-orphanprobe-live"
	dir := t.TempDir()
	if out, err := exec.Command("tmux", "new-session", "-d", "-s", live, "-c", dir).CombinedOutput(); err != nil {
		t.Skipf("cannot start a private tmux session: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "kill-session", "-t", "="+live).Run() })

	state, err := readOrphanSession(t.Context(), live)
	if err != nil || state.ID == "" || CanonicalWorkPath(state.Path) != CanonicalWorkPath(dir) || len(state.PanePaths) == 0 {
		t.Fatalf("live read = %+v, %v", state, err)
	}
	for _, name := range []string{"sidecar-ws-orphanprobe", "sidecar-ws-orphanprobe-missing"} {
		if _, err := readOrphanSession(t.Context(), name); !errors.Is(err, errOrphanSessionGone) {
			t.Errorf("read %q = %v, want errOrphanSessionGone", name, err)
		}
	}
}
