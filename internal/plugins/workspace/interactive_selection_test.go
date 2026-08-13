package workspace

import (
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

// newSelectionTestPlugin puts a live terminal with ten selectable rows on
// screen. The window is the one the surface draws — there is no second cached
// one to hit-test against — so the pane starts at Y=2 and its first buffer line
// is drawn below the border and the header row.
func newSelectionTestPlugin() *Plugin {
	p := &Plugin{
		viewMode:      ViewModeInteractive,
		mouseHandler:  mouse.NewHandler(),
		width:         100,
		height:        30,
		sidebarWidth:  40,
		previewTab:    PreviewTabOutput,
		shellSelected: true,
		shells: []*ShellSession{{
			TmuxName: "shell-1",
			Agent:    &Agent{OutputBuf: tty.NewOutputBuffer(100)},
		}},
		interactiveState: &InteractiveState{
			Active:        true,
			TargetSession: "shell-1",
			TargetPane:    "%1",
		},
	}
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row here\n", 10))
	p.selection.Clear() // initialize sentinels
	return p
}

// actionAt creates a mouse action at the given content column with the preview pane region.
// The x parameter is a content column; panelOverhead/2 is added to simulate the viewport
// X coordinate (accounting for left border + left padding).
func actionAt(x, y int) mouse.MouseAction {
	return mouse.MouseAction{
		Type: mouse.ActionClick,
		X:    x + panelOverhead/2,
		Y:    y,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 0, Y: 2, W: 80, H: 12},
		},
	}
}

func TestPrepareInteractiveDrag_NoSelection(t *testing.T) {
	p := newSelectionTestPlugin()

	// Y=6: contentRow = 6-2-1 = 3, outputRow = 3-1 = 2, lineIdx = 0+2 = 2
	action := actionAt(10, 6)
	p.prepareInteractiveDrag(action, tty.ClickNone)

	if p.selection.HasSelection() {
		t.Error("click without drag should not create selection")
	}
	if p.selection.Start.Valid() {
		t.Errorf("start should be invalid, got %+v", p.selection.Start)
	}
	if p.selection.End.Valid() {
		t.Errorf("end should be invalid, got %+v", p.selection.End)
	}
	if p.selection.Anchor.Line != 2 {
		t.Errorf("anchor line should be 2, got %d", p.selection.Anchor.Line)
	}
}

func TestDragAfterClick_CreatesSelection(t *testing.T) {
	p := newSelectionTestPlugin()

	// Click at line 2 (Y=6)
	action := actionAt(10, 6)
	p.prepareInteractiveDrag(action, tty.ClickNone)

	// Drag to line 4 (Y=8)
	dragAction := mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    10,
		Y:    8,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 0, Y: 2, W: 80, H: 12},
		},
	}
	p.handleInteractiveSelectionDrag(dragAction)

	if !p.selection.HasSelection() {
		t.Error("drag should create selection")
	}
	if !p.selection.Active {
		t.Error("selection should be active after drag")
	}
	if p.selection.Start.Line != 2 {
		t.Errorf("start line should be 2, got %d", p.selection.Start.Line)
	}
	if p.selection.End.Line != 4 {
		t.Errorf("end line should be 4, got %d", p.selection.End.Line)
	}
}

func TestDragUpward_FromAnchor(t *testing.T) {
	p := newSelectionTestPlugin()

	// Click at line 4 (Y=8)
	action := actionAt(10, 8)
	p.prepareInteractiveDrag(action, tty.ClickNone)

	// Drag up to line 1 (Y=5)
	dragAction := mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    10,
		Y:    5,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 0, Y: 2, W: 80, H: 12},
		},
	}
	p.handleInteractiveSelectionDrag(dragAction)

	if !p.selection.HasSelection() {
		t.Error("upward drag should create selection")
	}
	if p.selection.Start.Line != 1 {
		t.Errorf("start line should be 1, got %d", p.selection.Start.Line)
	}
	if p.selection.End.Line != 4 {
		t.Errorf("end line should be 4, got %d", p.selection.End.Line)
	}
}

func TestFinishInteractiveSelection_UnstartedClears(t *testing.T) {
	p := newSelectionTestPlugin()

	// Click without drag
	action := actionAt(10, 6)
	p.prepareInteractiveDrag(action, tty.ClickNone)

	// Finish without any drag motion
	p.finishInteractiveSelection()

	if p.selection.HasSelection() {
		t.Error("finish without drag should not leave selection")
	}
	if p.selection.Start.Valid() {
		t.Errorf("start should be invalid after clear, got %+v", p.selection.Start)
	}
	if p.selection.Anchor.Valid() {
		t.Errorf("anchor should be invalid after clear, got %+v", p.selection.Anchor)
	}
}

func TestFinishInteractiveSelection_AfterDrag(t *testing.T) {
	p := newSelectionTestPlugin()

	// Click and drag
	action := actionAt(10, 6)
	p.prepareInteractiveDrag(action, tty.ClickNone)
	dragAction := mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    10,
		Y:    8,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 0, Y: 2, W: 80, H: 12},
		},
	}
	p.handleInteractiveSelectionDrag(dragAction)

	// Finish
	p.finishInteractiveSelection()

	// Selection should persist (active=false but range preserved)
	if !p.selection.HasSelection() {
		t.Error("selection range should persist after finish")
	}
	if p.selection.Active {
		t.Error("active should be false after finish")
	}
	if p.selection.Start.Line != 2 {
		t.Errorf("start line should be 2, got %d", p.selection.Start.Line)
	}
	if p.selection.End.Line != 4 {
		t.Errorf("end line should be 4, got %d", p.selection.End.Line)
	}
}

func TestClearInteractiveSelection_ResetsSentinels(t *testing.T) {
	p := newSelectionTestPlugin()

	// Create a valid selection
	action := actionAt(10, 6)
	p.prepareInteractiveDrag(action, tty.ClickNone)
	dragAction := mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    10,
		Y:    8,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 0, Y: 2, W: 80, H: 12},
		},
	}
	p.handleInteractiveSelectionDrag(dragAction)

	// Clear
	p.selection.Clear()

	if p.selection.Active {
		t.Error("active should be false after clear")
	}
	if p.selection.Start.Valid() {
		t.Errorf("start should be invalid, got %+v", p.selection.Start)
	}
	if p.selection.End.Valid() {
		t.Errorf("end should be invalid, got %+v", p.selection.End)
	}
	if p.selection.Anchor.Valid() {
		t.Errorf("anchor should be invalid, got %+v", p.selection.Anchor)
	}
	if p.selection.HasSelection() {
		t.Error("HasSelection should return false after clear")
	}
}

