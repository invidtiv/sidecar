package modal

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/mouse"
)

func testComboItems() []DropdownItem {
	return []DropdownItem{
		{ID: "a", Label: "alpha", Value: "alpha"},
		{ID: "b", Label: "beta", Value: "beta"},
		{ID: "c", Label: "gamma", Value: "gamma"},
		{ID: "d", Label: "delta", Value: "delta"},
		{ID: "e", Label: "epsilon", Value: "eps"},
	}
}

func newTestInput(value string) *textinput.Model {
	ti := textinput.New()
	ti.SetValue(value)
	ti.CursorEnd()
	return &ti
}

func comboKey(s string) tea.KeyPressMsg {
	switch s {
	case "esc":
		return tea.KeyPressMsg{Code: tea.KeyEscape}
	case "enter":
		return tea.KeyPressMsg{Code: tea.KeyEnter}
	case "tab":
		return tea.KeyPressMsg{Code: tea.KeyTab}
	case "shift+tab":
		return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift}
	case "up":
		return tea.KeyPressMsg{Code: tea.KeyUp}
	case "down":
		return tea.KeyPressMsg{Code: tea.KeyDown}
	case "ctrl+p":
		return tea.KeyPressMsg{Code: 'p', Mod: tea.ModCtrl}
	case "ctrl+n":
		return tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl}
	default:
		r := []rune(s)
		if len(r) == 1 {
			return tea.KeyPressMsg{Code: r[0], Text: s}
		}
		return tea.KeyPressMsg{Text: s}
	}
}

func newComboModal(t *testing.T, opts ...ComboOption) (*Modal, *textinput.Model, *int) {
	t.Helper()
	ti := newTestInput("")
	sel := 2
	m := New("Create", WithWidth(44), WithHints(false), WithPrimaryAction("create")).
		AddSection(Combo("combo", ti, testComboItems(), &sel, opts...)).
		AddSection(Buttons(Btn(" Create ", "create"), Btn(" Cancel ", "cancel"))).
		AddSection(Text("filler-one")).
		AddSection(Text("filler-two")).
		AddSection(Text("filler-three")).
		AddSection(Text("filler-four")).
		AddSection(Text("filler-five")).
		AddSection(Text("filler-six")).
		AddSection(Text("filler-seven")).
		AddSection(Text("filler-eight"))
	return m, ti, &sel
}

func TestComboOverlayDoesNotChangeMeasuredHeight(t *testing.T) {
	m, _, _ := newComboModal(t, WithOpenOnFocus(false), WithComboMaxVisible(8))
	handler := mouse.NewHandler()

	closed := m.Render(80, 24, handler)
	closedH := lipgloss.Height(closed)

	m.HandleKey(comboKey("down"))
	open := m.Render(80, 24, handler)
	openH := lipgloss.Height(open)

	if closedH != openH {
		t.Fatalf("overlay changed modal height: closed=%d open=%d", closedH, openH)
	}
	if !strings.Contains(stripANSI(open), "alpha") {
		t.Fatalf("expected open overlay to show items, got %q", stripANSI(open))
	}

	combo := Combo("c", newTestInput(""), testComboItems(), nil, WithOpenOnFocus(true))
	closedSec := combo.Render(40, "", "")
	openSec := combo.Render(40, "c", "")
	if measureHeight(closedSec.Content) != measureHeight(openSec.Content) {
		t.Fatalf("section content height changed: closed=%d open=%d",
			measureHeight(closedSec.Content), measureHeight(openSec.Content))
	}
	if openSec.Overlay == nil || measureHeight(openSec.Overlay.Content) == 0 {
		t.Fatal("expected overlay content when focused")
	}
}

func TestComboFilterSelectsIndexZero(t *testing.T) {
	m, ti, sel := newComboModal(t)
	handler := mouse.NewHandler()
	m.Render(80, 24, handler)

	if *sel != 2 {
		t.Fatalf("precondition: selected=%d, want 2", *sel)
	}

	m.HandleKey(comboKey("b"))
	if ti.Value() != "b" {
		t.Fatalf("typed value = %q, want %q", ti.Value(), "b")
	}
	if *sel != 0 {
		t.Fatalf("after filter selected=%d, want 0 (top match)", *sel)
	}

	// Top match should be beta; committing writes that value.
	m.HandleKey(comboKey("tab"))
	if ti.Value() != "beta" {
		t.Fatalf("committed %q, want %q", ti.Value(), "beta")
	}
}

