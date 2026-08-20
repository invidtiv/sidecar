package ui

import (
	"fmt"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/styles"
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
func TestGetSelectionBgANSIUsesThemeSelectionBg(t *testing.T) {
	prev := styles.GetCurrentTheme()
	t.Cleanup(func() { styles.ApplyTheme(prev.Name) })

	styles.ApplyThemeWithOverrides("sidecar-modern", map[string]string{
		"selectionBg": "#3b5070",
	})
	hex := styles.GetCurrentTheme().Colors.SelectionBg
	var r, g, b int
	if _, err := fmt.Sscanf(hex, "#%02x%02x%02x", &r, &g, &b); err != nil {
		t.Fatalf("current SelectionBg %q is not hex: %v", hex, err)
	}
	got := GetSelectionBgANSI()
	want := fmt.Sprintf("\x1b[48;2;%d;%d;%dm", r, g, b)
	if got != want {
		t.Errorf("selection highlight = %q, want %q (from SelectionBg %s)", got, want, hex)
	}
	if hex == styles.GetCurrentTheme().Colors.BgTertiary {
		t.Errorf("SelectionBg fell back to BgTertiary %s", hex)
	}
}

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

func TestApplyTerminalDefaultBackgroundPreservesExplicitBackgrounds(t *testing.T) {
	canvas := "\x1b[48;2;20;20;20m"
	panel := "\x1b[48;2;36;36;36m"
	got := ApplyTerminalDefaultBackground("plain\x1b[0m default "+panel+"panel\x1b[49m tail", canvas, 80)

	if !strings.HasPrefix(got, canvas+"plain") {
		t.Fatalf("canvas background not established: %q", got)
	}
	if !strings.Contains(got, "\x1b[0m"+canvas+" default ") {
		t.Errorf("reset did not restore canvas background: %q", got)
	}
	if !strings.Contains(got, panel+"panel\x1b[49m"+canvas+" tail") {
		t.Errorf("explicit panel or following default background was lost: %q", got)
	}
}

func TestCarryRowBackgroundReopensInheritedBackground(t *testing.T) {
	green := "\x1b[48;2;0;80;0m"

	// Row one opens the background and never closes it — what capture-pane -e
	// delivers when the trailing filled cells are trimmed.
	out, trailing, touched := CarryRowBackground(green+"first", "")
	if out != green+"first" || trailing != green || !touched {
		t.Fatalf("row one = %q, trailing %q, touched %v", out, trailing, touched)
	}

	// Row two carries it with no sequence of its own, so it has to be re-opened.
	out, trailing, touched = CarryRowBackground("second", trailing)
	if out != green+"second" || trailing != green || !touched {
		t.Fatalf("row two = %q, trailing %q, touched %v", out, trailing, touched)
	}

	// An explicit reset ends the run; the next row inherits nothing.
	out, trailing, touched = CarryRowBackground("third\x1b[49m", trailing)
	if out != green+"third\x1b[49m" || trailing != "" || !touched {
		t.Fatalf("row three = %q, trailing %q, touched %v", out, trailing, touched)
	}

	out, trailing, touched = CarryRowBackground("plain", trailing)
	if out != "plain" || trailing != "" || touched {
		t.Fatalf("row four = %q, trailing %q, touched %v", out, trailing, touched)
	}
}
