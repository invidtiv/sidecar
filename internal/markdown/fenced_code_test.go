package markdown

import (
	"regexp"
	"sort"
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/filepreview"
)

// The rest of the theme tests assert the *config* (CodeBlock.Chroma == nil,
// CodeBlock.Theme == SyntaxTheme). These two assert the *behaviour* that config
// is supposed to buy, because the thing being defended against — Glamour giving
// its embedded Chroma table precedence over Theme — is a Glamour implementation
// detail that a dependency bump could reintroduce without any config change.

var indexedColor = regexp.MustCompile(`38;5;\d+`)

// indexedColors is the set of 256-colour foreground codes in a rendered string.
func indexedColors(s string) []string {
	seen := map[string]bool{}
	for _, m := range indexedColor.FindAllString(s, -1) {
		seen[m] = true
	}
	out := make([]string, 0, len(seen))
	for code := range seen {
		out = append(out, code)
	}
	sort.Strings(out)
	return out
}

func TestFencedCodeFollowsSyntaxTheme(t *testing.T) {
	const doc = "```go\npackage main\n\nfunc main() { println(\"hi\") }\n```\n"

	a := testPaletteA()
	a.SyntaxTheme = "monokai"
	b := testPaletteA()
	b.SyntaxTheme = "github-dark"

	r, err := NewRenderer()
	if err != nil {
		t.Fatal(err)
	}
	outA := strings.Join(r.renderContent(doc, 90, ThemeSnapshot{Palette: a}), "\n")
	outB := strings.Join(r.renderContent(doc, 90, ThemeSnapshot{Palette: b}), "\n")
	if outA == outB {
		t.Fatal("changing syntaxTheme did not change fenced-code output; " +
			"the preset's embedded Chroma table is winning over CodeBlock.Theme")
	}
}

// Fenced code must land on the same Chroma style as a raw file preview of the
// same source, which is the whole point of routing CodeBlock.Theme through the
// palette's syntaxTheme.
func TestFencedCodeMatchesRawFilePreviewHighlighting(t *testing.T) {
	const src = "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Println(\"hi\") }\n"

	for _, syntax := range []string{"monokai", "github-dark", "dracula"} {
		t.Run(syntax, func(t *testing.T) {
			pal := testPaletteA()
			pal.SyntaxTheme = syntax

			r, err := NewRenderer()
			if err != nil {
				t.Fatal(err)
			}
			rendered := strings.Join(r.renderContent("```go\n"+src+"```\n", 90, ThemeSnapshot{Palette: pal}), "\n")

			raw, err := filepreview.Highlight(src, ".go", syntax)
			if err != nil {
				t.Fatal(err)
			}
			want := indexedColors(raw)
			if len(want) == 0 {
				t.Fatalf("raw preview produced no indexed colours for %q", syntax)
			}
			got := indexedColors(rendered)
			gotSet := map[string]bool{}
			for _, c := range got {
				gotSet[c] = true
			}
			for _, c := range want {
				if !gotSet[c] {
					t.Errorf("fenced code is missing raw-preview colour %q (want %v, got %v)", c, want, got)
				}
			}
		})
	}
}
