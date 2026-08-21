package workspace

import (
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// The split terminal's ✕ asks tmux what is running in the pane before it
// closes it, because the answer is what decides whether the user is asked.
// shellCloseNeedsConfirm is the state-free rule; everything here is the wiring
// that gets it an answer and acts on it.

const (
	shellCloseConfirmID = "shell-close-confirm"
	shellCloseCancelID  = "shell-close-cancel"
)

// ShellLeafCloseProbeMsg carries what tmux said about the split terminal's pane
// when its ✕ was clicked. Err (or a session that no longer answers) means the
// pane is gone, which is a close with nothing to confirm.
type ShellLeafCloseProbeMsg struct {
	Session        string
	CurrentCommand string
	ShellCommand   string
	Err            error

	// Mode is the view mode the ✕ was clicked in. The probe is a tmux subprocess
	// spawn, which on an EDR machine is long enough for the user to have opened
	// the create modal or entered interactive mode in the meantime; this is the
	// only async modal-open path in the plugin, so it carries its own precondition
	// rather than assuming the mode it left behind.
	Mode ViewMode
}

// loginShellCommand is the shell a session of ours was started with. tmux
// starts split sessions with the user's login shell, so $SHELL is the same
// answer without a second tmux round trip.
func loginShellCommand() string {
	return filepath.Base(strings.TrimSpace(os.Getenv("SHELL")))
}

// requestCloseShellLeaf is the header ✕: probe the pane, then close or ask.
// The probe is a command rather than an inline tmux call because this runs on
// the update path, where a blocking spawn is a dropped frame.
func (p *Plugin) requestCloseShellLeaf() tea.Cmd {
	if !p.termPanelVisible {
		return nil
	}
	session := strings.TrimSpace(p.termPanelSession)
	if session == "" {
		return p.closeShellLeaf(shellCloseSessionEnded)
	}
	shell := loginShellCommand()
	mode := p.viewMode
	return func() tea.Msg {
		evidence, err := capturePaneEvidence(session)
		return ShellLeafCloseProbeMsg{
			Session:        session,
			CurrentCommand: evidence.CurrentCommand,
			ShellCommand:   shell,
			Err:            err,
			Mode:           mode,
		}
	}
}

// handleShellLeafCloseProbe acts on the probe: a pane that is gone, or one
// running nothing but its own shell, closes immediately; anything else asks.
func (p *Plugin) handleShellLeafCloseProbe(msg ShellLeafCloseProbeMsg) tea.Cmd {
	if !p.termPanelVisible || msg.Session != strings.TrimSpace(p.termPanelSession) {
		return nil
	}
	// The gesture is abandoned if the user has moved on: an async answer must not
	// force a modal over a view the user chose after clicking, nor yank them out
	// of interactive mode. Clicking ✕ again from the list re-asks.
	if p.viewMode != msg.Mode || p.viewMode != ViewModeList {
		return nil
	}
	if p.interactiveState != nil && p.interactiveState.Active {
		return nil
	}
	if msg.Err != nil {
		// tmux could not resolve the target: the session is already gone, so
		// there is no running process to ask about.
		return p.closeShellLeaf(shellCloseSessionEnded)
	}
	if !shellCloseNeedsConfirm(msg.CurrentCommand, msg.ShellCommand) {
		return p.closeShellLeaf(shellCloseExplicit)
	}
	p.shellCloseCommand = strings.TrimSpace(msg.CurrentCommand)
	p.viewMode = ViewModeConfirmCloseSplit
	p.clearConfirmCloseSplitModal()
	return nil
}

// ensureConfirmCloseSplitModal builds the split-terminal close confirmation.
func (p *Plugin) ensureConfirmCloseSplitModal() {
	modalW := 50
	if modalW > p.width-4 {
		modalW = p.width - 4
	}
	if modalW < 20 {
		modalW = 20
	}
	if p.closeSplitModal != nil && p.closeSplitModalWidth == modalW {
		return
	}
	p.closeSplitModalWidth = modalW

	running := p.shellCloseCommand
	if running == "" {
		running = "a process"
	}
	p.closeSplitModal = modal.New("Close Terminal Split?",
		modal.WithWidth(modalW),
		modal.WithVariant(modal.VariantWarning),
		modal.WithHints(false),
	).
		AddSection(modal.Text(lipgloss.NewStyle().Foreground(styles.Warning).
			Render(running + " is running in this terminal."))).
		AddSection(modal.Spacer()).
		AddSection(modal.Text(dimText("The split collapses and its tmux session is closed."))).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Close ", shellCloseConfirmID, modal.BtnPrimary()),
			modal.Btn(" Cancel ", shellCloseCancelID),
		))
}

func (p *Plugin) clearConfirmCloseSplitModal() {
	p.closeSplitModal = nil
	p.closeSplitModalWidth = 0
}

func (p *Plugin) confirmCloseSplit() tea.Cmd {
	p.viewMode = ViewModeList
	p.shellCloseCommand = ""
	p.clearConfirmCloseSplitModal()
	return p.closeShellLeaf(shellCloseExplicit)
}

func (p *Plugin) cancelCloseSplit() tea.Cmd {
	p.viewMode = ViewModeList
	p.shellCloseCommand = ""
	p.clearConfirmCloseSplitModal()
	return nil
}

// handleConfirmCloseSplitKeys is the modal's keyboard, in the shape every other
// confirm in this plugin uses.
func (p *Plugin) handleConfirmCloseSplitKeys(msg tea.KeyPressMsg) tea.Cmd {
	p.ensureConfirmCloseSplitModal()
	if p.closeSplitModal == nil {
		return nil
	}
	switch msg.String() {
	case "esc", "q":
		return p.cancelCloseSplit()
	case "j", "down", "l", "right":
		p.closeSplitModal.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab})
		return nil
	case "k", "up", "h", "left":
		p.closeSplitModal.HandleKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
		return nil
	}
	action, cmd := p.closeSplitModal.HandleKey(msg)
	switch action {
	case "cancel", shellCloseCancelID:
		return p.cancelCloseSplit()
	case shellCloseConfirmID:
		return p.confirmCloseSplit()
	}
	return cmd
}

func (p *Plugin) handleConfirmCloseSplitModalMouse(msg tea.MouseMsg) tea.Cmd {
	p.ensureConfirmCloseSplitModal()
	if p.closeSplitModal == nil {
		return nil
	}
	switch p.closeSplitModal.HandleMouse(msg, p.mouseHandler) {
	case "":
		return nil
	case "cancel", shellCloseCancelID:
		return p.cancelCloseSplit()
	case shellCloseConfirmID:
		return p.confirmCloseSplit()
	}
	return nil
}

// renderConfirmCloseSplitModal overlays the confirmation on the list view.
func (p *Plugin) renderConfirmCloseSplitModal(width, height int) string {
	p.ensureConfirmCloseSplitModal()
	background := p.renderListView(width, height)
	if p.closeSplitModal == nil {
		return background
	}
	return ui.OverlayModal(background, p.closeSplitModal.Render(width, height, p.mouseHandler), width, height)
}
