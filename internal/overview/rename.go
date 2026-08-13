package overview

import (
	"fmt"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/shellstate"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const (
	renameShellInputID  = "rename-shell-input"
	renameShellRenameID = "rename-shell-rename"
	renameShellCancelID = "rename-shell-cancel"
	renameShellActionID = "rename-shell-action"
)

// renameShellDoneMsg is the async persist result. The modal stays open on error.
type renameShellDoneMsg struct {
	ID      string
	NewName string
	Err     error
}

// RenameShellOpen reports that the Rename Shell overlay owns the keyboard.
func (m *Model) RenameShellOpen() bool { return m.renameOpen }

// SelectedShell reports that the list cursor is on a shell row.
func (m *Model) SelectedShell() bool {
	workspace, ok := m.SelectedWorkspace()
	return ok && workspace.Kind == workspaceinventory.KindShell
}

// OpenRenameShell opens the same Rename Shell modal the project plugin uses,
// prefilled with the selected shell's display name. Worktrees are ignored.
func (m *Model) OpenRenameShell() tea.Cmd {
	if m.PreviewInteractive() {
		return nil
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.Kind != workspaceinventory.KindShell {
		return nil
	}
	m.closeViewFlyout()
	m.renameOpen = true
	m.renameWorkspace = workspace
	m.renameInput = textinput.New()
	m.renameInput.SetValue(workspace.Name)
	m.renameInput.CharLimit = shellstate.MaxNameBytes
	m.renameInput.SetWidth(30)
	m.renameInput.Prompt = ""
	m.renameError = ""
	m.renameModal = nil
	m.renameModalWidth = 0
	m.ensureRenameShellModal()
	if m.renameModal == nil {
		return nil
	}
	if m.renameMouse == nil {
		m.renameMouse = mouse.NewHandler()
	}
	w, h := m.width, m.height
	if w < 1 {
		w = 80
	}
	if h < 1 {
		h = 24
	}
	_ = m.renameModal.Render(w, h, m.renameMouse)
	m.renameModal.Reset()
	m.renameModal.SetFocus(renameShellInputID)
	return nil
}

func (m *Model) closeRenameShell() {
	m.renameOpen = false
	m.renameWorkspace = workspaceinventory.Workspace{}
	m.renameInput = textinput.Model{}
	m.renameError = ""
	m.renameModal = nil
	m.renameModalWidth = 0
}

// RenameShellPaste inserts pasted text into the rename field.
func (m *Model) RenameShellPaste(text string) bool {
	if !m.renameOpen {
		return false
	}
	m.renameError = ""
	m.renameInput.SetValue(m.renameInput.Value() + text)
	return true
}

func (m *Model) overlayRenameShell(background string, width, height int) string {
	m.ensureRenameShellModal()
	if m.renameModal == nil {
		return background
	}
	if m.renameMouse == nil {
		m.renameMouse = mouse.NewHandler()
	}
	rendered := m.renameModal.Render(width, height, m.renameMouse)
	return ui.OverlayModal(background, rendered, width, height)
}

func (m *Model) ensureRenameShellModal() {
	if !m.renameOpen {
		return
	}
	modalW := 50
	if m.width > 0 && modalW > m.width-4 {
		modalW = m.width - 4
	}
	if modalW < 20 {
		modalW = 20
	}
	if m.renameModal != nil && m.renameModalWidth == modalW {
		return
	}
	m.renameModalWidth = modalW
	m.renameModal = modal.New("Rename Shell",
		modal.WithWidth(modalW),
		modal.WithPrimaryAction(renameShellActionID),
		modal.WithHints(false),
	).
		AddSection(m.renameShellInfoSection()).
		AddSection(modal.Spacer()).
		AddSection(modal.InputWithLabel(renameShellInputID, "New Name:", &m.renameInput)).
		AddSection(modal.When(func() bool { return m.renameError != "" }, m.renameShellErrorSection())).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Rename ", renameShellRenameID),
			modal.Btn(" Cancel ", renameShellCancelID),
		))
}

