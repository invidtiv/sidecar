package configui

import (
	"fmt"
	"strconv"
	"strings"
)

// The embedded terminal's capture limit is surfaced twice — plainly on Terminal
// and among the technical controls on Advanced — so its bounds, its clamp, and
// the way it is written for a human live here rather than in either page.
//
// The bounds are deliberate: below the minimum a preview loses the scrollback
// that makes it worth showing, and above the maximum a single capture costs
// more memory than any preview is worth. config.Validate already refuses a
// non-positive value; this narrows an accepted value to a range Sidecar is
// willing to stand behind, for typed and selected input alike.
const (
	// CaptureLimitDefault is the safe default, matching config.Default().
	CaptureLimitDefault = 2 * 1024 * 1024
	// CaptureLimitMin is the smallest capture Sidecar accepts.
	CaptureLimitMin = 256 * 1024
	// CaptureLimitMax is the largest capture Sidecar accepts.
	CaptureLimitMax = 64 * 1024 * 1024
)

// CaptureLimitChoices are the values the selector steps through, smallest
// first. Every one of them is inside the accepted range.
var CaptureLimitChoices = []int{
	256 * 1024,
	512 * 1024,
	1024 * 1024,
	2 * 1024 * 1024,
	4 * 1024 * 1024,
	8 * 1024 * 1024,
	16 * 1024 * 1024,
	32 * 1024 * 1024,
	64 * 1024 * 1024,
}

// ClampCaptureLimit narrows a value to the accepted range. A missing or
// nonsensical value becomes the safe default rather than the nearest bound:
// zero means "unset", not "as small as possible".
func ClampCaptureLimit(bytes int) int {
	if bytes <= 0 {
		return CaptureLimitDefault
	}
	if bytes < CaptureLimitMin {
		return CaptureLimitMin
	}
	if bytes > CaptureLimitMax {
		return CaptureLimitMax
	}
	return bytes
}

// FormatCaptureLimit writes a capture limit the way a person reads it.
func FormatCaptureLimit(bytes int) string {
	bytes = ClampCaptureLimit(bytes)
	const mb = 1024 * 1024
	if bytes%mb == 0 {
		return fmt.Sprintf("%d MB", bytes/mb)
	}
	return fmt.Sprintf("%d KB", bytes/1024)
}

// CaptureLimitRange writes the accepted range the way the help line states it,
// so the documented bounds and the enforced ones are the same two numbers.
func CaptureLimitRange() string {
	return FormatCaptureLimit(CaptureLimitMin) + " to " + FormatCaptureLimit(CaptureLimitMax)
}

// ParseCaptureLimit reads a typed capture limit — "4 MB", "512kb", or a plain
// byte count — and returns a value inside the accepted range. Blank or
// unreadable input keeps the safe default rather than being refused: this
// setting has no meaningful "unset", and a technical control should not trap a
// user in an error state over a typo.
func ParseCaptureLimit(value string) int {
	text := strings.ToLower(strings.TrimSpace(value))
	if text == "" {
		return CaptureLimitDefault
	}
	multiplier := 1
	switch {
	case strings.HasSuffix(text, "mb"):
		multiplier, text = 1024*1024, strings.TrimSuffix(text, "mb")
	case strings.HasSuffix(text, "kb"):
		multiplier, text = 1024, strings.TrimSuffix(text, "kb")
	case strings.HasSuffix(text, "m"):
		multiplier, text = 1024*1024, strings.TrimSuffix(text, "m")
	case strings.HasSuffix(text, "k"):
		multiplier, text = 1024, strings.TrimSuffix(text, "k")
	case strings.HasSuffix(text, "b"):
		text = strings.TrimSuffix(text, "b")
	}
	number, err := strconv.ParseFloat(strings.TrimSpace(text), 64)
	if err != nil || number <= 0 {
		return CaptureLimitDefault
	}
	return ClampCaptureLimit(int(number * float64(multiplier)))
}

// NextCaptureLimit is the value one step up the ladder, wrapping at the top.
// Stepping is how both pages change the setting, so both walk the same rungs.
func NextCaptureLimit(current int) int {
	current = ClampCaptureLimit(current)
	for _, choice := range CaptureLimitChoices {
		if choice > current {
			return choice
		}
	}
	return CaptureLimitChoices[0]
}