func TestDragToSameLine_SelectsSingleLine(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("line0\nline1\nline2\nline three has enough text to test\nline4")
	p.shellSelected = true
	p.shells = []*ShellSession{{
		Agent: &Agent{OutputBuf: buf},
	}}
	p.selectedShellIdx = 0

	// Click at line 3 (Y=7: contentRow=7-2-1=4, outputRow=4-1=3, lineIdx=3)
	action := actionAt(10, 7)
	p.prepareInteractiveDrag(action, tty.ClickNone)

	// Drag to same line (different X, same Y)
	dragAction := mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    30,
		Y:    7,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 0, Y: 2, W: 80, H: 12},
		},
	}
	p.handleInteractiveSelectionDrag(dragAction)

	if !p.selection.HasSelection() {
		t.Error("drag to same line should create selection")
	}
	if p.selection.Start.Line != 3 {
		t.Errorf("start line should be 3, got %d", p.selection.Start.Line)
	}
	if p.selection.End.Line != 3 {
		t.Errorf("end line should be 3, got %d", p.selection.End.Line)
	}
	// Column should differ between start and end
	if p.selection.Start.Col == p.selection.End.Col {
		t.Error("start and end col should differ for same-line drag with different X")
	}
}

func TestPrepareInteractiveDrag_InvalidY(t *testing.T) {
	p := newSelectionTestPlugin()

	// Click above content area (Y=2 -> border row)
	action := actionAt(10, 2)
	p.prepareInteractiveDrag(action, tty.ClickNone)

	if p.selection.Anchor.Valid() {
		t.Errorf("anchor should be invalid for invalid Y, got %+v", p.selection.Anchor)
	}
}

func TestPrepareInteractiveDrag_NilRegion(t *testing.T) {
	p := &Plugin{
		viewMode:         ViewModeInteractive,
		mouseHandler:     mouse.NewHandler(),
		interactiveState: &InteractiveState{Active: true},
	}
	p.selection.Clear()

	action := mouse.MouseAction{
		Type:   mouse.ActionClick,
		X:      10,
		Y:      6,
		Region: nil,
	}
	p.prepareInteractiveDrag(action, tty.ClickNone)

	if p.selection.Anchor.Valid() {
		t.Errorf("anchor should remain invalid for nil region, got %+v", p.selection.Anchor)
	}
}

func TestIsLineSelected(t *testing.T) {
	p := newSelectionTestPlugin()

	// Set up selection range [3, 5]
	p.selection.Start = ui.SelectionPoint{Line: 3, Col: 0}
	p.selection.End = ui.SelectionPoint{Line: 5, Col: 10}

	tests := []struct {
		lineIdx  int
		expected bool
	}{
		{2, false},
		{3, true},
		{4, true},
		{5, true},
		{6, false},
	}

	for _, tt := range tests {
		got := p.selection.IsLineSelected(tt.lineIdx)
		if got != tt.expected {
			t.Errorf("IsLineSelected(%d) = %v, want %v", tt.lineIdx, got, tt.expected)
		}
	}
}

func TestHasSelection_Sentinels(t *testing.T) {
	p := newSelectionTestPlugin()

	// Default: sentinels
	if p.selection.HasSelection() {
		t.Error("should return false with sentinel values")
	}

	// Only start set
	p.selection.Start = ui.SelectionPoint{Line: 3, Col: 0}
	if p.selection.HasSelection() {
		t.Error("should return false with only start set")
	}

	// Both set
	p.selection.End = ui.SelectionPoint{Line: 5, Col: 10}
	if !p.selection.HasSelection() {
		t.Error("should return true with both start and end set")
	}
}

// --- GetLineSelectionCols tests ---

func TestGetLineSelectionCols(t *testing.T) {
	p := newSelectionTestPlugin()

	tests := []struct {
		name        string
		start, end  ui.SelectionPoint
		lineIdx     int
		expectStart int
		expectEnd   int
	}{
		{
			"line before selection",
			ui.SelectionPoint{Line: 3, Col: 5}, ui.SelectionPoint{Line: 6, Col: 10},
			2, -1, -1,
		},
		{
			"line after selection",
			ui.SelectionPoint{Line: 3, Col: 5}, ui.SelectionPoint{Line: 6, Col: 10},
			7, -1, -1,
		},
		{
			"first line of multi-line",
			ui.SelectionPoint{Line: 3, Col: 5}, ui.SelectionPoint{Line: 6, Col: 10},
			3, 5, -1,
		},
		{
			"middle line",
			ui.SelectionPoint{Line: 3, Col: 5}, ui.SelectionPoint{Line: 6, Col: 10},
			4, 0, -1,
		},
		{
			"last line of multi-line",
			ui.SelectionPoint{Line: 3, Col: 5}, ui.SelectionPoint{Line: 6, Col: 10},
			6, 0, 10,
		},
		{
			"single-line selection",
			ui.SelectionPoint{Line: 5, Col: 3}, ui.SelectionPoint{Line: 5, Col: 15},
			5, 3, 15,
		},
	}

	for _, tt := range tests {
		p.selection.Start = tt.start
		p.selection.End = tt.end
		startCol, endCol := p.selection.GetLineSelectionCols(tt.lineIdx)
		if startCol != tt.expectStart || endCol != tt.expectEnd {
			t.Errorf("%s: GetLineSelectionCols(%d) = (%d, %d), want (%d, %d)",
				tt.name, tt.lineIdx, startCol, endCol, tt.expectStart, tt.expectEnd)
		}
	}
}

// --- interactiveColAtX tests ---

func TestInteractiveColAtX_PlainText(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("hello world")

	// Set up a shell with the buffer
	p.shellSelected = true
	p.shells = []*ShellSession{{
		Agent: &Agent{OutputBuf: buf},
	}}
	p.selectedShellIdx = 0
	p.selection.ViewRect = mouse.Rect{X: 0, Y: 2, W: 80, H: 12}

	col, ok := p.interactiveColAtX(5+panelOverhead/2, 0)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if col != 5 {
		t.Errorf("plain text: col = %d, want 5", col)
	}
}

