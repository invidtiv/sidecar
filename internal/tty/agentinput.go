package tty

import (
	"fmt"
	"sort"
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
)

// Ordered terminal input, shared by the embedded terminal and headless callers.
//
// Two encodings decide what a pane actually receives, and both already existed
// for the TUI: exact text goes through tmux's bracketed-paste-aware buffer path
// (SendPasteToTmux), and a key press becomes a tmux send-keys argument
// (MapKeyToTmux). This file adds the two things a headless caller needs on top
// of them and nothing else — a name for a key, and a way to say "these, in this
// order". A second encoder is what it exists to prevent: an agent that submits
// a prompt must produce byte-for-byte what a human typing into the same pane
// produces, or the two surfaces disagree about what the agent was asked.

// InputStepKind distinguishes the two things an ordered input sequence is made
// of. They are not interchangeable: text is pasted so the receiving application
// sees one bracketed unit, while a key is sent by name so tmux encodes it.
type InputStepKind uint8

const (
	// InputText is exact text delivered through a tmux paste buffer. tmux — not
	// the caller — decides whether the paste is bracketed, from the pane
	// application's own mode.
	InputText InputStepKind = iota
	// InputKey is one encoded key press.
	InputKey
)

// InputStep is one element of an ordered terminal input sequence.
type InputStep struct {
	Kind InputStepKind
	Text string
	Key  KeySpec
}

// TextStep pastes exact text.
func TextStep(text string) InputStep { return InputStep{Kind: InputText, Text: text} }

// KeyStep sends one already-encoded key.
func KeyStep(spec KeySpec) InputStep { return InputStep{Kind: InputKey, Key: spec} }

// PromptSteps is the ordered submission of one prompt: the exact text, then a
// separate Enter.
//
// The separation is the point. Appending "\n" to the pasted text would submit
// through whatever the application does with a newline mid-paste, which for an
// agent composer is usually "insert a line break" rather than "send" — and for
// a plain shell depends on tmux's LF/CR translation. Enter as its own key press
// is the same thing a human's Return does.
func PromptSteps(text string) []InputStep {
	return []InputStep{TextStep(text), KeyStep(KeySpec{Value: "Enter"})}
}

// SendInputSteps performs an ordered input sequence against one tmux target.
// It stops at the first failure so a caller is never told a partial sequence
// succeeded.
func SendInputSteps(target string, steps ...InputStep) error {
	for _, step := range steps {
		var err error
		switch step.Kind {
		case InputText:
			err = SendPasteToTmux(target, step.Text)
		case InputKey:
			err = SendKeys(target, step.Key)
		default:
			err = fmt.Errorf("unknown input step kind %d", step.Kind)
		}
		if err != nil {
			return err
		}
	}
	return nil
}

// logicalKeyCodes is the named half of the vocabulary: keys that have no
// character to type. Aliases exist where two spellings are both idiomatic;
// nothing here is invented, they are the names tmux, vim, and terminal
// documentation already use.
var logicalKeyCodes = map[string]rune{
	"enter":     tea.KeyEnter,
	"return":    tea.KeyEnter,
	"esc":       tea.KeyEscape,
	"escape":    tea.KeyEscape,
	"tab":       tea.KeyTab,
	"space":     tea.KeySpace,
	"backspace": tea.KeyBackspace,
	"bspace":    tea.KeyBackspace,
	"delete":    tea.KeyDelete,
	"del":       tea.KeyDelete,
	"insert":    tea.KeyInsert,
	"up":        tea.KeyUp,
	"down":      tea.KeyDown,
	"left":      tea.KeyLeft,
	"right":     tea.KeyRight,
	"home":      tea.KeyHome,
	"end":       tea.KeyEnd,
	"pageup":    tea.KeyPgUp,
	"pgup":      tea.KeyPgUp,
	"pagedown":  tea.KeyPgDown,
	"pgdn":      tea.KeyPgDown,
	"f1":        tea.KeyF1,
	"f2":        tea.KeyF2,
	"f3":        tea.KeyF3,
	"f4":        tea.KeyF4,
	"f5":        tea.KeyF5,
	"f6":        tea.KeyF6,
	"f7":        tea.KeyF7,
	"f8":        tea.KeyF8,
	"f9":        tea.KeyF9,
	"f10":       tea.KeyF10,
	"f11":       tea.KeyF11,
	"f12":       tea.KeyF12,
}

