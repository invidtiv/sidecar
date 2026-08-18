package workspaceops

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/testenv"
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
	// Axis 1 lives in internal/testenv, which also owns the teardown order:
	// kill the server BEFORE removing the temp dir. Doing it the other way
	// round unlinks the socket out from under any server that survives the
	// kill, leaving it permanently unaddressable (td-4d99ae).
	socket, teardownTmux, err := testenv.IsolateTmux()
	if err != nil {
		_, _ = os.Stderr.WriteString(err.Error() + "\n")
		os.Exit(1)
	}
	dir := filepath.Dir(filepath.Dir(socket))
	fail := func(msg string) {
		_, _ = os.Stderr.WriteString(msg + "\n")
		teardownTmux()
		os.Exit(1)
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

	stop := testenv.OnSignal(teardownTmux)
	code := m.Run()
	stop()
	teardownTmux()
	os.Exit(code)
}
