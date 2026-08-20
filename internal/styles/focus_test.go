package styles

import "testing"

// The border rule is the one place exactly-one-focused-pane can be enforced for
// every surface at once, so these are the properties every plugin inherits.
func TestFocusHeldOutsidePanesDrawsTheNormalBorder(t *testing.T) {
	t.Cleanup(func() { SetFocusHeldOutsidePanes(false) })

	SetFocusHeldOutsidePanes(false)
	focused := RenderPanel("body", 20, 5, true)
	normal := RenderPanel("body", 20, 5, false)
	interactive := RenderPanelWithGradient("body", 20, 5, GetInteractiveGradient())
	flash := RenderPanelWithGradient("body", 20, 5, GetFlashGradient())
	if focused == normal {
		t.Fatal("focused and normal borders are indistinguishable; the test proves nothing")
	}

	SetFocusHeldOutsidePanes(true)
	if got := RenderPanel("body", 20, 5, true); got != normal {
		t.Fatal("a pane still drew its focused border while focus was held outside the panes")
	}
	if got := RenderPanelWithGradient("body", 20, 5, GetInteractiveGradient()); got != normal {
		t.Fatal("interactive chrome survived focus moving outside the panes")
	}
	if got := RenderPanelWithGradient("body", 20, 5, GetFlashGradient()); got != normal {
		t.Fatal("flash chrome survived focus moving outside the panes")
	}
	if got := RenderPanel("body", 20, 5, false); got != normal {
		t.Fatal("an unfocused pane changed when focus moved outside the panes")
	}

	// Focus coming back must restore exactly what was there, not an approximation
	// of it: the surface's own focus state was never touched.
	SetFocusHeldOutsidePanes(false)
	if got := RenderPanel("body", 20, 5, true); got != focused {
		t.Fatal("focused border did not come back when focus returned")
	}
	if got := RenderPanelWithGradient("body", 20, 5, GetInteractiveGradient()); got != interactive {
		t.Fatal("interactive chrome did not come back when focus returned")
	}
	if got := RenderPanelWithGradient("body", 20, 5, GetFlashGradient()); got != flash {
		t.Fatal("flash chrome did not come back when focus returned")
	}
}
