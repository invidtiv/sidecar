package community

import (
	"strings"
	"testing"
)

func TestConvertCatppuccinMocha(t *testing.T) {
	scheme := GetScheme("Catppuccin Mocha")
	if scheme == nil {
		t.Fatal("Catppuccin Mocha not found")
	}

	palette := Convert(scheme)

	// Verify key mappings
	if palette.Primary != scheme.Blue {
		t.Errorf("Primary = %s, want %s (blue)", palette.Primary, scheme.Blue)
	}
	if palette.BgPrimary != scheme.Background {
		t.Errorf("BgPrimary = %s, want %s", palette.BgPrimary, scheme.Background)
	}
	if palette.TabStyle != "minimal" {
		t.Errorf("TabStyle = %s, want minimal", palette.TabStyle)
	}

	// Verify derived colors are valid hex
	derivedFields := []struct {
		name, val string
	}{
		{"TextSecondary", palette.TextSecondary},
		{"TextPrimary", palette.TextPrimary},
		{"TextSubtle", palette.TextSubtle},
		{"BgSecondary", palette.BgSecondary},
		{"BgTertiary", palette.BgTertiary},
		{"BorderMuted", palette.BorderMuted},
		{"DiffAddBg", palette.DiffAddBg},
		{"DiffRemoveBg", palette.DiffRemoveBg},
	}
	for _, f := range derivedFields {
		if !isValidHex(f.val) {
			t.Errorf("%s = %q, not valid hex", f.name, f.val)
		}
	}

	// Dark theme should get dark markdown theme
	if palette.MarkdownTheme != "dark" {
		t.Errorf("MarkdownTheme = %s, want dark", palette.MarkdownTheme)
	}

	// Syntax theme should be a known chroma theme
	if palette.SyntaxTheme == "" {
		t.Error("SyntaxTheme is empty")
	}
}

func TestConvertLightTheme(t *testing.T) {
	scheme := GetScheme("Alabaster")
	if scheme == nil {
		scheme = GetScheme("Apple System Colors Light")
	}
	if scheme == nil {
		t.Skip("No known light theme found")
	}

	palette := Convert(scheme)

	if Luminance(scheme.Background) >= 0.5 {
		if palette.MarkdownTheme != "light" {
			t.Errorf("Light theme MarkdownTheme = %s, want light", palette.MarkdownTheme)
		}
	}
}

func TestConvertTabMinimal(t *testing.T) {
	scheme := GetScheme("Catppuccin Mocha")
	if scheme == nil {
		t.Fatal("scheme not found")
	}

	palette := Convert(scheme)

	if palette.TabStyle != "minimal" {
		t.Errorf("TabStyle = %s, want minimal", palette.TabStyle)
	}
	if len(palette.TabColors) == 0 {
		t.Error("TabColors is empty")
	}
	for i, c := range palette.TabColors {
		if !isValidHex(c) {
			t.Errorf("TabColors[%d] = %q, not valid hex", i, c)
		}
	}
}

func TestConvertGradientBorders(t *testing.T) {
	scheme := GetScheme("Dracula")
	if scheme == nil {
		t.Fatal("Dracula not found")
	}

	palette := Convert(scheme)

	if len(palette.GradientBorderActive) != 2 {
		t.Errorf("GradientBorderActive has %d colors, want 2", len(palette.GradientBorderActive))
	}
	if len(palette.GradientBorderNormal) != 2 {
		t.Errorf("GradientBorderNormal has %d colors, want 2", len(palette.GradientBorderNormal))
	}
	if palette.GradientBorderAngle != 30 {
		t.Errorf("GradientBorderAngle = %f, want 30", palette.GradientBorderAngle)
	}
}

func TestPaletteToOverrides(t *testing.T) {
	scheme := GetScheme("Catppuccin Mocha")
	if scheme == nil {
		t.Fatal("scheme not found")
	}

	palette := Convert(scheme)
	overrides := PaletteToOverrides(palette)

	// Verify string fields
	if v, ok := overrides["primary"].(string); !ok || v != palette.Primary {
		t.Errorf("overrides[primary] = %v, want %s", overrides["primary"], palette.Primary)
	}
	if v, ok := overrides["bgPrimary"].(string); !ok || v != palette.BgPrimary {
		t.Errorf("overrides[bgPrimary] = %v, want %s", overrides["bgPrimary"], palette.BgPrimary)
	}

	// Verify gradient arrays
	if arr, ok := overrides["gradientBorderActive"].([]interface{}); !ok || len(arr) != 2 {
		t.Errorf("gradientBorderActive not a 2-element array: %v", overrides["gradientBorderActive"])
	}

	// Verify tab style
	if v, ok := overrides["tabStyle"].(string); !ok || v != "minimal" {
		t.Errorf("tabStyle = %v, want minimal", overrides["tabStyle"])
	}

	// Verify angle
	if v, ok := overrides["gradientBorderAngle"].(float64); !ok || v != 30 {
		t.Errorf("gradientBorderAngle = %v, want 30", overrides["gradientBorderAngle"])
	}
}

func TestMatchSyntaxTheme(t *testing.T) {
	// Dark themes
	got := matchSyntaxTheme("#282a36")
	if got != "dracula" {
		t.Errorf("matchSyntaxTheme(#282a36) = %s, want dracula", got)
	}
	got = matchSyntaxTheme("#1e1e2e")
	if got != "catppuccin-mocha" {
		t.Errorf("matchSyntaxTheme(#1e1e2e) = %s, want catppuccin-mocha", got)
	}
	// Light themes - #ffffff matches both "github" and "vs"
	got = matchSyntaxTheme("#ffffff")
	if got != "github" && got != "vs" {
		t.Errorf("matchSyntaxTheme(#ffffff) = %s, want github or vs", got)
	}
}

func TestConvertBgOverlayHandlesAlpha(t *testing.T) {
	base := GetScheme("Catppuccin Mocha")
	if base == nil {
		t.Skip("Catppuccin Mocha not found")
	}
	scheme := *base
	scheme.Background = "#112233aa"

	palette := Convert(&scheme)
	if palette.BgOverlay != "#112233cc" {
		t.Errorf("BgOverlay = %s, want #112233cc", palette.BgOverlay)
	}
}

func isValidHex(s string) bool {
	if len(s) < 7 || s[0] != '#' {
		return false
	}
	hex := s[1:7]
	for _, c := range hex {
		if !strings.ContainsRune("0123456789abcdefABCDEF", c) {
			return false
		}
	}
	return true
}
