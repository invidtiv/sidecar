package assembly

import (
	"log/slog"
	"os"
	"sort"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/palette"
	"github.com/marcus/sidecar/internal/plugin"
)

// TestKeylessEntriesDoNotLeakIntoOtherPlugins guards the blast radius of
// palette.BuildEntries emitting entries for commands with no binding.
//
// BuildEntries is shared by every tab. Most plugins publish Commands() purely
// as documentation for keys they handle inside their own Update: they register
// no bindings and carry no Handler, so a keyless entry for them would be a row
// that does nothing when selected. Assembling the real plugin set and asserting
// no such entry appears is the only way to be sure the Tasks fix did not
// quietly rewrite six other palettes.
func TestKeylessEntriesDoNotLeakIntoOtherPlugins(t *testing.T) {
	dir := t.TempDir()

	km := keymap.NewRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	pctx := &plugin.Context{Keymap: km, Logger: logger, WorkDir: dir}
	reg := plugin.NewRegistry(pctx)

	// Tasks is not planned here at all — it is a global tab owned by the app
	// shell, and its palette behaviour is covered end to end in internal/app.
	for _, entry := range Plan(config.Default()) {
		if err := reg.Register(entry.New()); err != nil {
			t.Fatalf("register %s: %v", entry.ID, err)
		}
	}

	var offenders []string
	for _, e := range palette.BuildEntries(km, reg.Plugins(), "global", "global") {
		if e.Key == "" {
			offenders = append(offenders, e.Context+"/"+e.CommandID)
		}
	}
	sort.Strings(offenders)
	if len(offenders) > 0 {
		t.Errorf("plugins that bind nothing gained %d inert palette rows: %v", len(offenders), offenders)
	}
}
