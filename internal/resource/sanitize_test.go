package resource

import (
	"strings"
	"testing"
	"time"
)

func TestSanitizeLine(t *testing.T) {
	tests := []struct {
		name string
		in   string
		max  int
		want string
	}{
		{"plain", "Refund totals differ", 300, "Refund totals differ"},
		{"trims", "  spaced  ", 300, "spaced"},
		{"strips C0", "a\x01b\x1fc", 300, "abc"},
		{"strips DEL", "a\x7fb", 300, "ab"},
		{"strips C1", "a\u009bb", 300, "ab"},
		{"newline becomes space", "a\nb", 300, "a b"},
		{"tab becomes space", "a\tb", 300, "a b"},
		{"strips OSC8", "before\x1b]8;;https://evil.test\x1b\\link\x1b]8;;\x1b\\after", 300, "beforelinkafter"},
		{"strips bel-terminated OSC", "x\x1b]0;title\x07y", 300, "xy"},
		{"truncates by rune", "ααααα", 3, "ααα"},
		{"empty stays empty", "", 300, ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SanitizeLine(tc.in, tc.max); got != tc.want {
				t.Fatalf("SanitizeLine(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSanitizeLineReplacesInvalidUTF8(t *testing.T) {
	got := SanitizeLine("ab\xffcd", 300)
	if !strings.Contains(got, replacementRune) {
		t.Fatalf("invalid UTF-8 not replaced: %q", got)
	}
	if strings.Contains(got, "\xff") {
		t.Fatalf("raw invalid byte survived: %q", got)
	}
}

func TestSanitizeBody(t *testing.T) {
	got := SanitizeBody("line one\r\nline two\ttabbed\x00\x1b[31mred", MaxBodyBytes)
	want := "line one\nline two\ttabbed[31mred"
	if got != want {
		t.Fatalf("SanitizeBody = %q, want %q", got, want)
	}
}

func TestSanitizeBodyBoundsBytesOnRuneBoundary(t *testing.T) {
	in := strings.Repeat("é", 100) // 200 bytes
	got := SanitizeBody(in, 51)
	if len(got) > 51 {
		t.Fatalf("body not bounded: %d bytes", len(got))
	}
	if !isValidUTF8(got) {
		t.Fatalf("truncation split a rune: %q", got)
	}
}

func isValidUTF8(s string) bool { return strings.ToValidUTF8(s, "?") == s }

func TestSanitizeURL(t *testing.T) {
	tests := []struct {
		in   string
		want string
	}{
		{"https://jira.example.test/browse/CASH-1245", "https://jira.example.test/browse/CASH-1245"},
		{"http://example.test/x", "http://example.test/x"},
		{"HTTPS://Example.test/x", "HTTPS://Example.test/x"},
		{"file:///etc/passwd", ""},
		{"ssh://host/repo", ""},
		{"data:text/html,<script>", ""},
		{"javascript:alert(1)", ""},
		{"//example.test/x", ""},
		{"not a url at all", ""},
		{"https://", ""},
		{"", ""},
		{"https://example.test/\x1b]8;;x\x1b\\", ""},
		{"https://example.test/a b", ""},
		{" https://example.test/x", ""},
	}
	for _, tc := range tests {
		if got := SanitizeURL(tc.in); got != tc.want {
			t.Fatalf("SanitizeURL(%q) = %q, want %q", tc.in, got, tc.want)
		}
	}
}

func TestSanitizeURLLengthBound(t *testing.T) {
	long := "https://example.test/" + strings.Repeat("a", MaxURLChars)
	if got := SanitizeURL(long); got != "" {
		t.Fatalf("oversize URL accepted (%d chars)", len(got))
	}
}

func TestClampResolveTimeout(t *testing.T) {
	cases := []struct{ in, want time.Duration }{
		{0, DefaultResolveTimeout},
		{-time.Second, DefaultResolveTimeout},
		{time.Millisecond, MinResolveTimeout},
		{MinResolveTimeout / 2, MinResolveTimeout},
		{5 * time.Second, 5 * time.Second},
		{15 * time.Second, 15 * time.Second},
		{MaxResolveTimeout, MaxResolveTimeout},
		{MaxResolveTimeout + 1, MaxResolveTimeout},
		{10 * time.Minute, MaxResolveTimeout},
	}
	for _, tc := range cases {
		if got := ClampResolveTimeout(tc.in); got != tc.want {
			t.Fatalf("ClampResolveTimeout(%s) = %s, want %s", tc.in, got, tc.want)
		}
	}
}

func TestClampFreshFor(t *testing.T) {
	if got := ClampFreshFor(0); got != DefaultFreshFor {
		t.Fatalf("zero hint = %s, want %s", got, DefaultFreshFor)
	}
	if got := ClampFreshFor(-5); got != DefaultFreshFor {
		t.Fatalf("negative hint = %s, want %s", got, DefaultFreshFor)
	}
	if got := ClampFreshFor(30); got != 30*time.Second {
		t.Fatalf("30s hint = %s", got)
	}
	if got := ClampFreshFor(1e9); got != MaxFreshFor {
		t.Fatalf("absurd hint = %s, want %s", got, MaxFreshFor)
	}
}
