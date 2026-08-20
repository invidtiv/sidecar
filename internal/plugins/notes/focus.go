package notes

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/plugin"
)

// Notes extends its two-pane Tab toggle with the shell's notification centre:
// list → note → centre → list while the panel is open, and exactly the toggle
// it has always been while the panel is closed.
var _ plugin.FocusCycler = (*Plugin)(nil)

// focusEditorPane and focusListPane are the two halves of what `tab` has always
// meant on this surface, extracted so the ring moves focus by running the same
// code the key does rather than by restating it. Landing on the body focuses
// the resting view so `m` and j/k work; Enter/i/click are what enter edit.
func (p *Plugin) focusEditorPane() {
	if p.editorNote == nil {
		return
	}
	p.activePane = PaneEditor
	if !p.previewMode {
		p.leaveEditToView()
	}
	p.editorTextarea.Blur()
}

func (p *Plugin) focusListPane() tea.Cmd {
	if !p.previewMode {
		p.leaveEditToView()
	}
	p.activePane = PaneList
	return p.saveEditorContent()
}

// editorPaneOnScreen answers the same question the `tab` handler asks before it
// moves focus: there is a note pane to land on only when a note is open in it.
func (p *Plugin) editorPaneOnScreen() bool {
	return p.editorNote != nil
}

// focusRing lists the windows Tab walks. The list is always drawn.
func (p *Plugin) focusRing() []panelayout.Target {
	return panelayout.TwoPaneRing(true, p.editorPaneOnScreen())
}

// currentFocusTarget names the window that holds focus now.
func (p *Plugin) currentFocusTarget() panelayout.Target {
	if p.activePane == PaneEditor && p.editorNote != nil {
		return panelayout.ContentPaneTarget
	}
	return panelayout.Target{Kind: panelayout.TargetSidebar}
}

// AtFocusCycleEnd reports the wrap point of the ring, and only in the two
// contexts that have one. Search, the modals, the inline editor and — above all
// — the editing pane keep `tab` for themselves: in `notes-editor` the key
// saves and leaves the edit, which is a mode exit, not a focus cycle.
// FocusContext is the surface's own answer to "what mode am I in", so the ring
// is offered against that rather than against a second list of booleans.
func (p *Plugin) AtFocusCycleEnd(reverse bool) bool {
	switch p.FocusContext() {
	case "notes-list", "notes-preview":
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
		p.focusEditorPane()
		return nil
	}
	return p.focusListPane()
}
