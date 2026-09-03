package tdmonitor

import (
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/td/pkg/monitor"
)

// cleanColor normalizes a color string by stripping any 8-character hex alpha
// suffix (e.g. #000000cc -> #000000) that is accepted by Sidecar's ColorPalette
// but invalid in standard Lip Gloss.
func cleanColor(c string) string {
	if len(c) == 9 && c[0] == '#' {
		return c[:7]
	}
	return c
}

// buildTheme projects sidecar's current normalized palette onto td's
// semantic monitor theme contract.
//
// It reads styles.GetCurrentTheme().Colors, which has already passed through
// Sidecar resolution, community conversion, overrides, and normalization, and
// maps semantic slots explicitly. It does not pass a Sidecar theme name or
// rely on td defaults for mapped fields.
func buildTheme() monitor.Theme {
	c := styles.GetCurrentTheme().Colors

	onError := c.TextInverse
	if onError == "" {
		onError = c.ToastErrorText
	}

	return monitor.Theme{
		Primary:       cleanColor(c.Primary),
		Secondary:     cleanColor(c.Secondary),
		Accent:        cleanColor(c.Accent),
		Success:       cleanColor(c.Success),
		Warning:       cleanColor(c.Warning),
		Error:         cleanColor(c.Error),
		Info:          cleanColor(c.Info),
		ReadyToClose:  cleanColor(c.Success),
		PendingReview: cleanColor(c.Secondary),
		PendingOther:  cleanColor(c.Accent),
		TextPrimary:   cleanColor(c.TextPrimary),
		TextSecondary: cleanColor(c.TextSecondary),
		TextMuted:     cleanColor(c.TextMuted),
		TextSubtle:    cleanColor(c.TextSubtle),
		TextSelection: cleanColor(c.TextSelection),
		OnPrimary:     cleanColor(c.OnPrimary),
		OnWarning:     cleanColor(c.OnWarning),
		OnError:       cleanColor(onError),
		Background:    cleanColor(c.BgPrimary),
		Surface:       cleanColor(c.BgSecondary),
		SurfaceRaised: cleanColor(c.SurfaceRaised),
		Selection:     cleanColor(c.BgTertiary),
		Backdrop:      cleanColor(c.BgOverlay),
		Border:        cleanColor(c.BorderNormal),
		BorderMuted:   cleanColor(c.BorderMuted),
		BorderActive:  cleanColor(c.BorderActive),
		Link:          cleanColor(c.Link),
		SyntaxTheme:   c.SyntaxTheme,
		MarkdownTheme: c.MarkdownTheme,
	}
}
