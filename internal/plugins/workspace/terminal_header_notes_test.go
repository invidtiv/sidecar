package workspace

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
)

// A header names the key that gets back to the live edge, and it has to be a key
// this surface answers in the state it is in. ⇧End is mapped only inside the
// live terminal component's key hook, which a watched pane never reaches, so
// naming it there advertises a chord that does nothing.
func TestWatchedPaneNamesTheLiveEdgeKeyItAnswers(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.viewMode = ViewModeList
	p.interactiveState = nil
	p.width, p.height = 120, 40
	givePaneScrollableOutput(p, 120)
	p.autoScrollOutput = false
	p.previewOffset = 10

	view := ansi.Strip(p.renderShellOutput(120, 40))
	if !strings.Contains(view, "lines back") {
		t.Fatalf("the watched header never says the window is off the live edge:\n%s", view)
	}
	if strings.Contains(view, tty.LiveEdgeKey) {
		t.Fatalf("the watched header advertises %q, which nothing answers here:\n%s", tty.LiveEdgeKey, view)
	}
	if !strings.Contains(view, watchedLiveEdgeKey+" live") {
		t.Fatalf("the watched header never names the key it does answer:\n%s", view)
	}

	// Answered, not merely advertised.
	p.activePane = PanePreview
	p.handleKeyPress(tea.KeyPressMsg{Code: rune(watchedLiveEdgeKey[0]), Text: watchedLiveEdgeKey})
	if !p.autoScrollOutput {
		t.Fatalf("%q did not put the watched window back on the live edge", watchedLiveEdgeKey)
	}
	if back := ansi.Strip(p.renderShellOutput(120, 40)); strings.Contains(back, "lines back") {
		t.Fatalf("the window is still off the live edge after the key that returns to it:\n%s", back)
	}
}

// The live pane answers the shifted chord — every unshifted key belongs to the
// pane — so that is the one its header names.
func TestLivePaneNamesTheShiftedLiveEdgeKey(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	givePaneScrollableOutput(p, 120)
	p.autoScrollOutput = false
	p.previewOffset = 10

	view := ansi.Strip(p.renderShellOutput(120, 40))
	if !strings.Contains(view, tty.LiveEdgeKey) {
		t.Fatalf("the live header never names the chord it answers:\n%s", view)
	}
}

// The header's notes are the shared derivation's, in its priority order: the
// window being off the live edge leads, because it is the only note that
// explains output the user cannot see, and this header clips from the right —
// so the note that survives clipping has to be the one that matters most. The
// order this surface used before the derivation was shared put "app mouse"
// first, which is the note a reader needs least.
func TestHeaderNotesLeadWithTheWindowBeingOffTheLiveEdge(t *testing.T) {
	p := newInteractiveInputTestPlugin()
	p.width, p.height = 120, 40
	givePaneScrollableOutput(p, 120)
	p.autoScrollOutput = false
	p.previewOffset = 10
	p.interactiveState.MouseReportingEnabled = true

	view := ansi.Strip(p.renderShellOutput(200, 40))
	back := strings.Index(view, "lines back")
	mouse := strings.Index(view, "app mouse")
	if back < 0 || mouse < 0 {
		t.Fatalf("the header dropped a note it owes the reader:\n%s", view)
	}
	if back > mouse {
		t.Fatalf("the header states the mouse before the window is off the live edge:\n%s", view)
	}
}
