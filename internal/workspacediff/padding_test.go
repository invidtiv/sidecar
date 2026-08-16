package workspacediff

import (
	"strings"
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
)

func paddedView() *View {
	v := &View{
		State: LoadStateReady,
		Files: []File{
			{Path: "alpha.go", Raw: "diff --git a/alpha.go b/alpha.go\n+one\n", Additions: 1},
			{Path: "beta.go", Raw: "diff --git a/beta.go b/beta.go\n-two\n", Deletions: 1},
		},
	}
	v.SetSize(160, 20)
	return v
}

// The Diff body keeps one column on each side, like the issue pane, so it does
// not sit flush against the neighbouring border.
func TestRenderInsetsContentByOneColumn(t *testing.T) {
	v := paddedView()
	out := v.Render(160, 20, RenderOpts{})
	for i, line := range strings.Split(out, "\n") {
		if line == "" {
			continue
		}
		if !strings.HasPrefix(line, " ") {
			t.Fatalf("line %d is flush against the left edge: %q", i, line)
		}
	}
}

func TestRenderKeepsLinesInsideTheLeafWidth(t *testing.T) {
	v := paddedView()
	const width = 160
	out := v.Render(width, 20, RenderOpts{})
	for i, line := range strings.Split(out, "\n") {
		if got := len([]rune(stripANSI(line))); got > width {
			t.Fatalf("line %d is %d cells wide, want <= %d", i, got, width)
		}
	}
}

// Hit regions must move with the drawn content, or a click lands one column
// off the row it points at.
func TestFileHitsFollowTheContentInset(t *testing.T) {
	v := paddedView()
	leaf := mouse.Rect{X: 10, Y: 4, W: 160, H: 20}
	hits := v.FileHits(leaf)
	if len(hits) == 0 {
		t.Fatal("no hits")
	}
	for _, hit := range hits {
		if hit.Rect.X < leaf.X+ContentInset {
			t.Fatalf("hit %s starts at %d, inside the left padding (leaf X %d)", hit.ID, hit.Rect.X, leaf.X)
		}
		if hit.Rect.X+hit.Rect.W > leaf.X+leaf.W-ContentInset {
			t.Fatalf("hit %s ends at %d, inside the right padding (leaf ends %d)", hit.ID, hit.Rect.X+hit.Rect.W, leaf.X+leaf.W)
		}
	}
}

// The divider is drawn at the list's right edge inside the padded body, and the
// drag target has to sit on it.
func TestDividerHitMatchesTheDrawnDivider(t *testing.T) {
	v := paddedView()
	leaf := mouse.Rect{X: 10, Y: 4, W: 160, H: 20}
	hit := v.DividerHit(leaf)
	wantX := leaf.X + ContentInset + v.EffectiveListWidth(leaf.W)
	if hit.X != wantX {
		t.Fatalf("divider hit X = %d, want %d", hit.X, wantX)
	}

	out := v.Render(leaf.W, leaf.H, RenderOpts{})
	first := stripANSI(strings.Split(out, "\n")[0])
	drawn := strings.IndexRune(first, '┃')
	if drawn < 0 {
		t.Fatalf("no handle drawn in %q", first)
	}
	if drawn != hit.X-leaf.X {
		t.Fatalf("divider drawn at column %d but hit registered at %d", drawn, hit.X-leaf.X)
	}
}

// stripANSI removes SGR sequences so a test can count cells.
func stripANSI(s string) string {
	var out strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] == 0x1b {
			for i < len(s) && s[i] != 'm' {
				i++
			}
			continue
		}
		out.WriteByte(s[i])
	}
	return out.String()
}
