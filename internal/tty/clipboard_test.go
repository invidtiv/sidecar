package tty

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCopyAndPasteChords(t *testing.T) {
	cfg := DefaultConfig()
	configured := Config{CopyKey: "ctrl+shift+c", PasteKey: "ctrl+shift+v"}

	for _, tc := range []struct {
		name           string
		cfg            Config
		msg            tea.KeyPressMsg
		copies, pastes bool
	}{
		{"default copy", cfg, tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}, true, false},
		{"default paste", cfg, tea.KeyPressMsg{Code: 'v', Mod: tea.ModAlt}, false, true},
		{"platform copy", cfg, tea.KeyPressMsg{Code: 'c', Mod: tea.ModSuper}, true, false},
		{"platform copy under a configured key", configured, tea.KeyPressMsg{Code: 'c', Mod: tea.ModSuper}, true, false},
		{"the configured key replaces the default", configured, tea.KeyPressMsg{Code: 'c', Mod: tea.ModAlt}, false, false},
		{"an ordinary key is neither", cfg, tea.KeyPressMsg{Code: 'c'}, false, false},
		{"an unconfigured surface answers only the platform chord", Config{}, tea.KeyPressMsg{Code: 'c'}, false, false},
	} {
		if got := tc.cfg.IsCopyChord(tc.msg); got != tc.copies {
			t.Errorf("%s: IsCopyChord(%s) = %v, want %v", tc.name, tc.msg.String(), got, tc.copies)
		}
		if got := tc.cfg.IsPasteChord(tc.msg); got != tc.pastes {
			t.Errorf("%s: IsPasteChord(%s) = %v, want %v", tc.name, tc.msg.String(), got, tc.pastes)
		}
	}
}

func TestCopySelectionRefusesAnEmptySelection(t *testing.T) {
	result := CopySelection(nil)
	if !result.Empty {
		t.Error("a copy with nothing selected did not report itself empty")
	}
	if result.Lines != 0 || result.Err != nil {
		t.Errorf("result = %+v, want an untouched clipboard", result)
	}
}
