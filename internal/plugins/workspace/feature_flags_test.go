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

// disableWorkspaceFeature turns a default-on flag off for one test. Proving a
// gate still exists has to be done explicitly once the flag ships enabled.
func disableWorkspaceFeature(t *testing.T, names ...string) {
	t.Helper()
	features.Init(config.Default())
	for _, name := range names {
		features.SetOverride(name, false)
	}
	t.Cleanup(func() { features.Init(config.Default()) })
}
