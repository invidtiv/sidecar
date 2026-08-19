package msg

import (
	tea "charm.land/bubbletea/v2"
)

// A status flash is the lightweight tier beside a notification: one line in the
// top-right of the content region, a coloured source glyph then the text,
// fading in and out. It is never stored — no centre entry, no history, no
// unread count — so it is the right home for routine confirmations ("Saved", a
// yank, a sidebar toggle) that deserve feedback but not persistence.
//
// Anything a user might want to find again later is a notification: use
// ShowToast for that. If in doubt, ask whether it would still be worth reading
// in five minutes.

// FlashMsg shows a status flash. A new flash immediately replaces the one on
// screen: flashes never queue.
type FlashMsg struct {
	// Text is the single line shown. It is truncated, never wrapped.
	Text string
	// Source names the notification source whose glyph and hue mark the line
	// (see internal/notify). Empty means the `system` source.
	Source string
	// IsError renders the line in the error hue, whatever the source is — the
	// same rule a toast applies to an error severity.
	IsError bool
}

// ShowFlash returns a command showing a status flash from the system source.
func ShowFlash(text string) tea.Cmd {
	return func() tea.Msg { return FlashMsg{Text: text} }
}

// ShowFlashFrom returns a command showing a status flash marked with a
// particular notification source's glyph and hue.
func ShowFlashFrom(source, text string) tea.Cmd {
	return func() tea.Msg { return FlashMsg{Text: text, Source: source} }
}
