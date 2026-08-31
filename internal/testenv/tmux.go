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
	"context"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"
)

// teardownCommandTimeout bounds the tmux calls teardown makes.
//
// The server teardown is trying to reach may be wedged rather than healthy —
// an fd-starved tmux accepts a connection and immediately drops it, which is
// the exact condition this package exists to stop causing. An unbounded
// kill-server against one of those hangs TestMain after m.Run has returned, so
// `go test ./...` never finishes and the run cannot be interrupted either.
const teardownCommandTimeout = 5 * time.Second

// RequireTmux keeps real-tmux tests convenient in an ordinary checkout while
// making the compatibility matrix fail closed. A missing prerequisite may be
// a local skip, but it is a broken compatibility proof when the driver sets
// SIDECAR_REQUIRE_TMUX_COMPAT=1.
func RequireTmux(t *testing.T) {
	t.Helper()
	required := os.Getenv("SIDECAR_REQUIRE_TMUX_COMPAT") == "1"
	if testing.Short() {
		if required {
			t.Fatal("SIDECAR_REQUIRE_TMUX_COMPAT=1 but the tmux integration suite is running in short mode")
		}
		t.Skip("skipping tmux integration in short mode")
	}
	if _, err := exec.LookPath("tmux"); err != nil {
		if required {
			t.Fatalf("SIDECAR_REQUIRE_TMUX_COMPAT=1 but tmux is not on PATH: %v", err)
		}
		t.Skip("tmux not installed")
	}
}

// TmuxCompatibilityRequired reports whether skips that would weaken the tmux
// matrix contract must instead fail. It is for integration setup failures
// that happen after RequireTmux has found the binary.
func TmuxCompatibilityRequired() bool {
	return os.Getenv("SIDECAR_REQUIRE_TMUX_COMPAT") == "1"
}

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
//
// The returned teardown is safe to call more than once and from more than one
// goroutine.
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
	// TMUX_PANE is the same leak one level down: it names the developer's own
	// pane, and code that excludes the hosting pane reads it as a default. A
	// test scripting panes %1-%4 then quietly loses whichever one collides with
	// the real pane the suite happens to be running in — and a fresh tmux
	// server hands out exactly those low IDs, so the collision arrives after an
	// unrelated restart and looks like the last commit broke something. Tests
	// that want a hosting pane set one explicitly with t.Setenv.
	if err := os.Unsetenv("TMUX_PANE"); err != nil {
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
	var once sync.Once
	return func() {
		once.Do(func() {
			socket := SocketPath(dir)
			if _, err := os.Stat(socket); os.IsNotExist(err) {
				// No server was ever started under this dir.
				_ = os.RemoveAll(dir)
				return
			}
			if err := runTmux(socket, "kill-server"); err != nil {
				// A server that was never started, or already gone, reports an
				// error too. Distinguish by whether the socket still answers.
				// A wedged server that answers neither is treated as alive:
				// retaining the directory is the conservative choice, because
				// removing it is the irreversible one.
				if runTmux(socket, "has-session") == nil {
					fmt.Fprintf(os.Stderr,
						"tmux isolation: could not kill test server at %s; leaving it addressable for `make reap-test-tmux`\n",
						socket)
					return
				}
			}
			_ = os.RemoveAll(dir)
		})
	}
}

// runTmux runs one socket-scoped tmux command under a timeout.
func runTmux(socket string, args ...string) error {
	ctx, cancel := context.WithTimeout(context.Background(), teardownCommandTimeout)
	defer cancel()
	return exec.CommandContext(ctx, "tmux", append([]string{"-S", socket}, args...)...).Run()
}

// Main runs m with tmux isolated, tears down, and returns the exit code.
// Callers do:
//
//	func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }
//
// Teardown also runs on SIGINT/SIGTERM, so an interrupted run does not leak.
// It cannot run when the binary dies by panic or by `go test` timeout — Go
// exits those paths without unwinding TestMain — which is why teardown leaves a
// reachable socket behind rather than an orphan with no path, and why
// `make reap-test-tmux` exists.
func Main(m *testing.M) int {
	_, teardown, err := IsolateTmux()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		return 1
	}
	stop := OnSignal(teardown)
	code := m.Run()
	stop()
	teardown()
	return code
}

// OnSignal runs fn if the process is interrupted, then restores the default
// disposition and re-raises so the exit status still reflects the signal.
//
// Packages that drive TestMain themselves — because they isolate a second axis
// as well — should call this alongside IsolateTmux, so an interrupted run gets
// the same cleanup Main provides.
//
// The returned stop waits for an in-flight handler to finish, so a signal
// arriving exactly as m.Run returns cannot leave teardown running concurrently
// with the caller's own teardown call.
func OnSignal(fn func()) (stop func()) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGINT, syscall.SIGTERM)
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
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
		<-finished
	}
}
