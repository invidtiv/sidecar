package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/notify"
	"github.com/marcus/sidecar/internal/plugin"
)

// cyclerPlugin is a surface that owns a Tab ring, like the workspace surfaces
// do. It answers the one question plugin.FocusCycler asks and records the
// handback.
type cyclerPlugin struct {
	sizingPlugin
	atEnd    bool
	restarts []bool
}

func (p *cyclerPlugin) AtFocusCycleEnd(bool) bool { return p.atEnd }
func (p *cyclerPlugin) FocusCycleStart(reverse bool) tea.Cmd {
	p.restarts = append(p.restarts, reverse)
	return nil
}

func tabKey() tea.KeyPressMsg      { return tea.KeyPressMsg{Code: tea.KeyTab} }
func shiftTabKey() tea.KeyPressMsg { return tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift} }
func (m *Model) pressTab()         { m.handleKeyMsg(tabKey()) }
func (m *Model) pressShiftTab()    { m.handleKeyMsg(shiftTabKey()) }

// The plain case: a surface at the wrap point of its own ring. The open panel
// is one more stop on the cycle — tab in, tab onward — and it never closes.
func TestTabCyclesIntoAndOutOfTheOpenCentre(t *testing.T) {
	p := &cyclerPlugin{sizingPlugin: sizingPlugin{id: "workspace"}, atEnd: true}
	m := centreTestModelWithPlugin(t, p)
	postCentreNotification(t, &m, notify.SourceTasks, "task one")

	m.toggleNotificationCentre()
	if !m.notificationCentreOwnsKeys() {
		t.Fatal("opening should focus the panel")
	}

	m.pressTab()
	if !m.notificationCentreOpen {
		t.Fatal("tab closed the panel; it is a focus stop, not a toggle")
	}
	if m.notificationCentreOwnsKeys() {
		t.Fatal("tab out of the panel left the keyboard on it")
	}
	if m.activeContext == notificationCentreContext {
		t.Fatalf("activeContext = %q, want the content's context", m.activeContext)
	}

	m.pressTab()
	if !m.notificationCentreOwnsKeys() {
		t.Fatal("tab did not move focus back into the open panel")
	}
	if m.activeContext != notificationCentreContext {
		t.Fatalf("activeContext = %q, want %q", m.activeContext, notificationCentreContext)
	}

	// shift+tab is the same stop in reverse.
	m.pressShiftTab()
	if m.notificationCentreOwnsKeys() {
		t.Fatal("shift+tab out of the panel left the keyboard on it")
	}
	m.pressShiftTab()
	if !m.notificationCentreOwnsKeys() {
		t.Fatal("shift+tab did not move focus back into the open panel")
	}
}

// With the panel closed, tab is exactly what it was: the shell must not take it.
func TestTabIsUntouchedWhileTheCentreIsClosed(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "files"})
	if handled, _ := m.notificationCentreTabKey(tabKey()); handled {
		t.Fatal("the shell claimed tab with the panel closed")
	}
	if m.notificationCentreFocused {
		t.Fatal("tab focused a panel that is not open")
	}
}

// A surface that binds tab for itself keeps it. The centre is reachable from
// there through alt+n, N, and the pointer — not by stealing a pane switch.
func TestTabStaysWithASurfaceThatBindsIt(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "git-status"})
	m.toggleNotificationCentre()
	m.blurNotificationCentre()

	if handled, _ := m.notificationCentreTabKey(tabKey()); handled {
		t.Fatal("the centre took tab from a context that binds it")
	}
	if m.notificationCentreFocused {
		t.Fatal("focus moved to the centre from a surface that owns tab")
	}
}

// A surface with a context of its own but no ring keeps tab even when nothing
// in the keymap registry says so: several surfaces switch panes on a hard-coded
// tab, and a stop the centre steals from one of them is a broken pane toggle.
func TestTabStaysWithASurfaceThatHasNoRing(t *testing.T) {
	m := centreTestModel(t, &sizingPlugin{id: "notes-list"})
	m.toggleNotificationCentre()
	m.blurNotificationCentre()

	if handled, _ := m.notificationCentreTabKey(tabKey()); handled {
		t.Fatal("the centre took tab from a surface that never offered its ring")
	}
	if m.notificationCentreFocused {
		t.Fatal("focus moved to the centre from a surface with no ring")
	}
}

