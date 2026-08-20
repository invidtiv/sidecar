package app

import (
	"testing"

	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/styles"
)

// focusProbePlugin records the app-wide focus signal as it was while the plugin
// was rendering. That is the only thing worth asserting from here: the shell's
// job is to say "focus is held outside the panes" for exactly the span in which
// a surface draws, and a plugin must not need to know the centre exists to hear
// it.
type focusProbePlugin struct {
	sizingPlugin
	heldDuringView []bool
}

func (p *focusProbePlugin) View(width, height int) string {
	p.heldDuringView = append(p.heldDuringView, styles.FocusHeldOutsidePanes())
	return ""
}

func (p *focusProbePlugin) lastHeld() bool {
	if len(p.heldDuringView) == 0 {
		return false
	}
	return p.heldDuringView[len(p.heldDuringView)-1]
}

func focusProbeModel(t *testing.T) (Model, *focusProbePlugin) {
	t.Helper()
	probe := &focusProbePlugin{sizingPlugin: sizingPlugin{id: "files"}}
	m := centreTestModel(t)
	if err := m.registry.Register(probe); err != nil {
		t.Fatal(err)
	}
	if len(m.registry.Plugins()) != 1 {
		t.Fatalf("probe plugin did not register: %d plugins", len(m.registry.Plugins()))
	}
	return m, probe
}

// Two panes drawn focused at once was the bug: the centre lit its border while
// the surface underneath kept its focused pane lit, because the surface has no
// way to learn that something outside its own tree took the keyboard. The shell
// tells it, once, around the content render.
func TestCentreFocusBlursTheSurfaceUnderneath(t *testing.T) {
	t.Cleanup(func() { styles.SetFocusHeldOutsidePanes(false) })
	m, probe := focusProbeModel(t)
	postCentreNotification(t, &m, notify.SourceAgent, "build finished")

	m.viewContent()
	if probe.lastHeld() {
		t.Fatal("focus reported as held outside the panes with the centre closed")
	}

	m.toggleNotificationCentre()
	m.focusNotificationCentre()
	if !m.notificationCentreOwnsKeys() {
		t.Fatal("centre did not take the keyboard")
	}
	m.viewContent()
	if !probe.lastHeld() {
		t.Fatal("surface rendered without being told the centre holds the keyboard, so its focused pane stayed lit")
	}
	if styles.FocusHeldOutsidePanes() {
		t.Fatal("signal outlived the content render; the centre panel, toasts and modals would blur too")
	}

	// Focus back on the surface: its focused pane must re-light.
	m.blurNotificationCentre()
	m.viewContent()
	if probe.lastHeld() {
		t.Fatal("surface stayed blurred after focus returned to it")
	}
}

// Attention is not focus. A toast or a pane flash must leave focus chrome
// exactly where it was, or every notification would read as a focus change.
func TestToastsAndFlashDoNotTouchFocusChrome(t *testing.T) {
	t.Cleanup(func() { styles.SetFocusHeldOutsidePanes(false) })
	m, probe := focusProbeModel(t)

	postCentreNotification(t, &m, notify.SourceAgent, "agent waiting")
	m.viewContent()
	if probe.lastHeld() {
		t.Fatal("a live toast blurred the focused pane underneath")
	}
	if styles.FocusHeldOutsidePanes() {
		t.Fatal("toast rendering left the focus signal set")
	}

	// The centre merely being open — visible but not focused — is not focus
	// either: the keyboard is still on the surface.
	m.toggleNotificationCentre()
	m.blurNotificationCentre()
	m.viewContent()
	if probe.lastHeld() {
		t.Fatal("an open but unfocused centre blurred the surface")
	}
}
