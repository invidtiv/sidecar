package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestViewDefaultsToAllMotionMouseMode(t *testing.T) {
	view := (Model{}).View()
	if view.MouseMode != tea.MouseModeAllMotion {
		t.Fatalf("MouseMode = %v, want all motion", view.MouseMode)
	}
}
