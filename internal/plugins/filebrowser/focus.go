package filebrowser

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
)

// The file browser extends its two-pane Tab toggle with the shell's
// notification centre: tree → preview → centre → tree while the panel is open,
// and exactly the toggle it has always been while the panel is closed.
var _ plugin.FocusCycler = (*Plugin)(nil)

// previewPaneOnScreen answers the same question the `tab` handler asks before
// it moves focus: there is a preview to land on only when a file is loaded in
// it. Reading it here rather than restating the condition keeps the ring and
// the toggle one behaviour.
func (p *Plugin) previewPaneOnScreen() bool {
	return p.previewFile != ""
}

// focusRing lists the windows Tab walks in the browser's ordinary view.
func (p *Plugin) focusRing() []panelayout.Target {
	return panelayout.TwoPaneRing(p.treeVisible, p.previewPaneOnScreen())
}

// currentFocusTarget names the window that holds focus now.
func (p *Plugin) currentFocusTarget() panelayout.Target {
	if p.activePane == PanePreview {
		return panelayout.ContentPaneTarget
	}
	return panelayout.Target{Kind: panelayout.TargetSidebar}
}

// AtFocusCycleEnd reports the wrap point of the ring, and only in the two
// contexts that have one. Every sub-mode of this surface — search, quick open,
// the file-operation modal, blame, info, the inline editor — is either typing
// or is a modal with its own `tab`, and a shell stop must not take the key from
// one of them. FocusContext is the surface's own answer to "what mode am I in",
// so the ring is offered against that rather than against a second list of
// booleans that could drift from it.
func (p *Plugin) AtFocusCycleEnd(reverse bool) bool {
	switch p.FocusContext() {
	case "file-browser-tree", "file-browser-preview":
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
		p.activePane = PanePreview
	} else {
		p.activePane = PaneTree
	}
	return nil
}
