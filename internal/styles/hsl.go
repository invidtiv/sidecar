package styles

import "math"

const achromaticEpsilon = 1e-6

// HexToHSL converts a hex colour (#RRGGBB) to HSL (h: 0-360, s: 0-1, l: 0-1).
func HexToHSL(hex string) (h, s, l float64) {
	rgb := HexToRGB(hex)
	r := rgb.R / 255.0
	g := rgb.G / 255.0
	b := rgb.B / 255.0

	max := math.Max(r, math.Max(g, b))
	min := math.Min(r, math.Min(g, b))
	l = (max + min) / 2.0

	if math.Abs(max-min) < achromaticEpsilon {
		return 0, 0, l
	}

	d := max - min
	if l > 0.5 {
		s = d / (2.0 - max - min)
	} else {
		s = d / (max + min)
	}

	switch max {
	case r:
		h = (g - b) / d
		if g < b {
			h += 6
		}
	case g:
		h = (b-r)/d + 2
	case b:
		h = (r-g)/d + 4
	}
	h *= 60

	return h, s, l
}

// HSLToHex converts HSL (h: 0-360, s: 0-1, l: 0-1) to a hex colour string.
func HSLToHex(h, s, l float64) string {
	if s == 0 {
		v := clampChannel(l * 255)
		return RGBToHex(RGB{R: v, G: v, B: v})
	}

	var q float64
	if l < 0.5 {
		q = l * (1 + s)
	} else {
		q = l + s - l*s
	}
	p := 2*l - q

	hNorm := h / 360.0
	r := hueToRGB(p, q, hNorm+1.0/3.0)
	g := hueToRGB(p, q, hNorm)
	b := hueToRGB(p, q, hNorm-1.0/3.0)

	return RGBToHex(RGB{
		R: clampChannel(r * 255),
		G: clampChannel(g * 255),
		B: clampChannel(b * 255),
	})
}

func hueToRGB(p, q, t float64) float64 {
	if t < 0 {
		t += 1
	}
	if t > 1 {
		t -= 1
	}
	switch {
	case t < 1.0/6.0:
		return p + (q-p)*6*t
	case t < 1.0/2.0:
		return q
	case t < 2.0/3.0:
		return p + (q-p)*(2.0/3.0-t)*6
	default:
		return p
	}
}

// Lighten increases HSL lightness by pct (0-1).
func Lighten(hex string, pct float64) string {
	h, s, l := HexToHSL(hex)
	return HSLToHex(h, s, math.Min(1.0, l+pct))
}

// Darken decreases HSL lightness by pct (0-1).
func Darken(hex string, pct float64) string {
	h, s, l := HexToHSL(hex)
	return HSLToHex(h, s, math.Max(0.0, l-pct))
}

// Saturation returns the HSL saturation (0-1) of a hex colour.
func Saturation(hex string) float64 {
	_, s, _ := HexToHSL(hex)
	return s
}

// HueDegrees returns hue in degrees (0-360).
func HueDegrees(hex string) float64 {
	h, _, _ := HexToHSL(hex)
	return h
}

// ColorDistance returns euclidean distance in RGB space (0-441.67).
func ColorDistance(a, b string) float64 {
	c1 := HexToRGB(a)
	c2 := HexToRGB(b)
	dr := c1.R - c2.R
	dg := c1.G - c2.G
	db := c1.B - c2.B
	return math.Sqrt(dr*dr + dg*dg + db*db)
}

func clampChannel(v float64) float64 {
	return math.Max(0, math.Min(255, v))
}
