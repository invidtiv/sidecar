package workspace

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/state"
)

// socketPath is where tmux puts its default socket under a given TMUX_TMPDIR.
func socketPath(tmpDir string) string {
	return filepath.Join(tmpDir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
}

// TestMain isolates this package on BOTH axes: the tmux server it talks to and
// the Sidecar state tree it persists to. Isolating either one alone is not
// enough (td-8d18de) — a suite on a private tmux socket that still resolves
// $HOME/.local/state/sidecar rewrites the manifest a live Sidecar is watching,
// and that instance drops shells whose sessions are still running.
//
// Axis 1, tmux. Points every tmux command this package runs at a throwaway
// server.
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
		_, _ = os.Stderr.WriteString("tmux isolation: " + err.Error() + "\n")
		os.Exit(1)
	}
	if err := os.Setenv("TMUX_TMPDIR", dir); err != nil {
		_, _ = os.Stderr.WriteString("tmux isolation: " + err.Error() + "\n")
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	// TMUX is set when the tests are themselves run from inside tmux. Left in
	// place, tmux treats commands as coming from that client and can resolve a
	// bare target against the outer server instead of the private one.
	if err := os.Unsetenv("TMUX"); err != nil {
		_, _ = os.Stderr.WriteString("tmux isolation: " + err.Error() + "\n")
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}

	// Axis 2, Sidecar state. Set for the whole package so no individual test
	// can opt out by forgetting to override. Tests that call
	// config.ResetTestStateDir in a cleanup fall back to the XDG_STATE_HOME
	// set here, which is still inside dir.
	stateHome := filepath.Join(dir, "state")
	configDir := filepath.Join(dir, "config")
	for _, d := range []string{stateHome, configDir} {
		if err := os.MkdirAll(d, 0755); err != nil {
			exitUnisolated(dir, "state isolation: "+err.Error())
		}
	}
	for k, v := range map[string]string{
		"XDG_STATE_HOME":    stateHome,
		"XDG_CONFIG_HOME":   configDir,
		"XDG_CACHE_HOME":    filepath.Join(dir, "cache"),
		config.IsolationEnv: "1",
	} {
		if err := os.Setenv(k, v); err != nil {
			exitUnisolated(dir, "state isolation: "+err.Error())
		}
	}
	config.SetTestStateDir(filepath.Join(stateHome, "sidecar"))
	config.SetTestConfigPath(filepath.Join(configDir, "config.json"))
	_ = state.InitWithDir(configDir)

	// Fail closed rather than run the suite against the developer's real tree.
	if err := config.CheckStateIsolation(); err != nil {
		exitUnisolated(dir, err.Error())
	}

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

// exitUnisolated aborts the suite before m.Run when isolation could not be
// established. Running anyway would put the developer's real state at risk.
func exitUnisolated(dir, msg string) {
	_, _ = os.Stderr.WriteString(msg + "\n")
	_ = os.RemoveAll(dir)
	os.Exit(1)
}