func (m *Model) renameShellInfoSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if !m.renameOpen {
			return modal.RenderedSection{}
		}
		var sb strings.Builder
		fmt.Fprintf(&sb, "Current: %s", lipgloss.NewStyle().Bold(true).Render(m.renameWorkspace.Name))
		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

func (m *Model) renameShellErrorSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if m.renameError == "" {
			return modal.RenderedSection{}
		}
		content := lipgloss.NewStyle().Foreground(styles.Error).Render("Error: " + m.renameError)
		return modal.RenderedSection{Content: content}
	}, nil)
}

func (m *Model) handleRenameShellKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	key := msg.String()
	// ctrl+c is the host's quit confirmation, same as a focused filter.
	if key == "ctrl+c" {
		return false, nil
	}
	m.ensureRenameShellModal()
	if m.renameModal == nil {
		m.closeRenameShell()
		return true, nil
	}
	// q is quit modal (locked product). Do not type it into the name.
	if key == "q" {
		m.closeRenameShell()
		return true, nil
	}
	if m.renameModal.FocusedID() == renameShellInputID {
		m.renameError = ""
	}
	action, cmd := m.renameModal.HandleKey(msg)
	switch action {
	case "cancel", renameShellCancelID:
		m.closeRenameShell()
		return true, nil
	case renameShellActionID, renameShellRenameID:
		return true, m.executeRenameShell()
	}
	return true, cmd
}

func (m *Model) handleRenameShellMouse(msg tea.MouseMsg) tea.Cmd {
	m.ensureRenameShellModal()
	if m.renameModal == nil || m.renameMouse == nil {
		return nil
	}
	action := m.renameModal.HandleMouse(msg, m.renameMouse)
	switch action {
	case "cancel", renameShellCancelID:
		m.closeRenameShell()
		return nil
	case renameShellActionID, renameShellRenameID:
		return m.executeRenameShell()
	}
	return nil
}

func (m *Model) executeRenameShell() tea.Cmd {
	newName, err := shellstate.NormalizeName(m.renameInput.Value())
	if err != nil {
		m.renameError = err.Error()
		return nil
	}
	workspace := m.renameWorkspace
	path, ok := owningShellsJSON(workspace.ProjectRoot)
	if !ok {
		m.renameError = "owning project shell manifest is unavailable"
		return nil
	}
	id := workspace.ID
	tmuxName := workspace.TmuxName
	if tmuxName == "" {
		tmuxName = workspace.Key
	}
	namespace := workspace.Namespace
	return func() tea.Msg {
		result, err := shellstate.RenameAtPath(path, shellstate.RenameRequest{
			TmuxName:  tmuxName,
			Namespace: namespace,
			Name:      newName,
		})
		name := newName
		if result.Name != "" {
			name = result.Name
		}
		return renameShellDoneMsg{ID: id, NewName: name, Err: err}
	}
}

func (m *Model) applyRenameShell(msg renameShellDoneMsg) {
	if !m.renameOpen || m.renameWorkspace.ID != msg.ID {
		return
	}
	if msg.Err != nil {
		m.renameError = msg.Err.Error()
		return
	}
	m.applyRenamedShell(msg.ID, msg.NewName)
	m.closeRenameShell()
}

func (m *Model) applyRenamedShell(id, newName string) {
	workspace, ok := m.catalog[id]
	if !ok {
		return
	}
	if result, ok := m.results[workspace.ProjectKey]; ok {
		for i := range result.Workspaces {
			if result.Workspaces[i].ID == id {
				result.Workspaces[i].Name = newName
				break
			}
		}
		m.results[workspace.ProjectKey] = result
	}
	m.syncBoard()
	m.workspaces.SelectID(id)
}

// owningShellsJSON is the owning project's shells.json, resolved the same way
// inventory finds it: projectdir.Lookup of ProjectRoot, then the canonical path.
func owningShellsJSON(projectRoot string) (string, bool) {
	if projectRoot == "" {
		return "", false
	}
	dir, ok := projectdir.Lookup(projectRoot)
	if !ok {
		dir, ok = projectdir.Lookup(workspaceinventory.CanonicalPath(projectRoot))
	}
	if !ok {
		return "", false
	}
	return filepath.Join(dir, "shells.json"), true
}
