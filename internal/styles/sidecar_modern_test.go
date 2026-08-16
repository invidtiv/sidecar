package styles

import (
	"fmt"
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

	// The design's most recessive tone is unreadable as text on this canvas;
	// it must never be adopted as a text role by a later edit.
	const tooRecessive = "#3c4247"
	for _, role := range []string{c.TextPrimary, c.TextSecondary, c.TextMuted, c.TextSubtle, c.TabTextInactive, c.TextHighlight} {
		if role == tooRecessive {
			t.Errorf("%s is below the readable floor on %s; keep it to non-text structure", tooRecessive, c.BgPrimary)
		}
	}
}

// The test above walks the canvas and the bars. This one walks the two fills
// that were missed first time round and that a muted palette fails on soonest:
// the selection row (BgTertiary), where every semantic accent can be drawn as
// text, and SurfaceRaised, where the de-emphasised end of the text ramp lands
// inside key-hint pills and bar chips.
//
// Both were found by review rather than by a test, which is the reason this
// exists: a colour is only "checked" against the backgrounds it is enumerated
// against.
func TestSidecarModernReadableOnSelectionAndRaisedChrome(t *testing.T) {
	c := NormalizePalette(SidecarModernTheme.Colors)

	// Semantic accents render as text on the selected row exactly as they do
	// on the canvas, so they are held to the same AA floor there.
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
	// A project name or a lane label can sit on any of the three fills, so the
	// worst case across all of them is the number that matters.
	fills := []string{c.BgPrimary, c.BgSecondary, c.BgTertiary}
	for _, a := range accents {
		if ratio := MinContrastRatio(a.hex, fills); ratio < 4.5-0.01 {
			t.Errorf("%s (%s): %.2f < 4.50 on the worst of canvas/bar/selection", a.name, a.hex, ratio)
		}
	}

	// Text drawn on raised chrome. The de-emphasised roles are held to the 3.0
	// floor NormalizePalette states for them; the roles that carry real content
	// are held to AA.
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