// LogicalKeyVocabulary lists the named keys, sorted, for help and error text.
// Single printable characters are accepted too and are not listed here.
func LogicalKeyVocabulary() []string {
	names := make([]string, 0, len(logicalKeyCodes))
	for name := range logicalKeyCodes {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// EncodeLogicalKeys validates every name before encoding any of them, so a
// caller writing a sequence to a live agent UI cannot deliver half of it and
// then fail. One bad name rejects the whole list.
func EncodeLogicalKeys(names []string) ([]KeySpec, error) {
	specs := make([]KeySpec, 0, len(names))
	for _, name := range names {
		spec, err := EncodeLogicalKey(name)
		if err != nil {
			return nil, err
		}
		specs = append(specs, spec)
	}
	if len(specs) == 0 {
		return nil, fmt.Errorf("no keys given")
	}
	return specs, nil
}

// EncodeLogicalKey turns one documented key name into the tmux encoding the
// embedded terminal produces for the same physical key press.
//
// It builds the key press and hands it to MapKeyToTmux rather than emitting
// tmux syntax itself: the mapping from a key to what a pane receives stays in
// one place, and a headless send cannot drift from what a human's keyboard
// does. Names are case-insensitive and modifiers may appear in any order.
func EncodeLogicalKey(name string) (KeySpec, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return KeySpec{}, fmt.Errorf("empty key name")
	}
	base, mod, err := parseLogicalKey(trimmed)
	if err != nil {
		return KeySpec{}, err
	}
	msg := tea.KeyPressMsg{Code: base, Mod: mod}
	if unicode.IsPrint(base) && mod&(tea.ModCtrl|tea.ModAlt) == 0 {
		msg.Text = string(base)
	}
	value, literal := MapKeyToTmux(msg)
	if value == "" {
		return KeySpec{}, fmt.Errorf("key %q has no terminal encoding", name)
	}
	return KeySpec{Value: value, Literal: literal}, nil
}

// shiftEncodable is the set MapKeyToTmux actually has a shifted encoding for.
// Shift on anything else would be dropped on the way to the pane, and a
// silently weaker key is worse than a refusal for a caller answering a live
// agent's UI.
var shiftEncodable = map[rune]bool{
	tea.KeyUp: true, tea.KeyDown: true, tea.KeyLeft: true, tea.KeyRight: true,
	tea.KeyTab: true, tea.KeyEnter: true,
}

func isArrow(base rune) bool {
	return base == tea.KeyUp || base == tea.KeyDown || base == tea.KeyLeft || base == tea.KeyRight
}

// ctrlEncodable and altEncodable are the modifier combinations MapKeyToTmux
// actually renders. They are stated here rather than discovered by trying,
// because the failure mode of the untested combination is silent: the modifier
// disappears and the bare key reaches the pane.
func ctrlEncodable(base rune) bool {
	return (base >= 'a' && base <= 'z') || base == tea.KeySpace || isArrow(base)
}

func altEncodable(base rune) bool {
	return unicode.IsPrint(base) || base == tea.KeyBackspace || isArrow(base)
}

// parseLogicalKey splits "ctrl+alt+left" into its base key and modifiers, and
// refuses the combinations that cannot survive the trip to a pane.
func parseLogicalKey(name string) (rune, tea.KeyMod, error) {
	lower := strings.ToLower(name)
	// "+" is a character somebody may need to type, and it is also the
	// separator. A name that is nothing but separators is the character.
	if strings.Trim(lower, "+") == "" {
		return '+', 0, nil
	}
	parts := strings.Split(lower, "+")
	// A trailing separator means the base key is "+" itself: "ctrl++".
	if parts[len(parts)-1] == "" {
		parts = append(parts[:len(parts)-1], "+")
	}

	var mod tea.KeyMod
	for _, part := range parts[:len(parts)-1] {
		switch part {
		case "ctrl", "control":
			mod |= tea.ModCtrl
		case "alt", "opt", "option", "meta":
			// Terminals encode Alt/Option as an ESC prefix, which is the only
			// thing a pane can receive. "meta" is accepted as the historical
			// spelling of exactly that, not as tea.ModMeta, which has no
			// encoding at all.
			mod |= tea.ModAlt
		case "shift":
			mod |= tea.ModShift
		case "cmd", "command", "super", "hyper":
			return 0, 0, fmt.Errorf("key %q uses %s, which no terminal can encode for a pane", name, part)
		default:
			return 0, 0, fmt.Errorf("key %q has unknown modifier %q", name, part)
		}
	}

	baseName := parts[len(parts)-1]
	base, named := logicalKeyCodes[baseName]
	if !named {
		runes := []rune(baseName)
		if len(runes) != 1 || !unicode.IsPrint(runes[0]) {
			return 0, 0, fmt.Errorf("unknown key %q; use a single character or one of: %s", name, strings.Join(LogicalKeyVocabulary(), ", "))
		}
		base = runes[0]
	}

	if mod&tea.ModCtrl != 0 && !ctrlEncodable(base) {
		// MapKeyToTmux encodes three ctrl families: C-<letter>, C-Space, and the
		// CSI arrow forms. Anything else falls through to its printable
		// fallback, which would send the bare character to a live agent UI.
		return 0, 0, fmt.Errorf("key %q is not an encodable control chord; ctrl combines with a letter, space, or an arrow key", name)
	}
	if mod&tea.ModAlt != 0 && !altEncodable(base) {
		return 0, 0, fmt.Errorf("key %q has no alt encoding; alt combines with a character, backspace, or an arrow key", name)
	}
	if mod&tea.ModShift != 0 && !shiftEncodable[base] {
		return 0, 0, fmt.Errorf("key %q has no shifted encoding; name the character it types, or use one of: shift+up, shift+down, shift+left, shift+right, shift+tab, shift+enter", name)
	}
	return base, mod, nil
}
