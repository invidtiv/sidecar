package palette

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
)

func TestNew(t *testing.T) {
	m := New()

	if m.textInput.Placeholder != "Search commands..." {
		t.Errorf("placeholder should be set")
	}
	if m.maxVisible != 15 {
		t.Errorf("maxVisible should be 15, got %d", m.maxVisible)
	}
}

func TestSetSize(t *testing.T) {
	m := New()
	m.SetSize(100, 50)

	if m.width != 100 {
		t.Errorf("width should be 100, got %d", m.width)
	}
	if m.height != 50 {
		t.Errorf("height should be 50, got %d", m.height)
	}
	if m.maxVisible < 5 {
		t.Errorf("maxVisible should be at least 5, got %d", m.maxVisible)
	}
}

func TestQuery(t *testing.T) {
	m := New()
	m.textInput.SetValue("test")

	if m.Query() != "test" {
		t.Errorf("Query() should return 'test', got %q", m.Query())
	}
}

func TestCursor(t *testing.T) {
	m := New()
	m.cursor = 5

	if m.Cursor() != 5 {
		t.Errorf("Cursor() should return 5, got %d", m.Cursor())
	}
}

func TestOffset(t *testing.T) {
	m := New()
	m.offset = 3

	if m.Offset() != 3 {
		t.Errorf("Offset() should return 3, got %d", m.Offset())
	}
}

func TestMaxVisible(t *testing.T) {
	m := New()

	if m.MaxVisible() != 15 {
		t.Errorf("MaxVisible() should return 15, got %d", m.MaxVisible())
	}
}

func TestFiltered(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{
		{Name: "A"},
		{Name: "B"},
	}

	if len(m.Filtered()) != 2 {
		t.Errorf("Filtered() should return 2 entries, got %d", len(m.Filtered()))
	}
}

func TestSelectedEntry_Valid(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{
		{Name: "First"},
		{Name: "Second"},
	}
	m.cursor = 1

	entry := m.SelectedEntry()
	if entry == nil {
		t.Fatal("SelectedEntry() should not be nil")
	}
	if entry.Name != "Second" {
		t.Errorf("SelectedEntry() should return 'Second', got %q", entry.Name)
	}
}

func TestSelectedEntry_Empty(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{}
	m.cursor = 0

	entry := m.SelectedEntry()
	if entry != nil {
		t.Errorf("SelectedEntry() should be nil for empty list")
	}
}

func TestSelectedEntry_OutOfBounds(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{{Name: "Only"}}
	m.cursor = 5

	entry := m.SelectedEntry()
	if entry != nil {
		t.Errorf("SelectedEntry() should be nil for out of bounds cursor")
	}
}

func TestMoveCursor_Down(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{{}, {}, {}}
	m.cursor = 0
	m.maxVisible = 10

	m.moveCursor(1)
	if m.cursor != 1 {
		t.Errorf("cursor should be 1 after moving down, got %d", m.cursor)
	}
}

func TestMoveCursor_Up(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{{}, {}, {}}
	m.cursor = 2
	m.maxVisible = 10

	m.moveCursor(-1)
	if m.cursor != 1 {
		t.Errorf("cursor should be 1 after moving up, got %d", m.cursor)
	}
}

func TestMoveCursor_ClampBottom(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{{}, {}, {}}
	m.cursor = 2
	m.maxVisible = 10

	m.moveCursor(5)
	if m.cursor != 2 {
		t.Errorf("cursor should be clamped to 2, got %d", m.cursor)
	}
}

func TestMoveCursor_ClampTop(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{{}, {}, {}}
	m.cursor = 0
	m.maxVisible = 10

	m.moveCursor(-5)
	if m.cursor != 0 {
		t.Errorf("cursor should be clamped to 0, got %d", m.cursor)
	}
}

func TestMoveCursor_ScrollOffset(t *testing.T) {
	m := New()
	m.filtered = make([]PaletteEntry, 20)
	m.cursor = 0
	m.offset = 0
	m.maxVisible = 5

	// Move down past visible area
	m.moveCursor(10)
	if m.offset == 0 {
		t.Errorf("offset should adjust when cursor moves past visible area")
	}
}

func TestUpdate_KeyDown(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{{}, {}, {}}
	m.cursor = 0
	m.maxVisible = 10

	msg := tea.KeyPressMsg{Code: tea.KeyDown}
	newModel, _ := m.Update(msg)

	if newModel.cursor != 1 {
		t.Errorf("down key should move cursor to 1, got %d", newModel.cursor)
	}
}

func TestUpdate_KeyUp(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{{}, {}, {}}
	m.cursor = 2
	m.maxVisible = 10

	msg := tea.KeyPressMsg{Code: tea.KeyUp}
	newModel, _ := m.Update(msg)

	if newModel.cursor != 1 {
		t.Errorf("up key should move cursor to 1, got %d", newModel.cursor)
	}
}

func TestUpdate_KeyEnter(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{
		{CommandID: "test-cmd", Context: "test-ctx"},
	}
	m.cursor = 0

	msg := tea.KeyPressMsg{Code: tea.KeyEnter}
	_, cmd := m.Update(msg)

	if cmd == nil {
		t.Fatal("enter key should return a command")
	}

	// Execute the command to get the message
	result := cmd()
	selectedMsg, ok := result.(CommandSelectedMsg)
	if !ok {
		t.Fatal("command should return CommandSelectedMsg")
	}
	if selectedMsg.CommandID != "test-cmd" {
		t.Errorf("CommandID should be 'test-cmd', got %q", selectedMsg.CommandID)
	}
	if selectedMsg.Context != "test-ctx" {
		t.Errorf("Context should be 'test-ctx', got %q", selectedMsg.Context)
	}
}

