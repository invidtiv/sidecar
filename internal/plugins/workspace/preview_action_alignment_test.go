package workspace

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/mouse"
)

// actionChipRegion is the registered hit region for one action chip.
func actionChipRegion(t *testing.T, p *Plugin, hit previewActionHit) mouse.Region {
	t.Helper()
	for _, r := range p.mouseHandler.HitMap.Regions() {
		if r.ID != regionPreviewAction {
			continue
		}
		if got, ok := r.Data.(previewActionHit); ok && got == hit {
			return r
		}
	}
	t.Fatalf("no action chip hit region for %v", hit)
	return mouse.Region{}
}

// diffChipText is the plain text of the Diff action chip, padding included.
func diffChipText(p *Plugin) string {
	return ansi.Strip(p.previewActionChips()[0])
}

// drawnColumn is the first cell of s on row y of a rendered view.
func drawnColumn(t *testing.T, view string, y int, s string) int {
	t.Helper()
	rows := strings.Split(view, "\n")
	if y < 0 || y >= len(rows) {
		t.Fatalf("row %d is outside the %d-row view", y, len(rows))
	}
	plain := ansi.Strip(rows[y])
	idx := strings.Index(plain, s)
	if idx < 0 {
		t.Fatalf("row %d %q does not contain %q", y, plain, s)
	}
	return ansi.StringWidth(plain[:idx])
}

// The Diff chip is right-aligned in the shell's header row, and its click
// target is where it was drawn.
func TestShellRowDiffChipIsRightAlignedAndClickable(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = false

	view := p.View(p.width, p.height)
	region := actionChipRegion(t, p, previewActionDiff)
	col := drawnColumn(t, view, region.Rect.Y, diffChipText(p))

	if col != region.Rect.X {
		t.Fatalf("Diff drawn at column %d but its hit region starts at %d", col, region.Rect.X)
	}
	nameCol := drawnColumn(t, view, region.Rect.Y, "Shell")
	if col <= nameCol+len("Shell")+2 {
		t.Fatalf("Diff at %d still hugs the shell name at %d; it should be right-aligned", col, nameCol)
	}
	if p.width-col > 12 {
		t.Fatalf("Diff at column %d is not near the right edge of a %d-cell view", col, p.width)
	}
}

// While the pane is interactive the row also carries the INTERACTIVE marker.
// The Diff chip keeps its place to the left of it, and stays clickable.
func TestInteractiveRowKeepsDiffChipLeftOfTheMarker(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = false
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, TargetPane: "%901", TargetSession: "test-shell"}

	view := p.View(p.width, p.height)
	region := actionChipRegion(t, p, previewActionDiff)
	diffCol := drawnColumn(t, view, region.Rect.Y, diffChipText(p))
	markerCol := drawnColumn(t, view, region.Rect.Y, "INTERACTIVE")

	if diffCol >= markerCol {
		t.Fatalf("Diff at %d is not left of INTERACTIVE at %d", diffCol, markerCol)
	}
	if diffCol != region.Rect.X {
		t.Fatalf("Diff drawn at %d but its hit region starts at %d", diffCol, region.Rect.X)
	}
}

// A narrow preview gives up the name before it gives up the buttons.
func TestNarrowRowTruncatesTheNameNotTheDiffChip(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.shells[0].Name = strings.Repeat("long-shell-name-", 4)
	p.sidebarVisible = false
	p.width = 46

	view := p.View(p.width, p.height)
	region := actionChipRegion(t, p, previewActionDiff)
	col := drawnColumn(t, view, region.Rect.Y, diffChipText(p))
	if col != region.Rect.X {
		t.Fatalf("Diff drawn at %d but its hit region starts at %d", col, region.Rect.X)
	}
	if strings.Contains(ansi.Strip(strings.Split(view, "\n")[region.Rect.Y]), p.shells[0].Name) {
		t.Fatal("the full name survived a narrow row; it should have been truncated")
	}
}
