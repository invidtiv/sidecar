package workspace

import (
	"fmt"
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uirequest"
)

type pendingView struct {
	Target    uirequest.Target
	CreatedAt time.Time
	TTLMs     int
}

func hostInstanceID() string {
	host, err := os.Hostname()
	if err != nil || host == "" {
		host = "localhost"
	}
	return fmt.Sprintf("%s-%d", host, os.Getpid())
}

func (p *Plugin) handleUIRequest(req uirequest.Request) tea.Cmd {
	if req.Action != uirequest.ActionOpen {
		return nil
	}

	// Match against shells in this workspace
	var targetShell *ShellSession
	for _, sh := range p.shells {
		if sh.TmuxName == req.Origin.TmuxSession {
			targetShell = sh
			break
		}
	}
	if targetShell == nil {
		// Not this instance's shell: ignore silently
		return nil
	}

	root, surface, ok := p.selectedTerminalSurface()
	isSelected := ok && surface == "shell:"+targetShell.TmuxName

	if isSelected {
		var cmd tea.Cmd
		if req.Target.Kind == uirequest.TargetKindFile {
			cmd = p.openDocPaneForSurface(root, surface, req.Target.Value, req.Target.Line)
		} else if req.Target.Kind == uirequest.TargetKindIssue {
			cmd = p.openIssuePaneForSurface(root, surface, req.Target.Value)
		}

		if cmd == nil && p.toastMessage != "" {
			// Refused due to fit / split constraints
			_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
				Instance: hostInstanceID(),
				Host:     "localhost",
				PID:      os.Getpid(),
				Status:   uirequest.StatusDeclined,
				Reason:   p.toastMessage,
				Surface:  surface,
				At:       time.Now().UTC(),
			})
			return nil
		}

		_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
			Instance: hostInstanceID(),
			Host:     "localhost",
			PID:      os.Getpid(),
			Status:   uirequest.StatusOpened,
			Surface:  surface,
			Pane:     p.paneFocus,
			At:       time.Now().UTC(),
		})
		return cmd
	}

	// Shell is not selected: queue it and write queued ack
	if p.pendingViews == nil {
		p.pendingViews = make(map[string]*pendingView)
	}
	p.pendingViews[targetShell.TmuxName] = &pendingView{
		Target:    req.Target,
		CreatedAt: req.CreatedAt,
		TTLMs:     req.TTLMs,
	}

	if err := uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
		Instance: hostInstanceID(),
		Host:     "localhost",
		PID:      os.Getpid(),
		Status:   uirequest.StatusQueued,
		Surface:  "shell:" + targetShell.TmuxName,
		At:       time.Now().UTC(),
	}); err != nil {
		_ = err
	}
	return nil
}

func (p *Plugin) consumePendingView(tmuxName string) tea.Cmd {
	if p.pendingViews == nil {
		return nil
	}
	pv, ok := p.pendingViews[tmuxName]
	if !ok || pv == nil {
		return nil
	}
	delete(p.pendingViews, tmuxName)

	ttl := time.Duration(pv.TTLMs) * time.Millisecond
	if ttl <= 0 {
		ttl = uirequest.DefaultTTL
	}
	if time.Now().Sub(pv.CreatedAt) > ttl {
		return nil
	}

	root, surface, ok := p.selectedTerminalSurface()
	if !ok || surface != "shell:"+tmuxName {
		return nil
	}

	if pv.Target.Kind == uirequest.TargetKindFile {
		return p.openDocPaneForSurface(root, surface, pv.Target.Value, pv.Target.Line)
	} else if pv.Target.Kind == uirequest.TargetKindIssue {
		return p.openIssuePaneForSurface(root, surface, pv.Target.Value)
	}
	return nil
}

func (p *Plugin) pendingViewBadge(tmuxName string) (string, bool) {
	if p.pendingViews == nil {
		return "", false
	}
	pv, ok := p.pendingViews[tmuxName]
	if !ok || pv == nil {
		return "", false
	}
	ttl := time.Duration(pv.TTLMs) * time.Millisecond
	if ttl <= 0 {
		ttl = uirequest.DefaultTTL
	}
	if time.Now().Sub(pv.CreatedAt) > ttl {
		return "", false
	}
	return " ◫", true
}
