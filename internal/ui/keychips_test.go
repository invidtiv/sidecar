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
	}, 0)

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
	line, regions := RenderKeyChips(chips, 10)
	if strings.Contains(ansiStrip(line), "Close") || len(regions) != 1 {
		t.Errorf("narrow width should keep only the first chip: %q %+v", ansiStrip(line), regions)
	}
	full, fullRegions := RenderKeyChips(chips, 0)
	if len(fullRegions) != 2 {
		t.Fatalf("unlimited width keeps both: %q", ansiStrip(full))
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
