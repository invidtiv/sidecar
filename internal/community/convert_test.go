package community

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/styles"
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

func TestConvertSelectionBgLiftsOffCanvas(t *testing.T) {
	scheme := GetScheme("Catppuccin Mocha")
	if scheme == nil {
		t.Fatal("Catppuccin Mocha not found")
	}
	palette := Convert(scheme)
	if palette.SelectionBg == "" {
		t.Fatal("SelectionBg is empty")
	}
	if palette.SelectionBg == palette.BgTertiary {
		t.Errorf("SelectionBg reused BgTertiary %s; the selected-row fill is too close to the canvas", palette.BgTertiary)
	}
	if styles.MaxContrastPole([]string{palette.SelectionBg}) != styles.MaxContrastPole([]string{palette.BgPrimary}) {
		t.Errorf("SelectionBg %s flipped the canvas ink pole (scheme selection was %s)",
			palette.SelectionBg, scheme.SelectionBackground)
	}
	if ratio := styles.ContrastRatio(palette.SelectionBg, palette.BgPrimary); ratio < styles.SelectionSeparationFloor-0.01 {
		if styles.ContrastRatio(palette.TextPrimary, palette.SelectionBg) > 4.5+0.15 {
			t.Errorf("SelectionBg %s vs canvas %s is %.2f (want >= %.2f) with text headroom still left",
				palette.SelectionBg, palette.BgPrimary, ratio, styles.SelectionSeparationFloor)
		}
	}
	if ratio := styles.ContrastRatio(palette.TextPrimary, palette.SelectionBg); ratio < 4.5-0.01 {
		t.Errorf("TextPrimary on SelectionBg is %.2f; want >= 4.5", ratio)
	}
}

func TestConvertKeepsSamePoleSchemeSelection(t *testing.T) {
	scheme := GetScheme("Dracula")
	if scheme == nil {
		t.Fatal("Dracula not found")
	}
	palette := Convert(scheme)
	// Dracula's scheme selection is a same-pole lift of the canvas, so the
	// converter should keep that hue rather than replacing it with a
	// canvas-grey. Lightness may still move to meet the contrast target.
	_, seedS, _ := HexToHSL(scheme.SelectionBackground)
	_, gotS, _ := HexToHSL(palette.SelectionBg)
	if seedS > 0.05 && gotS < seedS/2 {
		t.Errorf("same-pole scheme selection %s lost its hue (got %s, sat %.2f -> %.2f)",
			scheme.SelectionBackground, palette.SelectionBg, seedS, gotS)
	}
}

func TestCommunityConversionsHaveVisibleSelectionHighlight(t *testing.T) {
	names := ListSchemes()
	if len(names) == 0 {
		t.Fatal("no community schemes embedded")
	}
	for _, name := range names {
		scheme := GetScheme(name)
		if scheme == nil {
			t.Errorf("community scheme %q missing from map", name)
			continue
		}
		p := styles.NormalizePalette(Convert(scheme))
		if p.SelectionBg == "" {
			t.Errorf("%s: SelectionBg is empty", name)
			continue
		}
		if styles.MaxContrastPole([]string{p.SelectionBg}) != styles.MaxContrastPole([]string{p.BgPrimary}) {
			t.Errorf("%s: SelectionBg %s flipped the canvas ink pole", name, p.SelectionBg)
		}
		if ratio := styles.ContrastRatio(p.TextPrimary, p.SelectionBg); ratio < 4.5-0.01 {
			t.Errorf("%s: TextPrimary on SelectionBg is %.2f", name, ratio)
		}
		if p.BgPrimary != "" && p.SelectionBg == p.BgPrimary {
			t.Errorf("%s: SelectionBg reused the canvas %s", name, p.BgPrimary)
		}
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
