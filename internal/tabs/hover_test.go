package tabs

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/ui"
)

func hoverLabels() []Label {
	return []Label{{Text: "alpha.go"}, {Text: "beta.go"}, {Text: "gamma.go"}}
}

// Hover is paint, not layout: the row must keep its width and its glyphs, or
// tabs would shift under the pointer that is hovering them.
func TestHoverCloseKeepsTheRowIntact(t *testing.T) {
	strip := LayoutStrip(hoverLabels(), 0, 60, true, nil)
	hovered := strip.HoverClose(1)
	if lipgloss.Width(hovered.Row) != lipgloss.Width(strip.Row) {
		t.Fatalf("hover reflowed the row: %d want %d", lipgloss.Width(hovered.Row), lipgloss.Width(strip.Row))
	}
	if got, want := ansi.Strip(hovered.Row), ansi.Strip(strip.Row); got != want {
		t.Fatalf("hover changed the glyphs:\n got %q\nwant %q", got, want)
	}
	if hovered.Row == strip.Row {
		t.Fatal("hover painted nothing")
	}
	if strings.Count(ansi.Strip(hovered.Row), ui.CloseButtonLabel) != 3 {
		t.Fatalf("close glyphs lost: %q", ansi.Strip(hovered.Row))
	}
}

// The hits are the click targets already registered this frame; repainting
// under the pointer must not move them.
func TestHoverCloseLeavesHitsAlone(t *testing.T) {
	strip := LayoutStrip(hoverLabels(), 0, 60, true, nil)
	hovered := strip.HoverClose(2)
	if len(hovered.Tabs) != len(strip.Tabs) {
		t.Fatalf("hover changed hit count: %d want %d", len(hovered.Tabs), len(strip.Tabs))
	}
	for i, tab := range hovered.Tabs {
		if tab.CloseCol != strip.Tabs[i].CloseCol || tab.CloseW != strip.Tabs[i].CloseW {
			t.Fatalf("hover moved close hit %d: %+v want %+v", i, tab, strip.Tabs[i])
		}
	}
}

func TestHoverCloseIgnoresTabsItDoesNotPaint(t *testing.T) {
	strip := LayoutStrip(hoverLabels(), 0, 60, true, nil)
	for _, index := range []int{-1, 99} {
		if got := strip.HoverClose(index); got.Row != strip.Row {
			t.Fatalf("HoverClose(%d) painted something", index)
		}
	}
}

func TestCloseHoverAddressesOneLeaf(t *testing.T) {
	var none CloseHover
	if none.IndexFor(0) != -1 || none.Index0For() != -1 {
		t.Fatal("zero CloseHover claims a tab")
	}
	h := CloseHoverAt(7, 2)
	if h.IndexFor(7) != 2 {
		t.Fatalf("IndexFor(7) = %d, want 2", h.IndexFor(7))
	}
	if h.IndexFor(8) != -1 {
		t.Fatal("hover leaked to another leaf")
	}
}
