package tty

import (
	"errors"
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
	var notice CopyNotice
	cmd := DefaultConfig().CopySelectionCmd(nil, func(n CopyNotice) tea.Msg {
		notice = n
		return nil
	})
	if cmd == nil {
		t.Fatal("an empty copy said nothing at all")
	}
	cmd()
	if notice.Message != "Nothing selected — ctrl+a selects all output" {
		t.Errorf("empty copy notice = %+v, want an untouched clipboard", notice)
	}
}

func TestSelectionTextIsWhatTheScreenReads(t *testing.T) {
	text := SelectionText([]string{"\x1b[31mred\x1b[0m", "plain"})
	if text != "red\nplain" {
		t.Errorf("selection text = %q, want the lines without their styling", text)
	}
}

func TestCopyNoticeNamesTheClipboardItReached(t *testing.T) {
	native := DefaultConfig().Notice(CopyResult{Lines: 3})
	if native.Message != "Copied 3 line(s)" || native.IsError {
		t.Errorf("successful copy notice = %+v", native)
	}
	// A failed native write is not a failed copy: OSC 52 still ran, so the
	// notice claims only the clipboard it can vouch for.
	remote := DefaultConfig().Notice(CopyResult{Lines: 3, NativeErr: errors.New("clipboard unavailable")})
	if remote.Message != "Copied 3 line(s) to the terminal clipboard" || remote.IsError {
		t.Errorf("native-failure copy notice = %+v", remote)
	}
}
