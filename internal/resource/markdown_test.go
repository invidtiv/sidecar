package resource

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/markdown"
)

func TestSafeMarkdownSource(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"inline link", "[click me](javascript:alert(1))", "click me"},
		{"https link keeps label only", "[docs](https://example.test/a)", "docs"},
		{"image becomes alt text", "![alt text](file:///etc/passwd)", "alt text"},
		{"image with no alt", "![](https://x.test/a.png)", ""},
		{"autolink is escaped", "<https://example.test>", "\\<https://example.test>"},
		{"raw html is escaped", "<script>x</script>", "\\<script>x\\</script>"},
		{"html comment is escaped", "<!-- hi -->", "\\<!-- hi -->"},
		{"nested emphasis in label", "[**bold** link](ssh://h/x)", "**bold** link"},
		{"parens in destination", "[a](https://x.test/a(b)c)", "a"},
		{"collapsed reference", "[label][]\n\n[label]: data:text/html,x", "label"},
		{"full reference", "[label][r]\n\n[r]: ssh://h/x", "label"},
		{"shortcut reference with definition", "[r]\n\n[r]: https://x.test", "r"},
		{"bracketed prose with no definition survives", "see [note] here", "see [note] here"},
		{"code span is untouched", "`[a](b)`", "`[a](b)`"},
		{"unmatched bracket is literal", "a [ b", "a [ b"},
		{"escaped bracket is literal", `\[a](b)`, `\[a](b)`},
		{"bare url is left visible", "see https://example.test now", "see https://example.test now"},
		{"plain text passes through", "just words", "just words"},
		{"empty", "", ""},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := SafeMarkdownSource(tc.in); got != tc.want {
				t.Fatalf("SafeMarkdownSource(%q) = %q, want %q", tc.in, got, tc.want)
			}
		})
	}
}

func TestSafeMarkdownSourceKeepsFencedCode(t *testing.T) {
	in := "before\n\n```go\nx := \"[a](b)\"\nif x < y {}\n```\n\nafter [l](https://x.test)"
	got := SafeMarkdownSource(in)
	if !strings.Contains(got, `x := "[a](b)"`) {
		t.Fatalf("fenced code rewritten: %q", got)
	}
	if !strings.Contains(got, "if x < y {}") {
		t.Fatalf("fenced code escaped: %q", got)
	}
	if strings.Contains(got, "https://x.test") {
		t.Fatalf("link destination survived outside the fence: %q", got)
	}
}

// The end-to-end property: whatever the provider writes, no rendered line may
// carry an escape sequence, and no rendered line may carry a destination the
// provider chose.
func TestRenderSafeMarkdownIsInert(t *testing.T) {
	renderer, err := markdown.NewRenderer()
	if err != nil {
		t.Fatalf("NewRenderer: %v", err)
	}
	// hidden lists destinations that appear only as a link target in the
	// source. Those must not reach the rendered output at all. A destination
	// the author typed as visible text (an autolink, a bare URL) is allowed to
	// stay visible — what matters is that it is no longer clickable, which the
	// OSC assertion covers for every case.
	cases := []struct {
		in     string
		hidden []string
	}{
		{"[click me](javascript:alert(1))", []string{"javascript:"}},
		{"[click me](https://evil.test/steal)", []string{"evil.test"}},
		{"<https://evil.test/steal>", nil},
		{"![alt](file:///etc/passwd)", []string{"file://", "passwd"}},
		{"![alt](data:text/html;base64,PHNjcmlwdD4=)", []string{"data:text", "base64"}},
		{"[ref][r]\n\n[r]: ssh://evil.test/x", []string{"ssh://", "evil.test"}},
		{"<b>bold</b><script>alert(1)</script>", nil},
		{"<a href=\"https://evil.test\">x</a>", nil},
		{"[malformed](", nil},
		{"[malformed](unclosed", nil},
		{"plain \x1b]8;;https://evil.test\x1b\\osc\x1b]8;;\x1b\\ text", []string{"evil.test"}},
		{"control \x07 bytes \x1b[31m here", nil},
		{"# Heading\n\n- item [a](https://evil.test)\n- item two", []string{"evil.test"}},
	}
	for _, tc := range cases {
		// Provider bodies always arrive through SanitizeBody first.
		body := SanitizeBody(tc.in, MaxBodyBytes)
		lines := RenderSafeMarkdown(renderer, body, 80)
		joined := strings.Join(lines, "\n")
		// SGR styling is the renderer's own and stays. An OSC introducer or a
		// BEL terminator is what would carry a provider destination, and none
		// may survive.
		for _, intro := range []string{"\x1b]", "\x9d", "\x07", "\xc2\x9d"} {
			if strings.Contains(joined, intro) {
				t.Fatalf("input %q rendered an OSC sequence: %q", tc.in, joined)
			}
		}
		for _, bad := range tc.hidden {
			if strings.Contains(joined, bad) {
				t.Fatalf("input %q leaked hidden destination %q: %q", tc.in, bad, joined)
			}
		}
	}
}

func TestRenderSafeMarkdownWithoutRenderer(t *testing.T) {
	lines := RenderSafeMarkdown(nil, "a [b](https://evil.test) c", 40)
	joined := strings.Join(lines, " ")
	if strings.Contains(joined, "evil.test") {
		t.Fatalf("fallback wrapping leaked a destination: %q", joined)
	}
}

func TestStripRenderedOSC(t *testing.T) {
	got := StripRenderedOSC([]string{"a\x1b]8;;https://x.test\x1b\\b\x1b]8;;\x1b\\"})
	if got[0] != "ab" {
		t.Fatalf("StripRenderedOSC = %q", got[0])
	}
}
