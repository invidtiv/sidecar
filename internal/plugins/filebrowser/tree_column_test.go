package filebrowser

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
)

// The scrollbar is joined to the right of the tree block, and JoinHorizontal
// sizes a block to its widest line. These tests pin the block to a fixed width
// so the scrollbar cannot drift over to the right edge of the longest filename.

func TestTreeColumnPadsEveryLineToWidth(t *testing.T) {
	block := strings.Join([]string{"a.go", "much-longer-name.go", ""}, "\n")

	got := treeColumn(block, 30)

	for i, line := range strings.Split(got, "\n") {
		if w := ansi.StringWidth(line); w != 30 {
			t.Errorf("line %d width = %d, want 30 (%q)", i, w, line)
		}
	}
}

func TestTreeColumnLeavesOverlongLinesAlone(t *testing.T) {
	// Rows are truncated to width before they get here. If one somehow is not,
	// padding must not make it worse by wrapping.
	got := treeColumn("this-line-is-far-too-long", 10)

	if strings.Contains(got, "\n") {
		t.Errorf("treeColumn wrapped an overlong line: %q", got)
	}
}

func TestTreeColumnMeasuresDisplayCellsNotBytes(t *testing.T) {
	// A CJK filename is 2 cells per rune but 3 bytes. Padding by byte length
	// would under-pad and let the column collapse.
	got := treeColumn("日本語.go", 20)

	if w := ansi.StringWidth(got); w != 20 {
		t.Errorf("width = %d, want 20", w)
	}
}

func TestTreeColumnIgnoresANSIWhenMeasuring(t *testing.T) {
	styled := lipgloss.NewStyle().Bold(true).Render("a.go")

	got := treeColumn(styled, 12)

	if w := ansi.StringWidth(got); w != 12 {
		t.Errorf("width = %d, want 12", w)
	}
}

// The regression itself: during a drag the source row returns early, before the
// cursor highlight that is the only thing padding a row out to full width.
// Without treeColumn the block collapses to the longest filename and the
// scrollbar jumps left. Rendering must not depend on which rows happen to be
// highlighted.
func TestTreePaneScrollbarColumnIsStableDuringDrag(t *testing.T) {
	p := newDragTestPlugin(t)

	before := paneWidth(t, p.renderTreePane(6))

	// Start a drag on the cursor row, which is also the row the pane would
	// otherwise have padded.
	node := p.tree.GetNode(p.treeCursor)
	if node == nil {
		t.Fatal("no node under cursor")
	}
	p.dragActive = true
	p.dragSourcePath = node.Path
	p.dragDropIdx = -1

	during := paneWidth(t, p.renderTreePane(6))

	if before != during {
		t.Errorf("tree pane width changed when a drag started: %d -> %d; "+
			"the scrollbar moves with it", before, during)
	}
}

// The same collapse with no drag involved: move the cursor off the visible rows
// and nothing is highlighted at all. This one predates drag-to-move.
func TestTreePaneScrollbarColumnIsStableWithCursorOffscreen(t *testing.T) {
	p := newDragTestPlugin(t)

	withCursor := paneWidth(t, p.renderTreePane(6))

	p.treeCursor = -1 // no visible row is selected

	withoutCursor := paneWidth(t, p.renderTreePane(6))

	if withCursor != withoutCursor {
		t.Errorf("tree pane width changed when the cursor left the viewport: %d -> %d",
			withCursor, withoutCursor)
	}
}

// The column must be the full pane width, not merely stable at some collapsed
// value: two plugins whose filenames differ in length must still line up.
func TestTreePaneColumnIsPaneWidthNotContentWidth(t *testing.T) {
	p := newDragTestPlugin(t)

	got := paneWidth(t, p.renderTreePane(6))
	want := treeNodeWidth(p.treeWidth) + 1 // rows + scrollbar column

	if got != want {
		t.Errorf("tree pane width = %d, want %d (rows padded to %d plus scrollbar)",
			got, want, treeNodeWidth(p.treeWidth))
	}
}

// paneWidth returns the width of the widest rendered content line.
func paneWidth(t *testing.T, rendered string) int {
	t.Helper()

	width := -1
	for _, line := range strings.Split(rendered, "\n") {
		if w := ansi.StringWidth(line); w > width {
			width = w
		}
	}
	if width <= 0 {
		t.Fatal("rendered pane had no content lines")
	}
	return width
}
