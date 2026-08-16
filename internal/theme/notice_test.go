package theme

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/config"
)

// The three cases the notice has to tell apart, plus the once-ever guarantee.
func TestShouldAnnounceDefaultChange(t *testing.T) {
	cases := []struct {
		name          string
		global        config.ThemeConfig
		hasPriorState bool
		seen          bool
		want          bool
	}{
		{
			name:          "restyled user: no recorded choice, has run before",
			global:        config.ThemeConfig{},
			hasPriorState: true,
			want:          true,
		},
		{
			name:          "explicit choice is untouched, so says nothing",
			global:        config.ThemeConfig{Name: "nord"},
			hasPriorState: true,
		},
		{
			name:          "the original purple is a choice like any other",
			global:        config.ThemeConfig{Name: "default"},
			hasPriorState: true,
		},
		{
			name:          "a community theme is a choice too",
			global:        config.ThemeConfig{Community: "catppuccin"},
			hasPriorState: true,
		},
		{
			name:          "fresh install has no previous look to contrast against",
			global:        config.ThemeConfig{},
			hasPriorState: false,
		},
		{
			name:          "once ever",
			global:        config.ThemeConfig{},
			hasPriorState: true,
			seen:          true,
		},
		{
			name:          "overrides without a base are still being restyled",
			global:        config.ThemeConfig{Overrides: map[string]interface{}{"primary": "#ff0000"}},
			hasPriorState: true,
			want:          true,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := ShouldAnnounceDefaultChange(tc.global, tc.hasPriorState, tc.seen); got != tc.want {
				t.Errorf("ShouldAnnounceDefaultChange(%+v, prior=%v, seen=%v) = %v, want %v",
					tc.global, tc.hasPriorState, tc.seen, got, tc.want)
			}
		})
	}
}

// The message has to name the way back, because the toast is the only place the
// user is told the look changed on purpose.
func TestDefaultThemeNoticeNamesTheThemeSwitcher(t *testing.T) {
	if want := "#"; !strings.Contains(DefaultThemeNotice, want) {
		t.Errorf("notice %q does not name the %s theme switcher", DefaultThemeNotice, want)
	}
	if !strings.Contains(DefaultThemeNotice, "Sidecar Modern") {
		t.Errorf("notice %q does not name the new theme", DefaultThemeNotice)
	}
}
