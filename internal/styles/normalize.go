package styles

import (
	"fmt"
	"math"
)

// Contrast targets for the roles NormalizePalette guarantees. Small UI text is
// held to WCAG AA (4.5); roles that only ever carry de-emphasised or large text
// sit lower deliberately, so they stay visually secondary while remaining legible.
const (
	targetBodyText     = 4.5 // TextPrimary, TextMuted, key hints, selection, links
	targetSecondary    = 4.0 // TextSecondary
	targetDeemphasised = 3.0 // TextSubtle
	targetLargeText    = 3.5 // TabTextInactive
)

// surfaceSeparation is the minimum contrast a derived raised surface must have
// from both backgrounds it can be drawn over, so the pill reads as a distinct
// shape rather than dissolving into the bar.
const surfaceSeparation = 1.12

// NormalizePalette enforces the contrast guarantees the UI relies on and fills
// in the derived roles (SurfaceRaised, KeyHintFg, OnPrimary).
//
// It runs on every palette on its way into the style system — built-in themes,
// community conversions, and user overrides alike — because contrast is a
// property of the finished palette, not of any one source of colours. Colours
// that already clear their target are returned untouched, so a well-authored
// theme passes through unchanged.
func NormalizePalette(c ColorPalette) ColorPalette {
	if c.BgPrimary == "" {
		return c
	}
	bgSecondary := c.BgSecondary
	if bgSecondary == "" {
		bgSecondary = c.BgPrimary
	}

	// Chrome is every surface ordinary text can land on. Text roles are held
	// against the whole set, not just BgPrimary: the footer and header paint
	// BgSecondary, and enforcing against BgPrimary alone was why footer text
	// went unreadable even though the palette "passed".
	chrome := []string{c.BgPrimary, bgSecondary}

	// The raised surface is derived from BgPrimary rather than reusing
	// BgTertiary. BgTertiary carries the terminal scheme's selection colour,
	// which is often a saturated accent (or, in a few schemes, the text colour
	// itself) — fine behind a selection, unusable as chrome for key hints.
	surface := c.SurfaceRaised
	if surface == "" || !surfaceIsUsable(surface, c.BgPrimary, bgSecondary) {
		surface = deriveSurfaceRaised(c.BgPrimary, bgSecondary)
	}
	c.SurfaceRaised = surface

	textSurfaces := []string{c.BgPrimary, bgSecondary, surface}

	c.TextPrimary = EnsureContrastOn(c.TextPrimary, textSurfaces, targetBodyText)
	c.TextSecondary = EnsureContrastOn(c.TextSecondary, textSurfaces, targetSecondary)
	c.TextMuted = EnsureContrastOn(c.TextMuted, textSurfaces, targetBodyText)
	c.TextSubtle = EnsureContrastOn(c.TextSubtle, chrome, targetDeemphasised)
	c.TabTextInactive = EnsureContrastOn(c.TabTextInactive, chrome, targetLargeText)
	c.TextHighlight = EnsureContrastOn(c.TextHighlight, chrome, targetBodyText)
	c.Link = EnsureContrastOn(c.Link, chrome, targetBodyText)

	if c.SelectionBg == "" {
		c.SelectionBg = c.BgTertiary
	}

	if c.BgTertiary != "" {
		selection := c.TextSelection
		if selection == "" {
			selection = c.TextPrimary
		}
		c.TextSelection = EnsureContrastOn(selection, []string{c.BgTertiary}, targetBodyText)
	}

	if c.DiffAddBg != "" {
		c.DiffAddFg = EnsureContrastOn(c.DiffAddFg, []string{c.DiffAddBg}, targetBodyText)
	}
	if c.DiffRemoveBg != "" {
		c.DiffRemoveFg = EnsureContrastOn(c.DiffRemoveFg, []string{c.DiffRemoveBg}, targetBodyText)
	}

	// Toast text picked by measured contrast rather than a luminance threshold.
	if c.Success != "" {
		c.ToastSuccessText = onColor(c.ToastSuccessText, c.Success)
	}
	if c.Error != "" {
		c.ToastErrorText = onColor(c.ToastErrorText, c.Error)
	}

	c.KeyHintFg = EnsureContrastOn(fallback(c.KeyHintFg, c.TextPrimary), []string{surface}, targetBodyText)
	if c.Primary != "" {
		c.OnPrimary = onColor(c.OnPrimary, c.Primary)
	}
	if c.Warning != "" {
		c.OnWarning = onColor(c.OnWarning, c.Warning)
	}

	c = normalizeOverviewColors(c, chrome, surface)

	return c
}

