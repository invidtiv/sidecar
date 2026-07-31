package ui

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// PrintableKeyText returns the printable text carried by a key press.
// KeyPressMsg.String is intended for shortcut matching and returns names such
// as "space" for some printable keys, so text-entry code should use this helper
// instead.
func PrintableKeyText(msg tea.KeyPressMsg) string {
	text := msg.Text
	commandModifiers := tea.ModAlt | tea.ModCtrl | tea.ModMeta | tea.ModHyper | tea.ModSuper
	if text == "" && msg.Mod&commandModifiers == 0 && unicode.IsPrint(msg.Code) {
		// Keep synthetic key messages used by tests and integrations useful even
		// when they omit Text. Real terminal key presses populate Text.
		text = string(msg.Code)
	}
	if text == "" {
		return ""
	}
	for _, r := range text {
		if !unicode.IsPrint(r) {
			return ""
		}
	}
	return text
}