func TestInteractiveColAtX_BeyondLineEnd(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("hello")

	p.shellSelected = true
	p.shells = []*ShellSession{{
		Agent: &Agent{OutputBuf: buf},
	}}
	p.selectedShellIdx = 0
	p.selection.ViewRect = mouse.Rect{X: 0, Y: 2, W: 80, H: 12}

	col, ok := p.interactiveColAtX(100+panelOverhead/2, 0)
	if !ok {
		t.Fatal("expected ok=true")
	}
	// Should clamp to last char (col 4)
	if col != 4 {
		t.Errorf("beyond end: col = %d, want 4", col)
	}
}

func TestInteractiveColAtX_WithHorizOffset(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("hello world test")

	p.shellSelected = true
	p.shells = []*ShellSession{{
		Agent: &Agent{OutputBuf: buf},
	}}
	p.selectedShellIdx = 0
	p.selection.ViewRect = mouse.Rect{X: 0, Y: 2, W: 80, H: 12}

	// viewport X=4 (content col 2) -> visual col 2
	col, ok := p.interactiveColAtX(2+panelOverhead/2, 0)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if col != 2 {
		t.Errorf("col = %d, want 2", col)
	}
}

func TestInteractiveColAtX_EmptyLine(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("")

	p.shellSelected = true
	p.shells = []*ShellSession{{
		Agent: &Agent{OutputBuf: buf},
	}}
	p.selectedShellIdx = 0
	p.selection.ViewRect = mouse.Rect{X: 0, Y: 2, W: 80, H: 12}

	col, ok := p.interactiveColAtX(0+panelOverhead/2, 0)
	if !ok {
		t.Fatal("expected ok=true")
	}
	if col != 0 {
		t.Errorf("empty line: col = %d, want 0", col)
	}
}

// --- Character-level drag tests ---

func TestCharacterLevelDrag_SameLineRightward(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("hello world")

	p.shellSelected = true
	p.shells = []*ShellSession{{
		Agent: &Agent{OutputBuf: buf},
	}}
	p.selectedShellIdx = 0

	// Click at col 5 on line 0 (Y=4: contentRow=4-2-1=1, outputRow=1-1=0, lineIdx=0)
	action := actionAt(5, 4)
	p.prepareInteractiveDrag(action, tty.ClickNone)

	// Drag to col 10
	dragAction := mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    10 + panelOverhead/2,
		Y:    4,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 0, Y: 2, W: 80, H: 12},
		},
	}
	p.handleInteractiveSelectionDrag(dragAction)

	if p.selection.Start.Line != 0 || p.selection.Start.Col != 5 {
		t.Errorf("start = %+v, want {0, 5}", p.selection.Start)
	}
	if p.selection.End.Line != 0 || p.selection.End.Col != 10 {
		t.Errorf("end = %+v, want {0, 10}", p.selection.End)
	}
}

func TestCharacterLevelDrag_SameLineBackward(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("hello world")

	p.shellSelected = true
	p.shells = []*ShellSession{{
		Agent: &Agent{OutputBuf: buf},
	}}
	p.selectedShellIdx = 0

	// Click at col 10
	action := actionAt(10, 4)
	p.prepareInteractiveDrag(action, tty.ClickNone)

	// Drag backward to col 3
	dragAction := mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    3 + panelOverhead/2,
		Y:    4,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 0, Y: 2, W: 80, H: 12},
		},
	}
	p.handleInteractiveSelectionDrag(dragAction)

	// Start should be the lesser position
	if p.selection.Start.Col != 3 {
		t.Errorf("start col = %d, want 3", p.selection.Start.Col)
	}
	if p.selection.End.Col != 10 {
		t.Errorf("end col = %d, want 10", p.selection.End.Col)
	}
}

func TestCharacterLevelDrag_MultiLineDown(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("line zero\nline one\nline two\nline three")

	p.shellSelected = true
	p.shells = []*ShellSession{{
		Agent: &Agent{OutputBuf: buf},
	}}
	p.selectedShellIdx = 0

	// Click at (5, line 1) -> Y=5: contentRow=5-2-1=2, outputRow=2-1=1, lineIdx=1
	action := actionAt(5, 5)
	p.prepareInteractiveDrag(action, tty.ClickNone)

	// Drag to (3, line 3) -> Y=7
	dragAction := mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    3 + panelOverhead/2,
		Y:    7,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 0, Y: 2, W: 80, H: 12},
		},
	}
	p.handleInteractiveSelectionDrag(dragAction)

	if p.selection.Start.Line != 1 || p.selection.Start.Col != 5 {
		t.Errorf("start = %+v, want {1, 5}", p.selection.Start)
	}
	if p.selection.End.Line != 3 || p.selection.End.Col != 3 {
		t.Errorf("end = %+v, want {3, 3}", p.selection.End)
	}
}

func TestCharacterLevelDrag_DirectionReversal(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("abcdefghijklmnop")

	p.shellSelected = true
	p.shells = []*ShellSession{{
		Agent: &Agent{OutputBuf: buf},
	}}
	p.selectedShellIdx = 0

	// Click at col 8
	action := actionAt(8, 4)
	p.prepareInteractiveDrag(action, tty.ClickNone)

	// Drag right to col 12
	dragAction := mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    12 + panelOverhead/2,
		Y:    4,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 0, Y: 2, W: 80, H: 12},
		},
	}
	p.handleInteractiveSelectionDrag(dragAction)

	if p.selection.Start.Col != 8 || p.selection.End.Col != 12 {
		t.Errorf("after right drag: start.col=%d, end.col=%d, want 8, 12",
			p.selection.Start.Col, p.selection.End.Col)
	}

	// Now reverse past anchor to col 3
	dragAction.X = 3 + panelOverhead/2
	p.handleInteractiveSelectionDrag(dragAction)

	if p.selection.Start.Col != 3 || p.selection.End.Col != 8 {
		t.Errorf("after reversal: start.col=%d, end.col=%d, want 3, 8",
			p.selection.Start.Col, p.selection.End.Col)
	}
}

// --- interactiveSelectionLines integration test ---

