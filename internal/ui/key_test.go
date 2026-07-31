package ui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestPrintableKeyText(t *testing.T) {
	tests := []struct {
		name string
		msg  tea.KeyPressMsg
		want string
	}{
		{name: "letter", msg: tea.KeyPressMsg{Code: 'a', Text: "a"}, want: "a"},
		{name: "space text", msg: tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}, want: " "},
		{name: "space fallback", msg: tea.KeyPressMsg{Code: tea.KeySpace}, want: " "},
		{name: "unicode", msg: tea.KeyPressMsg{Code: 'é', Text: "é"}, want: "é"},
		{name: "multiple printable runes", msg: tea.KeyPressMsg{Code: tea.KeyExtended, Text: "two words"}, want: "two words"},
		{name: "shifted text", msg: tea.KeyPressMsg{Code: 'a', Text: "A", Mod: tea.ModShift}, want: "A"},
		{name: "control shortcut", msg: tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, want: ""},
		{name: "special key", msg: tea.KeyPressMsg{Code: tea.KeyEnter}, want: ""},
		{name: "non-printable text", msg: tea.KeyPressMsg{Code: tea.KeyExtended, Text: "a\n"}, want: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := PrintableKeyText(tt.msg); got != tt.want {
				t.Fatalf("PrintableKeyText(%+v) = %q, want %q", tt.msg, got, tt.want)
			}
		})
	}
}
