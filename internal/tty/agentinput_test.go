package tty

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestEncodeLogicalKeyMatchesTheEmbeddedTerminalEncoding(t *testing.T) {
	cases := []struct {
		name    string
		press   tea.KeyPressMsg
		value   string
		literal bool
	}{
		{"enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "Enter", false},
		{"Enter", tea.KeyPressMsg{Code: tea.KeyEnter}, "Enter", false},
		{"esc", tea.KeyPressMsg{Code: tea.KeyEscape}, "Escape", false},
		{"escape", tea.KeyPressMsg{Code: tea.KeyEscape}, "Escape", false},
		{"tab", tea.KeyPressMsg{Code: tea.KeyTab}, "Tab", false},
		{"space", tea.KeyPressMsg{Code: tea.KeySpace}, "Space", false},
		{"backspace", tea.KeyPressMsg{Code: tea.KeyBackspace}, "BSpace", false},
		{"delete", tea.KeyPressMsg{Code: tea.KeyDelete}, "DC", false},
		{"insert", tea.KeyPressMsg{Code: tea.KeyInsert}, "IC", false},
		{"up", tea.KeyPressMsg{Code: tea.KeyUp}, "Up", false},
		{"down", tea.KeyPressMsg{Code: tea.KeyDown}, "Down", false},
		{"left", tea.KeyPressMsg{Code: tea.KeyLeft}, "Left", false},
		{"right", tea.KeyPressMsg{Code: tea.KeyRight}, "Right", false},
		{"home", tea.KeyPressMsg{Code: tea.KeyHome}, "Home", false},
		{"end", tea.KeyPressMsg{Code: tea.KeyEnd}, "End", false},
		{"pageup", tea.KeyPressMsg{Code: tea.KeyPgUp}, "PPage", false},
		{"pgdn", tea.KeyPressMsg{Code: tea.KeyPgDown}, "NPage", false},
		{"f1", tea.KeyPressMsg{Code: tea.KeyF1}, "F1", false},
		{"f12", tea.KeyPressMsg{Code: tea.KeyF12}, "F12", false},
		{"ctrl+c", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "C-c", false},
		{"CTRL+C", tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl}, "C-c", false},
		{"control+d", tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}, "C-d", false},
		{"ctrl+space", tea.KeyPressMsg{Code: tea.KeySpace, Mod: tea.ModCtrl}, "C-Space", false},
		{"shift+tab", tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}, "\x1b[Z", true},
		{"shift+enter", tea.KeyPressMsg{Code: tea.KeyEnter, Mod: tea.ModShift}, "\x1b[13;2u", true},
		{"shift+up", tea.KeyPressMsg{Code: tea.KeyUp, Mod: tea.ModShift}, "\x1b[1;2A", true},
		{"ctrl+left", tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModCtrl}, "\x1b[1;5D", true},
		{"alt+left", tea.KeyPressMsg{Code: tea.KeyLeft, Mod: tea.ModAlt}, "\x1b[1;3D", true},
		{"alt+backspace", tea.KeyPressMsg{Code: tea.KeyBackspace, Mod: tea.ModAlt}, "\x1b\x7f", true},
		{"alt+b", tea.KeyPressMsg{Code: 'b', Mod: tea.ModAlt}, "\x1bb", true},
		{"ctrl+alt+f", tea.KeyPressMsg{Code: 'f', Mod: tea.ModCtrl | tea.ModAlt}, "\x1b\x06", true},
		{"y", tea.KeyPressMsg{Code: 'y', Text: "y"}, "y", true},
		{"2", tea.KeyPressMsg{Code: '2', Text: "2"}, "2", true},
		{"+", tea.KeyPressMsg{Code: '+', Text: "+"}, "+", true},
		{"雪", tea.KeyPressMsg{Code: '雪', Text: "雪"}, "雪", true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			spec, err := EncodeLogicalKey(tc.name)
			if err != nil {
				t.Fatalf("EncodeLogicalKey(%q): %v", tc.name, err)
			}
			if spec.Value != tc.value || spec.Literal != tc.literal {
				t.Fatalf("EncodeLogicalKey(%q) = %q literal=%v, want %q literal=%v", tc.name, spec.Value, spec.Literal, tc.value, tc.literal)
			}
			// The whole point of the extraction: the same key press typed into
			// the embedded terminal must produce the same bytes.
			wantValue, wantLiteral := MapKeyToTmux(tc.press)
			if spec.Value != wantValue || spec.Literal != wantLiteral {
				t.Fatalf("logical %q = %q literal=%v; embedded terminal press = %q literal=%v", tc.name, spec.Value, spec.Literal, wantValue, wantLiteral)
			}
		})
	}
}

func TestEncodeLogicalKeyRefusesUnencodableNames(t *testing.T) {
	for _, name := range []string{
		"",
		"   ",
		"enterr",
		"ctrl+enter",  // tmux has no encoding; would send a bare Enter
		"ctrl+1",      // not an encodable control chord
		"shift+a",     // name the character it types
		"shift+f1",    // shift would be silently dropped
		"cmd+c",       // no terminal encoding at all
		"super+left",  //
		"hyper+x",     //
		"wiggle+left", // unknown modifier
		"hello",       // prompt text is not a key
	} {
		if spec, err := EncodeLogicalKey(name); err == nil {
			t.Fatalf("EncodeLogicalKey(%q) = %+v, want an error", name, spec)
		}
	}
}

func TestEncodeLogicalKeysRejectsTheWholeListOnOneBadName(t *testing.T) {
	specs, err := EncodeLogicalKeys([]string{"escape", "down", "cmd+c", "enter"})
	if err == nil || specs != nil {
		t.Fatalf("EncodeLogicalKeys = %+v, %v; want no specs and an error", specs, err)
	}
	if !strings.Contains(err.Error(), "cmd+c") {
		t.Fatalf("error %v does not name the offending key", err)
	}
	if _, err := EncodeLogicalKeys(nil); err == nil {
		t.Fatal("EncodeLogicalKeys(nil) accepted an empty sequence")
	}
	good, err := EncodeLogicalKeys([]string{"escape", "down", "enter"})
	if err != nil || len(good) != 3 || good[2].Value != "Enter" {
		t.Fatalf("EncodeLogicalKeys = %+v, %v", good, err)
	}
}

func TestPromptStepsSubmitsTextAndEnterSeparately(t *testing.T) {
	steps := PromptSteps("first line\nsecond")
	if len(steps) != 2 {
		t.Fatalf("PromptSteps = %+v", steps)
	}
	if steps[0].Kind != InputText || steps[0].Text != "first line\nsecond" {
		t.Fatalf("text step = %+v", steps[0])
	}
	if steps[1].Kind != InputKey || steps[1].Key.Value != "Enter" || steps[1].Key.Literal {
		t.Fatalf("submit step = %+v", steps[1])
	}
}

func TestLogicalKeyVocabularyIsSortedAndCoversTheNamedKeys(t *testing.T) {
	names := LogicalKeyVocabulary()
	if len(names) != len(logicalKeyCodes) {
		t.Fatalf("vocabulary has %d names, map has %d", len(names), len(logicalKeyCodes))
	}
	for i := 1; i < len(names); i++ {
		if names[i-1] >= names[i] {
			t.Fatalf("vocabulary is not sorted at %d: %q %q", i, names[i-1], names[i])
		}
	}
	for _, name := range names {
		if _, err := EncodeLogicalKey(name); err != nil {
			t.Fatalf("documented key %q does not encode: %v", name, err)
		}
	}
}
