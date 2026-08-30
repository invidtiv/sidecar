package features

import "testing"

func TestPaneMoveIsKnownAndDefaultsOff(t *testing.T) {
	if PaneMove.Default || DefaultEnabled(PaneMove.Name) {
		t.Fatal("pane_move must remain off until the complete journey is proven")
	}
	if !IsKnownFeature(PaneMove.Name) {
		t.Fatal("pane_move is absent from the feature registry")
	}
}