func TestInteractiveSelectionLines_SingleLine(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("hello world foo bar")
	p.shellSelected = true
	p.shells = []*ShellSession{{Agent: &Agent{OutputBuf: buf}}}
	p.selectedShellIdx = 0

	p.selection.Start = ui.SelectionPoint{Line: 0, Col: 6}
	p.selection.End = ui.SelectionPoint{Line: 0, Col: 10}

	lines := p.interactiveSelectionLines()
	if len(lines) != 1 {
		t.Fatalf("expected 1 line, got %d", len(lines))
	}
	if !strings.Contains(lines[0], "world") {
		t.Errorf("expected 'world' in selection, got %q", lines[0])
	}
}

func TestInteractiveSelectionUsesAbsoluteCoordinatesAcrossPrepend(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.UpdateSnapshot("one\nstable target\nthree", 100)
	p.shellSelected = true
	p.shells = []*ShellSession{{
		TmuxName: "shell-1",
		Agent:    &Agent{TmuxSession: "shell-1", OutputBuf: buf},
	}}
	p.selectedShellIdx = 0
	p.selection.SelectRange(
		ui.SelectionPoint{Line: 101, Col: 0},
		ui.SelectionPoint{Line: 101, Col: 5},
		false,
	)

	before := p.interactiveSelectionLines()
	if !slices.Equal(before, []string{"stable"}) {
		t.Fatalf("selection before prepend = %#v, want stable", before)
	}
	buf.PrependSnapshot("older-a\nolder-b", 98)
	after := p.interactiveSelectionLines()
	if !slices.Equal(after, before) {
		t.Fatalf("selection drifted after prepend: before=%#v after=%#v", before, after)
	}
}

func TestTerminalWordAndLineGestures(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("open internal/foo.go:123 now")
	p.shellSelected = true
	p.shells = []*ShellSession{{Agent: &Agent{OutputBuf: buf}}}
	p.selectedShellIdx = 0

	wordAction := actionAt(10, 4)
	p.selectTerminalWord(wordAction)
	if got := p.interactiveSelectionLines(); !slices.Equal(got, []string{"internal/foo.go:123"}) {
		t.Fatalf("double-click word selection = %#v", got)
	}

	p.selectTerminalLine(wordAction)
	if got := p.interactiveSelectionLines(); !slices.Equal(got, []string{"open internal/foo.go:123 now"}) {
		t.Fatalf("triple-click line selection = %#v", got)
	}
}

func TestAltDragCreatesRectangularSelection(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("abcdef\nghijkl")
	p.shellSelected = true
	p.shells = []*ShellSession{{Agent: &Agent{OutputBuf: buf}}}
	p.selectedShellIdx = 0

	start := actionAt(1, 4)
	start.Alt = true
	p.prepareInteractiveDrag(start, tty.ClickNone)
	p.handleInteractiveSelectionDrag(mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    3 + panelOverhead/2,
		Y:    5,
	})
	if !p.selection.Rectangular {
		t.Fatal("Alt+drag did not set rectangular selection mode")
	}
	if got := p.interactiveSelectionLines(); !slices.Equal(got, []string{"bcd", "hij"}) {
		t.Fatalf("rectangular selection = %#v, want bcd/hij", got)
	}
}

func TestShiftClickExtendsExistingTerminalSelection(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("abcdef\nghijkl")
	p.shellSelected = true
	p.shells = []*ShellSession{{Agent: &Agent{OutputBuf: buf}}}
	p.selectedShellIdx = 0
	p.selection.SelectRange(
		ui.SelectionPoint{Line: 0, Col: 2},
		ui.SelectionPoint{Line: 0, Col: 3},
		false,
	)

	extend := actionAt(4, 5)
	extend.Shift = true
	p.prepareInteractiveDrag(extend, tty.ClickNone)
	if p.selection.Start != (ui.SelectionPoint{Line: 0, Col: 2}) ||
		p.selection.End != (ui.SelectionPoint{Line: 1, Col: 4}) {
		t.Fatalf("extended selection = %+v..%+v", p.selection.Start, p.selection.End)
	}
}

func TestPlainClickWithoutDragPreservesFollowMode(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("follow me")
	p.shellSelected = true
	p.shells = []*ShellSession{{Agent: &Agent{OutputBuf: buf}}}
	p.selectedShellIdx = 0

	p.prepareInteractiveDrag(actionAt(2, 4), tty.ClickNone)
	p.finishInteractiveSelection()
	if p.previewScroll != 0 || p.previewFreeze.Active() {
		t.Fatal("click without drag disabled follow mode")
	}
}

func TestShiftClickCannotExtendAcrossTerminalSources(t *testing.T) {
	p := New()
	p.width = 80
	p.height = 20
	p.viewMode = ViewModeList
	p.termPanelVisible = true
	p.termPanelOutput = testTerminalBuffer(strings.Repeat("panel row\n", 50))
	p.selectionTermPanel = false
	p.selection.SelectRange(
		ui.SelectionPoint{Line: 10, Col: 1},
		ui.SelectionPoint{Line: 10, Col: 3},
		false,
	)
	action := mouse.MouseAction{
		Type:  mouse.ActionClick,
		X:     12,
		Y:     6,
		Shift: true,
		Region: &mouse.Region{
			ID:   regionTermPanelContent,
			Rect: mouse.Rect{X: 10, Y: 5, W: 40, H: 8},
		},
	}
	p.prepareInteractiveDrag(action, tty.ClickNone)
	if p.selection.HasSelection() {
		t.Fatalf("agent selection was extended into panel coordinates: %+v..%+v",
			p.selection.Start, p.selection.End)
	}
	if !p.selectionTermPanel || !p.selection.Anchor.Valid() {
		t.Fatalf("panel started with wrong source/anchor: panel=%v anchor=%+v",
			p.selectionTermPanel, p.selection.Anchor)
	}
	if p.termPanelFreeze.Start() == 0 {
		t.Fatal("panel selection reused the stale zero offset instead of freezing its live viewport")
	}
	if p.selection.Anchor.Line < p.termPanelFreeze.Start() {
		t.Fatalf("panel anchor line %d precedes frozen viewport %d",
			p.selection.Anchor.Line, p.termPanelFreeze.Start())
	}
}

