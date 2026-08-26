package overview

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/termpanes"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	previewSplitCloseConfirmID = "preview-split-close-confirm"
	previewSplitCloseCancelID  = "preview-split-close-cancel"
)

var (
	probePreviewTerminal       = termpanes.ProbeClose
	killPreviewTerminalSession = termpanes.KillSession
)

type previewSplitCloseProbeMsg struct {
	WorkspaceID string
	LeafID      int
	Session     string
	Evidence    termpanes.CloseEvidence
	Err         error
}

func (m *Model) requestClosePreviewShellLeaf(leafID int) tea.Cmd {
	leaf := m.preview.terminalPanes.Leaf(leafID)
	if leaf == nil || leaf.Target.Source != "shell" {
		return nil
	}
	session := strings.TrimSpace(leaf.Session)
	if session == "" {
		return m.closePreviewShellLeaf(leafID, termpanes.CloseSessionEnded)
	}
	workspaceID := m.preview.workspaceID
	return func() tea.Msg {
		evidence, err := probePreviewTerminal(session)
		return previewSplitCloseProbeMsg{WorkspaceID: workspaceID, LeafID: leafID, Session: session, Evidence: evidence, Err: err}
	}
}

func (m *Model) applyPreviewSplitCloseProbe(msg previewSplitCloseProbeMsg) tea.Cmd {
	leaf := m.preview.terminalPanes.Leaf(msg.LeafID)
	if leaf == nil || msg.WorkspaceID != m.preview.workspaceID || leaf.Session != msg.Session {
		return nil
	}
	if m.renameOpen || m.createOpen || m.deleteOpen || m.viewFlyoutOpen || m.PreviewInteractive() {
		return nil
	}
	if msg.Err != nil {
		return m.closePreviewShellLeaf(msg.LeafID, termpanes.CloseSessionEnded)
	}
	if !termpanes.CloseNeedsConfirm(msg.Evidence.CurrentCommand, msg.Evidence.ShellCommand) {
		return m.closePreviewShellLeaf(msg.LeafID, termpanes.CloseExplicit)
	}
	m.previewSplitCloseLeaf = msg.LeafID
	m.previewSplitCloseCommand = strings.TrimSpace(msg.Evidence.CurrentCommand)
	m.previewSplitCloseModal = nil
	m.previewSplitCloseModalW = 0
	return nil
}

func (m *Model) closePreviewShellLeaf(leafID int, mode termpanes.CloseMode) tea.Cmd {
	leaf := m.preview.terminalPanes.Leaf(leafID)
	if leaf == nil || leaf.Target.Source != "shell" {
		return nil
	}
	session := strings.TrimSpace(leaf.Session)
	if state, ok := leaf.HostState.(*previewTerminalState); ok && state != nil && state.terminal != nil {
		state.terminal.ReleaseInput()
		if state.terminal.IsActive() {
			state.terminal.Close()
		}
	}
	leaf.Interactive = false
	m.preview.paneRoot, m.preview.paneFocus = panelayout.Close(m.preview.paneRoot, leafID)
	m.preview.paneNextID = panelayout.MaxID(m.preview.paneRoot) + 1
	m.preview.terminalPanes.Release(leafID)
	m.cancelPreviewSplitClose()
	var ended tea.Cmd
	if mode == termpanes.CloseExplicit {
		ended = killPreviewTerminalSession(session)
	}
	return tea.Batch(ended, m.syncTerminalGeometry())
}

func (m *Model) ensurePreviewSplitCloseModal() {
	if m.previewSplitCloseLeaf == 0 {
		return
	}
	modalW := 50
	if m.width > 0 && modalW > m.width-4 {
		modalW = m.width - 4
	}
	if modalW < 20 {
		modalW = 20
	}
	if m.previewSplitCloseModal != nil && m.previewSplitCloseModalW == modalW {
		return
	}
	m.previewSplitCloseModalW = modalW
	running := m.previewSplitCloseCommand
	if running == "" {
		running = "A process"
	}
	m.previewSplitCloseModal = modal.New("Close Terminal Split?",
		modal.WithWidth(modalW), modal.WithVariant(modal.VariantWarning), modal.WithHints(false),
	).
		AddSection(modal.Text(lipgloss.NewStyle().Foreground(styles.Warning).Render(running + " is running in this terminal."))).
		AddSection(modal.Spacer()).
		AddSection(modal.Text(styles.Muted.Render("The split collapses and its tmux session is closed."))).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Close ", previewSplitCloseConfirmID, modal.BtnPrimary()),
			modal.Btn(" Cancel ", previewSplitCloseCancelID),
		))
}

func (m *Model) overlayPreviewSplitClose(background string, width, height int) string {
	m.ensurePreviewSplitCloseModal()
	if m.previewSplitCloseModal == nil {
		return background
	}
	return ui.OverlayModal(background, m.previewSplitCloseModal.Render(width, height, m.workspacesMouse), width, height)
}

func (m *Model) cancelPreviewSplitClose() {
	m.previewSplitCloseLeaf = 0
	m.previewSplitCloseCommand = ""
	m.previewSplitCloseModal = nil
	m.previewSplitCloseModalW = 0
}

func (m *Model) confirmPreviewSplitClose() tea.Cmd {
	leafID := m.previewSplitCloseLeaf
	return m.closePreviewShellLeaf(leafID, termpanes.CloseExplicit)
}

func (m *Model) handlePreviewSplitCloseKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if msg.String() == "ctrl+c" {
		return false, nil
	}
	m.ensurePreviewSplitCloseModal()
	if m.previewSplitCloseModal == nil {
		m.cancelPreviewSplitClose()
		return true, nil
	}
	switch msg.String() {
	case "esc", "q":
		m.cancelPreviewSplitClose()
		return true, nil
	case "j", "down", "l", "right":
		m.previewSplitCloseModal.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
		return true, nil
	case "k", "up", "h", "left":
		m.previewSplitCloseModal.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		return true, nil
	}
	action, cmd := m.previewSplitCloseModal.HandleKey(msg)
	switch action {
	case "cancel", previewSplitCloseCancelID:
		m.cancelPreviewSplitClose()
		return true, nil
	case previewSplitCloseConfirmID:
		return true, m.confirmPreviewSplitClose()
	}
	return true, cmd
}

func (m *Model) handlePreviewSplitCloseMouse(msg tea.MouseMsg) tea.Cmd {
	m.ensurePreviewSplitCloseModal()
	if m.previewSplitCloseModal == nil {
		return nil
	}
	switch m.previewSplitCloseModal.HandleMouse(msg, m.workspacesMouse) {
	case "cancel", previewSplitCloseCancelID:
		m.cancelPreviewSplitClose()
	case previewSplitCloseConfirmID:
		return m.confirmPreviewSplitClose()
	}
	return nil
}
