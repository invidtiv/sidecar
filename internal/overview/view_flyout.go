package overview

import (
	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspacelist"
)

const (
	viewFlyoutSortListID = "view-sort"
	viewFlyoutIdleID     = "show-idle"
	viewFlyoutDoneID     = "done"
)

func workspacesEmptyText(showIdle bool) string {
	if showIdle {
		return "No shells or worktrees found in the configured projects"
	}
	return "no sessions"
}

func (m *Model) ViewFlyoutOpen() bool { return m.viewFlyoutOpen }

func (m *Model) openViewFlyout() {
	m.viewFlyoutOpen = true
	m.viewFlyoutSortIdx = sortIndex(m.workspaces.Sort())
	m.viewFlyout = nil
	m.viewFlyoutWidth = 0
	m.ensureViewFlyout()
	if m.viewFlyout == nil {
		return
	}
	if m.viewFlyoutMouse == nil {
		m.viewFlyoutMouse = mouse.NewHandler()
	}
	// Render once so focus IDs exist before the next Update. Without this,
	// the first key after `s` is dropped (View has not run yet).
	w, h := m.width, m.height
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	_ = m.viewFlyout.Render(w, h, m.viewFlyoutMouse)
	m.viewFlyout.Reset()
	m.viewFlyout.SetFocus(viewFlyoutSortListID)
}

func (m *Model) closeViewFlyout() {
	m.viewFlyoutOpen = false
}

func (m *Model) overlayViewFlyout(background string, width, height int) string {
	m.ensureViewFlyout()
	if m.viewFlyout == nil {
		return background
	}
	if m.viewFlyoutMouse == nil {
		m.viewFlyoutMouse = mouse.NewHandler()
	}
	rendered := m.viewFlyout.Render(width, height, m.viewFlyoutMouse)
	return ui.OverlayModal(background, rendered, width, height)
}

func (m *Model) ensureViewFlyout() {
	modalW := 42
	if m.width > 0 && modalW > m.width-4 {
		modalW = m.width - 4
	}
	if modalW < 20 {
		modalW = 20
	}
	if m.viewFlyout != nil && m.viewFlyoutWidth == modalW {
		return
	}
	m.viewFlyoutWidth = modalW
	m.viewFlyoutSortIdx = sortIndex(m.workspaces.Sort())

	items := make([]modal.ListItem, len(workspacelist.SortModes))
	for i, mode := range workspacelist.SortModes {
		items[i] = modal.ListItem{ID: sortActionID(mode), Label: mode.Label(), Data: mode}
	}

	m.viewFlyout = modal.New("View",
		modal.WithWidth(modalW),
		modal.WithHints(false),
	).
		AddSection(modal.Custom(func(contentWidth int, _, _ string) modal.RenderedSection {
			return modal.RenderedSection{Content: "Current sort: " + m.workspaces.Sort().Label()}
		}, nil)).
		AddSection(modal.Spacer()).
		AddSection(modal.List(viewFlyoutSortListID, items, &m.viewFlyoutSortIdx, modal.WithMaxVisible(len(items)))).
		AddSection(modal.Spacer()).
		AddSection(modal.When(func() bool { return m.workspaces.Filter().Active() },
			modal.Custom(func(contentWidth int, _, _ string) modal.RenderedSection {
				return modal.RenderedSection{Content: "Filter: " + m.workspaces.Filter().Query()}
			}, nil),
		)).
		AddSection(modal.When(func() bool { return !m.workspaces.Filter().Active() },
			modal.Text("Filter: none"),
		)).
		AddSection(modal.Spacer()).
		AddSection(modal.Checkbox(viewFlyoutIdleID, "show idle worktrees", &m.showIdleWorktrees)).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(modal.Btn(" Done ", viewFlyoutDoneID)))
}

func (m *Model) handleViewFlyoutKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	m.ensureViewFlyout()
	if m.viewFlyout == nil {
		return true, nil
	}
	beforeIdle := m.showIdleWorktrees
	action, cmd := m.viewFlyout.HandleKey(msg)
	return true, tea.Batch(cmd, m.applyViewFlyoutAction(action, beforeIdle))
}

func (m *Model) handleViewFlyoutMouse(msg tea.MouseMsg) tea.Cmd {
	m.ensureViewFlyout()
	if m.viewFlyout == nil || m.viewFlyoutMouse == nil {
		return nil
	}
	beforeIdle := m.showIdleWorktrees
	action := m.viewFlyout.HandleMouse(msg, m.viewFlyoutMouse)
	return m.applyViewFlyoutAction(action, beforeIdle)
}

func (m *Model) applyViewFlyoutAction(action string, beforeIdle bool) tea.Cmd {
	switch action {
	case "", viewFlyoutSortListID:
		if m.showIdleWorktrees != beforeIdle {
			return m.persistIdleAndSync()
		}
		return nil
	case "cancel", viewFlyoutDoneID:
		m.closeViewFlyout()
		if m.showIdleWorktrees != beforeIdle {
			return m.persistIdleAndSync()
		}
		return nil
	case viewFlyoutIdleID:
		if m.showIdleWorktrees == beforeIdle {
			m.showIdleWorktrees = !m.showIdleWorktrees
		}
		return m.persistIdleAndSync()
	}
	if mode, ok := sortFromAction(action); ok {
		m.workspaces.SetSort(mode)
		m.viewFlyoutSortIdx = sortIndex(mode)
		m.closeViewFlyout()
		return m.previewSync()
	}
	if m.showIdleWorktrees != beforeIdle {
		return m.persistIdleAndSync()
	}
	return nil
}

func (m *Model) persistIdleAndSync() tea.Cmd {
	_ = saveShowIdleWorktrees(m.showIdleWorktrees)
	m.syncWorkspaces()
	return m.previewSync()
}

func sortIndex(mode workspacelist.Sort) int {
	for i, candidate := range workspacelist.SortModes {
		if candidate == mode {
			return i
		}
	}
	return 0
}

func sortActionID(mode workspacelist.Sort) string {
	return "sort-" + mode.Label()
}

func sortFromAction(action string) (workspacelist.Sort, bool) {
	for _, mode := range workspacelist.SortModes {
		if action == sortActionID(mode) {
			return mode, true
		}
	}
	return 0, false
}
