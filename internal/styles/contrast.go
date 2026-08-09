package styles

import "math"

// darkBackgroundLuminance is the relative luminance at which white and black
// text have equal WCAG contrast against a background. Backgrounds below it are
// "dark" for the purposes of deciding which way to move a colour. The obvious
// 0.5 midpoint is wrong: it sits far into the light half of the range and
// misclassifies mid-tone backgrounds.
const darkBackgroundLuminance = 0.179

func contrastRatio(fg, bg RGB) float64 {
	l1 := relativeLuminance(fg)
	l2 := relativeLuminance(bg)
	if l1 < l2 {
		l1, l2 = l2, l1
	}
	return (l1 + 0.05) / (l2 + 0.05)
}

func minContrastRatio(fg RGB, bgs []RGB) float64 {
	if len(bgs) == 0 {
		return contrastRatio(fg, RGB{0, 0, 0})
	}
	minRatio := math.MaxFloat64
	for _, bg := range bgs {
		if ratio := contrastRatio(fg, bg); ratio < minRatio {
			minRatio = ratio
		}
	}
	return minRatio
}

func relativeLuminance(c RGB) float64 {
	r := linearize(c.R / 255.0)
	g := linearize(c.G / 255.0)
	b := linearize(c.B / 255.0)
	return 0.2126*r + 0.7152*g + 0.0722*b
}

func linearize(v float64) float64 {
	if v <= 0.03928 {
		return v / 12.92
	}
	return math.Pow((v+0.055)/1.055, 2.4)
}

// Luminance returns the relative luminance (0-1) of a hex colour.
func Luminance(hex string) float64 {
	return relativeLuminance(HexToRGB(hex))
}

// ContrastRatio returns the WCAG 2.0 contrast ratio (1 to 21) between two hex colours.
func ContrastRatio(fg, bg string) float64 {
	return contrastRatio(HexToRGB(fg), HexToRGB(bg))
}

// MinContrastRatio returns the worst contrast ratio of fg across every bg.
// With no backgrounds it measures against black.
func MinContrastRatio(fg string, bgs []string) float64 {
	if len(bgs) == 0 {
		return ContrastRatio(fg, "#000000")
	}
	minRatio := math.MaxFloat64
	for _, bg := range bgs {
		if ratio := ContrastRatio(fg, bg); ratio < minRatio {
			minRatio = ratio
		}
	}
	return minRatio
}

// IsDarkBackground reports whether a background is dark enough that foregrounds
// should be lightened rather than darkened.
func IsDarkBackground(hex string) bool {
	return Luminance(hex) < darkBackgroundLuminance
}

// MaxContrastPole returns whichever of white/black has the better worst-case
// contrast across bgs. Use this instead of thresholding luminance directly.
func MaxContrastPole(bgs []string) string {
	if MinContrastRatio("#ffffff", bgs) >= MinContrastRatio("#000000", bgs) {
		return "#ffffff"
	}
	return "#000000"
}

// Blend mixes two hex colours: result = (1-t)*c1 + t*c2. t is clamped to [0,1].
func Blend(c1, c2 string, t float64) string {
	t = math.Max(0, math.Min(1, t))
	rgb1 := HexToRGB(c1)
	rgb2 := HexToRGB(c2)
	return RGBToHex(RGB{
		R: rgb1.R*(1-t) + rgb2.R*t,
		G: rgb1.G*(1-t) + rgb2.G*t,
		B: rgb1.B*(1-t) + rgb2.B*t,
	})
}

// lightnessStep is the granularity of the EnsureContrastOn search. Small enough
// that the accepted colour is visually the nearest one that clears the target.
const lightnessStep = 0.005

// EnsureContrastOn returns a colour that meets target contrast against every
// background in bgs, staying as close to fg as it can.
//
// It first searches HSL lightness in both directions with hue and saturation
// held, so an adjusted colour keeps the theme's character. Only when no
// lightness clears the target — which happens when bgs straddle fg, or the
// hue simply cannot get bright/dark enough — does it fall back to blending
// toward the higher-contrast pole, which desaturates. If nothing reaches the
// target, the best attempt found is returned rather than the original.
func EnsureContrastOn(fg string, bgs []string, target float64) string {
	if len(bgs) == 0 || fg == "" {
		return fg
	}
	if MinContrastRatio(fg, bgs) >= target {
		return fg
	}

	h, s, l := HexToHSL(fg)
	best, bestRatio := fg, MinContrastRatio(fg, bgs)

	for step := lightnessStep; step <= 1.0; step += lightnessStep {
		for _, candidate := range []string{
			HSLToHex(h, s, clampUnit(l+step)),
			HSLToHex(h, s, clampUnit(l-step)),
		} {
			ratio := MinContrastRatio(candidate, bgs)
			if ratio >= target {
				return candidate
			}
			if ratio > bestRatio {
				best, bestRatio = candidate, ratio
			}
		}
	}

	pole := MaxContrastPole(bgs)
	lo, hi := 0.0, 1.0
	for i := 0; i < 20; i++ {
		mid := (lo + hi) / 2
		if MinContrastRatio(Blend(fg, pole, mid), bgs) >= target {
			hi = mid
		} else {
			lo = mid
		}
	}
	if blended := Blend(fg, pole, hi); MinContrastRatio(blended, bgs) > bestRatio {
		return blended
	}
	return best
}

// EnsureContrast is EnsureContrastOn against a single background.
func EnsureContrast(fg, bg string, target float64) string {
	return EnsureContrastOn(fg, []string{bg}, target)
}

func clampUnit(v float64) float64 {
	return math.Max(0, math.Min(1, v))
}
