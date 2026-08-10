package tasks

import (
	"github.com/marcus/sidecar/internal/styles"
	tasksui "github.com/marcus/tasks/pkg/tui"
)

// buildTheme projects sidecar's palette onto Tasks' semantic theme slots.
//
// The adapter is deliberately generic: it hands Tasks colour strings, never
// sidecar types or renderers. Name is left empty so Tasks keeps whichever base
// theme the user configured for the standalone TUI, and only the slots sidecar
// has a real opinion about are overridden. Tasks ignores unknown slots, so this
// map can lag behind Tasks' slot table without breaking the embed.
//
// ThemeOptions.Colors is an overlay: the slots named below win, and every slot
// sidecar does not name keeps whatever the user configured for their own Tasks.
// ReplaceColors is deliberately left unset — sidecar has an opinion about the
// handful of slots that must agree with its chrome, not about the user's whole
// palette.
func buildTheme() tasksui.ThemeOptions {
	c := styles.GetCurrentTheme().Colors

	colors := map[string]string{
		"accent":       c.Primary,
		"prompt":       "bold " + c.Primary,
		"link":         "underline " + c.Link,
		"link_system":  c.Link,
		"error":        c.Error,
		"form_error":   "bold " + c.Error,
		"warning":      c.Warning,
		"muted":        c.TextMuted,
		"description":  c.TextMuted,
		"note":         c.TextMuted,
		"form_hint":    c.TextMuted,
		"border":       c.BorderNormal,
		"context":      "bold " + c.Secondary,
		"project":      c.Accent,
		"state_next":   c.Info,
		"state_done":   c.TextMuted,
		"due_overdue":  c.Error,
		"due_soon":     c.Warning,
		"due_week":     c.Info,
		"due_far":      c.TextMuted,
		"form_focus":   "bold " + c.Primary,
		"form_unsaved": "bold " + c.Warning,
	}

	// A palette may leave any field empty; an empty spec would be rejected by
	// Tasks anyway, so drop it here rather than shipping noise across the seam.
	for slot, spec := range colors {
		if spec == "" || spec == "bold " || spec == "underline " {
			delete(colors, slot)
		}
	}

	return tasksui.ThemeOptions{Colors: colors}
}