// A surface with a ring hands over only at the wrap point, and gets focus back
// at the entry its cycle resumes at.
func TestTabJoinsAPaneRingAtItsWrapPoint(t *testing.T) {
	p := &cyclerPlugin{sizingPlugin: sizingPlugin{id: "workspace"}}
	m := centreTestModelWithPlugin(t, p)
	m.toggleNotificationCentre()
	m.blurNotificationCentre()

	if handled, _ := m.notificationCentreTabKey(tabKey()); handled {
		t.Fatal("the centre took tab mid-ring; the surface had panes left to visit")
	}

	p.atEnd = true
	if handled, _ := m.notificationCentreTabKey(tabKey()); !handled {
		t.Fatal("the centre did not take tab at the ring's wrap point")
	}
	if !m.notificationCentreOwnsKeys() {
		t.Fatal("the centre did not take the keyboard at the wrap point")
	}

	m.pressShiftTab()
	if m.notificationCentreOwnsKeys() {
		t.Fatal("shift+tab did not leave the panel")
	}
	if len(p.restarts) != 1 || !p.restarts[0] {
		t.Fatalf("restarts = %v, want one reverse handback to the surface", p.restarts)
	}
}

func centreTestModelWithPlugin(t *testing.T, p plugin.Plugin) Model {
	t.Helper()
	m := centreTestModel(t)
	if err := m.registry.Register(p); err != nil {
		t.Fatal(err)
	}
	m.updateContext()
	return m
}

// A surface that implements FocusCycler answers for its own tab, whatever the
// keymap says the key runs there. The two-pane surfaces bind `switch-pane`; the
// embedded td monitor registers td's own `next-panel`; git's diff pane
// registers nothing at all. All three are cycles, and the ring's answer is what
// decides — not the name the surface gave its cycle.
func TestARingWinsOverTheRegisteredBinding(t *testing.T) {
	for _, ctx := range []string{"git-status", "conversations-main", "file-browser-preview", "workspace-preview", "td-monitor"} {
		t.Run(ctx, func(t *testing.T) {
			p := &cyclerPlugin{sizingPlugin: sizingPlugin{id: ctx}, atEnd: true}
			m := centreTestModelWithPlugin(t, p)
			// td's cycle is registered under its own command name; the shell
			// must still ask the ring rather than reading that as a claim.
			if ctx == "td-monitor" {
				m.keymap.RegisterPluginBinding("tab", "next-panel", ctx)
			}
			m.toggleNotificationCentre()
			m.blurNotificationCentre()

			if handled, _ := m.notificationCentreTabKey(tabKey()); !handled {
				t.Fatalf("%s: the centre stood aside at the ring's wrap point", ctx)
			}
			if !m.notificationCentreOwnsKeys() {
				t.Fatalf("%s: the centre did not take the keyboard", ctx)
			}
		})
	}
}

// contextCyclerPlugin is a surface whose focus context changes when its ring
// resumes — every real two-pane surface does, since the context names the
// focused pane.
type contextCyclerPlugin struct {
	cyclerPlugin
	resumed bool
}

func (p *contextCyclerPlugin) FocusContext() string {
	if p.resumed {
		return "git-status"
	}
	return "git-status-diff"
}
func (p *contextCyclerPlugin) FocusCycleStart(reverse bool) tea.Cmd {
	p.resumed = true
	return p.cyclerPlugin.FocusCycleStart(reverse)
}

// Tabbing out of the panel must leave the shell describing the window the ring
// resumed at, not the one the keyboard just left: the footer is read from the
// context, and asking for it before the handback answered one window late.
func TestTabOutOfTheCentreReReadsTheSurfaceContext(t *testing.T) {
	p := &contextCyclerPlugin{cyclerPlugin: cyclerPlugin{sizingPlugin: sizingPlugin{id: "git-status"}, atEnd: true}}
	m := centreTestModelWithPlugin(t, p)
	m.toggleNotificationCentre()

	m.pressTab()
	if !p.resumed {
		t.Fatal("tab out of the panel did not resume the surface's ring")
	}
	if m.activeContext != "git-status" {
		t.Fatalf("activeContext = %q, want the window the ring resumed at", m.activeContext)
	}
}
