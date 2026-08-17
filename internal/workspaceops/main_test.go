package workspaceops

import (
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/state"
)

// TestMain isolates this package on both axes, tmux and Sidecar state, for the
// same reason internal/plugins/workspace does (td-8d18de).
//
// tmux matters here now that the shared delete path kills a worktree's session
// (td-a66836): DeleteWorktree derives the session name from the worktree's
// directory, and the existing tests in this package use directories called
// "feature" and "gone". On the developer's default server `sidecar-ws-feature`
// is a plausible live session — quite possibly one with an agent in it. A
// private socket makes that unreachable whatever directory a future test picks.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "workspaceops-test")
	if err != nil {
		_, _ = os.Stderr.WriteString("tmux isolation: " + err.Error() + "\n")
		os.Exit(1)
	}
	fail := func(msg string) {
		_, _ = os.Stderr.WriteString(msg + "\n")
		_ = os.RemoveAll(dir)
		os.Exit(1)
	}
	if err := os.Setenv("TMUX_TMPDIR", dir); err != nil {
		fail("tmux isolation: " + err.Error())
	}
	// Set when the tests are themselves run from inside tmux. Left in place,
	// tmux can resolve a bare target against the outer server.
	if err := os.Unsetenv("TMUX"); err != nil {
		fail("tmux isolation: " + err.Error())
	}

	stateHome := filepath.Join(dir, "state")
	configDir := filepath.Join(dir, "config")
	for _, d := range []string{stateHome, configDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			fail("state isolation: " + err.Error())
		}
	}
	for k, v := range map[string]string{
		"XDG_STATE_HOME":    stateHome,
		"XDG_CONFIG_HOME":   configDir,
		"XDG_CACHE_HOME":    filepath.Join(dir, "cache"),
		config.IsolationEnv: "1",
	} {
		if err := os.Setenv(k, v); err != nil {
			fail("state isolation: " + err.Error())
		}
	}
	config.SetTestStateDir(filepath.Join(stateHome, "sidecar"))
	config.SetTestConfigPath(filepath.Join(configDir, "config.json"))
	_ = state.InitWithDir(configDir)
	if err := config.CheckStateIsolation(); err != nil {
		fail(err.Error())
	}

	code := m.Run()

	// By explicit socket path, never a bare kill-server: if anything has
	// disturbed TMUX_TMPDIR by now, a bare one would destroy the developer's
	// server and every session on it.
	_ = exec.Command("tmux", "-S", tmuxSocketPath(dir), "kill-server").Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// tmuxSocketPath is where tmux puts its default socket under a TMUX_TMPDIR.
func tmuxSocketPath(tmpDir string) string {
	return filepath.Join(tmpDir, "tmux-"+strconv.Itoa(os.Getuid()), "default")
}