func TestUpdate_CtrlP(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{{}, {}, {}}
	m.cursor = 2
	m.maxVisible = 10

	msg := tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	newModel, _ := m.Update(msg)

	if newModel.cursor != 1 {
		t.Errorf("ctrl+p should move cursor up, got %d", newModel.cursor)
	}
}

func TestUpdate_CtrlN(t *testing.T) {
	m := New()
	m.filtered = []PaletteEntry{{}, {}, {}}
	m.cursor = 0
	m.maxVisible = 10

	msg := tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
	newModel, _ := m.Update(msg)

	if newModel.cursor != 1 {
		t.Errorf("ctrl+n should move cursor down, got %d", newModel.cursor)
	}
}

func TestUpdate_CtrlD_PageDown(t *testing.T) {
	m := New()
	m.filtered = make([]PaletteEntry, 30)
	m.cursor = 0
	m.maxVisible = 10

	msg := tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}
	newModel, _ := m.Update(msg)

	if newModel.cursor != 10 {
		t.Errorf("ctrl+d should page down by maxVisible, got %d", newModel.cursor)
	}
}

func TestUpdate_CtrlU_PageUp(t *testing.T) {
	m := New()
	m.filtered = make([]PaletteEntry, 30)
	m.cursor = 20
	m.maxVisible = 10

	msg := tea.KeyPressMsg{Code: 'u', Mod: tea.ModCtrl}
	newModel, _ := m.Update(msg)

	if newModel.cursor != 10 {
		t.Errorf("ctrl+u should page up by maxVisible, got %d", newModel.cursor)
	}
}

func TestMinMax(t *testing.T) {
	if min(5, 10) != 5 {
		t.Errorf("min(5, 10) should be 5")
	}
	if min(10, 5) != 5 {
		t.Errorf("min(10, 5) should be 5")
	}
	if max(5, 10) != 10 {
		t.Errorf("max(5, 10) should be 10")
	}
	if max(10, 5) != 10 {
		t.Errorf("max(10, 5) should be 10")
	}
}

func TestUpdate_TabToggle(t *testing.T) {
	m := New()
	m.allEntries = []PaletteEntry{
		{CommandID: "cmd-1", Context: "ctx-a"},
		{CommandID: "cmd-2", Context: "ctx-b"},
	}
	m.activeContext = "ctx-a"
	m.refilter()

	if m.ShowAllContexts() {
		t.Fatal("expected default to be current context mode")
	}

	// Press Tab
	newModel, _ := m.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if !newModel.ShowAllContexts() {
		t.Fatal("tab should toggle to all contexts mode")
	}

	// Press Tab again
	newModel2, _ := newModel.Update(tea.KeyPressMsg{Code: tea.KeyTab})
	if newModel2.ShowAllContexts() {
		t.Fatal("tab should toggle back to current context mode")
	}
}

func TestUpdate_MouseClick(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	m.filtered = []PaletteEntry{
		{CommandID: "cmd-0", Context: "ctx-0", Key: "k0"},
		{CommandID: "cmd-1", Context: "ctx-1", Key: "k1"},
	}

	// Render once to populate hit targets
	view := m.View()
	if view == "" {
		t.Fatal("View should not be empty")
	}

	// Find the hit region for item 1
	var targetRegion *mouse.Region
	for _, r := range m.mouseHandler.HitMap.Regions() {
		if r.ID == paletteItemPrefix+"1" {
			targetRegion = &r
			break
		}
	}
	if targetRegion == nil {
		t.Fatal("hit region for palette-item-1 not found")
	}

	clickMsg := tea.MouseClickMsg{
		X:      targetRegion.Rect.X + 2,
		Y:      targetRegion.Rect.Y,
		Button: tea.MouseLeft,
	}

	updatedModel, cmd := m.Update(clickMsg)
	if updatedModel.cursor != 1 {
		t.Errorf("cursor after click = %d, want 1", updatedModel.cursor)
	}
	if cmd == nil {
		t.Fatal("mouse click on entry should return execution command")
	}

	msg := cmd()
	selectedMsg, ok := msg.(CommandSelectedMsg)
	if !ok {
		t.Fatalf("expected CommandSelectedMsg, got %T", msg)
	}
	if selectedMsg.CommandID != "cmd-1" || selectedMsg.Context != "ctx-1" || selectedMsg.Key != "k1" {
		t.Errorf("unexpected selectedMsg: %+v", selectedMsg)
	}
}

func TestView_ScrollbarAndStableHeight(t *testing.T) {
	m := New()
	m.SetSize(80, 24)
	entries := make([]PaletteEntry, 30)
	for i := 0; i < 30; i++ {
		entries[i] = PaletteEntry{
			Name:      fmt.Sprintf("Command %02d", i),
			CommandID: fmt.Sprintf("cmd-%d", i),
		}
	}
	m.filtered = entries
	m.allEntries = entries

	var initialLines int
	for cur := 0; cur < len(entries); cur++ {
		m.cursor = cur
		m.clearModal()
		v := m.View()

		// Verify scrollbar characters are present
		if !strings.Contains(v, "┃") && !strings.Contains(v, "│") {
			t.Errorf("cursor %d: scrollbar characters missing from view", cur)
		}

		lines := strings.Count(v, "\n") + 1
		if cur == 0 {
			initialLines = lines
		} else if lines != initialLines {
			t.Fatalf("cursor %d: modal height changed to %d lines (expected %d lines)", cur, lines, initialLines)
		}
	}
}
