package agentcontrol

import (
	"os"
	"testing"

	"github.com/marcus/sidecar/internal/testenv"
)

func TestMain(m *testing.M) { os.Exit(testenv.Main(m)) }
