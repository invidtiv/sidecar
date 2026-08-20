package uirequest

import (
	"testing"

	"github.com/marcus/sidecar/internal/terminallink"
)

func TestTargetFromSpan(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name string
		span terminallink.Span
		want Target
		ok   bool
	}{
		{
			name: "file prefers raw and carries line",
			span: terminallink.Span{Kind: terminallink.KindFile, Value: "docs/plan.md", Extra: terminallink.Extra{Raw: "./docs/plan.md", Line: 12}},
			want: Target{Kind: TargetKindFile, Value: "./docs/plan.md", Line: 12},
			ok:   true,
		},
		{
			name: "file without raw falls back to value",
			span: terminallink.Span{Kind: terminallink.KindFile, Value: "main.go"},
			want: Target{Kind: TargetKindFile, Value: "main.go"},
			ok:   true,
		},
		{
			name: "url",
			span: terminallink.Span{Kind: terminallink.KindURL, Value: "https://example.com"},
			want: Target{Kind: TargetKindURL, Value: "https://example.com"},
			ok:   true,
		},
		{
			name: "issue",
			span: terminallink.Span{Kind: terminallink.KindIssue, Value: "td-331dbf19"},
			want: Target{Kind: TargetKindIssue, Value: "td-331dbf19"},
			ok:   true,
		},
		{
			name: "diff prefers raw",
			span: terminallink.Span{Kind: terminallink.KindDiff, Value: "abc1234", Extra: terminallink.Extra{Raw: "HEAD~1"}},
			want: Target{Kind: TargetKindDiff, Value: "HEAD~1"},
			ok:   true,
		},
		{
			name: "resource is not mapped yet",
			span: terminallink.Span{Kind: terminallink.KindResource, Value: "CASH-1245"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, ok := TargetFromSpan(tc.span)
			if ok != tc.ok {
				t.Fatalf("ok = %v, want %v", ok, tc.ok)
			}
			if ok && got != tc.want {
				t.Fatalf("got %+v, want %+v", got, tc.want)
			}
		})
	}
}
