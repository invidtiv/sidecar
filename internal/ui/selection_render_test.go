package ui

import (
	"strings"
	"testing"
)

// Apps that paint each row with their own background — grok's panel styling is
// the reported case — used to override the highlight on every middle line of a
// multi-line selection, because whole-line ranges took a shortcut that only
// re-applied the highlight after a bare reset.
func TestInjectCharacterRangeBackground_SurvivesLineOwnBackground(t *testing.T) {
	selBg := GetSelectionBgANSI()

	tests := []struct {
		name string
		line string
		// wantAfter are sequences the highlight must be re-applied after.
		wantAfter []string
	}{
		{
			name:      "leading row background",
			line:      "\x1b[48;2;30;30;40m  middle row text",
			wantAfter: []string{"\x1b[48;2;30;30;40m"},
		},
		{
			name:      "background changes mid-line",
			line:      "start\x1b[48;5;236m tail",
			wantAfter: []string{"\x1b[48;5;236m"},
		},
		{
			name:      "compound reset with a foreground colour",
			line:      "start\x1b[0;38;2;200;200;200m tail",
			wantAfter: []string{"\x1b[0;38;2;200;200;200m"},
		},
		{
			name:      "classic background code",
			line:      "start\x1b[44m tail",
			wantAfter: []string{"\x1b[44m"},
		},
		{
			name:      "default background",
			line:      "start\x1b[49m tail",
			wantAfter: []string{"\x1b[49m"},
		},
		{
			name:      "bare reset",
			line:      "start\x1b[0m tail",
			wantAfter: []string{"\x1b[0m"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := InjectCharacterRangeBackground(tt.line, 0, -1)
			if !strings.Contains(got, selBg) {
				t.Errorf("highlight never applied: %q", got)
			}
			for _, seq := range tt.wantAfter {
				if !strings.Contains(got, seq+selBg) {
					t.Errorf("highlight not re-applied after %q: %q", seq, got)
				}
			}
		})
	}
}

// A foreground colour is not a background change, and its arguments must not be
// read as codes of their own — 38;2;49;… contains a 49 that means blue, not
// "default background".
func TestInjectCharacterRangeBackground_ForegroundIsNotBackground(t *testing.T) {
	selBg := GetSelectionBgANSI()
	line := "start\x1b[38;2;49;0;0m tail"

	got := InjectCharacterRangeBackground(line, 0, -1)
	if strings.Contains(got, "\x1b[38;2;49;0;0m"+selBg) {
		t.Errorf("a foreground colour was treated as a background change: %q", got)
	}
	if strings.Count(got, selBg) != 1 {
		t.Errorf("highlight applied %d times, want once: %q", strings.Count(got, selBg), got)
	}
}

// Text after the selection keeps the row's own background rather than being
// blanked to the terminal default.
func TestInjectCharacterRangeBackground_RestoresLineBackground(t *testing.T) {
	rowBg := "\x1b[48;2;30;30;40m"
	line := rowBg + "abcdefgh"

	got := InjectCharacterRangeBackground(line, 2, 4)
	if !strings.Contains(got, rowBg+"f") {
		t.Errorf("row background not restored after the selection: %q", got)
	}
	if strings.Contains(got, "\x1b[49mf") {
		t.Errorf("selection end blanked the row background: %q", got)
	}
}

// A plain line — the search-match highlight's usual input — is unchanged.
func TestInjectCharacterRangeBackground_PlainLine(t *testing.T) {
	selBg := GetSelectionBgANSI()

	got := InjectCharacterRangeBackground("hello world", 6, 10)
	if want := "hello " + selBg + "world" + "\x1b[49m"; got != want {
		t.Errorf("plain partial range: got %q, want %q", got, want)
	}
}

func TestSgrBackground(t *testing.T) {
	tests := []struct {
		name        string
		seq         string
		wantTouches bool
		wantBg      string
	}{
		{name: "bare reset", seq: "\x1b[0m", wantTouches: true, wantBg: "\x1b[49m"},
		{name: "empty reset", seq: "\x1b[m", wantTouches: true, wantBg: "\x1b[49m"},
		{name: "default background", seq: "\x1b[49m", wantTouches: true, wantBg: "\x1b[49m"},
		{name: "compound reset", seq: "\x1b[0;38;2;200;200;200m", wantTouches: true, wantBg: "\x1b[49m"},
		{name: "256 background", seq: "\x1b[48;5;236m", wantTouches: true, wantBg: "\x1b[48;5;236m"},
		{name: "underline colour consumes its arguments", seq: "\x1b[58;2;10;45;30m", wantTouches: false, wantBg: ""},
		{name: "truecolor background", seq: "\x1b[48;2;30;30;40m", wantTouches: true, wantBg: "\x1b[48;2;30;30;40m"},
		{name: "classic background", seq: "\x1b[44m", wantTouches: true, wantBg: "\x1b[44m"},
		{name: "bright background", seq: "\x1b[101m", wantTouches: true, wantBg: "\x1b[101m"},
		{name: "colon subparameters", seq: "\x1b[48:2::30:30:40m", wantTouches: true, wantBg: "\x1b[48:2::30:30:40m"},
		{name: "foreground only", seq: "\x1b[38;5;42m"},
		{name: "foreground containing 49", seq: "\x1b[38;2;49;0;0m"},
		{name: "foreground containing 44", seq: "\x1b[38;2;44;1;1m"},
		{name: "bold", seq: "\x1b[1m"},
		{name: "default foreground", seq: "\x1b[39m"},
		{name: "not an SGR", seq: "\x1b[?25l"},
		{name: "background after a foreground", seq: "\x1b[38;5;42;48;5;236m", wantTouches: true, wantBg: "\x1b[48;5;236m"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			bg, touches := sgrBackground(tt.seq)
			if touches != tt.wantTouches {
				t.Fatalf("sgrBackground(%q) touches = %v, want %v", tt.seq, touches, tt.wantTouches)
			}
			if touches && bg != tt.wantBg {
				t.Errorf("sgrBackground(%q) bg = %q, want %q", tt.seq, bg, tt.wantBg)
			}
		})
	}
}
