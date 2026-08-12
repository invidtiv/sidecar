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