func TestComboEscConsumedThenCancels(t *testing.T) {
	m, _, _ := newComboModal(t)
	handler := mouse.NewHandler()
	m.Render(80, 24, handler)

	if m.FocusedID() != "combo" {
		t.Fatalf("focused %q, want combo", m.FocusedID())
	}

	action, _ := m.HandleKey(comboKey("esc"))
	if action != "" {
		t.Fatalf("first Esc with open overlay: action=%q, want empty (consumed)", action)
	}

	// Overlay stays closed while focused; second Esc cancels the modal.
	action, _ = m.HandleKey(comboKey("esc"))
	if action != "cancel" {
		t.Fatalf("second Esc: action=%q, want cancel", action)
	}

	// Closed overlay / other sections still cancel on the first Esc.
	plain := New("Plain", WithHints(false)).
		AddSection(Input("name", newTestInput("x"))).
		AddSection(Buttons(Btn(" OK ", "ok")))
	plain.Render(80, 24, mouse.NewHandler())
	action, _ = plain.HandleKey(comboKey("esc"))
	if action != "cancel" {
		t.Fatalf("input-focused Esc: action=%q, want cancel", action)
	}
}

func TestComboTabCommitsAndMovesFocus(t *testing.T) {
	m, ti, sel := newComboModal(t)
	handler := mouse.NewHandler()
	m.Render(80, 24, handler)

	*sel = 1
	action, _ := m.HandleKey(comboKey("tab"))
	if action != "" {
		t.Fatalf("Tab action=%q, want empty", action)
	}
	if ti.Value() != "beta" {
		t.Fatalf("Tab committed %q, want beta", ti.Value())
	}
	if m.FocusedID() != "create" {
		t.Fatalf("focus after Tab = %q, want create", m.FocusedID())
	}

	// Shift+Tab from the button should land back on the combo.
	m.HandleKey(comboKey("shift+tab"))
	if m.FocusedID() != "combo" {
		t.Fatalf("focus after Shift+Tab = %q, want combo", m.FocusedID())
	}
}

func TestComboEnterReturnsPrimaryAction(t *testing.T) {
	m, ti, sel := newComboModal(t)
	handler := mouse.NewHandler()
	m.Render(80, 24, handler)

	*sel = 3
	action, _ := m.HandleKey(comboKey("enter"))
	if action != "create" {
		t.Fatalf("Enter action=%q, want create", action)
	}
	if ti.Value() != "delta" {
		t.Fatalf("Enter committed %q, want delta", ti.Value())
	}
}

func TestComboEnterWithoutSubmitDoesNotSubmit(t *testing.T) {
	m, ti, sel := newComboModal(t, WithComboSubmitOnEnter(false))
	handler := mouse.NewHandler()
	m.Render(80, 24, handler)

	*sel = 0
	action, _ := m.HandleKey(comboKey("enter"))
	if action != "" {
		t.Fatalf("Enter without submit-on-enter: action=%q, want empty", action)
	}
	if ti.Value() != "alpha" {
		t.Fatalf("committed %q, want alpha", ti.Value())
	}
	if m.FocusedID() != "combo" {
		t.Fatalf("focus should stay on combo, got %q", m.FocusedID())
	}
}

func TestComboHighlightMoves(t *testing.T) {
	sel := 0
	ti := newTestInput("")
	s := Combo("combo", ti, testComboItems(), &sel, WithOpenOnFocus(true))
	s.Render(40, "combo", "")

	s.Update(comboKey("down"), "combo")
	if sel != 1 {
		t.Fatalf("down: selected=%d, want 1", sel)
	}
	s.Update(comboKey("ctrl+n"), "combo")
	if sel != 2 {
		t.Fatalf("ctrl+n: selected=%d, want 2", sel)
	}
	s.Update(comboKey("up"), "combo")
	if sel != 1 {
		t.Fatalf("up: selected=%d, want 1", sel)
	}
	s.Update(comboKey("ctrl+p"), "combo")
	if sel != 0 {
		t.Fatalf("ctrl+p: selected=%d, want 0", sel)
	}
}

