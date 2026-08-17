package theme

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/styles"
)

// The launch theme is a fresh-install default, not an upgrade. These tests pin
// both directions through the real loader, because the whole mechanism lives in
// the gap between "config file says nothing about a theme" and "config file
// records a name" — a distinction only LoadFrom can make.

func writeConfig(t *testing.T, body string) string {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	return path
}

func TestFreshInstallResolvesToLaunchTheme(t *testing.T) {
	// No config file at all: the first run any new user gets.
	cfg, err := config.LoadFrom(filepath.Join(t.TempDir(), "config.json"))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := ResolveTheme(cfg, "/code/proj").BaseName; got != styles.FreshInstallTheme {
		t.Errorf("no config file: BaseName = %q, want %q", got, styles.FreshInstallTheme)
	}

	// A config that exists but records no theme choice is the same case.
	path := writeConfig(t, `{"ui":{"showClock":true}}`)
	cfg, err = config.LoadFrom(path)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	if got := ResolveTheme(cfg, "/code/proj").BaseName; got != styles.FreshInstallTheme {
		t.Errorf("config without ui.theme: BaseName = %q, want %q", got, styles.FreshInstallTheme)
	}
}

func TestRecordedThemeSurvivesUpgrade(t *testing.T) {
	// Each case is a config written by a Sidecar that predates sidecar-modern.
	cases := []struct {
		name string
		body string
		want string
	}{
		{
			name: "the original default, chosen explicitly",
			body: `{"ui":{"theme":{"name":"default"}}}`,
			want: "default",
		},
		{
			name: "a built-in theme",
			body: `{"ui":{"theme":{"name":"nord"}}}`,
			want: "nord",
		},
		{
			name: "a theme with overrides",
			body: `{"ui":{"theme":{"name":"dracula","overrides":{"primary":"#ff0000"}}}}`,
			want: "dracula",
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg, err := config.LoadFrom(writeConfig(t, tc.body))
			if err != nil {
				t.Fatalf("LoadFrom: %v", err)
			}
			if got := ResolveTheme(cfg, "/code/proj").BaseName; got != tc.want {
				t.Errorf("BaseName = %q, want %q (upgrade must not restyle a user)", got, tc.want)
			}
		})
	}

	// A community scheme is recorded on the "default" base; the upgrade must
	// leave both halves alone.
	cfg, err := config.LoadFrom(writeConfig(t, `{"ui":{"theme":{"name":"default","community":"Solarized Dark"}}}`))
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	resolved := ResolveTheme(cfg, "/code/proj")
	if resolved.BaseName != "default" || resolved.CommunityName != "Solarized Dark" {
		t.Errorf("community upgrade = %+v, want default/Solarized Dark", resolved)
	}
}

func TestFreshInstallThemeIsRegisteredAndListedAsBuiltIn(t *testing.T) {
	if !styles.IsValidTheme(styles.FreshInstallTheme) {
		t.Fatalf("%s is not in the theme registry", styles.FreshInstallTheme)
	}
	var found Entry
	for _, entry := range List() {
		if entry.IsBuiltIn && entry.ThemeKey == styles.FreshInstallTheme {
			found = entry
		}
	}
	if found.IsZero() {
		t.Fatalf("%s missing from the picker list", styles.FreshInstallTheme)
	}
	if found.Name != "Sidecar Modern" {
		t.Errorf("display name = %q, want %q", found.Name, "Sidecar Modern")
	}
	if Label(found) != "Built-in" {
		t.Errorf("label = %q, want Built-in", Label(found))
	}
	if len(Filter(List(), "sidecar")) == 0 {
		t.Error(`searching the picker for "sidecar" finds nothing`)
	}
	for i, swatch := range Swatch(found) {
		if !styles.IsValidHexColor(swatch) {
			t.Errorf("swatch[%d] = %q, not a hex colour", i, swatch)
		}
	}

	// With nothing recorded, the picker must open on the theme that is
	// actually on screen rather than on an empty cursor.
	if got := GlobalEntry(config.ThemeConfig{}); got.ThemeKey != styles.FreshInstallTheme {
		t.Errorf("GlobalEntry(zero).ThemeKey = %q, want %q", got.ThemeKey, styles.FreshInstallTheme)
	}
	if got := GlobalEntry(config.ThemeConfig{Name: "nord"}); got.ThemeKey != "nord" {
		t.Errorf("GlobalEntry(nord).ThemeKey = %q, want nord", got.ThemeKey)
	}
}
