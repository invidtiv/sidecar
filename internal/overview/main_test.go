package overview

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/testenv"
	"github.com/marcus/sidecar/internal/tty"
)

// TestMain keeps activity persistence inside the test's own directory. The
// store path is resolved from the user's state dir in production, and no test
// should write there.
//
// It also isolates tmux, for the same reason: this package builds workspace
// models that reach internal/tty and shell out to tmux (td-4d99ae).
func TestMain(m *testing.M) {
	dir, err := os.MkdirTemp("", "overview-activity-")
	if err != nil {
		panic(err)
	}
	ActivityStorePath = func() string { return filepath.Join(dir, "agent-activity.json") }
	loadShowIdleWorktrees = func() bool { return false }
	saveShowIdleWorktrees = func(bool) error { return nil }
	loadPinnedWorkspaceIDs = func() []string { return nil }
	savePinnedWorkspaceIDs = func([]string) error { return nil }
	// The sort preference is package-global in production, so without this a
	// test that picks a sort leaks it into every later New() in the same run —
	// and the save would reach the developer's real state file.
	loadWorkspaceListSort = func() string { return "" }
	saveWorkspaceListSort = func(string) error { return nil }
	loadLastGlobalCreateProject = func() string { return "" }
	saveLastGlobalCreateProject = func(string) error { return nil }
	loadSessionsSelected = func() string { return "" }
	saveSessionsSelected = func(string) error { return nil }
	loadSessionsPaneLayout = func(string) *state.PaneLayoutJSON { return nil }
	saveSessionsPaneLayout = func(string, *state.PaneLayoutJSON) error { return nil }
	sessionsSelectedDebounce = 0
	if err := state.InitWithDir(dir); err != nil {
		panic(err)
	}
	// Manifest resolution scans the state dir's projects/ tree. Left at its
	// production default it would read the developer's real one, making every
	// test that runs an Update depend on which projects happen to be registered
	// on this machine.
	lookupProjectDirs = func([]string) map[string]string { return nil }
	code := testenv.Main(m)
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// settleWheel ends the burst the shared accumulator is coalescing. Notches that
// arrive inside one debounce window are one flick, so a test that means two
// separate gestures has to let the window pass.
func settleWheel() { time.Sleep(2 * tty.WheelDebounceInterval) }
