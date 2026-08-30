package hostserve

import (
	"os"
	"testing"

	"github.com/marcus/sidecar/internal/testenv"
)

// TestMain isolates this package's tmux access. It matters more here than in
// most packages: serve now reaps, so its default ProbeShell is a real
// `tmux list-sessions` and its default ForgetShell writes a real shells.json.
// Without this, a test whose fixture happens to name a session the developer is
// actually running would take a verdict from — and could write about — the
// user's live server. See internal/testenv.
func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }
