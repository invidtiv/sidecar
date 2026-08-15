package tabs

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

func truncateFit(text string, _, _, maxWidth int, _ bool) string {
	return ui.TruncateStart(text, maxWidth)
}

func TestLayoutStripEmptyAndNarrow(t *testing.T) {
	if got := LayoutStrip(nil, 0, 0, true, truncateFit); got.Row != "" || got.Tabs != nil {
		t.Fatalf("width 0 = %#v", got)
	}
	got := LayoutStrip(nil, 0, 6, true, truncateFit)
	if got.Row != "      " || len(got.Tabs) != 0 {
		t.Fatalf("empty labels = %#v", got)
	}
}

func TestLayoutStripHitGeometry(t *testing.T) {
	labels := []Label{{Text: "main.go"}, {Text: "README.md"}, {Text: "helper.go"}}
	strip := LayoutStrip(labels, 0, 80, true, truncateFit)
	if len(strip.Tabs) != 3 {
		t.Fatalf("hits = %d, want 3", len(strip.Tabs))
	}
	if strip.Tabs[0].Index != 0 || strip.Tabs[0].Col != 0 || strip.Tabs[0].Width <= 0 {
		t.Fatalf("first hit = %#v", strip.Tabs[0])
	}
	for i := 1; i < len(strip.Tabs); i++ {
		prev := strip.Tabs[i-1]
		hit := strip.Tabs[i]
		if hit.Index != i || hit.Width <= 0 {
			t.Fatalf("hit[%d] = %#v", i, hit)
		}
		if hit.Col <= prev.Col {
			t.Fatalf("hits not left-to-right: %d at %d, %d at %d", i-1, prev.Col, i, hit.Col)
		}
		if hit.Rendered == "" {
			t.Fatalf("hit[%d] missing rendered text", i)
		}
	}
}

func TestLayoutStripLeftoverGoesToActive(t *testing.T) {
	long := strings.Repeat("abcdefghij", 6)
	labels := []Label{{Text: long}, {Text: long}}
	strip := LayoutStrip(labels, 0, 40, true, truncateFit)
	if len(strip.Tabs) != 2 {
		t.Fatalf("hits = %d", len(strip.Tabs))
	}
	if strip.Tabs[0].Width <= strip.Tabs[1].Width {
		t.Fatalf("active width %d should exceed inactive %d", strip.Tabs[0].Width, strip.Tabs[1].Width)
	}
}

func TestLayoutStripOverflowMarkers(t *testing.T) {
	labels := make([]Label, 8)
	for i := range labels {
		labels[i] = Label{Text: "filename-that-is-long.go"}
	}
	strip := LayoutStrip(labels, 7, 36, true, truncateFit)
	row := ansi.Strip(strip.Row)
	if !strings.Contains(row, "<") {
		t.Fatalf("expected left overflow marker in %q", row)
	}
	if len(strip.Tabs) == 0 {
		t.Fatal("expected visible hits")
	}
	if strip.Tabs[len(strip.Tabs)-1].Index != 7 {
		t.Fatalf("active tab not visible: %+v", strip.Tabs)
	}
	for _, hit := range strip.Tabs {
		if hit.Col < 1 {
			t.Fatalf("overflow should shift hits right of '<': %#v", hit)
		}
	}

	first := LayoutStrip(labels, 0, 36, true, truncateFit)
	if !strings.Contains(ansi.Strip(first.Row), ">") {
		t.Fatalf("expected right overflow marker in %q", ansi.Strip(first.Row))
	}
	if first.Tabs[0].Index != 0 || first.Tabs[0].Col != 0 {
		t.Fatalf("first-tab overflow hit = %#v", first.Tabs[0])
	}
}

func TestLayoutStripPreviewFlag(t *testing.T) {
	preview := LayoutStrip([]Label{{Text: "main.go", Preview: true}}, 0, 40, true, truncateFit)
	plain := LayoutStrip([]Label{{Text: "main.go"}}, 0, 40, true, truncateFit)
	if len(preview.Tabs) != 1 || len(plain.Tabs) != 1 {
		t.Fatalf("hits preview=%d plain=%d", len(preview.Tabs), len(plain.Tabs))
	}
	if preview.Tabs[0].Rendered == plain.Tabs[0].Rendered {
		t.Fatal("preview flag should change RenderTab output")
	}
	want := styles.RenderTab("main.go", 0, 1, true, true)
	if !strings.Contains(preview.Tabs[0].Rendered, ansi.Strip(want)) && preview.Tabs[0].Rendered == "" {
		t.Fatalf("preview rendered empty")
	}
}
