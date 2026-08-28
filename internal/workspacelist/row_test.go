package workspacelist

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
)

func TestRowMarkersShareTheResolvedVisualVocabulary(t *testing.T) {
	cases := []struct {
		name   string
		marker RowMarker
		want   string
	}{
		{"working", RowMarker{Icon: "●", Lane: "working"}, "●"},
		{"blocked", RowMarker{Icon: "◆", Lane: "blocked"}, "◆"},
		{"done", RowMarker{Icon: "✓", Lane: "done"}, "✓"},
		{"idle", RowMarker{Icon: "○", Lane: "idle"}, "○"},
		{"unknown", RowMarker{Icon: "?", Lane: "paused"}, "?"},
		{"ambiguous", RowMarker{Icon: "?", Tone: MarkerWarning}, "?"},
		{"missing", RowMarker{Icon: "✗", Tone: MarkerError}, "✗"},
		{"orphan", RowMarker{Icon: "⚠", Tone: MarkerWarning}, "⚠"},
		{"plain live", RowMarker{Icon: "◎", Tone: MarkerLive}, "◎"},
		{"main", RowMarker{Icon: "◉", Tone: MarkerMain}, "◉"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := ansi.Strip(strings.Join(RenderRow(RowPresentation{Marker: tc.marker, Name: "workspace"}, 40, false, true), "\n"))
			if !strings.HasPrefix(got, " "+tc.want+" workspace") {
				t.Fatalf("row = %q, want leading %q", got, tc.want)
			}
		})
	}
}

func TestSelectedRowNameKeepsSelectionBackground(t *testing.T) {
	row := RowPresentation{
		Marker:         RowMarker{Icon: "◎", Tone: MarkerLive},
		Name:           "workspace sidebar plan",
		BeforeProvider: []RowField{PlainField("shell")},
		AfterProvider:  []RowField{PlainField("live")},
	}
	for _, focused := range []bool{true, false} {
		lines := RenderRow(row, 46, true, focused)
		if len(lines) != 2 {
			t.Fatalf("focused=%v lines = %d, want 2", focused, len(lines))
		}
		// The previous wrap put a full reset after the marker, so the name sat
		// on the pane background while the detail line kept the fill.
		if strings.Contains(lines[0], "\x1b[m workspace sidebar plan") {
			t.Fatalf("focused=%v name follows a full reset with no selection style: %q", focused, lines[0])
		}
		if !strings.Contains(ansi.Strip(lines[0]), "workspace sidebar plan") {
			t.Fatalf("focused=%v lost name: %q", focused, lines[0])
		}
		if !segmentHasBackground(lines[0], "workspace sidebar plan") {
			t.Fatalf("focused=%v name has no selection background: %q", focused, lines[0])
		}
		if !segmentHasBackground(lines[1], "shell") {
			t.Fatalf("focused=%v detail line lost the fill: %q", focused, lines[1])
		}
		if !strings.Contains(lines[0], "◎") {
			t.Fatalf("focused=%v lost marker: %q", focused, lines[0])
		}
	}

	narrow := RenderRow(row, 20, true, true)
	if len(narrow) != 1 {
		t.Fatalf("narrow selected row lines = %d, want 1", len(narrow))
	}
	if strings.Contains(narrow[0], "\x1b[m workspac") || !segmentHasBackground(narrow[0], "workspac") {
		t.Fatalf("narrow selected name lost the fill: %q", narrow[0])
	}

	withKind := row
	withKind.Kind = KindShell
	kindLines := RenderRow(withKind, 46, true, true)
	if !segmentHasBackground(kindLines[0], "workspace sidebar plan") {
		t.Fatalf("selected name after kind glyph lost the fill: %q", kindLines[0])
	}
}

func TestCardTintCoversBothLinesAndSelectionOverridesIt(t *testing.T) {
	row := RowPresentation{
		Marker:        RowMarker{Icon: "●", Lane: "working"},
		Kind:          KindWorktree,
		Name:          "workspace cards",
		Provider:      "codex",
		AfterProvider: []RowField{PlainField("working")},
	}
	unselected := RenderRow(row, 46, false, true)
	if len(unselected) != 2 {
		t.Fatalf("unselected row has %d lines, want 2", len(unselected))
	}
	for _, target := range []struct {
		line int
		text string
	}{
		{0, "●"},         // nested marker foreground
		{0, "workspace"}, // content after the marker reset
		{1, "working"},   // content after the provider chip reset
	} {
		if !segmentHasBackground(unselected[target.line], target.text) {
			t.Fatalf("card line %d segment %q has no background: %q", target.line, target.text, unselected[target.line])
		}
	}

	cardBackground := segmentBackground(unselected[0], "workspace")
	if cardBackground == "" {
		t.Fatal("unselected card has no identifiable background")
	}
	for _, focused := range []bool{true, false} {
		selected := RenderRow(row, 46, true, focused)
		if len(selected) != 2 {
			t.Fatalf("focused=%v selected row has %d lines, want 2", focused, len(selected))
		}
		for line, target := range []string{"workspace", "working"} {
			got := segmentBackground(selected[line], target)
			if got == "" || got == cardBackground {
				t.Fatalf("focused=%v selected line %d did not override the card tint: card=%q selected=%q line=%q", focused, line, cardBackground, got, selected[line])
			}
		}
	}
}