// normalizeOverviewColors derives the Agent Overview board's project hues,
// agent chip colours, and lane colours when a theme leaves them empty, then
// runs every one of them through the same contrast guarantee as the text
// roles above.
func normalizeOverviewColors(c ColorPalette, chrome []string, surface string) ColorPalette {
	if len(c.ProjectHues) == 0 {
		// A tab ramp is only worth borrowing if it can actually tell projects
		// apart. Nord ships a single tab colour, which would hash every project
		// to the same spine and defeat the encoding entirely.
		if len(c.TabColors) >= minProjectHues {
			c.ProjectHues = append([]string(nil), c.TabColors...)
		} else {
			c.ProjectHues = append([]string(nil), defaultProjectHues...)
		}
	}
	for i, hue := range c.ProjectHues {
		c.ProjectHues[i] = EnsureContrastOn(hue, chrome, targetBodyText)
	}

	agentColors := make(map[string]string, len(defaultAgentColors))
	for provider, hex := range defaultAgentColors {
		agentColors[provider] = hex
	}
	for provider, hex := range c.AgentColors {
		agentColors[provider] = hex
	}
	for provider, hex := range agentColors {
		agentColors[provider] = EnsureContrastOn(hex, []string{surface}, targetBodyText)
	}
	c.AgentColors = agentColors

	if c.LaneWorking == "" {
		c.LaneWorking = c.Success
	}
	if c.LaneBlocked == "" {
		c.LaneBlocked = c.Warning
	}
	if c.LaneDone == "" {
		c.LaneDone = c.Info
	}
	if c.LaneIdle == "" {
		c.LaneIdle = c.TextSecondary
	}
	if c.LanePaused == "" {
		c.LanePaused = c.TextMuted
	}
	c.LaneWorking = EnsureContrastOn(c.LaneWorking, chrome, targetBodyText)
	c.LaneBlocked = EnsureContrastOn(c.LaneBlocked, chrome, targetBodyText)
	c.LaneDone = EnsureContrastOn(c.LaneDone, chrome, targetBodyText)
	c.LaneIdle = EnsureContrastOn(c.LaneIdle, chrome, targetBodyText)
	c.LanePaused = EnsureContrastOn(c.LanePaused, chrome, targetBodyText)

	return c
}

// onColor returns a foreground guaranteed legible on bg, keeping the supplied
// colour when it already clears AA and falling back to the better pole when not.
func onColor(fg, bg string) string {
	if fg != "" && ContrastRatio(fg, bg) >= targetBodyText {
		return fg
	}
	return EnsureContrastOn(MaxContrastPole([]string{bg}), []string{bg}, targetBodyText)
}

// DeriveSurfaceRaised returns the neutral raised-chrome surface for a pair of
// backgrounds. Exported for theme converters that want to build the role
// themselves rather than leave it to NormalizePalette.
func DeriveSurfaceRaised(bgPrimary, bgSecondary string) string {
	return deriveSurfaceRaised(bgPrimary, bgSecondary)
}

