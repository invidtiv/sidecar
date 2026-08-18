package testenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// These pin the ordering invariant the package exists for: a server is killed
// before its socket directory is removed, and a directory is never removed
// while a server that survived the kill could still be addressed through it.
// Getting that backwards is what left 18 unaddressable orphans in td-4d99ae.

// shortTempDir keeps socket paths under the ~104-byte sockaddr_un limit.
// t.TempDir embeds the test name, which on macOS is long enough to overflow it.
func shortTempDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "tenv")
	if err != nil {
		t.Fatalf("temp dir: %v", err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func requireTmux(t *testing.T) {
	t.Helper()
	if _, err := exec.LookPath("tmux"); err != nil {
		t.Skip("tmux not installed")
	}
}

// startServer brings up a server on socket with one long-lived session. Every
// tmux call is -S scoped, so nothing here can reach the developer's server.
func startServer(t *testing.T, socket string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		t.Fatalf("mkdir socket dir: %v", err)
	}
	cmd := exec.Command("tmux", "-S", socket, "new-session", "-d", "-s", "probe", "sleep 300")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("start test server: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = exec.Command("tmux", "-S", socket, "kill-server").Run() })
}

func serverAlive(socket string) bool {
	return exec.Command("tmux", "-S", socket, "has-session").Run() == nil
}

func TestIsolateTmuxPointsAtAPrivateSocket(t *testing.T) {
	t.Setenv("TMUX_TMPDIR", "")
	t.Setenv("TMUX", "outer-server-should-be-scrubbed")

	socket, teardown, err := IsolateTmux()
	if err != nil {
		t.Fatalf("IsolateTmux: %v", err)
	}
	defer teardown()

	if got := os.Getenv("TMUX_TMPDIR"); got == "" || SocketPath(got) != socket {
		t.Fatalf("TMUX_TMPDIR=%q does not resolve to socket %q", got, socket)
	}
	// A bare tmux command must not be able to resolve against an outer server.
	if _, ok := os.LookupEnv("TMUX"); ok {
		t.Error("TMUX still set; a bare target could resolve to the outer server")
	}
}

func TestTeardownKillsTheServerThenRemovesTheDirectory(t *testing.T) {
	requireTmux(t)
	t.Setenv("TMUX_TMPDIR", "")

	socket, teardown, err := IsolateTmux()
	if err != nil {
		t.Fatalf("IsolateTmux: %v", err)
	}
	dir := filepath.Dir(filepath.Dir(socket))
	startServer(t, socket)
	if !serverAlive(socket) {
		t.Fatal("precondition: server did not start")
	}

	teardown()

	if serverAlive(socket) {
		t.Error("teardown left the test server running")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("teardown left %s behind after a successful kill", dir)
	}
}

// The directory is the only thing that makes a surviving server addressable, so
// it must outlive a kill that did not take. Simulated by pointing teardown at a
// socket path whose server it cannot kill but which still answers.
func TestTeardownRetainsTheDirectoryWhenTheServerSurvives(t *testing.T) {
	requireTmux(t)

	dir := shortTempDir(t)
	socket := SocketPath(dir)
	startServer(t, socket)

	// A stub tmux that fails kill-server but reports the session alive, which
	// is what a wedged server looks like from the outside.
	stub := filepath.Join(shortTempDir(t), "tmux")
	script := "#!/bin/sh\nfor a in \"$@\"; do\n  [ \"$a\" = kill-server ] && exit 1\ndone\nexit 0\n"
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatalf("write stub: %v", err)
	}
	t.Setenv("PATH", filepath.Dir(stub)+string(os.PathListSeparator)+os.Getenv("PATH"))

	teardownFor(dir)()

	if _, err := os.Stat(dir); os.IsNotExist(err) {
		t.Fatal("teardown removed the directory of a server it could not kill; " +
			"that server is now unaddressable, which is td-4d99ae")
	}
	if _, err := os.Stat(socket); os.IsNotExist(err) {
		t.Error("socket was unlinked out from under a surviving server")
	}
}

// A directory whose server is already gone is just files, and must not be
// retained forever.
func TestTeardownRemovesADirectoryWhoseServerIsAlreadyGone(t *testing.T) {
	requireTmux(t)

	dir := shortTempDir(t)
	socket := SocketPath(dir)
	if err := os.MkdirAll(filepath.Dir(socket), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	// A socket file with nothing listening: what a crashed server leaves.
	if err := os.WriteFile(socket, nil, 0o600); err != nil {
		t.Fatalf("write socket stub: %v", err)
	}

	teardownFor(dir)()

	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Errorf("stale directory %s was retained", dir)
	}
}

func TestTeardownIsIdempotent(t *testing.T) {
	dir := shortTempDir(t)
	teardown := teardownFor(dir)
	teardown()
	teardown() // must not panic or fail on the already-removed directory
}

// stop() must not return while a signal handler is still running teardown,
// otherwise Main's own teardown call races it.
func TestOnSignalStopWaitsForTheHandler(t *testing.T) {
	released := make(chan struct{})
	entered := make(chan struct{})
	stop := OnSignal(func() {
		close(entered)
		<-released
	})
	// No signal is delivered, so the handler never runs and stop returns.
	close(released)
	stop()

	select {
	case <-entered:
		t.Error("handler ran without a signal")
	default:
	}
}
