package workspace

import (
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/features"
	appmsg "github.com/marcus/sidecar/internal/msg"
	"github.com/marcus/sidecar/internal/projectdir"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspacediff"
)

type pendingView struct {
	Target    uirequest.Target
	Options   uirequest.Options
	CreatedAt time.Time
	TTLMs     int
}

func hostInstanceID() string {
	return uirequest.InstanceID("workspace")
}

func (p *Plugin) handleUIRequest(req uirequest.Request) tea.Cmd {
	if req.Action == uirequest.ActionRenameWorktree {
		p.applyWorktreeRenameRequest(req)
		return nil
	}
	if req.Action != uirequest.ActionOpen {
		return nil
	}

	if req.Origin.TmuxSession == "" {
		if req.Origin.ProjectKey == "" || !p.matchesProjectTarget(req) {
			return nil
		}
		return p.openOnSelectedSurface(req)
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
		return p.applyOpenRequest(req, root, surface)
	}

	// Shell is not selected: queue it and write queued ack
	if p.pendingViews == nil {
		p.pendingViews = make(map[string]*pendingView)
	}
	p.pendingViews[targetShell.TmuxName] = &pendingView{
		Target:    req.Target,
		Options:   req.Options,
		CreatedAt: req.CreatedAt,
		TTLMs:     req.TTLMs,
	}

	_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
		Instance: hostInstanceID(),
		Host:     uirequest.HostName(),
		PID:      os.Getpid(),
		Status:   uirequest.StatusQueued,
		Surface:  "shell:" + targetShell.TmuxName,
		At:       time.Now().UTC(),
	})
	return nil
}

func (p *Plugin) applyWorktreeRenameRequest(req uirequest.Request) {
	if req.Target.Kind != uirequest.TargetKindWorktree || req.Origin.WorkDir == "" || req.Target.Value == "" {
		return
	}
	for _, wt := range p.worktrees {
		if sameCanonicalPath(wt.Path, req.Origin.WorkDir) {
			wt.Name = req.Target.Value
			return
		}
	}
}

func (p *Plugin) matchesProjectTarget(req uirequest.Request) bool {
	if p.ctx == nil || req.Origin.ProjectKey == "" {
		return false
	}
	if dir, ok := projectdir.Lookup(p.ctx.ProjectRoot); ok {
		return filepath.Base(dir) == req.Origin.ProjectKey
	}
	return sameCanonicalPath(p.ctx.ProjectRoot, req.Origin.WorkDir)
}

func (p *Plugin) openOnSelectedSurface(req uirequest.Request) tea.Cmd {
	root, surface, ok := p.selectedTerminalSurface()
	if !ok {
		_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
			Instance: hostInstanceID(),
			Host:     uirequest.HostName(),
			PID:      os.Getpid(),
			Status:   uirequest.StatusDeclined,
			Reason:   "no selected workspace surface",
			At:       time.Now().UTC(),
		})
		return nil
	}
	return p.applyOpenRequest(req, root, surface)
}

func (p *Plugin) applyOpenRequest(req uirequest.Request, root, surface string) tea.Cmd {
	prevSplit := p.openSplit
	p.openSplit = req.Options.Split
	defer func() { p.openSplit = prevSplit }()

	var cmd tea.Cmd
	opened := false
	// Asked before the open, because afterwards the pane exists either way:
	// the planner is what decides between a new split and an existing pane.
	retargeted := false
	switch req.Target.Kind {
	case uirequest.TargetKindFile:
		retargeted = p.willRetargetPane(PaneDoc)
		cmd = p.openDocPaneForSurface(root, surface, req.Target.Value, req.Target.Line)
		// A document open is not reported by its command: a split that did
		// not fit still returns the reopen command, and re-opening a file
		// already on screen legitimately returns none. The pane tree is the
		// only honest witness.
		opened = p.docPaneShows(req.Target.Value)
	case uirequest.TargetKindIssue:
		retargeted = p.willRetargetPane(PaneIssue)
		cmd = p.openIssuePaneForSurface(root, surface, req.Target.Value)
		opened = cmd != nil
	case uirequest.TargetKindDiff:
		if p.paneRoot == nil {
			_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
				Instance: hostInstanceID(),
				Host:     uirequest.HostName(),
				PID:      os.Getpid(),
				Status:   uirequest.StatusDeclined,
				Reason:   features.WorkspaceDocPanesDisabledDiff,
				Surface:  surface,
				At:       time.Now().UTC(),
			})
			return appmsg.ShowToast(features.WorkspaceDocPanesDisabledDiff, 3*time.Second)
		}
		retargeted = p.willRetargetPane(PaneDiff)
		spec := uirequest.DiffTarget(root, req.Target.Value)
		cmd = p.openDiffPaneForSurface(root, surface, spec)
		opened = p.diffPaneShows(spec)
	}

	// Nothing on screen: the split did not fit, or the target could not be
	// loaded. Say so rather than claiming an open the user cannot see; the
	// toast, when there is one, carries the reason.
	if !opened {
		reason := p.toastMessage
		if reason == "" {
			reason = "the window is too small to split"
		}
		_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
			Instance: hostInstanceID(),
			Host:     uirequest.HostName(),
			PID:      os.Getpid(),
			Status:   uirequest.StatusDeclined,
			Reason:   reason,
			Surface:  surface,
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
		Surface:  surface,
		Pane:     p.paneFocus,
		At:       time.Now().UTC(),
	})
	return cmd
}

func sameCanonicalPath(a, b string) bool {
	if a == "" || b == "" {
		return false
	}
	return canonicalOpenPath(a) == canonicalOpenPath(b)
}

func canonicalOpenPath(path string) string {
	path = filepath.Clean(path)
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = filepath.Clean(resolved)
	}
	return path
}

// willRetargetPane reports whether opening kind would land in a pane that is
// already on screen rather than splitting a new one.
func (p *Plugin) willRetargetPane(kind PaneKind) bool {
	plan, ok := planPaneOpen(p.paneRoot, kind, p.lastPaneBoxes())
	return ok && plan.Retarget != 0
}

// docPaneShows reports whether the live document pane is showing rel.
func (p *Plugin) docPaneShows(rel string) bool {
	doc, leaf := p.activeDocPane()
	if doc == nil || leaf == nil {
		return false
	}
	view := doc.view()
	if view == nil {
		return false
	}
	return docview.NormalizeTabPath(view.Title()) == docview.NormalizeTabPath(rel)
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
	if time.Since(pv.CreatedAt) > ttl {
		return nil
	}

	root, surface, ok := p.selectedTerminalSurface()
	if !ok || surface != "shell:"+tmuxName {
		return nil
	}

	prevSplit := p.openSplit
	p.openSplit = pv.Options.Split
	defer func() { p.openSplit = prevSplit }()

	switch pv.Target.Kind {
	case uirequest.TargetKindFile:
		return p.openDocPaneForSurface(root, surface, pv.Target.Value, pv.Target.Line)
	case uirequest.TargetKindIssue:
		return p.openIssuePaneForSurface(root, surface, pv.Target.Value)
	case uirequest.TargetKindDiff:
		return p.openDiffPaneForSurface(root, surface, uirequest.DiffTarget(root, pv.Target.Value))
	}
	return nil
}

func (p *Plugin) diffPaneShows(target workspacediff.Target) bool {
	diff, leaf := p.activeDiffPane()
	if diff == nil || leaf == nil {
		return false
	}
	return diff.tabs.Find(target.Identity()) >= 0
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
	if time.Since(pv.CreatedAt) > ttl {
		return "", false
	}
	return " ◫", true
}
