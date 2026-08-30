package overview

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	appmsg "github.com/marcus/sidecar/internal/msg"
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
	remoteReply
	ID      string
	NewName string
	Err     error
}

// RenameShellOpen reports that the rename overlay owns the keyboard.
func (m *Model) RenameShellOpen() bool { return m.renameOpen }

// RenameWorktreeOpen reports that the worktree display-name modal is open.
func (m *Model) RenameWorktreeOpen() bool {
	return m.renameOpen && m.renameWorkspace.Kind == workspaceinventory.KindWorktree
}

// SelectedShell reports that the list cursor is on a shell row.
func (m *Model) SelectedShell() bool {
	workspace, ok := m.SelectedWorkspace()
	return ok && workspace.Kind == workspaceinventory.KindShell
}

// OpenRenameShell opens the Rename Shell modal, prefilled with the selected
// shell's display name.
func (m *Model) OpenRenameShell() tea.Cmd {
	m.renameTerminalLeafID = 0
	return m.openRename(workspaceinventory.KindShell)
}

// OpenRenameTerminalLeaf opens the same rename surface for a peer terminal.
// The leaf ID is the identity: the selected catalog row is only its owner and
// must not accidentally rename a persisted shell record.
func (m *Model) OpenRenameTerminalLeaf(leafID int) tea.Cmd {
	if m.PreviewInteractive() {
		return nil
	}
	leaf := m.preview.terminalPanes.Leaf(leafID)
	if leaf == nil || leaf.Target.Source != "shell" {
		return nil
	}
	m.closeViewFlyout()
	m.renameTerminalLeafID = leafID
	m.renameOpen = true
	m.renameBusy = false
	m.renameWorkspace = workspaceinventory.Workspace{Kind: workspaceinventory.KindShell, Name: leaf.Name}
	m.renameInput = textinput.New()
	m.renameInput.SetValue(leaf.Name)
	m.renameInput.CharLimit = shellstate.MaxNameBytes
	m.renameInput.SetWidth(30)
	m.renameInput.Prompt = ""
	m.renameError = ""
	m.renameModal = nil
	m.renameModalWidth = 0
	m.ensureRenameShellModal()
	if m.renameModal != nil {
		m.renameModal.Reset()
		m.renameModal.SetFocus(renameShellInputID)
	}
	return nil
}

// OpenRenameWorktree opens the display-name modal for the selected worktree.
// The git branch and directory are not renamed.
func (m *Model) OpenRenameWorktree() tea.Cmd {
	return m.openRename(workspaceinventory.KindWorktree)
}

func (m *Model) openRename(kind workspaceinventory.Kind) tea.Cmd {
	m.renameTerminalLeafID = 0
	if m.PreviewInteractive() {
		return nil
	}
	workspace, ok := m.SelectedWorkspace()
	if !ok || workspace.Kind != kind {
		return nil
	}
	// One guard, on the shared path both rename entry points delegate to.
	if reason := remoteActionRefusal(workspace, "rename"); reason != "" {
		return appmsg.Blocked(reason)
	}
	// A permitted remote verb still needs a host that can be asked. A disabled
	// host's rows stay on screen as last-known state, and opening a rename on
	// one would end in an ssh failure reported as "removed or retargeted".
	if reason := m.remoteHostUnavailable(workspace.HostID); reason != "" {
		return appmsg.Blocked(reason)
	}
	m.closeViewFlyout()
	m.renameOpen = true
	m.renameBusy = false
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
	m.renameBusy = false
	m.renameWorkspace = workspaceinventory.Workspace{}
	m.renameInput = textinput.Model{}
	m.renameError = ""
	m.renameModal = nil
	m.renameModalWidth = 0
	m.renameTerminalLeafID = 0
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
	title := "Rename Shell"
	if m.renameWorkspace.Kind == workspaceinventory.KindWorktree {
		title = "Rename Worktree"
	}
	m.renameModal = modal.New(title,
		modal.WithWidth(modalW),
		modal.WithPrimaryAction(renameShellActionID),
		modal.WithHints(false),
	).
		AddSection(m.renameShellInfoSection()).
		AddSection(modal.Spacer()).
		AddSection(modal.InputWithLabel(renameShellInputID, "New Name:", &m.renameInput)).
		AddSection(modal.When(func() bool { return m.renameError != "" }, m.renameShellErrorSection())).
		AddSection(modal.When(func() bool { return m.renameBusy }, m.renameShellBusySection())).
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

// renameShellBusySection is the in-flight line, the same treatment the create
// modal shows while it waits: a remote rename is a whole ssh round trip, and a
// modal that looks idle invites the second Enter the busy guard exists to stop.
func (m *Model) renameShellBusySection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if !m.renameBusy {
			return modal.RenderedSection{}
		}
		return modal.RenderedSection{Content: "Renaming…"}
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
	// While a rename is in flight nothing is accepted — the same treatment the
	// create flow's createBusy gives. A second Enter here dispatched a second
	// rename that raced the first on the host.
	if m.renameBusy {
		return true, nil
	}
	m.ensureRenameShellModal()
	if m.renameModal == nil {
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
		return true, m.executeRename()
	}
	return true, cmd
}

