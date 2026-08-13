package tty

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// Every host answers the surface's chords in one order, so the same key cannot
// mean different things on two surfaces embedding the same terminal.
func TestResolveSurfaceChordAnswersOneSetInOneOrder(t *testing.T) {
	config := DefaultConfig()
	var answered []string
	chords := SurfaceChords{
		Copy:      func() tea.Cmd { answered = append(answered, "copy"); return nil },
		SelectAll: func() tea.Cmd { answered = append(answered, "select-all"); return nil },
		Scrollback: func(msg tea.KeyPressMsg) (tea.Cmd, bool) {
			if !IsScrollbackKey(msg) {
				return nil, false
			}
			answered = append(answered, "scrollback")
			return nil, true
		},
	}
	for _, tc := range []struct {
		key  string
		want string
	}{
		{config.CopyKey, "copy"},
		{config.SelectAllKey, "select-all"},
		{"shift+up", "scrollback"},
	} {
		answered = nil
		if _, handled := config.ResolveSurfaceChord(keyPress(tc.key), chords); !handled {
			t.Fatalf("%q was not answered", tc.key)
		}
		if len(answered) != 1 || answered[0] != tc.want {
			t.Fatalf("%q was answered by %v, want %s", tc.key, answered, tc.want)
		}
	}
}

// A host that contributes nothing for a chord declines it rather than the shared
// resolution inventing an answer.
func TestResolveSurfaceChordDeclinesWhatTheHostDoesNotAnswer(t *testing.T) {
	config := DefaultConfig()
	if cmd, handled := config.ResolveSurfaceChord(keyPress(config.CopyKey), SurfaceChords{}); handled || cmd != nil {
		t.Fatal("a chord with no host answer was claimed")
	}
}

func keyPress(key string) tea.KeyPressMsg {
	switch key {
	case "shift+up":
		return tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}
	case "alt+c":
		return tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}
	case "ctrl+a":
		return tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}
	}
	return tea.KeyPressMsg{Code: rune(key[0]), Text: key}
}
