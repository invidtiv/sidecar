package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

func enableWorkspaceFeature(t *testing.T, names ...string) {
	t.Helper()
	cfg := config.Default()
	features.Init(cfg)
	for _, name := range names {
		features.SetOverride(name, true)
	}
	t.Cleanup(func() { features.Init(config.Default()) })
}
