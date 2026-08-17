package theme

import (
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/styles"
)

// DefaultThemeNotice is the one-time message shown to a user whose look changed
// underneath them when Sidecar's fresh-install default became Sidecar Modern.
//
// `#` is the theme switcher, so the message names the way back rather than a
// path through Configuration.
const DefaultThemeNotice = "New default theme: Sidecar Modern — press # to switch back"

// ShouldAnnounceDefaultChange reports whether the notice is owed.
//
// It is deliberately a pure function of three facts the caller gathers, so the
// policy can be read, tested, and reused without a running app:
//
//   - global: the recorded global theme configuration. Sidecar only writes
//     config.json when a setting changes, so an empty one means the user never
//     chose a theme and ResolveTheme is about to restyle them. A user with any
//     recorded choice — including the original "default" — is not being
//     restyled and must see nothing.
//   - hasPriorState: whether this installation has run before. A genuinely
//     fresh install has nothing to announce: there is no previous look to
//     contrast against, and "the default changed" is meaningless to someone
//     who has never seen the old one.
//   - seen: whether the notice has already been shown. It fires once, ever.
func ShouldAnnounceDefaultChange(global config.ThemeConfig, hasPriorState, seen bool) bool {
	if seen || !hasPriorState {
		return false
	}
	// Only the absence of a choice restyles, matching ResolveTheme's last step.
	// Overrides alone do not count as a choice of theme: they layer on top of
	// whatever base resolves, so a user with only overrides is being restyled
	// too.
	if global.Name != "" || global.Community != "" {
		return false
	}
	// Guard against the notice outliving the change it describes: if the
	// fresh-install default is ever moved back, there is nothing to announce.
	return styles.FreshInstallTheme != "default"
}
