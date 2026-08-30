package paneframe

import "testing"

// The pane tree's reading of the shared signal: while an app-level surface holds
// the keyboard, every leaf wears the idle border no matter what its host says.
func TestLeafChromeIsIdleWhileFocusIsHeldOutsideThePanes(t *testing.T) {
	t.Cleanup(func() { SetFocusHeldOutsidePanes(false) })
	outer := Box{W: 24, H: 6}

	SetFocusHeldOutsidePanes(false)
	idle := WrapLeaf("body", outer, ChromeIdle)
	for _, chrome := range []Chrome{ChromeActive, ChromeInteractive, ChromeFlash} {
		if WrapLeaf("body", outer, chrome) == idle {
			t.Fatalf("chrome %v is indistinguishable from idle; the test proves nothing", chrome)
		}
	}

	SetFocusHeldOutsidePanes(true)
	for _, chrome := range []Chrome{ChromeIdle, ChromeActive, ChromeInteractive, ChromeFlash} {
		if got := EffectiveChrome(chrome); got != ChromeIdle {
			t.Fatalf("EffectiveChrome(%v) = %v, want ChromeIdle", chrome, got)
		}
		if WrapLeaf("body", outer, chrome) != idle {
			t.Fatalf("leaf with chrome %v still drew a lit border under a focused app-level surface", chrome)
		}
	}

	SetFocusHeldOutsidePanes(false)
	if got := EffectiveChrome(ChromeActive); got != ChromeActive {
		t.Fatalf("EffectiveChrome(ChromeActive) = %v after focus returned, want ChromeActive", got)
	}
}
