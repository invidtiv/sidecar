package gitstatus

import (
	"charm.land/lipgloss/v2"
	"fmt"
	"strings"
	"testing"
)

// jsonlDiff builds a unified diff that looks like a real JSONL change: many
// lines, each a long single-line JSON record.
func jsonlDiff(lines, recordFields int) string {
	record := func(seed int) string {
		var sb strings.Builder
		sb.WriteString(`{"id":`)
		fmt.Fprintf(&sb, "%d", seed)
		for i := 0; i < recordFields; i++ {
			fmt.Fprintf(&sb, `,"field_%d":"value_%d_payload"`, i, i+seed)
		}
		sb.WriteString("}")
		return sb.String()
	}

	var sb strings.Builder
	sb.WriteString("diff --git a/data.jsonl b/data.jsonl\n--- a/data.jsonl\n+++ b/data.jsonl\n")
	fmt.Fprintf(&sb, "@@ -1,%d +1,%d @@\n", lines, lines)
	for i := 0; i < lines; i++ {
		switch i % 4 {
		case 0:
			sb.WriteString("-" + record(i) + "\n")
			sb.WriteString("+" + record(i+1) + "\n")
		default:
			sb.WriteString(" " + record(i) + "\n")
		}
	}
	return sb.String()
}

func benchRender(b *testing.B, lines, fields int, mode DiffViewMode) {
	b.Helper()
	raw := jsonlDiff(lines, fields)
	parsed, err := ParseUnifiedDiff(raw)
	if err != nil {
		b.Fatal(err)
	}
	h := NewSyntaxHighlighter("data.jsonl")
	b.ReportMetric(float64(len(raw))/1024, "KB")
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		// Simulate scrolling: a different viewport offset each frame.
		start := (i * 7) % 200
		switch mode {
		case DiffViewSideBySide:
			RenderSideBySide(parsed, 200, start, 50, 0, h, false)
		default:
			RenderLineDiff(parsed, 200, start, 50, 0, h, false)
		}
		GetSideBySideClipInfo(parsed, 90, 0)
	}
}

func BenchmarkRenderUnifiedJSONL(b *testing.B)    { benchRender(b, 2000, 40, DiffViewUnified) }
func BenchmarkRenderSideBySideJSONL(b *testing.B) { benchRender(b, 2000, 40, DiffViewSideBySide) }

// A single minified JSON blob: one enormous line.
func BenchmarkRenderMinifiedJSON(b *testing.B) { benchRender(b, 8, 4000, DiffViewUnified) }

func TestTruncateLineWidths(t *testing.T) {
	cases := []struct {
		in       string
		maxWidth int
		want     string
	}{
		{"short", 10, "short"},
		{"exactly-10", 10, "exactly-10"},
		{"a longer line than fits", 10, "a longer l"[:7] + "..."},
		{"日本語のテキスト", 7, "日本..."},
		{"日本語のテキスト", 3, "日本語"},
		{"abcdef", 2, "ab"},
		{"abcdef", 0, ""},
		{"", 5, ""},
	}
	for _, tc := range cases {
		if got := truncateLine(tc.in, tc.maxWidth); got != tc.want {
			t.Errorf("truncateLine(%q, %d) = %q, want %q", tc.in, tc.maxWidth, got, tc.want)
		}
		if tc.maxWidth <= 3 {
			// Below ellipsis width the result is a rune count, not a cell
			// count, so wide runes may overflow. Longstanding behavior.
			continue
		}
		if got := truncateLine(tc.in, tc.maxWidth); lipgloss.Width(got) > tc.maxWidth {
			t.Errorf("truncateLine(%q, %d) = %q exceeds max width", tc.in, tc.maxWidth, got)
		}
	}
}
