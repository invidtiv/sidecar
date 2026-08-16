package configui

import (
	"errors"
	"strings"
	"unicode/utf8"
)

// The interactive terminal chords are compared against a key press's own string
// form (tty.Model checks msg.String() == Config.ExitKey), so a value Sidecar
// will never see is a setting that silently does nothing. Validation is a
// state-free function for that reason: it is the rule, not the rendering, and a
// headless caller could adopt it unchanged.

// keyModifiers are the modifier names a key string may carry, in the order
// bubbletea writes them.
var keyModifiers = map[string]bool{
	"ctrl":  true,
	"alt":   true,
	"shift": true,
	"super": true,
}

// namedKeys are the non-printable bases a chord may end on.
var namedKeys = map[string]bool{
	"enter": true, "tab": true, "esc": true, "escape": true, "space": true,
	"backspace": true, "delete": true, "insert": true,
	"up": true, "down": true, "left": true, "right": true,
	"home": true, "end": true, "pgup": true, "pgdown": true,
	"f1": true, "f2": true, "f3": true, "f4": true, "f5": true, "f6": true,
	"f7": true, "f8": true, "f9": true, "f10": true, "f11": true, "f12": true,
}

// splitKey splits a chord into its modifiers and its base key. A chord whose
// base is the plus key itself — ctrl++ — is the one case a plain split gets
// wrong, so it is handled here rather than in each caller.
func splitKey(key string) []string {
	// Only "…++" names the plus key. A single trailing "+" is a modifier with
	// nothing after it, which is a mistake rather than a chord.
	if strings.HasSuffix(key, "++") {
		rest := strings.TrimSuffix(key, "++")
		if rest == "" {
			return []string{"+"}
		}
		return append(strings.Split(rest, "+"), "+")
	}
	return strings.Split(key, "+")
}

// ValidateInteractiveKey reports whether a value is a key string Sidecar can
// match against a real key press. It requires a modifier: a bare character
// would be swallowed the moment the user typed it into the terminal.
func ValidateInteractiveKey(value string) error {
	key := strings.TrimSpace(value)
	if key == "" {
		return errors.New("empty")
	}
	if key != strings.ToLower(key) {
		return errors.New("use lower case, for example ctrl+] or alt+c")
	}
	parts := splitKey(key)
	if len(parts) < 2 {
		return errors.New("needs a modifier, for example ctrl+] or alt+c")
	}
	seen := map[string]bool{}
	for _, part := range parts[:len(parts)-1] {
		if !keyModifiers[part] {
			return errors.New(part + " is not a modifier (ctrl, alt, shift, super)")
		}
		if seen[part] {
			return errors.New("repeated modifier: " + part)
		}
		seen[part] = true
	}
	base := parts[len(parts)-1]
	if base == "" {
		return errors.New("missing a key after the modifier")
	}
	if namedKeys[base] {
		return nil
	}
	if utf8.RuneCountInString(base) == 1 {
		return nil
	}
	return errors.New(base + " is not a key Sidecar recognizes")
}

// FormatKeyLabel writes a key string the way the mockups show it — Ctrl+\ —
// without changing the stored value, which stays in the form a key press
// produces.
func FormatKeyLabel(key string) string {
	key = strings.TrimSpace(key)
	if key == "" {
		return ""
	}
	parts := splitKey(key)
	for i, part := range parts {
		switch part {
		case "ctrl":
			parts[i] = "Ctrl"
		case "alt":
			parts[i] = "Alt"
		case "shift":
			parts[i] = "Shift"
		case "super":
			parts[i] = "Super"
		default:
			if utf8.RuneCountInString(part) == 1 {
				parts[i] = strings.ToUpper(part)
			} else {
				parts[i] = strings.ToUpper(part[:1]) + part[1:]
			}
		}
	}
	return strings.Join(parts, "+")
}
