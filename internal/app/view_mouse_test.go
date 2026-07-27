package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestViewUsesCellMotionMouseMode(t *testing.T) {
	view := (Model{}).View()
	if view.MouseMode != tea.MouseModeCellMotion {
		t.Fatalf("MouseMode = %v, want cell motion", view.MouseMode)
	}
}