func (m *Model) handleRenameShellMouse(msg tea.MouseMsg) tea.Cmd {
	if m.renameBusy {
		return nil
	}
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
		return m.executeRename()
	}
	return nil
}

func (m *Model) executeRename() tea.Cmd {
	// Resolved from the workspace this modal was opened on, every time it is
	// submitted. Nothing here remembers that a previous rename was remote.
	if m.renameTerminalLeafID == 0 && m.renameWorkspace.Remote() {
		return m.executeRemoteRename()
	}
	if m.renameTerminalLeafID != 0 {
		newName, err := shellstate.NormalizeName(m.renameInput.Value())
		if err != nil {
			m.renameError = err.Error()
			return nil
		}
		leaf := m.preview.terminalPanes.Leaf(m.renameTerminalLeafID)
		if leaf == nil || leaf.Target.Source != "shell" {
			m.closeRenameShell()
			return nil
		}
		leaf.Name = newName
		m.closeRenameShell()
		m.persistSessionsLayout()
		return nil
	}
	if m.renameWorkspace.Kind == workspaceinventory.KindWorktree {
		return m.executeRenameWorktree()
	}
	return m.executeRenameShell()
}

// executeRemoteRename renames a shell or a worktree on the host that owns it.
//
// One call for both kinds: `sidecar shell rename --target` resolves the target
// against that project's manifest and dispatches to the shell record or the
// worktree display name itself, which is the same dispatch the local path makes
// — made on the machine that can actually see the state.
//
// The name is normalised here as well as there so a bad name is refused without
// a round trip; the host's answer still wins.
func (m *Model) executeRemoteRename() tea.Cmd {
	newName, err := shellstate.NormalizeName(m.renameInput.Value())
	if err != nil {
		m.renameError = err.Error()
		return nil
	}
	m.renameBusy = true
	return m.renameRemoteWorkspace(m.renameWorkspace, newName)
}

func (m *Model) executeRenameWorktree() tea.Cmd {
	newName, err := shellstate.NormalizeName(m.renameInput.Value())
	if err != nil {
		m.renameError = err.Error()
		return nil
	}
	workspace := m.renameWorkspace
	root := workspace.ProjectRoot
	if root == "" {
		m.renameError = "owning project worktree state is unavailable"
		return nil
	}
	id := workspace.ID
	path := workspace.Path
	m.renameBusy = true
	return func() tea.Msg {
		err := persistWorktreeDisplayName(root, path, newName)
		return renameShellDoneMsg{ID: id, NewName: newName, Err: err}
	}
}

func persistWorktreeDisplayName(projectRoot, worktreePath, name string) error {
	if projectRoot == "" || worktreePath == "" {
		return fmt.Errorf("owning project worktree state is unavailable")
	}
	dir, err := projectdir.WorktreeDir(projectRoot, worktreePath)
	if err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "display-name"), []byte(name+"\n"), 0644)
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
	m.renameBusy = true
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
	// Whatever the answer says, the round trip it belongs to is over.
	m.renameBusy = false
	if m.hostReplyStale(msg.HostID, msg.Incarnation) {
		// The host was removed or retargeted while the rename was in flight.
		// Whatever it did is not this configuration's to show.
		m.renameError = remoteReplyDropped(msg.HostID)
		return
	}
	if msg.Err != nil {
		m.renameError = remoteActionError(msg.Err)
		return
	}
	m.applyRenamedShell(msg.ID, msg.NewName)
	m.closeRenameShell()
}

// applyRenamedShell shows the new name immediately, on whichever machine's
// results the row came from. A host's next snapshot restates it either way;
// this is only so the row does not lag the confirmation.
func (m *Model) applyRenamedShell(id, newName string) {
	workspace, ok := m.catalog[id]
	if !ok {
		return
	}
	renameIn := func(results []workspaceinventory.Workspace) bool {
		for i := range results {
			if results[i].ID == id {
				results[i].Name = newName
				return true
			}
		}
		return false
	}
	if workspace.HostID != "" {
		for _, result := range m.hostResults[workspace.HostID] {
			if renameIn(result.Workspaces) {
				break
			}
		}
	} else if result, ok := m.results[workspace.ProjectKey]; ok {
		renameIn(result.Workspaces)
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
