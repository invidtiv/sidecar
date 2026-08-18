package cli

import (
	"os"
	"testing"

	"github.com/marcus/sidecar/internal/testenv"
)

// TestMain isolates this package's tmux access. See internal/testenv: without
// it, every tmux command these tests run lands on the developer's own server.
func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }
