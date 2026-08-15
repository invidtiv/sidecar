package modal

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	"charm.land/lipgloss/v2"
)

// TestModalBackgroundIsUniform is the regression guard for the splotchy-modal
// bug. Nested styled elements (buttons, key hints, list selections) emit SGR
// resets mid-line; if the background is not re-applied after them, the rest of
// the line - including the modal's own padding - renders on the terminal
// default, producing the mismatched blocks visible inside modals.
//
// Every cell strictly inside the modal border must carry a background.
func TestModalBackgroundIsUniform(t *testing.T) {
	checked := false

	newInput := func(value string) *textinput.Model {
		ti := textinput.New()
		ti.SetValue(value)
		return &ti
	}

	selected := 0
	toggled := true

	tests := []struct {
		name  string
		build func() *Modal
	}{
		{"buttons", func() *Modal {
			return New("Confirm", WithWidth(44)).
				AddSection(Text("Delete this worktree?")).
				AddSection(Spacer()).
				AddSection(Buttons(Btn("Delete", "delete", BtnDanger()), Btn("Cancel", "cancel")))
		}},
		{"list", func() *Modal {
			return New("Switch Project", WithWidth(50)).
				AddSection(List("projects", []ListItem{
					{ID: "a", Label: "sidecar"},
					{ID: "b", Label: "td"},
					{ID: "c", Label: "braid"},
				}, &selected))
		}},
		{"input", func() *Modal {
			return New("Rename", WithWidth(50)).
				AddSection(InputWithLabel("name", "Name", newInput("Shell 10")))
		}},
		{"checkbox", func() *Modal {
			return New("Options", WithWidth(44)).
				AddSection(Checkbox("force", "Force push", &toggled)).
				AddSection(CheckboxDisplay("Include untracked", &toggled, "ctrl+a"))
		}},
		{"combo", func() *Modal {
			sel := 0
			ti := textinput.New()
			return New("Create", WithWidth(50)).
				AddSection(Combo("base", &ti, []DropdownItem{
					{ID: "a", Label: "main"},
					{ID: "b", Label: "develop"},
					{ID: "c", Label: "feature"},
				}, &sel)).
				AddSection(Buttons(Btn(" Create ", "create"), Btn(" Cancel ", "cancel")))
		}},
		{"no title", func() *Modal {
			return New("", WithWidth(40)).AddSection(Text("body only"))
		}},
		{"scrolling content", func() *Modal {
			m := New("Long", WithWidth(40))
			for i := range 60 {
				m.AddSection(Text(strings.Repeat("line ", i%5+1)))
			}
			return m
		}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out := tt.build().Render(100, 30, nil)
			assertModalInteriorHasBackground(t, out)
			checked = true
		})
	}

	if !checked {
		t.Fatal("no modal was rendered")
	}
}

// assertModalInteriorHasBackground checks every cell between the left and
// right border glyphs of each modal line.
func assertModalInteriorHasBackground(t *testing.T, render string) {
	t.Helper()

	for lineNo, line := range strings.Split(render, "\n") {
		plain := lipgloss.NewStyle().Render(line)
		if !strings.Contains(stripANSI(plain), "│") {
			continue // top/bottom border rows have no interior
		}

		col := 0
		bgSet := false
		interior := false
		for _, run := range splitSGR(line) {
			if run.isEscape {
				bgSet = applySGRBackground(bgSet, run.text)
				continue
			}
			for _, r := range run.text {
				switch {
				case r == '│' && !interior:
					interior = true
				case r == '│' && interior:
					interior = false
				case interior && !bgSet:
					t.Errorf("line %d column %d (%q) has no background:\n%q", lineNo, col, string(r), line)
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
// escape sequence. Extended 38/48 color parameters are consumed so that a "0"
// component of an RGB triple is not misread as a reset.
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

// stripANSI removes CSI escape sequences from a string.
func stripANSI(s string) string {
	var b strings.Builder
	for _, run := range splitSGR(s) {
		if !run.isEscape {
			b.WriteString(run.text)
		}
	}
	return b.String()
}
