package gitstatus

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestGitWheelBoundaryFollowsHoveredPane(t *testing.T) {
	p := New()
	p.width, p.height, p.sidebarWidth = 120, 30, 40
	p.viewMode = ViewModeStatus
	p.tree = NewFileTree(t.TempDir())
	p.tree.Modified = []*FileEntry{
		{Path: "a.go", Status: StatusModified},
		{Path: "b.go", Status: StatusModified},
	}
	p.mouseHandler.HitMap.AddRect(regionSidebar, 0, 0, 42, 30, nil)
	p.mouseHandler.HitMap.AddRect(regionDiffPane, 43, 0, 77, 30, nil)

	p.cursor = p.totalSelectableItems() - 1
	if !p.WheelAtBoundary(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelDown}) {
		t.Fatal("sidebar wheel at final row was not bounded")
	}
	if p.WheelAtBoundary(tea.MouseWheelMsg{X: 10, Y: 5, Button: tea.MouseWheelUp}) {
		t.Fatal("sidebar wheel toward earlier rows was dropped")
	}

	p.diffPaneScroll = 0
	if !p.WheelAtBoundary(tea.MouseWheelMsg{X: 70, Y: 5, Button: tea.MouseWheelUp}) {
		t.Fatal("diff wheel above its first row was not bounded")
	}
	p.viewMode = ViewModeDiff
	p.diffRaw = "diff --git a/a.go b/a.go\nindex 111..222 100644\n--- a/a.go\n+++ b/a.go\n@@ -1,3 +1,3 @@\n-old\n+new\n context\n"
	parsed, err := ParseUnifiedDiff(p.diffRaw)
	if err != nil {
		t.Fatal(err)
	}
	p.parsedDiff = parsed
	p.height = 6 // two visible rendered rows
	p.diffScroll = 0
	if !p.WheelAtBoundary(tea.MouseWheelMsg{X: 70, Y: 5, Button: tea.MouseWheelUp}) {
		t.Fatal("full-screen diff top was not bounded")
	}
	if p.WheelAtBoundary(tea.MouseWheelMsg{X: 70, Y: 5, Button: tea.MouseWheelDown}) {
		t.Fatal("full-screen diff dropped movement toward remaining content")
	}
	wantMax := max(countParsedDiffLines(parsed)-(p.height-4), 0)
	if got := p.diffMaxScroll(); got != wantMax {
		t.Fatalf("full-screen max = %d, want parsed-render bound %d", got, wantMax)
	}
	p.diffScroll = p.diffMaxScroll()
	if !p.WheelAtBoundary(tea.MouseWheelMsg{X: 70, Y: 5, Button: tea.MouseWheelDown}) {
		t.Fatal("full-screen diff bottom was not bounded")
	}
	p.handleDiffMouse(tea.MouseWheelMsg{X: 70, Y: 5, Button: tea.MouseWheelUp})
	if p.diffScroll >= wantMax {
		t.Fatalf("first reverse wheel left scroll at %d, want below %d", p.diffScroll, wantMax)
	}

	p.diffViewMode = DiffViewSideBySide
	wantMax = max(countSideBySideDiffRows(parsed)-(p.height-4), 0)
	p.diffScroll = p.diffMaxScroll()
	if p.diffScroll != wantMax {
		t.Fatalf("side-by-side max = %d, want paired-row bound %d", p.diffScroll, wantMax)
	}
	if !p.WheelAtBoundary(tea.MouseWheelMsg{X: 70, Y: 5, Button: tea.MouseWheelDown}) {
		t.Fatal("side-by-side bottom was not bounded")
	}
	p.handleDiffMouse(tea.MouseWheelMsg{X: 70, Y: 5, Button: tea.MouseWheelUp})
	if p.diffScroll >= wantMax {
		t.Fatalf("first side-by-side reverse wheel left scroll at %d, want below %d", p.diffScroll, wantMax)
	}
}
