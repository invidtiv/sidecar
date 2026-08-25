package ui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/styles"
)

func TestRenderKeyChips_FooterStyleOneLine(t *testing.T) {
	line, regions := RenderKeyChips([]KeyChip{
		{Keys: "[enter/u]", Label: "Update", ID: "update"},
		{Keys: "[esc]", Label: "Close", ID: "cancel"},
	}, 0, "", "")

	stripped := ansiStrip(line)
	if !strings.Contains(stripped, "[enter/u]") || !strings.Contains(stripped, "Update") ||
		!strings.Contains(stripped, "[esc]") || !strings.Contains(stripped, "Close") {
		t.Fatalf("chips missing: %q", stripped)
	}
	if !strings.Contains(stripped, "Update") || !strings.Contains(strings.Join(strings.Fields(stripped), " "), "[enter/u] Update [esc] Close") {
		t.Errorf("unexpected chip layout: %q", stripped)
	}
	if !strings.Contains(line, styles.KeyHint.Render("[enter/u]")) {
		t.Error("keys must carry the shared KeyHint style verbatim")
	}
	if len(regions) != 2 || regions[0].ID != "update" || regions[1].ID != "cancel" {
		t.Fatalf("regions = %+v", regions)
	}
	if regions[1].OffsetX != regions[0].Width+2 {
		t.Errorf("second chip offset %d, want first width + 2", regions[1].OffsetX)
	}
	if lipgloss.Width(line) != regions[1].OffsetX+regions[1].Width {
		t.Errorf("line width disagrees with last region")
	}
}

func TestRenderKeyChips_StopsBeforeMaxWidth(t *testing.T) {
	chips := []KeyChip{
		{Keys: "[enter]", Label: "Quit & Restart", ID: "quit"},
		{Keys: "[esc]", Label: "Close", ID: "cancel"},
	}
	line, regions := RenderKeyChips(chips, 10, "", "")
	if strings.Contains(ansiStrip(line), "Close") || len(regions) != 1 {
		t.Errorf("narrow width should keep only the first chip: %q %+v", ansiStrip(line), regions)
	}
	full, fullRegions := RenderKeyChips(chips, 0, "", "")
	if len(fullRegions) != 2 {
		t.Fatalf("unlimited width keeps both: %q", ansiStrip(full))
	}
}

// Hovering or focusing a chip highlights it with the block buttons' hover and
// focus colours — and because the highlight swaps only colours, never padding,
// the line's geometry is identical across all three states.
func TestRenderKeyChips_HoverAndFocusHighlight(t *testing.T) {
	chips := []KeyChip{
		{Keys: "[enter/u]", Label: "Update", ID: "update"},
		{Keys: "[esc]", Label: "Close", ID: "close"},
	}
	plain, plainRegions := RenderKeyChips(chips, 0, "", "")
	hovered, hoverRegions := RenderKeyChips(chips, 0, "", "update")
	focused, focusRegions := RenderKeyChips(chips, 0, "update", "")

	if plain == hovered {
		t.Error("hovered chip must render differently")
	}
	if plain == focused {
		t.Error("focused chip must render differently")
	}
	if !strings.Contains(hovered, styles.KeyHint.Background(styles.ButtonHoverColor).Render("[enter/u]")) {
		t.Error("hover highlight must recolour the KeyHint chip with the shared button-hover colour")
	}
	if !strings.Contains(focused, styles.KeyHint.Background(styles.Primary).Bold(true).Render("[enter/u]")) {
		t.Error("focus highlight must recolour the chip like a focused block button")
	}
	// The unhighlighted neighbour keeps the verbatim footer style.
	if !strings.Contains(hovered, styles.KeyHint.Render("[esc]")) {
		t.Error("non-highlighted chips must keep the KeyHint style verbatim")
	}
	for name, r := range map[string][]KeyChipRegion{"plain": plainRegions, "hover": hoverRegions, "focus": focusRegions} {
		if len(r) != 2 || r[0].OffsetX != 0 || r[1].OffsetX != r[0].Width+2 {
			t.Errorf("%s: highlighting must not move the geometry: %+v", name, r)
		}
	}
}

// On an ultra-narrow line the leading chip's glyphs are truncated at the
// column edge; its hit rect must stop there too, so clicks and hovers past
// the visible text land on nothing.
func TestRenderKeyChips_TruncatedChipHasHonestHitRect(t *testing.T) {
	chips := []KeyChip{{Keys: "[enter/u]", Label: "Update", ID: "update"}}
	_, regions := RenderKeyChips(chips, 5, "", "")
	if len(regions) != 1 {
		t.Fatalf("leading chip always renders and registers: %+v", regions)
	}
	if regions[0].Width > 5 {
		t.Errorf("hit rect width %d exceeds the visible column budget 5", regions[0].Width)
	}
}

func ansiStrip(s string) string {
	out := s
	for {
		i := strings.IndexByte(out, '\x1b')
		if i < 0 {
			break
		}
		j := i
		for j < len(out) && !isFinalByte(out[j]) {
			j++
		}
		if j >= len(out) {
			j = len(out)
		} else {
			j++
		}
		out = out[:i] + out[j:]
	}
	return out
}

func isFinalByte(b byte) bool { return (b >= 'a' && b <= 'z') || (b >= 'A' && b <= 'Z') }