func TestDoubleClickSwitchesTerminalSourceBeforeHitTesting(t *testing.T) {
	p := New()
	p.width = 80
	p.height = 20
	p.viewMode = ViewModeList
	p.termPanelVisible = true
	p.termPanelOutput = testTerminalBuffer(strings.Repeat("panelword tail\n", 50))
	p.selectionTermPanel = false
	p.selection.SelectRange(
		ui.SelectionPoint{Line: 10, Col: 1},
		ui.SelectionPoint{Line: 10, Col: 3},
		false,
	)
	action := mouse.MouseAction{
		Type: mouse.ActionDoubleClick, X: 12, Y: 6,
		Region: &mouse.Region{
			ID:   regionTermPanelContent,
			Rect: mouse.Rect{X: 10, Y: 5, W: 40, H: 8},
		},
	}

	p.selectTerminalWord(action)
	if !p.selectionTermPanel || !p.selection.HasSelection() {
		t.Fatal("double-click did not create a terminal-panel selection")
	}
	if p.termPanelFreeze.Start() == 0 {
		t.Fatal("double-click reused stale panel offset zero")
	}
	if p.selection.Start.Line < p.termPanelFreeze.Start() {
		t.Fatalf("selected line %d precedes panel viewport %d",
			p.selection.Start.Line, p.termPanelFreeze.Start())
	}
}

func TestWordSelectionMapsVisualColumnAfterWideGlyph(t *testing.T) {
	p := newSelectionTestPlugin()
	buf := tty.NewOutputBuffer(100)
	buf.Write("界 foo bar")
	p.shellSelected = true
	p.shells = []*ShellSession{{Agent: &Agent{OutputBuf: buf}}}
	p.selectedShellIdx = 0

	p.selectTerminalWord(actionAt(3, 4))
	if got := p.interactiveSelectionLines(); !slices.Equal(got, []string{"foo"}) {
		t.Fatalf("wide-glyph word selection = %#v, want foo", got)
	}
}

// newTerminalDragTestPlugin builds a passive preview pane over enough scrollback
// for a drag to run off both edges.
func newTerminalDragTestPlugin() *Plugin {
	p := newPreviewClickTestPlugin()
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row here\n", 200))
	// The window starts at the oldest row the buffer holds, so a gesture over it
	// names rows these tests can assert by absolute line.
	p.previewScroll = p.terminalSelectionViewportLayout().MaxOffset
	return p
}

func terminalDragTo(p *Plugin, x, y int) tea.Cmd {
	return p.handleMouseDrag(mouse.MouseAction{
		Type: mouse.ActionDrag, X: x, Y: y, DragStartID: regionPreviewPane,
		Region: previewClickAction(false, false).Region,
	})
}

// The pointer leaves the pane constantly while the button is held. A drag that
// stops tracking there reads as selection simply not working.
func TestDragOutsideContentClampsInsteadOfStalling(t *testing.T) {
	tests := []struct {
		name    string
		x, y    int
		wantCol int
		wantRow func(layout terminalViewportLayout) int
	}{
		{
			name: "below the last row", x: 66, y: 40, wantCol: 24,
			wantRow: func(l terminalViewportLayout) int { return l.End - 1 },
		},
		{
			name: "left of the content", x: 0, y: 8, wantCol: 0,
			wantRow: func(l terminalViewportLayout) int { return l.Start + 4 },
		},
		{
			name: "right of the content", x: 200, y: 8, wantCol: 27,
			wantRow: func(l terminalViewportLayout) int { return l.Start + 4 },
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newTerminalDragTestPlugin()
			p.handleMouseClick(previewClickAction(false, false))
			terminalDragTo(p, 66, 8)
			stalled := p.selection.End

			terminalDragTo(p, tt.x, tt.y)
			if p.selection.End == stalled && tt.y != 8 {
				t.Fatalf("drag %s did not move the selection (still %+v)", tt.name, stalled)
			}
			layout := p.terminalSelectionViewportLayout()
			want := ui.SelectionPoint{Line: tt.wantRow(layout), Col: tt.wantCol}
			if p.selection.End != want {
				t.Errorf("selection end = %+v, want %+v", p.selection.End, want)
			}
		})
	}
}

// Dragging past an edge is how a selection reaches text that is not on screen.
func TestDragPastEdgeScrollsSelectionThroughScrollback(t *testing.T) {
	p := newTerminalDragTestPlugin()
	p.previewScroll = p.terminalSelectionViewportLayout().MaxOffset - 50
	p.handleMouseClick(previewClickAction(false, false))
	terminalDragTo(p, 66, 8)
	start := p.terminalSelectionViewportLayout().Start

	terminalDragTo(p, 66, 0)
	up := p.terminalSelectionViewportLayout().Start
	if up >= start {
		t.Fatalf("drag above the top edge left the window at %d (was %d)", up, start)
	}
	if p.selection.Start.Line != up {
		t.Errorf("selection start = %d, want the newly revealed top row %d", p.selection.Start.Line, up)
	}
	// One motion event must not skip a screenful the user never had a chance to see.
	if start-up > tty.DragScrollStep {
		t.Errorf("one motion event scrolled %d rows, want at most %d", start-up, tty.DragScrollStep)
	}

	for range 4 {
		terminalDragTo(p, 66, 40)
	}
	down := p.terminalSelectionViewportLayout().Start
	if down <= up {
		t.Fatalf("drag below the bottom edge left the window at %d (was %d)", down, up)
	}
	if p.selection.End.Line != down+p.selection.ViewRect.H-previewBorderRows-terminalHeaderRows-1 &&
		p.selection.End.Line != p.terminalSelectionViewportLayout().End-1 {
		t.Errorf("selection end %d did not follow the scrolled window ending at %d",
			p.selection.End.Line, p.terminalSelectionViewportLayout().End-1)
	}
}

// A drag whose button-down landed on chrome is still unambiguously a selection
// by the time it is moving.
func TestDragFromChromeAnchorsAtNearestCell(t *testing.T) {
	p := newTerminalDragTestPlugin()
	header := previewClickAction(false, false)
	header.Y = header.Region.Rect.Y // border row, above the header and the output
	p.handleMouseClick(header)
	if p.selection.Anchor.Valid() {
		t.Fatal("a click on chrome should not anchor a selection")
	}

	p.handleMouseDrag(mouse.MouseAction{
		Type: mouse.ActionDrag, X: 66, Y: 9, DragStartID: regionPreviewPane,
		DragDX: 6, DragDY: 9 - header.Y,
		Region: header.Region,
	})
	if !p.selection.HasSelection() {
		t.Fatal("drag starting on chrome never anchored a selection")
	}
	layout := p.terminalSelectionViewportLayout()
	if p.selection.Start.Line != layout.Start {
		t.Errorf("anchor line = %d, want the first visible row %d", p.selection.Start.Line, layout.Start)
	}
}

