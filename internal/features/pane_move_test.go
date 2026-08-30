package features

import "testing"

// pane_move now defaults ON: the keyboard, modal, and agent journeys are
// proven together. It stays a registered flag rather than becoming
// unconditional because the pane header's layout button is visible to every
// user of every surface on the first frame after install, and turning it off
// must remove the whole feature — key, header control, and the CLI verb's
// availability — not part of it.
func TestPaneMoveIsKnownAndDefaultsOn(t *testing.T) {
	if !PaneMove.Default || !DefaultEnabled(PaneMove.Name) {
		t.Fatal("pane_move must default on now that the complete journey has shipped")
	}
	if !IsKnownFeature(PaneMove.Name) {
		t.Fatal("pane_move is absent from the feature registry")
	}
}

// Turning it off must be possible, and must be answered by the same lookup the
// key bindings and the header reserve use.
func TestPaneMoveCanStillBeTurnedOff(t *testing.T) {
	Init(nil)
	t.Cleanup(func() { globalManager = nil })
	SetOverride(PaneMove.Name, false)
	if IsEnabled(PaneMove.Name) {
		t.Fatal("pane_move stayed enabled after being turned off")
	}
	if !DefaultEnabled(PaneMove.Name) {
		t.Fatal("an override changed what the build defaults to")
	}
}
