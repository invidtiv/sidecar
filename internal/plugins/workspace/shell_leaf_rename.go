package workspace

import (
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/shellstate"
)

// A shell leaf has no sidebar row to press R on — the sidebar badges a split
// workspace rather than listing its panes — so its name is renamed from the
// pane itself. The click target is the shared frame's title region, and what it
// opens is the Rename Shell modal the sidebar already uses: one rename surface,
// two ways in.

// openRenameShellLeaf points the rename modal at a shell leaf rather than at a
// shell session. Everything downstream reads renameShellLeafID first, so the
// two targets cannot both be live.
func (p *Plugin) openRenameShellLeaf(leafID int) tea.Cmd {
	leaf := FindPane(p.paneRoot, leafID)
	if leaf == nil || leaf.Split != nil || leaf.Kind != PaneShell {
		return nil
	}
	p.viewMode = ViewModeRenameShell
	p.renameShellSession = nil
	p.renameShellLeafID = leafID
	p.renameShellInput = textinput.New()
	p.renameShellInput.SetValue(p.shellLeafTitle())
	p.renameShellInput.CharLimit = 50
	p.renameShellInput.SetWidth(30)
	p.renameShellInput.Prompt = ""
	p.renameShellError = ""
	p.renameShellModal = nil
	p.renameShellModalWidth = 0
	return nil
}

// clickPaneTitle is the frame's title region arriving as a press. Focus has
// already moved: FocusLeafAt answers from geometry, so the leaf under the
// pointer owns the ring whatever this does next.
func (p *Plugin) clickPaneTitle(data any) tea.Cmd {
	leafID, ok := data.(int)
	if !ok {
		return nil
	}
	return p.openRenameShellLeaf(leafID)
}

// renameShellLeafTarget is the shell leaf the open rename modal is aimed at, or
// nil when it is aimed at a shell session instead.
func (p *Plugin) renameShellLeafTarget() *PaneNode {
	if p.renameShellLeafID == 0 {
		return nil
	}
	return FindPane(p.paneRoot, p.renameShellLeafID)
}

// executeRenameShellLeaf renames the pane, not the tmux session. The session's
// name is this workspace's durable selector — deriving it is how the leaf finds
// its terminal again after a restart — so what the user is naming here is the
// pane's label, which is what the header shows.
func (p *Plugin) executeRenameShellLeaf() tea.Cmd {
	name, err := shellstate.NormalizeName(p.renameShellInput.Value())
	if err != nil {
		p.renameShellError = err.Error()
		return nil
	}
	p.shellLeafName = strings.TrimSpace(name)
	p.viewMode = ViewModeList
	p.clearRenameShellModal()
	return nil
}
