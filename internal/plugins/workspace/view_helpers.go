package workspace

import (
	"time"

	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspacelist"
)

// dimText renders dim placeholder text using theme style.
func dimText(s string) string {
	return styles.Muted.Render(s)
}

// formatRelativeTime formats a time as relative (e.g., "now", "3m", "2h").
//
// It defers to the shared formatter rather than keeping a second copy that
// happened to agree above a minute and disagreed below it: this surface once
// said "now" for anything under a minute while the global list counted seconds,
// so the same workspace could read "now" here and "12s" there. Both now read
// "now" — see workspacelist.RelativeAge for why the seconds went away.
func formatRelativeTime(t time.Time) string {
	return workspacelist.RelativeAge(t, time.Now())
}
