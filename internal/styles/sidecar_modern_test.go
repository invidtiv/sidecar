package styles

import (
	"reflect"
	"testing"
)

// SidecarModernTheme is the launch theme, so it is held to a stricter standard
// than the older built-ins: every field is authored, and the palette is already
// contrast-clean before NormalizePalette touches it.

func TestSidecarModernCoversEveryPaletteField(t *testing.T) {
	v := reflect.ValueOf(SidecarModernTheme.Colors)
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		if v.Field(i).IsZero() {
			t.Errorf("ColorPalette.%s is unset — every role must be chosen deliberately", field.Name)
		}
	}
}

func TestSidecarModernPassesNormalizationUnchanged(t *testing.T) {
	in := SidecarModernTheme.Colors
	out := NormalizePalette(in)
	if !reflect.DeepEqual(in, out) {
		vi, vo := reflect.ValueOf(in), reflect.ValueOf(out)
		for i := 0; i < vi.NumField(); i++ {
			if !reflect.DeepEqual(vi.Field(i).Interface(), vo.Field(i).Interface()) {
				t.Errorf("NormalizePalette rewrote %s: %v -> %v",
					vi.Type().Field(i).Name, vi.Field(i).Interface(), vo.Field(i).Interface())
			}
		}
	}
	if failures := CheckPaletteContrast(out); len(failures) > 0 {
		for _, f := range failures {
			t.Errorf("contrast: %s", f)
		}
	}
}

// The palette is low-contrast by design, which makes the pairings the
// normalizer does not police the easy ones to get wrong. These are the ones a
// muted theme actually breaks: body text on the selection fill, and the
// semantic accents on both bars.
func TestSidecarModernReadableWhereNormalizationDoesNotReach(t *testing.T) {
	c := NormalizePalette(SidecarModernTheme.Colors)
	cases := []struct {
		name   string
		fg, bg string
		min    float64
	}{
		{"textPrimary on selection", c.TextPrimary, c.BgTertiary, 4.5},
		{"textSecondary on selection", c.TextSecondary, c.BgTertiary, 4.0},
		{"textMuted on selection", c.TextMuted, c.BgTertiary, 4.5},
		{"textSelection on selection", c.TextSelection, c.BgTertiary, 4.5},
		{"primary on canvas", c.Primary, c.BgPrimary, 4.5},
		{"secondary on canvas", c.Secondary, c.BgPrimary, 4.5},
		{"success on canvas", c.Success, c.BgPrimary, 4.5},
		{"error on canvas", c.Error, c.BgPrimary, 4.5},
		{"info on canvas", c.Info, c.BgPrimary, 4.5},
		{"success on bar", c.Success, c.BgSecondary, 4.5},
		{"error on bar", c.Error, c.BgSecondary, 4.4},
		{"dangerLight on dangerDark", c.DangerLight, c.DangerDark, 4.5},
		{"textInverse on dangerBright", c.TextInverse, c.DangerBright, 4.5},
		{"link on selection", c.Link, c.BgTertiary, 4.5},
	}
	for _, tc := range cases {
		if ratio := ContrastRatio(tc.fg, tc.bg); ratio < tc.min-0.01 {
			t.Errorf("%s: %.2f < %.2f (%s on %s)", tc.name, ratio, tc.min, tc.fg, tc.bg)
		}
	}

	// The design's most recessive tone is unreadable as text on this canvas;
	// it must never be adopted as a text role by a later edit.
	const tooRecessive = "#3c4247"
	for _, role := range []string{c.TextPrimary, c.TextSecondary, c.TextMuted, c.TextSubtle, c.TabTextInactive, c.TextHighlight} {
		if role == tooRecessive {
			t.Errorf("%s is below the readable floor on %s; keep it to non-text structure", tooRecessive, c.BgPrimary)
		}
	}
}
