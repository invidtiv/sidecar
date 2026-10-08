package cli

import (
	"context"
	"errors"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/workspaceinventory"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/shellstate"
)

func TestMobileTerminalEndRequiresSameLiveServer(t *testing.T) {
	for _, mode := range []string{"gone", "empty", "live", "dead", "server-gone", "replaced", "replaced-during-probe", "source-replaced", "malformed-live", "empty-no-target"} {
		t.Run(mode, func(t *testing.T) {
			state, target, write := mobileShellRevalidationFixture(t)
			target.ServerPID, target.SessionID, target.SessionCreated = 123, "$7", "456"
			if mode == "source-replaced" {
				target.DurableSessionCreated = "2026-10-07T12:00:00Z"
				write("repo", "Replacement")
			}
			reads := 0
			observer := mobileTerminalEndObserverWithRead(Env{StateDir: state}, func(_ context.Context, args ...string) (string, error) {
				if args[0] == "list-sessions" && (mode == "empty-no-target" || mode == "empty") {
					return "", nil
				}
				if args[0] == "display-message" {
					reads++
					if mode == "server-gone" {
						return "no server running", errors.New("not running")
					}
					if mode == "replaced" || mode == "replaced-during-probe" && reads > 1 {
						return "124", nil
					}
					return "123", nil
				}
				switch mode {
				case "empty-no-target":
					return "no current target", errors.New("empty")
				case "empty":
					return "no sessions", errors.New("empty")
				case "malformed-live":
					return target.Session + "\t$7\t456\t%7\t0", nil
				case "live":
					return target.Session + "\t$7\t456\t%7\t0\t", nil
				case "dead":
					return target.Session + "\t$7\t456\t%7\t1\t7", nil
				default:
					return "other\t$8\t789\t%8\t0\t", nil
				}
			})
			ended, err := observer(context.Background(), target)
			wantEnd := mode == "gone" || mode == "empty" || mode == "empty-no-target" || mode == "dead"
			if (ended != nil) != wantEnd {
				t.Fatalf("ended=%+v err=%v", ended, err)
			}
			if mode == "dead" && (ended.ExitStatus == nil || *ended.ExitStatus != 7) {
				t.Fatalf("exit status=%+v", ended)
			}
			path := filepath.Join(state, "projects", "repo", "shells.json")
			snapshot, err := shellstate.SnapshotAtPath(path)
			if err != nil {
				t.Fatal(err)
			}
			removed := mode == "gone" || mode == "empty" || mode == "empty-no-target"
			if (len(snapshot.Tombstones) == 1) != removed || (len(snapshot.Shells) == 0) != removed {
				t.Fatalf("manifest=%+v", snapshot)
			}
			if removed {
				again, err := observer(context.Background(), target)
				if err != nil || again == nil {
					t.Fatalf("another viewer end=%+v err=%v", again, err)
				}
			}
		})
	}
}

func TestMobileTerminalEndLastShellOnPrivateServer(t *testing.T) {
	testenv.RequireTmux(t)
	// Short, so the socket path fits; resolved, because /tmp is a symlink on
	// macOS and tmux names its socket by the real path.
	tmuxRoot, err := os.MkdirTemp("/tmp", "sc-end-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmuxRoot) })
	if tmuxRoot, err = filepath.EvalSymlinks(tmuxRoot); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMUX", "")
	t.Setenv("TMUX_PANE", "")
	t.Setenv("TMUX_TMPDIR", tmuxRoot)
	t.Setenv("SIDECAR_ISOLATED_STATE", "1")
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	socket := testenv.SocketPath(tmuxRoot)
	if err := os.MkdirAll(filepath.Dir(socket), 0o700); err != nil {
		t.Fatal(err)
	}
	read := func(ctx context.Context, args ...string) (string, error) {
		cmd := exec.CommandContext(ctx, "tmux", append([]string{"-u", "-f", "/dev/null", "-S", socket}, args...)...)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "TMUX=") && !strings.HasPrefix(entry, "TMUX_PANE=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		out, err := cmd.CombinedOutput()
		return strings.TrimRight(string(out), "\r\n"), err
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	t.Cleanup(func() {
		cleanup, stop := context.WithTimeout(context.Background(), 2*time.Second)
		defer stop()
		_, _ = read(cleanup, "kill-server")
	})
	state, target, _ := mobileShellRevalidationFixture(t)
	if out, err := read(ctx, "start-server", ";", "set-option", "-s", "exit-empty", "off"); err != nil {
		t.Fatalf("prepare: %s %v", out, err)
	}
	if out, err := read(ctx, "new-session", "-d", "-s", target.Session, "/bin/sh", "-i"); err != nil {
		t.Fatalf("create: %s %v", out, err)
	}
	identity, err := read(ctx, "display-message", "-p", "-t", target.Session, "#{pid}\t#{session_id}\t#{session_created}\t#{pane_id}")
	if err != nil {
		t.Fatalf("identity=%q err=%v", identity, err)
	}
	fields := strings.Split(identity, "\t")
	if len(fields) != 4 {
		t.Fatalf("identity=%q", identity)
	}
	target.ServerPID, _ = strconv.Atoi(fields[0])
	target.SessionID, target.SessionCreated, target.Pane = fields[1], fields[2], fields[3]
	if out, err := read(ctx, "send-keys", "-t", target.Session, "exit", "Enter"); err != nil {
		t.Fatalf("exit: %s %v", out, err)
	}
	for {
		sessions, err := read(ctx, "list-sessions", "-F", "#{session_name}")
		if err == nil && sessions == "" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("last shell did not exit")
		case <-time.After(10 * time.Millisecond):
		}
	}
	end, err := mobileTerminalEndObserverWithRead(Env{StateDir: state}, read)(ctx, target)
	if err != nil || end == nil {
		t.Fatalf("last shell end=%+v err=%v", end, err)
	}
	snapshot, err := shellstate.SnapshotAtPath(filepath.Join(state, "projects", "repo", "shells.json"))
	if err != nil || len(snapshot.Shells) != 0 || len(snapshot.Tombstones) != 1 {
		t.Fatalf("manifest=%+v err=%v", snapshot, err)
	}
	panes, err := (workspaceinventory.Collector{}).ListPanes(ctx)
	if err != nil || len(panes) != 0 {
		t.Fatalf("empty catalog panes=%+v err=%v", panes, err)
	}
	t.Log("Private final shell exit produced ended, one forgotten tombstone, and an error-free empty pane inventory with the same server still running")
}
