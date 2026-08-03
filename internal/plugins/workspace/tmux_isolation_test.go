package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"
)

// socketPath is where tmux puts its default socket under a given TMUX_TMPDIR.
func socketPath(tmpDir string) string {
	return filepath.Join(tmpDir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
}

// TestMain points every tmux command this package runs at a throwaway server.
//
// These tests exercise real session creation, and a session's name is derived
// from the basename of its WorkDir (generateShellSessionName). A test whose
// WorkDir happened to be the checkout itself would therefore generate the same
// name as the developer's own shell session for this project — and the cleanups
// here end with `tmux kill-session -t <that name>`. On the default server that
// is somebody's live terminal, quite possibly the one running the tests.
//
// TMUX_TMPDIR relocates the default socket, so tmux resolves to a private
// server for this process and every tmux child it spawns. Nothing the suite
// does can reach the developer's sessions, whatever WorkDir a future test picks.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sidecar-tmux-test")
	if err != nil {
		os.Stderr.WriteString("tmux isolation: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := os.Setenv("TMUX_TMPDIR", dir); err != nil {
		os.Stderr.WriteString("tmux isolation: " + err.Error() + "\n")
		os.Exit(1)
	}
	// TMUX is set when the tests are themselves run from inside tmux. Left in
	// place, tmux treats commands as coming from that client and can resolve a
	// bare target against the outer server instead of the private one.
	os.Unsetenv("TMUX")

	code := m.Run()

	// Tear down by explicit socket path rather than a bare `tmux kill-server`.
	// A bare kill-server trusts the environment to still be pointing somewhere
	// private; if anything has disturbed TMUX_TMPDIR by now it destroys the
	// developer's own server and every session on it. -S names the file we
	// created, so the blast radius cannot leave this temp dir.
	_ = exec.Command("tmux", "-S", socketPath(dir), "kill-server").Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
