// Package testenv establishes process-wide isolation for test binaries that
// shell out to tmux.
//
// Any package whose tests reach tmux — directly, or indirectly through
// internal/tty — must isolate, because the alternative is that `go test ./...`
// operates on the developer's own tmux server. That is not a hypothetical:
// td-4d99ae found 66 orphaned control-mode clients and 18 orphaned servers
// accumulated over about a week of test runs, which drove the real server past
// its file-descriptor limit and made every Sidecar tmux call intermittently
// fail. td-8d18de is the same lesson learned the harder way.
//
// TMUX_TMPDIR relocates tmux's default socket, so tmux resolves to a private
// server for this process and every tmux child it spawns. Nothing an isolated
// suite does can reach the developer's sessions, whatever session name a future
// test happens to pick.
package testenv

import (
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"syscall"
	"testing"
)

// SocketPath is where tmux puts its default socket under a given TMUX_TMPDIR.
func SocketPath(tmuxTmpDir string) string {
	return filepath.Join(tmuxTmpDir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
}

// IsolateTmux points every tmux command this process runs at a throwaway
// server and returns the socket path plus a teardown.
//
// Teardown kills the server by explicit socket path rather than running a bare
// `tmux kill-server`. A bare kill-server trusts the environment to still be
// pointing somewhere private; if anything has disturbed TMUX_TMPDIR by then it
// destroys the developer's own server and every session on it. -S names the
// file we created, so the blast radius cannot leave this temp dir.
func IsolateTmux() (socket string, teardown func(), err error) {
	dir, err := os.MkdirTemp("", "sidecar-tmux-test")
	if err != nil {
		return "", nil, fmt.Errorf("tmux isolation: %w", err)
	}
	if err := os.Setenv("TMUX_TMPDIR", dir); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("tmux isolation: %w", err)
	}
	// TMUX is set when the tests are themselves run from inside tmux. Left in
	// place, tmux treats commands as coming from that client and can resolve a
	// bare target against the outer server instead of the private one.
	if err := os.Unsetenv("TMUX"); err != nil {
		_ = os.RemoveAll(dir)
		return "", nil, fmt.Errorf("tmux isolation: %w", err)
	}
	return SocketPath(dir), teardownFor(dir), nil
}

// teardownFor kills the private server and only then removes the directory.
//
// The order matters and the previous code had it backwards. Removing the temp
// dir first unlinks the socket, and a server that outlives teardown then has no
// path any tmux command can address — it is unreachable and unkillable by name
// for the rest of the machine's uptime. All 18 servers leaked in td-4d99ae were
// in exactly that state. If the kill does not take, the directory is left in
// place on purpose so the server stays addressable and `make reap-test-tmux`
// can still find it.
func teardownFor(dir string) func() {
	var once bool
	return func() {
		if once {
			return
		}
		once = true
		socket := SocketPath(dir)
		if _, err := os.Stat(socket); os.IsNotExist(err) {
			// No server was ever started under this dir.
			_ = os.RemoveAll(dir)
			return
		}
		if err := exec.Command("tmux", "-S", socket, "kill-server").Run(); err != nil {
			// A server that was never started, or already gone, reports an
			// error too. Distinguish by whether the socket still answers.
			if exec.Command("tmux", "-S", socket, "has-session").Run() == nil {
				fmt.Fprintf(os.Stderr,
					"tmux isolation: could not kill test server at %s; leaving it addressable for `make reap-test-tmux`\n",
					socket)
				return
			}
		}
		_ = os.RemoveAll(dir)
	}
}

// Main runs m with tmux isolated, tears down, and returns the exit code.
// Callers do:
//
//	func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }
//
// Teardown also runs on SIGINT/SIGTERM, so an interrupted run does not leak.
// It cannot run when the binary dies by panic or by `go test` timeout — Go
// exits those paths without unwinding TestMain — which is why teardown leaves a
// reachable socket behind rather than an orphan with no path.
func Main(m *testing.M) int {
	_, teardown, err := IsolateTmux()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	stop := onSignal(func() { teardown() })
	code := m.Run()
	stop()
	teardown()
	return code
}

// onSignal runs fn if the process is interrupted, then restores the default
// disposition and re-raises so the exit status still reflects the signal.
func onSignal(fn func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		select {
		case sig := <-ch:
			fn()
			signal.Stop(ch)
			if p, err := os.FindProcess(os.Getpid()); err == nil {
				_ = p.Signal(sig)
			}
		case <-done:
		}
	}()
	return func() {
		close(done)
		signal.Stop(ch)
	}
}
