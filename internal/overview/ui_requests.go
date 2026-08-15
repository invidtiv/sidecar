package overview

import (
	"os"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/terminallink"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

type pendingView struct {
	Target    uirequest.Target
	CreatedAt time.Time
	TTLMs     int
}

func hostInstanceID() string {
	return uirequest.InstanceID("overview")
}

func (m *Model) handleUIRequest(req uirequest.Request) tea.Cmd {
	if req.Action != uirequest.ActionOpen {
		return nil
	}

	var targetWorkspace *workspaceinventory.Workspace
	for _, ws := range m.catalog {
		if ws.TmuxName == req.Origin.TmuxSession {
			targetWorkspace = &ws
			break
		}
	}
	if targetWorkspace == nil {
		return nil
	}

	selected, hasSelected := m.SelectedWorkspace()
	isSelected := hasSelected && selected.TmuxName == req.Origin.TmuxSession

	if isSelected {
		var cmd tea.Cmd
		// Asked before the open, because afterwards the pane exists either way:
		// the planner is what decides between a new split and an existing pane.
		retargeted := false
		switch req.Target.Kind {
		case uirequest.TargetKindFile:
			retargeted = m.willRetargetPreviewPane(panelayout.Document)
			span := terminallink.Span{
				Kind:  terminallink.KindFile,
				Value: req.Target.Value,
				Extra: terminallink.Extra{
					Line: req.Target.Line,
					Raw:  req.Target.Value,
				},
			}
			cmd = m.openPreviewDoc(span)
		case uirequest.TargetKindIssue:
			retargeted = m.willRetargetPreviewPane(panelayout.Issue)
			cmd = m.openPreviewIssue(req.Target.Value)
		}

		if cmd == nil {
			_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
				Instance: hostInstanceID(),
				Host:     uirequest.HostName(),
				PID:      os.Getpid(),
				Status:   uirequest.StatusDeclined,
				Reason:   "window too small to split",
				Surface:  "shell:" + targetWorkspace.TmuxName,
				At:       time.Now().UTC(),
			})
			return nil
		}

		status := uirequest.StatusOpened
		if retargeted {
			status = uirequest.StatusRetargeted
		}
		_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
			Instance: hostInstanceID(),
			Host:     uirequest.HostName(),
			PID:      os.Getpid(),
			Status:   status,
			Surface:  "shell:" + targetWorkspace.TmuxName,
			Pane:     m.preview.paneFocus,
			At:       time.Now().UTC(),
		})
		return cmd
	}

	if m.pendingViews == nil {
		m.pendingViews = make(map[string]*pendingView)
	}
	m.pendingViews[targetWorkspace.TmuxName] = &pendingView{
		Target:    req.Target,
		CreatedAt: req.CreatedAt,
		TTLMs:     req.TTLMs,
	}

	_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
		Instance: hostInstanceID(),
		Host:     uirequest.HostName(),
		PID:      os.Getpid(),
		Status:   uirequest.StatusQueued,
		Surface:  "shell:" + targetWorkspace.TmuxName,
		At:       time.Now().UTC(),
	})
	return nil
}

// willRetargetPreviewPane reports whether opening kind would land in a pane
// that is already on screen rather than splitting a new one.
func (m *Model) willRetargetPreviewPane(kind panelayout.Kind) bool {
	plan, ok := panelayout.PlanOpen(m.preview.paneRoot, kind)
	return ok && plan.Retarget != 0
}

func (m *Model) consumePendingView(tmuxName string) tea.Cmd {
	if m.pendingViews == nil {
		return nil
	}
	pv, ok := m.pendingViews[tmuxName]
	if !ok || pv == nil {
		return nil
	}
	delete(m.pendingViews, tmuxName)

	ttl := time.Duration(pv.TTLMs) * time.Millisecond
	if ttl <= 0 {
		ttl = uirequest.DefaultTTL
	}
	if time.Since(pv.CreatedAt) > ttl {
		return nil
	}

	switch pv.Target.Kind {
	case uirequest.TargetKindFile:
		span := terminallink.Span{
			Kind:  terminallink.KindFile,
			Value: pv.Target.Value,
			Extra: terminallink.Extra{
				Line: pv.Target.Line,
				Raw:  pv.Target.Value,
			},
		}
		return m.openPreviewDoc(span)
	case uirequest.TargetKindIssue:
		return m.openPreviewIssue(pv.Target.Value)
	}
	return nil
}

func (m *Model) pendingViewBadge(tmuxName string) (string, bool) {
	if m.pendingViews == nil {
		return "", false
	}
	pv, ok := m.pendingViews[tmuxName]
	if !ok || pv == nil {
		return "", false
	}
	ttl := time.Duration(pv.TTLMs) * time.Millisecond
	if ttl <= 0 {
		ttl = uirequest.DefaultTTL
	}
	if time.Since(pv.CreatedAt) > ttl {
		return "", false
	}
	return " ◫", true
}