// deriveSurfaceRaised steps a neutral surface away from the base background
// until it separates from both bars it can sit on while still leaving room for
// text.
//
// Stepping HSL lightness is tried first because it keeps the background's
// character. On a strongly saturated background that is not enough: lightness
// steps on, say, a bright yellow move the colour toward pure yellow without
// moving its luminance much, so the surface never separates. The fallback
// blends toward whichever pole preserves text headroom, which is monotone in
// luminance and always gets there.
func deriveSurfaceRaised(bgPrimary, bgSecondary string) string {
	pole := MaxContrastPole([]string{bgPrimary})

	for _, amount := range []float64{0.10, 0.14, 0.18, 0.24, 0.30} {
		candidate := AdjustSurface(bgPrimary, amount)
		if surfaceIsUsable(candidate, bgPrimary, bgSecondary) &&
			ContrastRatio(pole, candidate) >= surfaceTextHeadroom {
			return candidate
		}
	}

	best, bestSeparation := "", 0.0
	for _, target := range []string{"#ffffff", "#000000"} {
		for t := 0.04; t <= 0.6; t += 0.04 {
			candidate := Blend(bgPrimary, target, t)
			if ContrastRatio(pole, candidate) < surfaceTextHeadroom {
				break
			}
			separation := math.Min(
				ContrastRatio(candidate, bgPrimary),
				ContrastRatio(candidate, bgSecondary))
			if separation >= surfaceSeparation {
				return candidate
			}
			if separation > bestSeparation {
				best, bestSeparation = candidate, separation
			}
		}
	}
	if best != "" {
		return best
	}
	return AdjustSurface(bgPrimary, 0.30)
}

// surfaceTextHeadroom is the contrast a derived surface must still afford the
// base background's text pole. Below it, no single foreground can serve that
// surface and the rest of the chrome at once.
const surfaceTextHeadroom = 4.6

// cardSelectedBgHex returns a selection fill darker than the board so multi-
// coloured kanban/overview card text stays readable. ListItemSelected uses
// BgTertiary (a lighter lift), which washes out project hues, agent chips, and
// lane colours.
//
// When BgPrimary is already near black and cannot darken further, fall back to
// a darkened BgSecondary so selection still separates from the board.
func cardSelectedBgHex(bgPrimary, bgSecondary string) string {
	darker := Blend(bgPrimary, "#000000", 0.45)
	if ColorDistance(darker, bgPrimary) >= 10 {
		return darker
	}
	if bgSecondary != "" {
		return Darken(bgSecondary, 0.2)
	}
	return darker
}

// AdjustSurface derives a chrome surface `amount` away from a background.
//
// The conventional move is to lighten dark backgrounds and darken light ones.
// On a mid-luminance background that move is actively harmful: it walks the
// surface across the point where white and black text swap places, so the bar
// and the surface on top of it want opposite foregrounds and any colour shared
// between them has to compromise. The test is therefore made against the *base
// background's* pole, not the surface's own — when the conventional direction
// erodes that pole, the direction is flipped, which separates the surface just
// as well while keeping every chrome tone on one side of the crossover.
func AdjustSurface(bg string, amount float64) string {
	pole := MaxContrastPole([]string{bg})
	isDark := IsDarkBackground(bg)

	conventional := adjustLightness(bg, amount, isDark)
	if ContrastRatio(pole, conventional) >= surfaceTextHeadroom {
		return conventional
	}
	flipped := adjustLightness(bg, amount, !isDark)
	if ContrastRatio(pole, flipped) > ContrastRatio(pole, conventional) {
		return flipped
	}
	return conventional
}

func surfaceIsUsable(surface, bgPrimary, bgSecondary string) bool {
	return ContrastRatio(surface, bgPrimary) >= surfaceSeparation &&
		ContrastRatio(surface, bgSecondary) >= surfaceSeparation
}

// adjustLightness lightens dark backgrounds and darkens light ones.
func adjustLightness(hex string, amount float64, isDark bool) string {
	if isDark {
		return Lighten(hex, amount)
	}
	return Darken(hex, amount)
}

func fallback(value, alt string) string {
	if value != "" {
		return value
	}
	return alt
}

// contrastRequirement is one foreground/background pairing the UI actually
// renders, and the ratio it has to clear.
type contrastRequirement struct {
	name   string
	target float64
	fg     func(ColorPalette) string
	bg     func(ColorPalette) string
}

