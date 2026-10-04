package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/testenv"
)

func TestSetupIsolatedCLIRepairsBothIsolationAxes(t *testing.T) {
	// No tmux command runs while the simulated caller context is unsafe.
	t.Setenv("TMUX", "/unsafe/default,1,0")
	t.Setenv("TMUX_PANE", "%123")
	t.Setenv("TMUX_TMPDIR", "")
	stateHome, _ := setupIsolatedCLI(t)
	tmuxDir := os.Getenv("TMUX_TMPDIR")
	if tmuxDir == "" || !strings.HasPrefix(tmuxDir, stateHome+string(filepath.Separator)) {
		t.Fatalf("helper left tmux outside its private state tree: state=%q tmux=%q", stateHome, tmuxDir)
	}
	if os.Getenv("TMUX") != "" || os.Getenv("TMUX_PANE") != "" {
		t.Fatal("caller tmux identity survived")
	}
	if os.Getenv("SIDECAR_ISOLATED_STATE") != "1" || !strings.HasPrefix(config.ConfigPath(), stateHome+string(filepath.Separator)) {
		t.Fatal("config/state isolation missing")
	}
	if len(testenv.SocketPath(tmuxDir)) > 103 {
		t.Fatal("private socket exceeds platform path bound")
	}
}
