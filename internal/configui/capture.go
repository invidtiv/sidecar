package configui

import "fmt"

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