// paletteRequirements mirrors the style definitions in styles.go. Adding a
// style that pairs a foreground with a background belongs here too.
var paletteRequirements = []contrastRequirement{
	{"keyHintFg on surfaceRaised", targetBodyText,
		func(p ColorPalette) string { return p.KeyHintFg },
		func(p ColorPalette) string { return p.SurfaceRaised }},
	{"textMuted on bgPrimary", targetBodyText,
		func(p ColorPalette) string { return p.TextMuted },
		func(p ColorPalette) string { return p.BgPrimary }},
	{"textMuted on bgSecondary", targetBodyText,
		func(p ColorPalette) string { return p.TextMuted },
		func(p ColorPalette) string { return p.BgSecondary }},
	{"textMuted on surfaceRaised", targetBodyText,
		func(p ColorPalette) string { return p.TextMuted },
		func(p ColorPalette) string { return p.SurfaceRaised }},
	{"textPrimary on bgPrimary", targetBodyText,
		func(p ColorPalette) string { return p.TextPrimary },
		func(p ColorPalette) string { return p.BgPrimary }},
	{"textPrimary on bgSecondary", targetBodyText,
		func(p ColorPalette) string { return p.TextPrimary },
		func(p ColorPalette) string { return p.BgSecondary }},
	{"textSecondary on bgSecondary", targetSecondary,
		func(p ColorPalette) string { return p.TextSecondary },
		func(p ColorPalette) string { return p.BgSecondary }},
	{"textSubtle on bgSecondary", targetDeemphasised,
		func(p ColorPalette) string { return p.TextSubtle },
		func(p ColorPalette) string { return p.BgSecondary }},
	{"tabTextInactive on bgSecondary", targetLargeText,
		func(p ColorPalette) string { return p.TabTextInactive },
		func(p ColorPalette) string { return p.BgSecondary }},
	{"textHighlight on bgPrimary", targetBodyText,
		func(p ColorPalette) string { return p.TextHighlight },
		func(p ColorPalette) string { return p.BgPrimary }},
	{"link on bgPrimary", targetBodyText,
		func(p ColorPalette) string { return p.Link },
		func(p ColorPalette) string { return p.BgPrimary }},
	{"textSelection on bgTertiary", targetBodyText,
		func(p ColorPalette) string { return p.TextSelection },
		func(p ColorPalette) string { return p.BgTertiary }},
	{"onPrimary on primary", targetBodyText,
		func(p ColorPalette) string { return p.OnPrimary },
		func(p ColorPalette) string { return p.Primary }},
	{"onWarning on warning", targetBodyText,
		func(p ColorPalette) string { return p.OnWarning },
		func(p ColorPalette) string { return p.Warning }},
	{"diffAddFg on diffAddBg", targetBodyText,
		func(p ColorPalette) string { return p.DiffAddFg },
		func(p ColorPalette) string { return p.DiffAddBg }},
	{"diffRemoveFg on diffRemoveBg", targetBodyText,
		func(p ColorPalette) string { return p.DiffRemoveFg },
		func(p ColorPalette) string { return p.DiffRemoveBg }},
	{"toastSuccessText on success", targetBodyText,
		func(p ColorPalette) string { return p.ToastSuccessText },
		func(p ColorPalette) string { return p.Success }},
	{"toastErrorText on error", targetBodyText,
		func(p ColorPalette) string { return p.ToastErrorText },
		func(p ColorPalette) string { return p.Error }},
	{"surfaceRaised against bgSecondary", surfaceSeparation,
		func(p ColorPalette) string { return p.SurfaceRaised },
		func(p ColorPalette) string { return p.BgSecondary }},
	{"surfaceRaised against bgPrimary", surfaceSeparation,
		func(p ColorPalette) string { return p.SurfaceRaised },
		func(p ColorPalette) string { return p.BgPrimary }},
}

// CheckPaletteContrast reports every requirement a normalized palette misses.
// Exported for the community converter's own sweep over all schemes.
func CheckPaletteContrast(p ColorPalette) []string {
	var failures []string
	for _, req := range paletteRequirements {
		fg, bg := req.fg(p), req.bg(p)
		if fg == "" || bg == "" {
			continue
		}
		// Tolerance absorbs the quantisation of the lightness search onto
		// 8-bit channels; a hit within 0.01 of target is not a readability bug.
		if ratio := ContrastRatio(fg, bg); ratio < req.target-0.01 {
			failures = append(failures, fmt.Sprintf("%s: %.2f < %.2f (fg=%s, bg=%s)", req.name, ratio, req.target, fg, bg))
		}
	}
	return failures
}