func segmentHasBackground(styled, text string) bool {
	return segmentBackground(styled, text) != ""
}

func segmentBackground(styled, text string) string {
	plain := ansi.Strip(styled)
	at := strings.Index(plain, text)
	if at < 0 {
		return ""
	}
	target := at
	pos := 0
	bg := ""
	i := 0
	for i < len(styled) {
		if styled[i] != 0x1b {
			if pos == target {
				return bg
			}
			pos++
			i++
			continue
		}
		j := i + 1
		for j < len(styled) && styled[j] != 'm' {
			j++
		}
		if j >= len(styled) {
			return ""
		}
		code := styled[i : j+1]
		if code == "\x1b[m" || code == "\x1b[0m" {
			bg = ""
		} else if strings.Contains(code, "48;") {
			bg = code
		}
		i = j + 1
	}
	return ""
}

func TestRowProviderChipAndSelectedLabelTreatment(t *testing.T) {
	row := RowPresentation{Marker: RowMarker{Icon: "●", Lane: "working"}, Name: "feature", Provider: "codex", AfterProvider: []RowField{PlainField("working")}}
	unselected := strings.Join(RenderRow(row, 46, false, true), "\n")
	if chip := styles.RenderAgentChip("codex"); chip == "" || !strings.Contains(unselected, chip) {
		t.Fatalf("unselected row lacks shared provider chip: %q", unselected)
	}
	selected := strings.Join(RenderRow(row, 46, true, true), "\n")
	if !strings.Contains(ansi.Strip(selected), styles.AgentLabel("codex")) {
		t.Fatalf("selected row lacks bare provider label: %q", selected)
	}
	if chip := styles.RenderAgentChip("codex"); chip != "" && strings.Contains(selected, chip) {
		t.Fatalf("selected row retained raised provider chip: %q", selected)
	}
}

func TestKindGlyphMatchesTheAgentsBoardPair(t *testing.T) {
	if got := KindGlyph(KindWorktree); got != "⑂" {
		t.Fatalf("worktree glyph = %q, want ⑂", got)
	}
	if got := KindGlyph(KindShell); got != "❯" {
		t.Fatalf("shell glyph = %q, want ❯", got)
	}
	if got := KindGlyph(""); got != "" {
		t.Fatalf("empty kind should render nothing, got %q", got)
	}
}

func TestTwoLineRowPutsKindProjectNameAgeThenAgent(t *testing.T) {
	row := RowPresentation{
		Marker:     RowMarker{Icon: "●", Lane: "working"},
		Kind:       KindWorktree,
		NamePrefix: PlainField("sidecar "),
		Name:       "review td-196c42",
		Age:        "1m",
		Provider:   "grok",
		AfterProvider: []RowField{
			PlainField("working"),
			PlainField("td-196c42"),
		},
	}
	lines := RenderRow(row, 56, false, true)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	line1 := ansi.Strip(lines[0])
	line2 := ansi.Strip(lines[1])
	if !strings.Contains(line1, "sidecar review td-196c42") {
		t.Fatalf("line 1 lost project+name: %q", line1)
	}
	if !strings.Contains(line1, "1m") {
		t.Fatalf("line 1 lost age: %q", line1)
	}
	if !strings.HasPrefix(strings.TrimRight(line1, " "), " ● ⑂ sidecar") {
		t.Fatalf("line 1 lost the marker + kind gutter before the project: %q", line1)
	}
	if strings.Contains(line2, "⑂") {
		t.Fatalf("kind glyph belongs on line 1, not line 2: %q", line2)
	}
	if !strings.Contains(line2, "grok") {
		t.Fatalf("line 2 lost agent: %q", line2)
	}
	if idxName, idxAge := strings.Index(line1, "review"), strings.Index(line1, "1m"); idxAge < idxName {
		t.Fatalf("age must be right of the name on line 1: %q", line1)
	}
	// Agent detail hangs under the name, clear of the gutter it shares with line 1.
	if !strings.HasPrefix(line2, "     ") {
		t.Fatalf("line 2 is not indented under the name: %q", line2)
	}

	shell := row
	shell.Kind = KindShell
	shell.Name = "Shell 2"
	shell.NamePrefix = PlainField("braid ")
	shell.Marker = RowMarker{Icon: "◎", Tone: MarkerLive}
	shell.AfterProvider = nil
	shell.Provider = ""
	got := ansi.Strip(strings.Join(RenderRow(shell, 56, false, true), "\n"))
	shellLines := strings.Split(got, "\n")
	if !strings.Contains(shellLines[0], "❯ braid Shell 2") {
		t.Fatalf("shell line 1 lost kind glyph + project + name: %q", shellLines[0])
	}
	// Nothing but the kind and identity: an agentless shell owes no second line.
	if len(shellLines) > 1 && strings.TrimSpace(shellLines[1]) != "" {
		t.Fatalf("agentless shell drew a second line: %q", shellLines[1])
	}
}

