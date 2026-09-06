package workspace

import (
	"path/filepath"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/broadcastmodal"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/projectdir"
)

func (p *Plugin) openBroadcast() tea.Cmd {
	if !broadcastmodal.Enabled() {
		return nil
	}
	p.broadcast = broadcastmodal.New(p.broadcastProjectKey(), config.StateDir(), broadcastmodal.ScopeThisProject, false)
	return p.broadcast.Replan()
}

func (p *Plugin) broadcastProjectKey() string {
	if p.ctx == nil {
		return ""
	}
	if p.ctx.ProjectKey != "" {
		return filepath.Base(p.ctx.ProjectKey)
	}
	root := p.ctx.ProjectRoot
	if root == "" {
		root = p.ctx.WorkDir
	}
	if dir, ok := projectdir.Lookup(root); ok {
		return filepath.Base(dir)
	}
	return filepath.Base(root)
}

func (p *Plugin) handleBroadcastKey(msg tea.KeyPressMsg) (bool, tea.Cmd) {
	if msg.String() != "B" || !broadcastmodal.Enabled() || p.activePane != PaneSidebar {
		return false, nil
	}
	if p.broadcast != nil {
		return true, nil
	}
	return true, p.openBroadcast()
}

func (p *Plugin) handleBroadcastModalKey(msg tea.KeyPressMsg) tea.Cmd {
	if p.broadcast == nil {
		return nil
	}
	p.broadcast.Ensure(p.width)
	close, cmd := p.broadcast.HandleKey(msg)
	if close {
		p.broadcast = nil
	}
	return cmd
}

func (p *Plugin) handleBroadcastModalMouse(msg tea.MouseMsg) tea.Cmd {
	if p.broadcast == nil {
		return nil
	}
	p.broadcast.Ensure(p.width)
	close, cmd := p.broadcast.HandleMouse(msg, p.mouseHandler)
	if close {
		p.broadcast = nil
	}
	return cmd
}

func (p *Plugin) applyBroadcastPlan(msg broadcastmodal.PlannedMsg) {
	if p.broadcast != nil {
		p.broadcast.ApplyPlan(msg)
	}
}

func (p *Plugin) applyBroadcastSent(msg broadcastmodal.SentMsg) tea.Cmd {
	p.broadcast = nil
	if msg.Err != nil {
		return broadcastmodal.NotifyError(msg.Err)
	}
	return broadcastmodal.NotifyCmd(msg.Result)
}
