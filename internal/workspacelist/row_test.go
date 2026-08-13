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

func TestTwoLineRowPutsProjectNameAgeThenKindAndAgent(t *testing.T) {
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
	if !strings.HasPrefix(strings.TrimRight(line1, " "), " ● ") {
		t.Fatalf("line 1 lost outdented status marker: %q", line1)
	}
	if !strings.Contains(line2, "⑂") {
		t.Fatalf("line 2 lost worktree glyph: %q", line2)
	}
	if !strings.Contains(line2, "grok") {
		t.Fatalf("line 2 lost agent: %q", line2)
	}
	if idxGlyph, idxAgent := strings.Index(line2, "⑂"), strings.Index(line2, "grok"); idxGlyph < 0 || idxAgent < idxGlyph {
		t.Fatalf("kind glyph must precede the agent on line 2: %q", line2)
	}

	shell := row
	shell.Kind = KindShell
	shell.Name = "Shell 2"
	shell.NamePrefix = PlainField("braid ")
	shell.Marker = RowMarker{Icon: "◎", Tone: MarkerLive}
	shell.AfterProvider = nil
	got := ansi.Strip(strings.Join(RenderRow(shell, 56, false, true), "\n"))
	if !strings.Contains(got, "braid Shell 2") {
		t.Fatalf("shell line 1 lost project+name: %q", got)
	}
	if !strings.Contains(got, "❯") {
		t.Fatalf("shell line 2 lost kind glyph: %q", got)
	}
}

func TestPinnedMarkSitsOnLineTwoAndDoesNotStealTheMarker(t *testing.T) {
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
	if !strings.HasPrefix(strings.TrimRight(line1, " "), " ● review") {
		t.Fatalf("pin stole the status marker: %q", line1)
	}
	if !strings.Contains(line2, "⑂") || !strings.Contains(line2, "*") {
		t.Fatalf("line 2 lost kind or pin mark: %q", line2)
	}
}

func TestKindGlyphIsTheFirstNarrowSecondary(t *testing.T) {
	row := RowPresentation{
		Marker:        RowMarker{Icon: "●", Lane: "working"},
		Kind:          KindWorktree,
		Name:          "feature",
		NamePrefix:    PlainField("sidecar "),
		Provider:      "codex",
		AfterProvider: []RowField{PlainField("working")},
	}
	plain := ansi.Strip(strings.Join(RenderRow(row, 20, false, true), "\n"))
	if !strings.HasPrefix(plain, " ● ") || !strings.Contains(plain, "feature") {
		t.Fatalf("narrow row lost gutter+name: %q", plain)
	}
	if !strings.Contains(plain, "⑂") {
		t.Fatalf("narrow row dropped the kind glyph first: %q", plain)
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