func TestPinnedMarkSitsOnLineTwoAndDoesNotStealTheMarker(t *testing.T) { //nolint:dupword
	row := RowPresentation{
		Marker: RowMarker{Icon: "●", Lane: "working"},
		Kind:   KindWorktree,
		Name:   "review",
		Pinned: true,
	}
	lines := RenderRow(row, 40, false, true)
	if len(lines) != 2 {
		t.Fatalf("lines = %d, want 2", len(lines))
	}
	line1, line2 := ansi.Strip(lines[0]), ansi.Strip(lines[1])
	if !strings.HasPrefix(strings.TrimRight(line1, " "), " ● ⑂ review") {
		t.Fatalf("pin stole the marker or kind gutter: %q", line1)
	}
	if !strings.Contains(line2, "*") {
		t.Fatalf("line 2 lost the pin mark: %q", line2)
	}
}

func TestKindGlyphStaysInTheGutterWhenNarrow(t *testing.T) {
	row := RowPresentation{
		Marker:        RowMarker{Icon: "●", Lane: "working"},
		Kind:          KindWorktree,
		Name:          "feature",
		NamePrefix:    PlainField("sidecar "),
		Provider:      "codex",
		AfterProvider: []RowField{PlainField("working")},
	}
	plain := ansi.Strip(strings.Join(RenderRow(row, 20, false, true), "\n"))
	if !strings.HasPrefix(plain, " ● ⑂ ") || !strings.Contains(plain, "feat") {
		t.Fatalf("narrow row lost gutter+name: %q", plain)
	}
	if strings.Contains(plain, "sidecar") {
		t.Fatalf("narrow row promoted project over name: %q", plain)
	}
}

func TestRowWidthsAndNarrowPriorityAreANSISafe(t *testing.T) {
	row := RowPresentation{
		Marker: RowMarker{Icon: "◆", Lane: "blocked"}, Name: "a very long résumé workspace", Age: "12m",
		BeforeProvider: []RowField{{Text: "sidecar", Rendered: styles.RenderAgentLabel("claude")}},
		Provider:       "codex", AfterProvider: []RowField{PlainField("blocked"), PlainField("feature/résumé")},
	}
	for _, width := range []int{20, 33, 34, 46} {
		for _, selected := range []bool{false, true} {
			lines := RenderRow(row, width, selected, true)
			wantLines := 2
			if width < twoLineWidth {
				wantLines = 1
			}
			if len(lines) != wantLines {
				t.Fatalf("width %d selected=%v returned %d lines, want %d", width, selected, len(lines), wantLines)
			}
			for _, line := range lines {
				if got := ansi.StringWidth(line); got != width {
					t.Fatalf("width %d selected=%v line has ANSI width %d: %q", width, selected, got, line)
				}
			}
			plain := ansi.Strip(lines[0])
			if !strings.HasPrefix(plain, " ◆ ") || !strings.Contains(plain, "a ") {
				t.Fatalf("width %d selected=%v lost marker/name priority: %q", width, selected, plain)
			}
		}
	}
}

// A row that collapses to one line still gets the full-width selection fill and
// still keeps its marker's own colour. The two changes meet here: the collapse
// removed a physical line, and the fill/marker treatment is per-line, so a
// collapsed row must not fall back to the plain unselected path.
func TestCollapsedRowKeepsSelectionFillAndMarkerColour(t *testing.T) {
	// A plain live shell: nothing for line two to say.
	row := RowPresentation{
		Marker: RowMarker{Icon: "◎", Tone: MarkerLive},
		Kind:   KindShell,
		Name:   "scratch",
		Age:    "3m",
	}
	lines := RenderRow(row, 44, true, true)
	if len(lines) != 1 {
		t.Fatalf("collapsed row rendered %d lines, want 1: %q", len(lines), lines)
	}
	if got := ansi.StringWidth(lines[0]); got != 44 {
		t.Fatalf("collapsed selected row is %d cells wide, want the full 44", got)
	}
	selected := lines[0]
	if !strings.Contains(selected, "\x1b[") {
		t.Fatal("collapsed selected row carries no styling at all")
	}
	// The same row unselected must differ — otherwise the fill is not being
	// painted and the test above would pass on width alone.
	if unselected := RenderRow(row, 44, false, false); unselected[0] == selected {
		t.Fatal("collapsed row renders identically selected and unselected")
	}
	// The marker keeps its live colour rather than inheriting the fill's
	// foreground, which is what makes a working row keep breathing under the
	// cursor.
	if !strings.Contains(selected, ansi.Strip("◎")) {
		t.Fatalf("collapsed row lost its marker: %q", selected)
	}
}
