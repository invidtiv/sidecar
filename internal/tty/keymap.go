// Package tty provides an embeddable tmux terminal model for TUI plugins.
// It handles tmux session management, key mapping, cursor rendering, and adaptive polling.
package tty

import (
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// MapKeyToTmux translates a Bubble Tea key message to a tmux send-keys argument.
// Returns the tmux key name and whether to use literal mode (-l).
// For modified keys and special keys, returns the tmux key name.
// For literal characters, returns the character with useLiteral=true.
func MapKeyToTmux(msg tea.KeyPressMsg) (key string, useLiteral bool) {
	switch msg.String() {
	case "shift+up":
		return "\x1b[1;2A", true
	case "shift+down":
		return "\x1b[1;2B", true
	case "shift+right":
		return "\x1b[1;2C", true
	case "shift+left":
		return "\x1b[1;2D", true
	case "ctrl+up":
		return "\x1b[1;5A", true
	case "ctrl+down":
		return "\x1b[1;5B", true
	case "ctrl+right":
		return "\x1b[1;5C", true
	case "ctrl+left":
		return "\x1b[1;5D", true
	case "alt+up":
		return "\x1b[1;3A", true
	case "alt+down":
		return "\x1b[1;3B", true
	case "alt+right":
		return "\x1b[1;3C", true
	case "alt+left":
		return "\x1b[1;3D", true
	case "shift+tab":
		return "\x1b[Z", true
	case "shift+enter":
		return "\x1b[13;2u", true // CSI u: shift+return
	}

	// Command/Super has no send-keys encoding — tmux has no notion of the
	// modifier, and terminals that report it (Kitty protocol) send nothing a pane
	// could interpret. The fallback at the bottom of this function used to strip
	// the modifier and forward the bare rune, so cmd+c typed a literal "c" into
	// the pane instead of copying. An unbound super chord does nothing; bound
	// ones are handled by the app before they ever reach here. Meta and Hyper
	// share the no-encoding fate, so they take the same exit.
	if msg.Mod.Contains(tea.ModSuper) || msg.Mod.Contains(tea.ModMeta) || msg.Mod.Contains(tea.ModHyper) {
		return "", false
	}

	// Terminals conventionally encode Alt/Option as an ESC prefix. Bubble Tea
	// decodes that prefix into ModAlt, so put it back before forwarding input to
	// the pane. This is what makes common macOS bindings such as Option+Left /
	// Option+Right (often reported as alt+b / alt+f) and Option+Backspace behave
	// like they do in a regular terminal.
	if msg.Mod.Contains(tea.ModAlt) {
		if msg.Code == tea.KeyBackspace {
			return "\x1b\x7f", true
		}
		// Preserve both modifiers for meta-control letters: ESC followed by the
		// corresponding control byte, as an ordinary terminal would emit.
		if msg.Mod.Contains(tea.ModCtrl) && msg.Code >= 'a' && msg.Code <= 'z' {
			return "\x1b" + string(rune(msg.Code-'a'+1)), true
		}
		if !msg.Mod.Contains(tea.ModCtrl) {
			// Prefer Text when the terminal produced one: it already carries the
			// other modifiers' effect, so alt+shift+f stays "F" rather than
			// collapsing to the base rune.
			if base := []rune(msg.Text); len(base) == 1 && unicode.IsPrint(base[0]) {
				return "\x1b" + msg.Text, true
			}
			if unicode.IsPrint(msg.Code) {
				return "\x1b" + string(msg.Code), true
			}
		}
	}

	// Ctrl combinations. In v2 the KeyCtrl* constants are gone; a ctrl combo
	// arrives as the base rune with ModCtrl set (e.g. ctrl+a -> Code=='a',
	// ModCtrl; ctrl+space -> Code==KeySpace, ModCtrl). Handle these BEFORE the
	// special-key switch so ctrl+space isn't swallowed by the KeySpace case.
	// ctrl+i / ctrl+m arrive as Code==KeyTab/KeyEnter (no ModCtrl) and fall
	// through to the special switch, preserving the v1 "Tab/Enter win" behavior.
	if msg.Mod.Contains(tea.ModCtrl) {
		if msg.Code == tea.KeySpace {
			return "C-Space", false
		}
		if msg.Code >= 'a' && msg.Code <= 'z' {
			return "C-" + string(msg.Code), false
		}
	}

	// Handle special keys. A real Tab/Enter keypress arrives as Code==KeyTab/
	// KeyEnter.
	switch msg.Code {
	case tea.KeyEnter:
		return "Enter", false
	case tea.KeyBackspace:
		return "BSpace", false
	case tea.KeyDelete:
		return "DC", false
	case tea.KeyTab:
		return "Tab", false
	case tea.KeySpace:
		return "Space", false
	case tea.KeyUp:
		return "Up", false
	case tea.KeyDown:
		return "Down", false
	case tea.KeyLeft:
		return "Left", false
	case tea.KeyRight:
		return "Right", false
	case tea.KeyHome:
		return "Home", false
	case tea.KeyEnd:
		return "End", false
	case tea.KeyPgUp:
		return "PPage", false
	case tea.KeyPgDown:
		return "NPage", false
	case tea.KeyInsert:
		return "IC", false
	case tea.KeyEscape:
		return "Escape", false

	// Function keys (F1-F12)
	case tea.KeyF1:
		return "F1", false
	case tea.KeyF2:
		return "F2", false
	case tea.KeyF3:
		return "F3", false
	case tea.KeyF4:
		return "F4", false
	case tea.KeyF5:
		return "F5", false
	case tea.KeyF6:
		return "F6", false
	case tea.KeyF7:
		return "F7", false
	case tea.KeyF8:
		return "F8", false
	case tea.KeyF9:
		return "F9", false
	case tea.KeyF10:
		return "F10", false
	case tea.KeyF11:
		return "F11", false
	case tea.KeyF12:
		return "F12", false
	}

	// Regular character input: v2 populates Text for printable keypresses.
	if len(msg.Text) > 0 {
		return msg.Text, true
	}

	// Modified printable key that carries no Text. Alt/Option was handled above;
	// preserve the base character for any other modifier combination rather than
	// emitting a modifier-prefixed key name as pane text.
	if unicode.IsPrint(msg.Code) {
		return string(msg.Code), true
	}
	return "", true
}

// KeySpec describes a key to send to tmux with ordering preserved.
type KeySpec struct {
	Value   string
	Literal bool
}
