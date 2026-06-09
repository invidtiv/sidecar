package tty

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestMapKeyToTmux_Printable(t *testing.T) {
	msg := tea.KeyPressMsg{Code: 'a', Text: "a"}
	key, literal := MapKeyToTmux(msg)
	if key != "a" {
		t.Errorf("expected key='a', got '%s'", key)
	}
	if !literal {
		t.Error("expected literal=true for printable character")
	}
}

func TestMapKeyToTmux_Enter(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyEnter}
	key, literal := MapKeyToTmux(msg)
	if key != "Enter" {
		t.Errorf("expected key='Enter', got '%s'", key)
	}
	if literal {
		t.Error("expected literal=false for Enter key")
	}
}

func TestMapKeyToTmux_Backspace(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyBackspace}
	key, literal := MapKeyToTmux(msg)
	if key != "BSpace" {
		t.Errorf("expected key='BSpace', got '%s'", key)
	}
	if literal {
		t.Error("expected literal=false for Backspace")
	}
}

func TestMapKeyToTmux_Escape(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyEscape}
	key, literal := MapKeyToTmux(msg)
	if key != "Escape" {
		t.Errorf("expected key='Escape', got '%s'", key)
	}
	if literal {
		t.Error("expected literal=false for Escape")
	}
}

func TestMapKeyToTmux_ArrowKeys(t *testing.T) {
	tests := []struct {
		code rune
		want string
	}{
		{tea.KeyUp, "Up"},
		{tea.KeyDown, "Down"},
		{tea.KeyLeft, "Left"},
		{tea.KeyRight, "Right"},
	}

	for _, tt := range tests {
		msg := tea.KeyPressMsg{Code: tt.code}
		key, literal := MapKeyToTmux(msg)
		if key != tt.want {
			t.Errorf("expected key='%s', got '%s'", tt.want, key)
		}
		if literal {
			t.Errorf("expected literal=false for %s", tt.want)
		}
	}
}

func TestMapKeyToTmux_CtrlKeys(t *testing.T) {
	tests := []struct {
		key  tea.KeyPressMsg
		want string
	}{
		{tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, "C-a"},
		{tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "C-c"},
		{tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, "C-d"},
		{tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}, "C-z"},
	}

	for _, tt := range tests {
		msg := tt.key
		key, literal := MapKeyToTmux(msg)
		if key != tt.want {
			t.Errorf("expected key='%s', got '%s'", tt.want, key)
		}
		if literal {
			t.Errorf("expected literal=false for %s", tt.want)
		}
	}
}

func TestMapKeyToTmux_FunctionKeys(t *testing.T) {
	tests := []struct {
		code rune
		want string
	}{
		{tea.KeyF1, "F1"},
		{tea.KeyF5, "F5"},
		{tea.KeyF12, "F12"},
	}

	for _, tt := range tests {
		msg := tea.KeyPressMsg{Code: tt.code}
		key, literal := MapKeyToTmux(msg)
		if key != tt.want {
			t.Errorf("expected key='%s', got '%s'", tt.want, key)
		}
		if literal {
			t.Errorf("expected literal=false for %s", tt.want)
		}
	}
}

func TestMapKeyToTmux_NavigationKeys(t *testing.T) {
	tests := []struct {
		code rune
		want string
	}{
		{tea.KeyHome, "Home"},
		{tea.KeyEnd, "End"},
		{tea.KeyPgUp, "PPage"},
		{tea.KeyPgDown, "NPage"},
		{tea.KeyInsert, "IC"},
		{tea.KeyDelete, "DC"},
	}

	for _, tt := range tests {
		msg := tea.KeyPressMsg{Code: tt.code}
		key, literal := MapKeyToTmux(msg)
		if key != tt.want {
			t.Errorf("expected key='%s', got '%s'", tt.want, key)
		}
		if literal {
			t.Errorf("expected literal=false for %s", tt.want)
		}
	}
}

func TestMapKeyToTmux_Space(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeySpace}
	key, literal := MapKeyToTmux(msg)
	if key != "Space" {
		t.Errorf("expected key='Space', got '%s'", key)
	}
	if literal {
		t.Error("expected literal=false for Space")
	}
}

func TestMapKeyToTmux_Tab(t *testing.T) {
	msg := tea.KeyPressMsg{Code: tea.KeyTab}
	key, literal := MapKeyToTmux(msg)
	if key != "Tab" {
		t.Errorf("expected key='Tab', got '%s'", key)
	}
	if literal {
		t.Error("expected literal=false for Tab")
	}
}

// TestMapKeyToTmux_Modifiers locks in v1→v2 parity for modified keys (regression
// guard for the v2 migration review findings).
func TestMapKeyToTmux_Modifiers(t *testing.T) {
	cases := []struct {
		name        string
		msg         tea.KeyPressMsg
		wantKey     string
		wantLiteral bool
	}{
		{"ctrl+a", tea.KeyPressMsg{Code: 'a', Mod: tea.ModCtrl}, "C-a", false},
		{"ctrl+z", tea.KeyPressMsg{Code: 'z', Mod: tea.ModCtrl}, "C-z", false},
		{"ctrl+space", tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl}, "C-Space", false},
		{"plain space", tea.KeyPressMsg{Code: tea.KeySpace}, "Space", false},
		{"alt+a sends base char (not literal 'alt+a')", tea.KeyPressMsg{Code: 'a', Mod: tea.ModAlt}, "a", true},
		{"plain rune a", tea.KeyPressMsg{Code: 'a', Text: "a"}, "a", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			key, literal := MapKeyToTmux(tc.msg)
			if key != tc.wantKey {
				t.Errorf("key = %q, want %q", key, tc.wantKey)
			}
			if literal != tc.wantLiteral {
				t.Errorf("literal = %v, want %v", literal, tc.wantLiteral)
			}
		})
	}
}
