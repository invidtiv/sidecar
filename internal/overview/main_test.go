package overview

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/tty"
)

// TestMain keeps activity persistence inside the test's own directory. The
// store path is resolved from the user's state dir in production, and no test
// should write there.
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
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}

// settleWheel ends the burst the shared accumulator is coalescing. Notches that
// arrive inside one debounce window are one flick, so a test that means two
// separate gestures has to let the window pass.
func settleWheel() { time.Sleep(2 * tty.WheelDebounceInterval) }
