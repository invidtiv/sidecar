package overview

import (
	"os"
	"path/filepath"
	"testing"
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
	code := m.Run()
	_ = os.RemoveAll(dir)
	os.Exit(code)
}
