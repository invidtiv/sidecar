package styles

import (
	"fmt"
	"reflect"
	"testing"
)

func TestCatppuccinMochaCoversEveryPaletteField(t *testing.T) {
	v := reflect.ValueOf(CatppuccinMochaTheme.Colors)
	for i := 0; i < v.NumField(); i++ {
		field := v.Type().Field(i)
		if v.Field(i).IsZero() {
			t.Errorf("ColorPalette.%s is unset — every role must be chosen deliberately", field.Name)
		}
	}
}

func TestCatppuccinMochaPassesNormalizationUnchanged(t *testing.T) {
	in := CatppuccinMochaTheme.Colors
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

func TestCatppuccinMochaReadableWhereNormalizationDoesNotReach(t *testing.T) {
	c := NormalizePalette(CatppuccinMochaTheme.Colors)
	cases := []struct {
		name   string
		fg, bg string
		min    float64
	}{
		{"textPrimary on selection", c.TextPrimary, c.BgTertiary, 4.5},
		{"textPrimary on text-selection highlight", c.TextPrimary, c.SelectionBg, 4.5},
		{"primary on text-selection highlight", c.Primary, c.SelectionBg, 3.0},
		{"textSecondary on selection", c.TextSecondary, c.BgTertiary, 4.5},
		{"textMuted on selection", c.TextMuted, c.BgTertiary, 4.5},
		{"textSelection on selection", c.TextSelection, c.BgTertiary, 4.5},
		{"primary on canvas", c.Primary, c.BgPrimary, 4.5},
		{"secondary on canvas", c.Secondary, c.BgPrimary, 4.5},
		{"success on canvas", c.Success, c.BgPrimary, 4.5},
		{"error on canvas", c.Error, c.BgPrimary, 4.5},
		{"info on canvas", c.Info, c.BgPrimary, 4.5},
		{"success on bar", c.Success, c.BgSecondary, 4.5},
		{"error on bar", c.Error, c.BgSecondary, 4.5},
		{"dangerLight on dangerDark", c.DangerLight, c.DangerDark, 4.5},
		{"textInverse on dangerBright", c.TextInverse, c.DangerBright, 4.5},
		{"link on selection", c.Link, c.BgTertiary, 4.5},
	}
	for _, tc := range cases {
		if ratio := ContrastRatio(tc.fg, tc.bg); ratio < tc.min-0.01 {
			t.Errorf("%s: %.2f < %.2f (%s on %s)", tc.name, ratio, tc.min, tc.fg, tc.bg)
		}
	}
}

func TestCatppuccinMochaReadableOnSelectionAndRaisedChrome(t *testing.T) {
	c := NormalizePalette(CatppuccinMochaTheme.Colors)

	accents := []struct{ name, hex string }{
		{"primary", c.Primary},
		{"secondary", c.Secondary},
		{"accent", c.Accent},
		{"success", c.Success},
		{"warning", c.Warning},
		{"error", c.Error},
		{"info", c.Info},
		{"link", c.Link},
		{"laneWorking", c.LaneWorking},
		{"laneBlocked", c.LaneBlocked},
		{"laneDone", c.LaneDone},
		{"laneIdle", c.LaneIdle},
		{"lanePaused", c.LanePaused},
	}
	for i, hue := range c.ProjectHues {
		accents = append(accents, struct{ name, hex string }{fmt.Sprintf("projectHues[%d]", i), hue})
	}
	fills := []string{c.BgPrimary, c.BgSecondary, c.BgTertiary}
	for _, a := range accents {
		if ratio := MinContrastRatio(a.hex, fills); ratio < 4.5-0.01 {
			t.Errorf("%s (%s): %.2f < 4.50 on the worst of canvas/bar/selection", a.name, a.hex, ratio)
		}
	}

	raised := []struct {
		name, hex string
		min       float64
	}{
		{"textPrimary", c.TextPrimary, 4.5},
		{"textSecondary", c.TextSecondary, 4.5},
		{"textMuted", c.TextMuted, 4.5},
		{"keyHintFg", c.KeyHintFg, 4.5},
		{"textSubtle", c.TextSubtle, 3.0},
		{"tabTextInactive", c.TabTextInactive, 3.0},
	}
	for _, r := range raised {
		if ratio := ContrastRatio(r.hex, c.SurfaceRaised); ratio < r.min-0.01 {
			t.Errorf("%s on SurfaceRaised (%s): %.2f < %.2f", r.name, r.hex, ratio, r.min)
		}
	}
}

// Text-selection highlight has to lift off the canvas enough to find, without
// walking so far toward mid-grey that body text would need to invert.
func TestCatppuccinMochaTextSelectionHighlightIsVisibleWithoutInvertingInk(t *testing.T) {
	c := NormalizePalette(CatppuccinMochaTheme.Colors)
	if c.SelectionBg == c.BgTertiary {
		t.Fatalf("SelectionBg reused BgTertiary (%s); the selected-row fill is too close to the canvas to mark a span", c.BgTertiary)
	}
	sep := ContrastRatio(c.SelectionBg, c.BgPrimary)
	if sep < TargetSelectionSeparation {
		if ContrastRatio(c.TextPrimary, c.SelectionBg) > targetBodyText+0.15 {
			t.Errorf("SelectionBg %s vs canvas %s is %.2f (want >= %.2f) with text headroom still left (%.2f)",
				c.SelectionBg, c.BgPrimary, sep, TargetSelectionSeparation, ContrastRatio(c.TextPrimary, c.SelectionBg))
		}
	}
	if Luminance(c.SelectionBg) <= Luminance(c.BgPrimary) {
		t.Errorf("SelectionBg %s is not lighter than the canvas %s", c.SelectionBg, c.BgPrimary)
	}
	if MaxContrastPole([]string{c.SelectionBg}) != MaxContrastPole([]string{c.BgPrimary}) {
		t.Errorf("SelectionBg %s wants the opposite ink pole from the canvas; that forces inverted selection text", c.SelectionBg)
	}
}
