package configui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
)

// advancedFixture is a model on Advanced with a temp config file and the
// feature manager pointed at it.
func advancedFixture(t *testing.T, mutate func(*config.Config)) *Model {
	t.Helper()
	cfg := config.Default()
	if mutate != nil {
		mutate(cfg)
	}
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	m, _ := configFixture(t, cfg)
	m.Open(PageAdvanced)
	return m
}

func TestAdvancedPageRendersPreviewsAndPerformance(t *testing.T) {
	m := advancedFixture(t, nil)
	view := ansi.Strip(m.View(160, 45))

	for _, want := range []string{
		"Feature previews and technical controls. Most people never need these.",
		"Feature previews",
		"Cross-project Activity", "Show workspaces from every configured project in Activity.",
		"Document panes", "Open files, issues, and diffs beside your active workspace.",
		"Full tmux attach", "Hand the terminal over to tmux's native client and shortcuts.",
		"Split workspace terminal", "Show a dedicated terminal next to the workspace list.",
		"Performance", "Terminal preview capture", "2 MB",
		"Accepts 256 KB to 64 MB",
		"Any setting that needs a reload is called out before it is saved.",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("Advanced is missing %q:\n%s", want, view)
		}
	}
}

// Every preview round-trips to features.flags under its real name.
func TestAdvancedFlagsRoundTrip(t *testing.T) {
	for _, item := range previews() {
		t.Run(item.flag, func(t *testing.T) {
			m := advancedFixture(t, nil)
			before := m.flagEnabled(item.flag)
			activate(t, m, regionAdvancedFlag+item.flag)
			got, ok := loadSaved(t).Features.Flags[item.flag]
			if !ok {
				t.Fatalf("%s was not written to features.flags", item.flag)
			}
			if got == before {
				t.Fatalf("%s did not change (still %v)", item.flag, got)
			}
			if m.flagEnabled(item.flag) != got {
				t.Fatalf("the page disagrees with the file for %s", item.flag)
			}
			// The live answer must move with the file, or a saved preview would
			// do nothing until a restart it does not need.
			if features.IsEnabled(item.flag) != got {
				t.Fatalf("features.IsEnabled(%s) = %v, file says %v", item.flag, features.IsEnabled(item.flag), got)
			}
		})
	}
}

// Only a flag that is genuinely read once at startup claims a restart.
func TestAdvancedRestartNoteIsPerFlag(t *testing.T) {
	m := advancedFixture(t, nil)
	activate(t, m, regionAdvancedFlag+features.WorkspaceDocPanes.Name)
	if view := ansi.Strip(m.View(160, 45)); strings.Contains(view, panelRestartNote) {
		t.Fatalf("a live flag claimed it needed a restart:\n%s", view)
	}

	m = advancedFixture(t, nil)
	activate(t, m, regionAdvancedFlag+features.CrossProjectOverview.Name)
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, panelRestartNote) {
		t.Fatalf("a startup-scoped flag did not mention the restart:\n%s", view)
	}
}

// Turning on Full tmux attach is what makes the Terminal page's attach chord
// editable — the two pages share one config state, not a copy each.
func TestFullAttachUnlocksTheTerminalAttachControl(t *testing.T) {
	m := advancedFixture(t, func(cfg *config.Config) {
		cfg.Plugins.Workspace.InteractiveAttachKey = "ctrl+]"
	})
	m.Navigate(PageTerminal)
	if view := ansi.Strip(m.View(160, 45)); !strings.Contains(view, "Turn on Full tmux attach under Advanced") {
		t.Fatalf("the attach control was not locked to begin with:\n%s", view)
	}

	m.Navigate(PageAdvanced)
	activate(t, m, regionAdvancedFlag+features.TmuxFullAttach.Name)
	m.Navigate(PageTerminal)
	view := ansi.Strip(m.View(160, 45))
	if strings.Contains(view, "Turn on Full tmux attach under Advanced") {
		t.Fatalf("the attach control stayed locked after enabling the preview:\n%s", view)
	}
	if !strings.Contains(view, "Ctrl+]") {
		t.Fatalf("the attach chord is not editable after enabling the preview:\n%s", view)
	}
}

// A typed capture limit is read, clamped, and only then stored.
func TestAdvancedCaptureLimitClampsTypedValues(t *testing.T) {
	cases := map[string]int{
		"4 MB":       4 * 1024 * 1024,
		"512kb":      512 * 1024,
		"1":          CaptureLimitMin, // below the floor
		"999 MB":     CaptureLimitMax, // above the ceiling
		"":           CaptureLimitDefault,
		"not a size": CaptureLimitDefault,
		"1048576":    1024 * 1024,
	}
	for typed, want := range cases {
		t.Run(typed, func(t *testing.T) {
			m := advancedFixture(t, nil)
			m.View(160, 45)
			m.detailFocus = true
			m.editCaptureLimit()
			m.advanced().capture.SetValue(typed)
			_, cmd := m.Key(tea.KeyPressMsg{Code: tea.KeyEnter})
			if cmd == nil {
				t.Fatal("a typed capture limit was not saved")
			}
			reload(t, m, cmd())
			if got := loadSaved(t).Plugins.Workspace.TmuxCaptureMaxBytes; got != want {
				t.Fatalf("typed %q stored %d, want %d", typed, got, want)
			}
		})
	}
}

// The selected values — the ladder the Terminal page steps through — are inside
// the same accepted range the typed field enforces.
func TestSelectedCaptureLimitsStayInRange(t *testing.T) {
	for _, choice := range CaptureLimitChoices {
		if ClampCaptureLimit(choice) != choice {
			t.Fatalf("selector rung %d is outside the accepted range", choice)
		}
	}
	if got := ParseCaptureLimit("   "); got != CaptureLimitDefault {
		t.Fatalf("a blank value stored %d", got)
	}
}
