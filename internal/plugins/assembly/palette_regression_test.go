package assembly

import (
	"log/slog"
	"os"
	"sort"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
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
	// Sibling assembly tests call features.Init with notes on. Reset to
	// code defaults so this assertion is about Plan(Default()), not leftover
	// singleton state.
	features.Init(config.Default())

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

	// Notes is off by default. Enabling it must not start advertising a
	// keyless edit-note row on every other tab's palette.
	initFeatures(t, map[string]bool{features.NotesPlugin.Name: true})
	notesReg := plugin.NewRegistry(pctx)
	for _, entry := range Plan(config.Default()) {
		if err := notesReg.Register(entry.New()); err != nil {
			t.Fatalf("register %s: %v", entry.ID, err)
		}
	}
	var notesOffenders []string
	for _, e := range palette.BuildEntries(km, notesReg.Plugins(), "global", "global") {
		if e.Key == "" {
			notesOffenders = append(notesOffenders, e.Context+"/"+e.CommandID)
		}
	}
	sort.Strings(notesOffenders)
	if len(notesOffenders) > 0 {
		t.Errorf("enabling notes leaked %d inert palette rows: %v", len(notesOffenders), notesOffenders)
	}
}
