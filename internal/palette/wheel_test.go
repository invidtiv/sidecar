package palette

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func wheelTestModel(t *testing.T, entries int) *Model {
	t.Helper()
	m := New()
	m.SetSize(120, 40)
	m.filtered = make([]PaletteEntry, entries)
	for i := range m.filtered {
		m.filtered[i] = PaletteEntry{CommandID: "cmd"}
	}
	return &m
}

func paletteWheel(x, y int, up bool) tea.MouseWheelMsg {
	button := tea.MouseWheelDown
	if up {
		button = tea.MouseWheelUp
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: button}
}

func TestPaletteWheelAtBoundary(t *testing.T) {
	inside := func(m *Model) (int, int) {
		// Centre of the modal.
		return m.width / 2, m.height / 2
	}
	tests := []struct {
		name    string
		entries int
		cursor  int
		up      bool
		outside bool
		want    bool
	}{
		{name: "top, up", entries: 40, cursor: 0, up: true, want: true},
		{name: "top, down", entries: 40, cursor: 0, up: false},
		{name: "middle, up", entries: 40, cursor: 20, up: true},
		{name: "middle, down", entries: 40, cursor: 20, up: false},
		{name: "bottom, down", entries: 40, cursor: 39, up: false, want: true},
		{name: "bottom, up", entries: 40, cursor: 39, up: true},
		{name: "no matches, up", entries: 0, cursor: 0, up: true, want: true},
		{name: "no matches, down", entries: 0, cursor: 0, up: false, want: true},
		{name: "single entry, down", entries: 1, cursor: 0, up: false, want: true},
		{name: "outside modal is absorbed", entries: 40, cursor: 20, up: false, outside: true, want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := wheelTestModel(t, tt.entries)
			m.cursor = tt.cursor
			x, y := inside(m)
			if tt.outside {
				x, y = 0, 0
			}
			if got := m.WheelAtBoundary(paletteWheel(x, y, tt.up)); got != tt.want {
				t.Fatalf("WheelAtBoundary = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestPaletteWheelReverseAfterBoundary(t *testing.T) {
	m := wheelTestModel(t, 40)
	m.cursor = 39
	x, y := m.width/2, m.height/2
	if !m.WheelAtBoundary(paletteWheel(x, y, false)) {
		t.Fatal("expected bottom boundary")
	}
	if m.WheelAtBoundary(paletteWheel(x, y, true)) {
		t.Fatal("reverse event after boundary must be movable")
	}
}

// The boundary query must not move the cursor or re-run filtering.
func TestPaletteWheelAtBoundaryIsReadOnly(t *testing.T) {
	m := wheelTestModel(t, 40)
	m.cursor, m.offset = 39, 25
	for i := 0; i < 50; i++ {
		m.WheelAtBoundary(paletteWheel(m.width/2, m.height/2, false))
	}
	if m.cursor != 39 || m.offset != 25 || len(m.filtered) != 40 {
		t.Fatalf("state changed: cursor=%d offset=%d filtered=%d", m.cursor, m.offset, len(m.filtered))
	}
}

// The boundary answer must agree with what the wheel actually does.
func TestPaletteWheelBoundaryMatchesMovement(t *testing.T) {
	for _, cursor := range []int{0, 1, 20, 38, 39} {
		for _, up := range []bool{true, false} {
			m := wheelTestModel(t, 40)
			m.cursor = cursor
			bounded := m.WheelAtBoundary(paletteWheel(m.width/2, m.height/2, up))
			moved, _ := m.handleMouse(paletteWheel(m.width/2, m.height/2, up))
			changed := moved.cursor != cursor
			if bounded == changed {
				t.Fatalf("cursor=%d up=%v: bounded=%v but changed=%v", cursor, up, bounded, changed)
			}
		}
	}
}
