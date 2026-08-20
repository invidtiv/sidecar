package assembly

import (
	"log/slog"
	"os"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/plugin"
)

// TestEveryPluginRendersInsideANarrowContentBox is the guard behind the
// notification centre's width reservation.
//
// The centre is an app-level right panel: it hands every plugin a *narrower*
// content box and re-announces it exactly as a terminal resize does. That only
// works if every plugin lays out against the box it is given, so this walks the
// real plugin set at the narrowest boxes the panel can produce and asserts each
// one stays inside — no row wider than the box, no more rows than the box is
// tall. A plugin that overflows would paint under the panel, or push the
// header off screen.
func TestEveryPluginRendersInsideANarrowContentBox(t *testing.T) {
	features.Init(config.Default())
	dir := t.TempDir()

	km := keymap.NewRegistry()
	logger := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelError}))
	pctx := &plugin.Context{Keymap: km, Logger: logger, WorkDir: dir, ProjectRoot: dir, Config: config.Default()}
	reg := plugin.NewRegistry(pctx)
	for _, entry := range Plan(config.Default()) {
		if err := reg.Register(entry.New()); err != nil {
			t.Fatalf("register %s: %v", entry.ID, err)
		}
	}

	// The widths a 100-, 80-, and 62-column terminal leaves when the panel is
	// open at its default, its widest, and its narrowest.
	boxes := []struct{ width, height int }{
		{40, 28},
		{46, 24},
		{65, 30},
	}
	for _, box := range boxes {
		for _, p := range reg.Plugins() {
			p.Update(tea.WindowSizeMsg{Width: box.width, Height: box.height})
			out := p.View(box.width, box.height)
			lines := strings.Split(out, "\n")
			if len(lines) > box.height {
				t.Errorf("%s at %dx%d rendered %d rows, want at most %d",
					p.ID(), box.width, box.height, len(lines), box.height)
			}
			for i, line := range lines {
				if w := lipgloss.Width(line); w > box.width {
					t.Errorf("%s at %dx%d overflowed row %d: %d columns", p.ID(), box.width, box.height, i, w)
					break
				}
			}
		}
	}
}
