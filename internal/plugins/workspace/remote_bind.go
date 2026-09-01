package workspace

import (
	"fmt"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func (p *Plugin) remoteBound() bool {
	return p.ctx != nil && p.ctx.HostID != ""
}

func (p *Plugin) applyHostInventory() {
	if !p.remoteBound() {
		return
	}
	var workspaces []plugin.HostWorkspace
	if p.ctx.HostWorkspaces != nil {
		workspaces = p.ctx.HostWorkspaces()
	}
	selected := ""
	if shell := p.getSelectedShell(); shell != nil {
		selected = shell.TmuxName
	}
	shells := make([]*ShellSession, 0)
	worktrees := make([]*Worktree, 0)
	for _, ws := range workspaces {
		switch workspaceinventory.Kind(ws.Kind) {
		case workspaceinventory.KindShell:
			shells = append(shells, hostWorkspaceToShell(ws))
		case workspaceinventory.KindWorktree:
			worktrees = append(worktrees, hostWorkspaceToWorktree(ws))
		}
	}
	p.shells = shells
	p.worktrees = worktrees
	p.worktreesLoaded = true
	p.nestedByWorkDir = nil
	if selected != "" {
		for i, shell := range p.shells {
			if shell.TmuxName == selected {
				p.selectedShellIdx = i
				p.shellSelected = true
				return
			}
		}
	}
	if len(p.shells) > 0 {
		p.selectedShellIdx = 0
		p.shellSelected = true
		return
	}
	p.shellSelected = false
	p.selectedShellIdx = 0
}

func hostWorkspaceToShell(ws plugin.HostWorkspace) *ShellSession {
	session := ws.TmuxName
	if session == "" {
		session = ws.Key
	}
	shell := &ShellSession{
		Name:        ws.Name,
		TmuxName:    session,
		WorkDir:     ws.Path,
		InventoryID: ws.ID,
		CreatedAt:   ws.CreatedAt,
		ChosenAgent: AgentType(ws.Provider),
	}
	if session != "" || ws.PaneID != "" {
		shell.Agent = &Agent{
			Type:        AgentType(ws.Provider),
			TmuxSession: session,
			TmuxPane:    ws.PaneID,
			OutputBuf:   tty.NewOutputBuffer(outputBufferCap),
		}
	}
	if !ws.Live && session != "" {
		shell.IsOrphaned = true
	}
	return shell
}

func hostWorkspaceToWorktree(ws plugin.HostWorkspace) *Worktree {
	wt := &Worktree{
		Key:             ws.Key,
		Name:            ws.Name,
		Path:            ws.Path,
		Branch:          ws.Branch,
		TaskID:          ws.TaskID,
		IsMain:          ws.IsMain,
		IsMissing:       ws.IsMissing,
		IsBare:          ws.IsBare,
		IsDetached:      ws.IsDetached,
		IsLocked:        ws.IsLocked,
		IsPrunable:      ws.IsPrunable,
		ChosenAgentType: AgentType(ws.Provider),
		Status:          StatusPaused,
	}
	if ws.TmuxName != "" {
		wt.Agent = &Agent{
			Type:        AgentType(ws.Provider),
			TmuxSession: ws.TmuxName,
			TmuxPane:    ws.PaneID,
			OutputBuf:   tty.NewOutputBuffer(outputBufferCap),
		}
		wt.Status = StatusActive
	}
	return wt
}

func (p *Plugin) applyTerminalControl(model *tty.Model) {
	if model == nil {
		return
	}
	if p.remoteBound() {
		var spawn tty.ControlSpawner
		if p.ctx.RemoteControlSpawner != nil {
			spawn = p.ctx.RemoteControlSpawner()
		}
		if spawn != nil {
			model.UseRemoteControl(spawn)
		}
		return
	}
	model.UseLocalControl()
}

func (p *Plugin) remoteControlReady() bool {
	if !p.remoteBound() {
		return true
	}
	if p.ctx.RemoteControlSpawner == nil {
		return false
	}
	return p.ctx.RemoteControlSpawner() != nil
}

func (p *Plugin) refuseRemoteCreate(kind string) tea.Cmd {
	host := ""
	if p.ctx != nil {
		host = p.ctx.HostID
	}
	return func() tea.Msg {
		return ShellCreatedMsg{Err: fmt.Errorf("creating a %s on [%s] is not available from this workspace yet", kind, host)}
	}
}

func (p *Plugin) handleHostInventory() (plugin.Plugin, tea.Cmd) {
	if !p.remoteBound() {
		return p, nil
	}
	p.applyHostInventory()
	cmds := p.reconcileTerminalModels()
	if p.contentDeck != nil {
		if root, surface, ok := p.selectedTerminalSurface(); ok {
			cmds = append(cmds, p.contentDeck.SetContext(p.workspaceDeckContext(root, surface))...)
		}
	}
	return p, tea.Batch(cmds...)
}
