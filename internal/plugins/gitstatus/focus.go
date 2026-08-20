package gitstatus

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
)

// Git status extends its two-pane Tab toggle with the shell's notification
// centre: sidebar → diff → centre → sidebar while the panel is open, and
// exactly the toggle it has always been while the panel is closed.
var _ plugin.FocusCycler = (*Plugin)(nil)

// diffPaneOnScreen answers the same question the `tab` handler asks before it
// moves focus: there is a diff pane to land on only when something is selected
// to draw in it. Reading it here rather than restating the condition is what
// keeps the ring and the toggle one behaviour.
func (p *Plugin) diffPaneOnScreen() bool {
	return p.selectedDiffFile != "" || p.previewCommit != nil
}

// focusRing lists the windows Tab walks in this surface's status view.
func (p *Plugin) focusRing() []panelayout.Target {
	return panelayout.TwoPaneRing(p.sidebarVisible, p.diffPaneOnScreen())
}

// currentFocusTarget names the window that holds focus now.
func (p *Plugin) currentFocusTarget() panelayout.Target {
	if p.activePane == PaneDiff {
		return panelayout.ContentPaneTarget
	}
	return panelayout.Target{Kind: panelayout.TargetSidebar}
}

// AtFocusCycleEnd reports the wrap point of the status ring — the only view of
// this surface that has one. The full-screen diff, the commit editor, the
// history search and the push/pull menus each own the keyboard for something
// else; a shell stop taking `tab` there would take it from a mode that is still
// using it, or is about to. FocusContext is the surface's own answer to "what
// mode am I in", so the ring is offered against that rather than against a
// second list of booleans that could drift from it.
func (p *Plugin) AtFocusCycleEnd(reverse bool) bool {
	switch p.FocusContext() {
	case "git-status", "git-status-commits", "git-status-diff", "git-commit-preview":
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
		p.activePane = PaneDiff
	} else {
		p.activePane = PaneSidebar
	}
	return nil
}
