package notify

import (
	"image/color"

	"github.com/marcus/sidecar/internal/styles"
)

// ResolveHue maps a source's hue name onto the active theme's palette. It is
// the only place in this package that knows a colour exists: the model, the
// store, and the resolution rules stay renderable by anything.
//
// The values are the existing theme keys, re-read on every call so a theme
// switch reaches notifications the same way it reaches every other surface.
func ResolveHue(h Hue) color.Color {
	switch h {
	case HuePrimary:
		return styles.Primary
	case HueSecondary:
		return styles.Secondary
	case HueAccent:
		return styles.Accent
	case HueSuccess:
		return styles.Success
	case HueWarning:
		return styles.Warning
	case HueError:
		return styles.Error
	case HueInfo:
		return styles.Info
	case HueMuted:
		return styles.TextMuted
	}
	return styles.TextMuted
}

// SourceColor is the colour of a notification's source chrome: its border, its
// glyph, and its section rule in the centre. It is ChromeColor without a
// severity — the shorthand for the places that mark a source rather than a
// particular notification.
func SourceColor(id SourceID) color.Color {
	return ChromeColor(id, SeverityInfo)
}
