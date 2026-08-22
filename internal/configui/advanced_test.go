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

func TestAdvancedPageRendersPerformance(t *testing.T) {
	m := advancedFixture(t, nil)
	view := ansi.Strip(m.View(160, 45))

	for _, want := range []string{
		"Technical controls. Most people never need these.",
		"Performance", "Terminal preview capture", "2 MB",
		"Accepts 256 KB to 64 MB",
		"Any setting that needs a reload is called out before it is saved.",
		// The flags left, so the page has to say where they went rather than
		// leaving a user who knew them here with nothing.
		"Feature flags moved to Feature Flags.",
	} {
		if !strings.Contains(view, want) {
			t.Fatalf("Advanced is missing %q:\n%s", want, view)
		}
	}
}

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