// Shift-clicking chrome reaches for the nearest cell, as xterm does — never for
// the delete key.
func TestShiftClickMissExtendsToClampedPoint(t *testing.T) {
	p := newTerminalDragTestPlugin()
	p.handleMouseClick(previewClickAction(false, false))
	terminalDragTo(p, 66, 8)
	p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane})
	anchor := p.selection.Anchor

	if anchor != (ui.SelectionPoint{Line: 2, Col: 18}) {
		t.Fatalf("test setup anchored at %+v, want line 2 col 18", anchor)
	}

	miss := previewClickAction(true, false)
	miss.Y = miss.Region.Rect.Y // border row, above the header and the output
	p.handleMouseClick(miss)
	// The border row clamps to the first visible row, x=60 to col 18.
	want := ui.SelectionPoint{Line: 0, Col: 18}
	if p.selection.Start != want || p.selection.End != anchor {
		t.Errorf("shift-click on chrome gave %+v..%+v, want %+v..%+v",
			p.selection.Start, p.selection.End, want, anchor)
	}
}

// terminalMultiClickAt builds a double/triple click on the preview pane at a
// content column of "selectable terminal row here" (col 0 sits at x=42).
func terminalMultiClickAt(action mouse.ActionType, col, y int) mouse.MouseAction {
	a := previewClickAction(false, false)
	a.Type = action
	a.X, a.Y = col+42, y
	return a
}

// After a double-click, the held button drags in whole words: the word under the
// pointer comes along entire, and the word the gesture started on is never eaten
// into, in either direction.
func TestDoubleClickDragExtendsByWords(t *testing.T) {
	p := newTerminalDragTestPlugin()
	p.handleMouseDoubleClick(terminalMultiClickAt(mouse.ActionDoubleClick, 11, 6))
	line := p.selection.Start.Line
	if p.selection.Start != (ui.SelectionPoint{Line: line, Col: 11}) ||
		p.selection.End != (ui.SelectionPoint{Line: line, Col: 18}) {
		t.Fatalf("double-click selected %+v..%+v, want the whole word terminal",
			p.selection.Start, p.selection.End)
	}

	terminalDragTo(p, 25+42, 6) // into "here"
	if p.selection.Start != (ui.SelectionPoint{Line: line, Col: 11}) ||
		p.selection.End != (ui.SelectionPoint{Line: line, Col: 27}) {
		t.Errorf("forward word drag = %+v..%+v, want cols 11..27",
			p.selection.Start, p.selection.End)
	}

	terminalDragTo(p, 2+42, 6) // back into "selectable"
	if p.selection.Start != (ui.SelectionPoint{Line: line, Col: 0}) ||
		p.selection.End != (ui.SelectionPoint{Line: line, Col: 18}) {
		t.Errorf("backward word drag = %+v..%+v, want cols 0..18 (anchor word kept whole)",
			p.selection.Start, p.selection.End)
	}
}

func TestTripleClickDragExtendsByLines(t *testing.T) {
	p := newTerminalDragTestPlugin()
	p.handleMouseTripleClick(terminalMultiClickAt(mouse.ActionTripleClick, 11, 6))
	line := p.selection.Start.Line
	if p.selection.Start != (ui.SelectionPoint{Line: line, Col: 0}) ||
		p.selection.End != (ui.SelectionPoint{Line: line, Col: 27}) {
		t.Fatalf("triple-click selected %+v..%+v, want the whole line",
			p.selection.Start, p.selection.End)
	}

	terminalDragTo(p, 50, 8)
	if p.selection.Start != (ui.SelectionPoint{Line: line, Col: 0}) ||
		p.selection.End != (ui.SelectionPoint{Line: line + 2, Col: 27}) {
		t.Errorf("downward line drag = %+v..%+v, want whole lines %d..%d",
			p.selection.Start, p.selection.End, line, line+2)
	}

	terminalDragTo(p, 50, 4)
	if p.selection.Start != (ui.SelectionPoint{Line: line - 2, Col: 0}) ||
		p.selection.End != (ui.SelectionPoint{Line: line, Col: 27}) {
		t.Errorf("upward line drag = %+v..%+v, want whole lines %d..%d",
			p.selection.Start, p.selection.End, line-2, line)
	}
}

// A one-character word is a legitimate selection whose start and end coincide,
// so the jitter collapse must not treat its release as a click.
func TestSingleCharWordSurvivesDragEnd(t *testing.T) {
	p := newPreviewClickTestPlugin()
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("a bb ccc\n", 50))

	p.handleMouseDoubleClick(terminalMultiClickAt(mouse.ActionDoubleClick, 0, 6))
	if !p.selection.HasSelection() || p.selection.Start != p.selection.End {
		t.Fatalf("double-click on a one-character word selected %+v..%+v",
			p.selection.Start, p.selection.End)
	}
	want := p.selection.Start

	p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane})
	if !p.selection.HasSelection() || p.selection.Start != want {
		t.Errorf("drag end discarded the one-character word selection (%+v..%+v)",
			p.selection.Start, p.selection.End)
	}
	if p.viewMode == ViewModeInteractive {
		t.Error("a word selection activated the terminal on release")
	}
}

// Motion events stop the moment the hand does. A pointer parked past an edge has
// to keep scrolling on its own, and has to stop at the buffer edge.
func TestHeldPointerPastEdgeKeepsScrollingUntilTheTop(t *testing.T) {
	p := newTerminalDragTestPlugin()
	p.previewScroll = p.terminalSelectionViewportLayout().MaxOffset - 60
	p.handleMouseClick(previewClickAction(false, false))
	terminalDragTo(p, 66, 8)
	if cmd := terminalDragTo(p, 66, 0); cmd == nil {
		t.Fatal("a drag past the top edge scheduled no auto-scroll tick")
	}
	generation := p.pointer.Generation()
	start := p.terminalSelectionViewportLayout().Start

	if _, cmd := p.update(selectionAutoScrollTickMsg{generation: generation}); cmd == nil {
		t.Fatal("the first tick did not re-arm while the pointer was still past the edge")
	}
	scrolled := p.terminalSelectionViewportLayout().Start
	if scrolled >= start {
		t.Fatalf("held pointer did not scroll: window still at %d (was %d)", scrolled, start)
	}
	if p.selection.Start.Line != scrolled {
		t.Errorf("selection start = %d, want the newly revealed top row %d",
			p.selection.Start.Line, scrolled)
	}

	for range 100 {
		_, cmd := p.update(selectionAutoScrollTickMsg{generation: generation})
		if cmd == nil {
			break
		}
	}
	if top := p.terminalSelectionViewportLayout().Start; top != 0 {
		t.Fatalf("auto-scroll stopped at %d, want the top of the buffer", top)
	}
}

