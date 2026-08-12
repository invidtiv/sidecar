package styles

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

// TestFillBackgroundHandlesBothResetSpellings is the regression guard for the
// splotchy-modal bug: lipgloss v2 emits the implicit-zero reset "\x1b[m", so
// matching only "\x1b[0m" left every run after a nested styled element on the
// terminal default background.
func TestFillBackgroundHandlesBothResetSpellings(t *testing.T) {
	bg := lipgloss.Color("#1F2937")

	tests := []struct {
		name    string
		content string
	}{
		{"implicit zero reset", "\x1b[38;2;255;0;0mred\x1b[m"},
		{"explicit zero reset", "\x1b[38;2;255;0;0mred\x1b[0m"},
		{"nested background", lipgloss.NewStyle().Background(lipgloss.Color("#374151")).Render("btn")},
		{"plain text", "hello"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := FillBackground(tt.content, 20, bg)
			assertEveryCellHasBackground(t, got)
			if w := lipgloss.Width(got); w != 20 {
				t.Errorf("width = %d, want 20", w)
			}
		})
	}
}

func TestFillBackgroundEndsWithReset(t *testing.T) {
	got := FillBackground("\x1b[38;2;255;0;0mred\x1b[m", 20, lipgloss.Color("#1F2937"))
	if !strings.HasSuffix(got, resetSeq) {
		t.Fatalf("line must end with a reset so the fill cannot bleed past the content width: %q", got)
	}
}

func TestFillBackgroundPreservesEachLine(t *testing.T) {
	content := "one\n\x1b[38;2;255;0;0mtwo\x1b[m\nthree"
	got := FillBackground(content, 10, lipgloss.Color("#1F2937"))
	lines := strings.Split(got, "\n")
	if len(lines) != 3 {
		t.Fatalf("got %d lines, want 3", len(lines))
	}
	for i, line := range lines {
		if w := lipgloss.Width(line); w != 10 {
			t.Errorf("line %d width = %d, want 10", i, w)
		}
		assertEveryCellHasBackground(t, line)
	}
}

// assertEveryCellHasBackground walks a single rendered line, tracking SGR
// state, and fails if any printable cell is emitted with no background set.
func assertEveryCellHasBackground(t *testing.T, line string) {
	t.Helper()
	for _, l := range strings.Split(line, "\n") {
		col := 0
		bgSet := false
		for _, run := range splitSGR(l) {
			if run.isEscape {
				bgSet = applySGRBackground(bgSet, run.text)
				continue
			}
			for _, r := range run.text {
				if !bgSet {
					t.Errorf("column %d (%q) has no background set in %q", col, string(r), l)
					return
				}
				col++
			}
		}
	}
}

type sgrRun struct {
	text     string
	isEscape bool
}

// splitSGR splits a string into alternating literal and CSI-escape runs.
func splitSGR(s string) []sgrRun {
	var runs []sgrRun
	for len(s) > 0 {
		idx := strings.Index(s, "\x1b[")
		if idx == -1 {
			runs = append(runs, sgrRun{text: s})
			break
		}
		if idx > 0 {
			runs = append(runs, sgrRun{text: s[:idx]})
		}
		end := strings.IndexByte(s[idx:], 'm')
		if end == -1 {
			runs = append(runs, sgrRun{text: s[idx:]})
			break
		}
		runs = append(runs, sgrRun{text: s[idx : idx+end+1], isEscape: true})
		s = s[idx+end+1:]
	}
	return runs
}

// applySGRBackground updates whether a background is active given one SGR
// escape sequence. Recognizes reset (0 or empty), default background (49),
// the 40-47/100-107 color ranges, and extended 38/48 colors - whose trailing
// parameters must be consumed, or a "0" component of an RGB triple would be
// misread as a reset.
func applySGRBackground(bgSet bool, esc string) bool {
	params := strings.TrimSuffix(strings.TrimPrefix(esc, "\x1b["), "m")
	if params == "" {
		return false
	}
	parts := strings.Split(params, ";")
	for i := 0; i < len(parts); i++ {
		p := parts[i]
		switch {
		case p == "" || p == "0":
			bgSet = false
		case p == "38" || p == "48":
			// Extended color: "5;n" (256-color) or "2;r;g;b" (truecolor).
			if p == "48" {
				bgSet = true
			}
			if i+1 < len(parts) {
				switch parts[i+1] {
				case "5":
					i += 2
				case "2":
					i += 4
				default:
					i++
				}
			}
		case p == "49":
			bgSet = false
		case len(p) == 2 && p[0] == '4' && p[1] >= '0' && p[1] <= '7':
			bgSet = true
		case len(p) == 3 && strings.HasPrefix(p, "10") && p[2] >= '0' && p[2] <= '7':
			bgSet = true
		}
	}
	return bgSet
}
