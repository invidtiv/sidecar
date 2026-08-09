package community

import (
	"fmt"
	"math"
	"strings"

	"github.com/marcus/sidecar/internal/styles"
)

// The colour maths lives in internal/styles so the converter and the theme
// system share one implementation. These wrappers keep the community-facing
// names stable.

// HexToHSL converts a hex color (#RRGGBB) to HSL (h: 0-360, s: 0-1, l: 0-1).
func HexToHSL(hex string) (h, s, l float64) { return styles.HexToHSL(hex) }

// HSLToHex converts HSL (h: 0-360, s: 0-1, l: 0-1) to a hex color string.
func HSLToHex(h, s, l float64) string { return styles.HSLToHex(h, s, l) }

// Luminance returns relative luminance (0-1) using sRGB formula.
func Luminance(hex string) float64 { return styles.Luminance(hex) }

// Blend mixes two hex colors: result = (1-t)*c1 + t*c2. t is clamped to [0,1].
func Blend(c1, c2 string, t float64) string { return styles.Blend(c1, c2, t) }

// Lighten increases HSL lightness by pct (0-1).
func Lighten(hex string, pct float64) string { return styles.Lighten(hex, pct) }

// Darken decreases HSL lightness by pct (0-1).
func Darken(hex string, pct float64) string { return styles.Darken(hex, pct) }

// Saturation returns the HSL saturation (0-1) of a hex color.
func Saturation(hex string) float64 { return styles.Saturation(hex) }

// HueDegrees returns hue in degrees (0-360).
func HueDegrees(hex string) float64 { return styles.HueDegrees(hex) }

// ColorDistance returns euclidean distance in RGB space (0-441.67).
func ColorDistance(a, b string) float64 { return styles.ColorDistance(a, b) }

// ContrastRatio returns the WCAG 2.0 contrast ratio between two colors (1 to 21).
func ContrastRatio(fg, bg string) float64 { return styles.ContrastRatio(fg, bg) }

// EnsureContrast adjusts fg until it meets minRatio against bg.
func EnsureContrast(fg, bg string, minRatio float64) string {
	return styles.EnsureContrast(fg, bg, minRatio)
}

// EnsureContrastOn adjusts fg until it meets minRatio against every background
// it can be drawn on. Prefer this over EnsureContrast for any colour that
// appears on more than one surface.
func EnsureContrastOn(fg string, bgs []string, minRatio float64) string {
	return styles.EnsureContrastOn(fg, bgs, minRatio)
}

// FormatHex ensures a color string is in #rrggbb lowercase format.
func FormatHex(hex string) string {
	rgb := styles.HexToRGB(hex)
	return fmt.Sprintf("#%02x%02x%02x", clampByte(rgb.R), clampByte(rgb.G), clampByte(rgb.B))
}

// WithAlpha returns a lowercase #rrggbbaa color, normalizing the base hex if needed.
func WithAlpha(hex, alpha string) string {
	trimmed := strings.TrimPrefix(hex, "#")
	if len(trimmed) >= 6 {
		return "#" + strings.ToLower(trimmed[:6]) + strings.ToLower(strings.TrimPrefix(alpha, "#"))
	}
	base := FormatHex(hex)
	return base + strings.ToLower(strings.TrimPrefix(alpha, "#"))
}

func clampByte(v float64) uint8 {
	if v < 0 {
		return 0
	}
	if v > 255 {
		return 255
	}
	return uint8(math.Round(v))
}