func TestSelectionAutoScrollStopsWhenGestureEnds(t *testing.T) {
	p := newTerminalDragTestPlugin()
	p.previewScroll = p.terminalSelectionViewportLayout().MaxOffset - 60
	p.handleMouseClick(previewClickAction(false, false))
	terminalDragTo(p, 66, 8)
	terminalDragTo(p, 66, 0)
	generation := p.pointer.Generation()

	p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane})
	before := p.terminalSelectionViewportLayout().Start
	if _, cmd := p.update(selectionAutoScrollTickMsg{generation: generation}); cmd != nil {
		t.Error("a tick from the finished gesture re-armed itself")
	}
	if after := p.terminalSelectionViewportLayout().Start; after != before {
		t.Errorf("a tick from the finished gesture scrolled the window to %d (was %d)", after, before)
	}
}

func TestSelectionEdgeScrollRows(t *testing.T) {
	tests := []struct {
		name      string
		outputRow int
		want      int
	}{
		{"inside the content", 5, 0},
		{"one row above", -1, -1},
		{"far above, capped", -40, -5},
		{"one row below", 10, 1},
		{"far below, capped", 60, 5},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tty.EdgeScrollRows(tt.outputRow, 10, 5); got != tt.want {
				t.Errorf("tty.EdgeScrollRows(%d, 10, 5) = %d, want %d", tt.outputRow, got, tt.want)
			}
		})
	}
}

// The terminal panel browses its own frozen offset, so the held-pointer scroll
// has to reach it too.
func TestHeldPointerPastEdgeScrollsTerminalPanel(t *testing.T) {
	p := New()
	p.width = 80
	p.height = 20
	p.viewMode = ViewModeList
	p.termPanelVisible = true
	p.termPanelOutput = testTerminalBuffer(strings.Repeat("panel row\n", 200))
	panelRegion := &mouse.Region{ID: regionTermPanelContent, Rect: mouse.Rect{X: 10, Y: 5, W: 40, H: 8}}

	p.handleMouseClick(mouse.MouseAction{Type: mouse.ActionClick, X: 12, Y: 8, Region: panelRegion})
	p.handleMouseDrag(mouse.MouseAction{
		Type: mouse.ActionDrag, X: 14, Y: 9, DragStartID: regionTermPanelContent, Region: panelRegion,
	})
	cmd := p.handleMouseDrag(mouse.MouseAction{
		Type: mouse.ActionDrag, X: 14, Y: 0, DragStartID: regionTermPanelContent, Region: panelRegion,
	})
	if cmd == nil {
		t.Fatal("a panel drag past the top edge scheduled no auto-scroll tick")
	}
	before := p.termPanelFreeze.Start()
	if _, cmd := p.update(selectionAutoScrollTickMsg{generation: p.pointer.Generation()}); cmd == nil {
		t.Fatal("the panel tick did not re-arm while the pointer was still past the edge")
	}
	if p.termPanelFreeze.Start() >= before {
		t.Errorf("panel offset = %d, want less than %d", p.termPanelFreeze.Start(), before)
	}
	if p.selection.Start.Line != p.termPanelFreeze.Start() {
		t.Errorf("panel selection start = %d, want the newly revealed top row %d",
			p.selection.Start.Line, p.termPanelFreeze.Start())
	}

	for range tty.AutoScrollMaxRun {
		// Fresh motion at the same position keeps the hold budget alive; the run
		// under test is the buffer edge, not the lost-release bound.
		p.handleMouseDrag(mouse.MouseAction{
			Type: mouse.ActionDrag, X: 14, Y: 0, DragStartID: regionTermPanelContent, Region: panelRegion,
		})
		if _, cmd := p.update(selectionAutoScrollTickMsg{generation: p.pointer.Generation()}); cmd == nil {
			break
		}
	}
	if p.termPanelFreeze.Start() != 0 {
		t.Fatalf("panel auto-scroll stopped at %d, want the top of the panel buffer",
			p.termPanelFreeze.Start())
	}
	if _, cmd := p.update(selectionAutoScrollTickMsg{generation: p.pointer.Generation()}); cmd != nil {
		t.Error("panel auto-scroll re-armed at the buffer edge")
	}
}

// A modal swallows the release, so the tick chain has to notice on its own or it
// scrolls the pane under the modal all the way to line zero.
func TestSelectionAutoScrollStopsUnderAModal(t *testing.T) {
	p := newTerminalDragTestPlugin()
	p.previewScroll = p.terminalSelectionViewportLayout().MaxOffset - 60
	p.handleMouseClick(previewClickAction(false, false))
	terminalDragTo(p, 66, 8)
	terminalDragTo(p, 66, 0)
	generation := p.pointer.Generation()

	p.viewMode = ViewModeConfirmDelete
	before := p.terminalSelectionViewportLayout().Start
	if _, cmd := p.update(selectionAutoScrollTickMsg{generation: generation}); cmd != nil {
		t.Error("a tick under a modal re-armed itself")
	}
	if after := p.terminalSelectionViewportLayout().Start; after != before {
		t.Errorf("a tick under a modal scrolled the window to %d (was %d)", after, before)
	}
	p.viewMode = ViewModeList
	if _, cmd := p.update(selectionAutoScrollTickMsg{generation: generation}); cmd != nil {
		t.Error("the modal did not end the gesture: a stale tick came back to life")
	}
}

