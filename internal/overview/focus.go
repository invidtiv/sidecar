package overview

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/panelayout"
)

// focusRing is the windows this tab has on screen, in cycle order. It is the
// arrangement's answer, not the tree's alone: an arrangement that draws no list
// has no sidebar to focus, and one that draws no preview has nothing but the
// list. There is no terminal-panel entry here — the panel is the project
// surface's, and this ring never names one.
func (m *Model) focusRing() []panelayout.Target {
	layout := m.workspacesLayout()
	if layout.listOnly {
		return []panelayout.Target{{Kind: panelayout.TargetSidebar}}
	}
	sidebarVisible := !layout.previewOnly
	return panelayout.Ring(m.preview.paneRoot, sidebarVisible, false)
}

// currentFocusTarget names the window that owns the keyboard now.
func (m *Model) currentFocusTarget() panelayout.Target {
	if !m.PreviewFocused() {
		return panelayout.Target{Kind: panelayout.TargetSidebar}
	}
	return panelayout.Target{Kind: panelayout.TargetLeaf, Leaf: m.preview.paneFocus}
}

// setFocusTarget is the only writer of this surface's focus state. Every
// keyboard cycle and every click routes through it, so a window taking focus
// and the previous one losing it are one act rather than two hand-wired ones.
func (m *Model) setFocusTarget(target panelayout.Target) tea.Cmd {
	if target.Kind == panelayout.TargetSidebar {
		return m.focusList()
	}
	_, cmd := m.focusPreviewLeaf(target.Leaf)
	return cmd
}

// cyclePaneFocus moves the keyboard to the next window on screen, wrapping.
func (m *Model) cyclePaneFocus(reverse bool) tea.Cmd {
	ring := m.focusRing()
	return m.setFocusTarget(panelayout.CycleTarget(ring, m.currentFocusTarget(), reverse))
}
