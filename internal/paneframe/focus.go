package paneframe

import "github.com/marcus/sidecar/internal/styles"

// Focus exclusivity, expressed for the pane tree.
//
// The signal itself lives one layer down, in internal/styles, because every
// bordered pane in the app — pane-tree leaf or plain plugin split — is painted
// by styles.RenderPanel. See styles/focus.go for why it exists and why it is
// process-global. This file is only the pane tree's reading of it, so a caller
// reasoning about pane chrome (a test, a hit-region rule) does not have to reach
// past the frame it composes through.

// SetFocusHeldOutsidePanes tells the frame that an app-level surface outside
// every pane tree owns the keyboard. It is an alias for the shared signal: the
// shell sets it once, around the content render, and both pane surfaces and
// every other plugin inherit it.
func SetFocusHeldOutsidePanes(held bool) { styles.SetFocusHeldOutsidePanes(held) }

// FocusHeldOutsidePanes reports the current signal.
func FocusHeldOutsidePanes() bool { return styles.FocusHeldOutsidePanes() }

// EffectiveChrome is the border a leaf actually wears once app-level focus is
// accounted for: the surface's own answer, or the idle border when focus is held
// outside the pane trees. WrapLeaf applies it, so a surface never has to call
// this; it is exported so a caller that needs to reason about the drawn state
// reads the same rule the renderer used.
func EffectiveChrome(chrome Chrome) Chrome {
	if chrome == ChromeNone {
		return ChromeNone
	}
	if styles.FocusHeldOutsidePanes() {
		return ChromeIdle
	}
	return chrome
}
