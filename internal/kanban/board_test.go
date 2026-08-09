package kanban

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
)

func fixture() Board {
	return Board{Lanes: []Lane{
		{ID: "shells", Cards: []Card{{ID: "shell:a"}}},
		{ID: "working", Cards: []Card{{ID: "wt:a"}, {ID: "wt:b"}}},
		{ID: "done", Cards: []Card{{ID: "wt:c"}}},
	}}
}

func TestNavigationClampsToLaneContents(t *testing.T) {
	b := fixture()
	s := b.MoveColumn(Selection{Column: 1, Row: 1}, 1)
	if s != (Selection{Column: 2, Row: 0}) {
		t.Fatalf("selection = %#v", s)
	}
	if got := b.MoveRow(Selection{Column: 1}, 99); got.Row != 1 {
		t.Fatalf("row = %d, want 1", got.Row)
	}
}

func TestPreserveSelectionUsesStableCardID(t *testing.T) {
	old := fixture()
	next := Board{Lanes: []Lane{
		{ID: "shells"},
		{ID: "working", Cards: []Card{{ID: "wt:b"}}},
		{ID: "done", Cards: []Card{{ID: "wt:a"}, {ID: "wt:c"}}},
	}}
	got := next.PreserveSelection(old, Selection{Column: 1, Row: 0})
	if got != (Selection{Column: 2, Row: 0}) {
		t.Fatalf("selection = %#v", got)
	}
}

func TestLayoutIsHeightConstrained(t *testing.T) {
	got := CalculateLayout(200, 50, 6, 16, 4)
	if got.ColumnWidth != 31 || got.ContentRows != 44 || got.MaxCards != 11 {
		t.Fatalf("layout = %#v", got)
	}
	short := CalculateLayout(200, 2, 6, 16, 4)
	if short.ContentRows != 4 || short.MaxCards != 1 {
		t.Fatalf("short layout = %#v", short)
	}
}

func TestCompactThreshold(t *testing.T) {
	if !UsesCompact(104, 6, 16, 4) || UsesCompact(105, 6, 16, 4) {
		t.Fatal("compact threshold changed")
	}
}

func TestComponentRenderOwnsScrollHitRegionsAndHeight(t *testing.T) {
	var c Component
	cards := []Card{{ID: "a", Title: "a"}, {ID: "b", Title: "b"}, {ID: "c", Title: "c"}, {ID: "d", Title: "d"}}
	c.SetBoard(Board{Lanes: []Lane{{ID: "work", Label: "Work", Cards: cards}}})
	c.Select(Selection{Row: 3})
	got := c.Render(RenderOptions{Width: 40, Height: 14, Header: "Board", MinColumnWidth: 16, CardHeight: 4})
	if got.Compact || lipgloss.Height(got.View) != 14 {
		t.Fatalf("render compact=%v height=%d", got.Compact, lipgloss.Height(got.View))
	}
	if len(got.Regions) != 3 { // one column plus the two visible cards
		t.Fatalf("regions = %#v", got.Regions)
	}
	selectedRegion := got.Regions[len(got.Regions)-1]
	if selectedRegion.CardID != "d" || selectedRegion.Row != 3 {
		t.Fatalf("selected card was not scrolled into view: %#v", selectedRegion)
	}
	if action := c.HandlePointer(PointerClick, selectedRegion); action.Kind != ActionSelected || action.CardID != "d" {
		t.Fatalf("click action = %#v", action)
	}
	if action := c.HandlePointer(PointerDoubleClick, selectedRegion); action.Kind != ActionActivated || action.CardID != "d" {
		t.Fatalf("double-click action = %#v", action)
	}
	if action := c.HandlePointer(PointerHover, selectedRegion); action.Kind != ActionNone {
		t.Fatalf("hover action = %#v", action)
	}
}

func TestComponentRendersLoadingErrorAndEmptyCells(t *testing.T) {
	var c Component
	c.SetBoard(Board{Lanes: []Lane{
		{ID: "loading", Label: "Loading", State: CellLoading, Message: "loading project"},
		{ID: "error", Label: "Error", State: CellError, Message: "project unavailable"},
		{ID: "empty", Label: "Empty", State: CellEmpty, Message: "no agents"},
	}})
	got := c.Render(RenderOptions{Width: 60, Height: 12, Header: "Board", MinColumnWidth: 16, CardHeight: 4})
	for _, want := range []string{"loading project", "project unavail", "no agents"} {
		if !strings.Contains(got.View, want) {
			t.Fatalf("view lacks %q: %q", want, got.View)
		}
	}
}

func TestComponentReturnsCompactSignal(t *testing.T) {
	var c Component
	c.SetBoard(fixture())
	if got := c.Render(RenderOptions{Width: 40, Height: 10, MinColumnWidth: 16, CardHeight: 4}); !got.Compact || got.View != "" {
		t.Fatalf("compact result = %#v", got)
	}
}
