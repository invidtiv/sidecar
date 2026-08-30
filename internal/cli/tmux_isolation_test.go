package cli

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/testenv"
)

// defaultTestConfigPath is the config path every test in this package runs
// against unless it sets its own. Helpers that move it restore this rather than
// "", which would put the process back on the developer's real config file.
var defaultTestConfigPath string

// TestMain isolates this package's tmux access AND its Sidecar state paths.
//
// They are separate axes and isolating one is not isolating (td-8d18de).
// TMUX_TMPDIR moves tmux; XDG_STATE_HOME moves the state tree; the config file
// and state.json are $HOME-based and only -config / SetConfigPath moves them.
// Every test here was already isolated on the first two and on none of the
// third, which is why `sidecar create …` in this package resolved the
// developer's own ~/.config/sidecar — harmless while nothing read it, and a
// hard refusal now that mutating verbs check isolation before they run.
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "sidecar-cli-test-state")
	if err != nil {
		fmt.Fprintf(os.Stderr, "cli test isolation: %v\n", err)
		os.Exit(1)
	}
	if err := os.Setenv("XDG_STATE_HOME", filepath.Join(dir, "state")); err != nil {
		fmt.Fprintf(os.Stderr, "cli test isolation: %v\n", err)
		os.Exit(1)
	}
	defaultTestConfigPath = filepath.Join(dir, "config", "config.json")
	config.SetConfigPath(defaultTestConfigPath)

	code := testenv.Main(m)
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