func TestComboOverlayHitRegionsLandOnOverlayRows(t *testing.T) {
	m, ti, _ := newComboModal(t, WithComboMaxVisible(4))
	handler := mouse.NewHandler()
	rendered := m.Render(80, 24, handler)

	regions := handler.HitMap.Regions()
	var itemRegion, createRegion *mouse.Region
	for i := range regions {
		switch regions[i].ID {
		case comboItemID("combo", 0):
			itemRegion = &regions[i]
		case "create":
			createRegion = &regions[i]
		}
	}
	if itemRegion == nil {
		t.Fatal("expected overlay item hit region")
	}
	if createRegion == nil {
		t.Fatal("expected create button hit region")
	}

	hit := handler.HitMap.Test(itemRegion.Rect.X+1, itemRegion.Rect.Y)
	if hit == nil || hit.ID != comboItemID("combo", 0) {
		t.Fatalf("overlay cell hit %v, want %s", hit, comboItemID("combo", 0))
	}

	// Overlay sits on the button row and is registered last, so the button
	// cell must resolve to the item, not "create".
	if !createRegion.Rect.Contains(itemRegion.Rect.X, itemRegion.Rect.Y) {
		t.Fatalf("expected overlay row to cover the create button (item=%+v create=%+v)",
			itemRegion.Rect, createRegion.Rect)
	}
	hit = handler.HitMap.Test(itemRegion.Rect.X, itemRegion.Rect.Y)
	if hit == nil || hit.ID != comboItemID("combo", 0) {
		t.Fatalf("covered button cell hit %v, want overlay item", hit)
	}

	// Render() returns the modal box; hit regions are in screen coordinates.
	modalY := (24 - lipgloss.Height(rendered)) / 2
	lineIdx := itemRegion.Rect.Y - modalY
	lines := strings.Split(rendered, "\n")
	if lineIdx < 0 || lineIdx >= len(lines) {
		t.Fatalf("overlay region y=%d (modal line %d) outside render (%d lines)", itemRegion.Rect.Y, lineIdx, len(lines))
	}
	if !strings.Contains(stripANSI(lines[lineIdx]), "alpha") {
		t.Fatalf("overlay region y=%d (modal line %d) renders %q, want alpha", itemRegion.Rect.Y, lineIdx, stripANSI(lines[lineIdx]))
	}

	action := m.HandleMouse(tea.MouseClickMsg{
		X:      itemRegion.Rect.X + 1,
		Y:      itemRegion.Rect.Y,
		Button: tea.MouseLeft,
	}, handler)
	if action != "" {
		t.Fatalf("overlay click action=%q, want empty (no submit)", action)
	}
	if ti.Value() != "alpha" {
		t.Fatalf("overlay click committed %q, want alpha", ti.Value())
	}
}

func TestComboValueFallsBackToLabel(t *testing.T) {
	ti := newTestInput("")
	sel := 0
	items := []DropdownItem{{ID: "x", Label: "only-label"}}
	s := Combo("combo", ti, items, &sel)
	s.Render(40, "combo", "")
	s.Update(comboKey("enter"), "combo")
	if ti.Value() != "only-label" {
		t.Fatalf("commit value=%q, want only-label", ti.Value())
	}
}

func TestPlaceOverlayFlipsAboveWhenClipped(t *testing.T) {
	ov := Overlay{Content: "one\ntwo\nthree", OffsetY: 1}
	// Section at y=8 in a 10-line viewport: below would clip, above fits.
	p, ok := placeOverlay(ov, 8, 0, 10)
	if !ok {
		t.Fatal("expected placement")
	}
	if p.y != 5 { // 8 - 3
		t.Fatalf("flipped y=%d, want 5", p.y)
	}

	// Section at the top: flipping would hide the overlay, so stay below.
	p, ok = placeOverlay(ov, 0, 0, 10)
	if !ok {
		t.Fatal("expected placement")
	}
	if p.y != 1 {
		t.Fatalf("top-of-viewport y=%d, want 1 (below)", p.y)
	}
}
