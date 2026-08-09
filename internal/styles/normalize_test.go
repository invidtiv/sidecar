package styles

import (
	"reflect"
	"testing"
)

func TestBuiltInThemesMeetContrastTargets(t *testing.T) {
	for _, name := range ListThemes() {
		t.Run(name, func(t *testing.T) {
			for _, failure := range CheckPaletteContrast(NormalizePalette(GetTheme(name).Colors)) {
				t.Errorf("%s: %s", name, failure)
			}
		})
	}
}

// The shortcut row was the worst offender before normalization: it drew
// TextMuted (validated only against BgPrimary) on BgTertiary (an arbitrary
// terminal selection colour). Both halves of that bug are covered here.
func TestKeyHintIsLegibleOnEveryBuiltInTheme(t *testing.T) {
	for _, name := range ListThemes() {
		p := NormalizePalette(GetTheme(name).Colors)
		if ratio := ContrastRatio(p.KeyHintFg, p.SurfaceRaised); ratio < targetBodyText-0.01 {
			t.Errorf("%s: key hint %.2f < %.1f", name, ratio, targetBodyText)
		}
		if ratio := ContrastRatio(p.TextMuted, p.BgSecondary); ratio < targetBodyText-0.01 {
			t.Errorf("%s: footer label %.2f < %.1f", name, ratio, targetBodyText)
		}
	}
}

func TestNormalizePaletteIsIdempotent(t *testing.T) {
	for _, name := range ListThemes() {
		once := NormalizePalette(GetTheme(name).Colors)
		twice := NormalizePalette(once)
		if !reflect.DeepEqual(once, twice) {
			t.Errorf("%s: second normalization changed the palette", name)
		}
	}
}

func TestNormalizePaletteLeavesCompliantColorsAlone(t *testing.T) {
	p := NormalizePalette(ColorPalette{
		BgPrimary:   "#000000",
		BgSecondary: "#0a0a0a",
		TextPrimary: "#ffffff",
		TextMuted:   "#bbbbbb",
	})
	if p.TextPrimary != "#ffffff" {
		t.Errorf("TextPrimary was adjusted despite passing: %s", p.TextPrimary)
	}
	if p.TextMuted != "#bbbbbb" {
		t.Errorf("TextMuted was adjusted despite passing: %s", p.TextMuted)
	}
}

func TestEnsureContrastOnPreservesHue(t *testing.T) {
	// A blue that is far too dark for a dark background should come back blue,
	// not blended toward white.
	fixed := EnsureContrastOn("#001a4d", []string{"#111827"}, targetBodyText)
	if ratio := ContrastRatio(fixed, "#111827"); ratio < targetBodyText-0.01 {
		t.Fatalf("target not met: %.2f", ratio)
	}
	h, s, _ := HexToHSL(fixed)
	origH, origS, _ := HexToHSL("#001a4d")
	if diff := h - origH; diff > 1 || diff < -1 {
		t.Errorf("hue drifted from %.1f to %.1f", origH, h)
	}
	if s < origS-0.05 {
		t.Errorf("saturation dropped from %.2f to %.2f", origS, s)
	}
}

func TestMaxContrastPoleUsesMeasuredContrast(t *testing.T) {
	// Luminance 0.2-0.5: the old "luminance > 0.5 means use black" rule picked
	// white here, which is the lower-contrast choice.
	midtone := "#8a8a8a"
	if got := MaxContrastPole([]string{midtone}); got != "#000000" {
		t.Errorf("expected black on %s, got %s (white %.2f, black %.2f)",
			midtone, got, ContrastRatio("#ffffff", midtone), ContrastRatio("#000000", midtone))
	}
}

func TestAdjustSurfaceFlipsDirectionAcrossTheCrossover(t *testing.T) {
	// A mid-luminance teal: lightening walks the surface past the point where
	// white stops winning, so the derived surface must go darker instead.
	bg := "#006984"
	surface := AdjustSurface(bg, 0.08)
	if Luminance(surface) >= Luminance(bg) {
		t.Errorf("expected a darker surface, got %s (L %.3f) from %s (L %.3f)",
			surface, Luminance(surface), bg, Luminance(bg))
	}
	pole := MaxContrastPole([]string{bg})
	if ratio := ContrastRatio(pole, surface); ratio < surfaceTextHeadroom {
		t.Errorf("surface %s left only %.2f for %s", surface, ratio, pole)
	}
}
