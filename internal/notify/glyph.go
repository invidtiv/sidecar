package notify

import (
	"image/color"

	"charm.land/lipgloss/v2"
)

// One symbol, one place. The toast, the status flash, and the centre all mark a
// notification with the same glyph in the same colour, so a source looks the
// same wherever it is shown. Anything that draws a source's mark calls
// RenderGlyph; anything that needs the bare colour (a border, a rule, an unread
// dot) calls ChromeColor.

// ChromeColor is the colour a notification's chrome takes: its source's hue,
// overridden by error severity. An error is loud whatever posted it — that
// override lived in the toast renderer alone, which is how an error could look
// like an error in one surface and not in another.
func ChromeColor(source SourceID, severity Severity) color.Color {
	if severity == SeverityError {
		return ResolveHue(HueError)
	}
	return ResolveHue(SourceOf(source).Hue)
}

// Glyph is the bare glyph for a source, with no styling.
func Glyph(source SourceID) string { return SourceOf(source).Glyph }

// RenderGlyph is the source's glyph in its chrome colour.
func RenderGlyph(source SourceID, severity Severity) string {
	return lipgloss.NewStyle().Foreground(ChromeColor(source, severity)).Render(Glyph(source))
}