// A release lost off-window is only noticed when the pointer returns. Until then
// the chain must not keep dragging the selection through all of scrollback.
func TestSelectionAutoScrollSelfLimitsWithoutMotion(t *testing.T) {
	p := newTerminalDragTestPlugin()
	// Deep enough scrollback that an unbounded chain would keep going.
	p.shells[0].Agent.OutputBuf = tty.NewOutputBuffer(1000)
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row here\n", 900))
	// From the live bottom, so the chain has the whole buffer to run through.
	p.previewScroll = 0
	p.handleMouseClick(previewClickAction(false, false))
	terminalDragTo(p, 66, 8)
	terminalDragTo(p, 66, 0)
	generation := p.pointer.Generation()

	ticks := 0
	for range tty.AutoScrollMaxRun * 4 {
		_, cmd := p.update(selectionAutoScrollTickMsg{generation: generation})
		if cmd == nil {
			break
		}
		ticks++
	}
	if ticks > tty.AutoScrollMaxRun {
		t.Fatalf("auto-scroll ran %d ticks with no fresh motion, want at most %d",
			ticks, tty.AutoScrollMaxRun)
	}
	if p.terminalSelectionViewportLayout().Start == 0 {
		t.Fatal("the unbounded chain reached the top of the buffer")
	}

	// Real motion re-arms the run.
	if cmd := terminalDragTo(p, 66, 0); cmd == nil {
		t.Fatal("fresh motion past the edge did not restart the auto-scroll chain")
	}
	if _, cmd := p.update(selectionAutoScrollTickMsg{generation: p.pointer.Generation()}); cmd == nil {
		t.Error("the restarted chain gave up immediately")
	}
}

func TestSelectionAutoScrollHoldExpired(t *testing.T) {
	if tty.AutoScrollHoldExpired(tty.AutoScrollMaxRun) {
		t.Error("the run expired one tick early")
	}
	if !tty.AutoScrollHoldExpired(tty.AutoScrollMaxRun + 1) {
		t.Error("the run never expired")
	}
}

// Select-all is its own anchor: a word span left over from an earlier
// double-click must not redefine where a later shift-click extends from.
func TestSelectAllResetsWordGestureBeforeShiftClick(t *testing.T) {
	p := newTerminalDragTestPlugin()
	p.handleMouseDoubleClick(terminalMultiClickAt(mouse.ActionDoubleClick, 11, 6))
	p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane})

	p.selectAllTerminalOutput(false)
	anchor := p.selection.Anchor

	extend := previewClickAction(true, false)
	extend.Y = 8
	p.handleMouseClick(extend)
	if p.selection.Anchor != anchor {
		t.Fatalf("shift-click extended from %+v, want the select-all anchor %+v",
			p.selection.Anchor, anchor)
	}
	if p.selection.End != (ui.SelectionPoint{Line: 4, Col: 18}) {
		t.Errorf("shift-click ended at %+v, want the clicked cell (line 4, col 18)", p.selection.End)
	}
}

// A gesture that walked the panel window back through scrollback and then
// selected nothing hands those rows back as a distance from the live bottom,
// the same half the primary surface pays in thawPreviewGesturePin. Releasing
// the pin instead resumed from the offset behind it and snapped the window back
// to where the gesture started (td-e0a220).
func TestEmptyPanelGestureThawsTheWindowItScrolled(t *testing.T) {
	p := passiveWheelPanelPlugin(t)
	p.termPanelScroll = 0

	// Arming the gesture pins the panel to the window it was armed on, then the
	// edge auto-scroll body walks that window back through the scrollback.
	p.prepareTerminalSelectionSource(true)
	if !p.termPanelFreeze.Active() {
		t.Fatal("test premise: arming the panel gesture pinned no window")
	}
	armed := p.termPanelFreeze.Start()
	p.scrollTerminalSelectionViewport(-5)
	scrolled := p.termPanelFreeze.Start()
	if scrolled >= armed {
		t.Fatalf("test premise: the gesture did not move the window (%d -> %d)", armed, scrolled)
	}

	p.finishInteractiveSelection()

	if p.termPanelFreeze.Active() {
		t.Fatal("the empty release left the gesture pin holding the panel window")
	}
	want := tty.ThawOffsetFrom(scrolled, p.termPanelMaxScroll())
	if want == 0 {
		t.Fatal("test premise: the scrolled window is indistinguishable from the live edge")
	}
	if p.termPanelScroll != want {
		t.Fatalf("empty panel gesture left scroll %d, want the rows it scrolled to at %d",
			p.termPanelScroll, want)
	}
}

// The other half of the same rule: a pin taken at the live edge thaws to offset
// zero, so a click that selects nothing leaves the panel following output
// (td-ac8c74).
func TestEmptyPanelGestureAtTheLiveEdgeResumesFollowing(t *testing.T) {
	p := passiveWheelPanelPlugin(t)
	p.termPanelScroll = 0

	p.prepareTerminalSelectionSource(true)
	p.finishInteractiveSelection()

	if p.termPanelFreeze.Active() || p.termPanelScroll != 0 {
		t.Fatalf("live-edge gesture left frozen %v scroll %d, want a following window",
			p.termPanelFreeze.Active(), p.termPanelScroll)
	}
}

// Clearing a selection outside a gesture — a scroll away from it, leaving
// interactive mode — ends the panel pin the same way, so the rows on screen
// survive the clear instead of snapping back to the pre-gesture offset.
func TestClearingATerminalSelectionThawsThePanelPin(t *testing.T) {
	p := passiveWheelPanelPlugin(t)
	p.termPanelScroll = 0
	p.prepareTerminalSelectionSource(true)
	p.scrollTerminalSelectionViewport(-5)
	pinned := p.termPanelFreeze.Start()

	p.clearTerminalSelection()

	want := tty.ThawOffsetFrom(pinned, p.termPanelMaxScroll())
	if p.termPanelFreeze.Active() || p.termPanelScroll != want {
		t.Fatalf("clear left frozen %v scroll %d, want the thawed offset %d",
			p.termPanelFreeze.Active(), p.termPanelScroll, want)
	}
}

// A document's pin outlives the selection a click made, so neither path ends it.
func TestPanelDocPinSurvivesAnEmptyGesture(t *testing.T) {
	p := passiveWheelPanelPlugin(t)
	p.termPanelScroll = 0
	p.pinTermPanelWindow(20, true)

	p.selectionTermPanel = true
	p.finishInteractiveSelection()
	p.clearTerminalSelection()

	if !p.termPanelFreeze.Active() || p.termPanelFreeze.Start() != 20 || !p.termPanelFreezeDoc {
		t.Fatalf("gesture end dropped the document's pin: active %v start %d doc %v",
			p.termPanelFreeze.Active(), p.termPanelFreeze.Start(), p.termPanelFreezeDoc)
	}
}
