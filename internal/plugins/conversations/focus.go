package conversations

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
)

// Conversations extends its two-pane Tab toggle with the shell's notification
// centre: sidebar → messages → centre → sidebar while the panel is open, and
// exactly the toggle it has always been while the panel is closed.
var _ plugin.FocusCycler = (*Plugin)(nil)

// messagesPaneOnScreen answers the same question the `tab` handler asks before
// it moves focus: there is a message pane to land on only when a session is
// selected to draw in it.
func (p *Plugin) messagesPaneOnScreen() bool {
	return p.selectedSession != ""
}

// focusRing lists the windows Tab walks in the session view.
func (p *Plugin) focusRing() []panelayout.Target {
	return panelayout.TwoPaneRing(p.sidebarVisible, p.messagesPaneOnScreen())
}

// currentFocusTarget names the window that holds focus now.
func (p *Plugin) currentFocusTarget() panelayout.Target {
	if p.activePane == PaneMessages {
		return panelayout.ContentPaneTarget
	}
	return panelayout.Target{Kind: panelayout.TargetSidebar}
}

// AtFocusCycleEnd reports the wrap point of the ring, and only in the two
// contexts that have one. Search, the filter, the content-search modal, the
// resume modal, the turn detail and the analytics view are each a mode of their
// own, and a shell stop must not take `tab` from one of them. FocusContext is
// the surface's own answer to "what mode am I in", so the ring is offered
// against that rather than against a second list of booleans.
func (p *Plugin) AtFocusCycleEnd(reverse bool) bool {
	switch p.FocusContext() {
	case "conversations-sidebar", "conversations-main":
	default:
		return false
	}
	return panelayout.AtRingEnd(p.focusRing(), p.currentFocusTarget(), reverse)
}

// FocusCycleStart puts focus back on the window the toggle resumes at.
func (p *Plugin) FocusCycleStart(reverse bool) tea.Cmd {
	target, ok := panelayout.RingStart(p.focusRing(), reverse)
	if !ok {
		return nil
	}
	if target == panelayout.ContentPaneTarget {
		p.activePane = PaneMessages
	} else {
		p.activePane = PaneSidebar
	}
	return nil
}
