package overview

import (
	"os"
	"path/filepath"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/resourceview"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

type pendingView struct {
	Target    uirequest.Target
	Options   uirequest.Options
	CreatedAt time.Time
	TTLMs     int
}

func hostInstanceID() string {
	return uirequest.InstanceID("overview")
}

func (m *Model) handleUIRequest(req uirequest.Request) tea.Cmd {
	if req.Action == uirequest.ActionRenameWorktree {
		m.applyWorktreeRenameRequest(req)
		return nil
	}
	if req.Action == uirequest.ActionRenameShell {
		m.applyShellRenameRequest(req)
		return nil
	}
	if req.Action == uirequest.ActionCreate {
		return m.applyCreateRequest(req)
	}
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
		prevSplit := m.openSplit
		m.openSplit = req.Options.Split
		defer func() { m.openSplit = prevSplit }()

		var cmd tea.Cmd
		// Asked before the open, because afterwards the pane exists either way:
		// the planner is what decides between a new split and an existing pane.
		retargeted := false
		switch req.Target.Kind {
		case uirequest.TargetKindFile:
			retargeted = m.willRetargetPreviewPane(panelayout.Document)
			// The request already carries a uirequest.Target; the pane opener
			// takes one directly, so nothing is re-wrapped as a span here.
			cmd = m.openPreviewDocTarget(req.Target)
		case uirequest.TargetKindIssue:
			retargeted = m.willRetargetPreviewPane(panelayout.Issue)
			cmd = m.openPreviewIssue(req.Target.Value)
		case uirequest.TargetKindDiff:
			retargeted = m.willRetargetPreviewPane(panelayout.Diff)
			cmd = m.openPreviewDiff(uirequest.DiffTarget(targetWorkspace.Path, req.Target.Value))
		case uirequest.TargetKindResource:
			ref, refusal := resourceview.ReferenceForLocator(m.resourceMatchers, req.Target.Provider, req.Target.Value)
			if refusal != "" {
				_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
					Instance: hostInstanceID(), Host: uirequest.HostName(), PID: os.Getpid(),
					Status: uirequest.StatusDeclined, Reason: refusal,
					Surface: "shell:" + targetWorkspace.TmuxName, At: time.Now().UTC(),
				})
				return nil
			}
			retargeted = m.willRetargetPreviewPane(panelayout.Resource)
			cmd = m.OpenPreviewResource(ref)
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
		Options:   req.Options,
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

func (m *Model) applyCreateRequest(req uirequest.Request) tea.Cmd {
	payload, err := uirequest.DecodeCreatePayload(req.Payload)
	if err != nil {
		return nil
	}
	project, key, ok := m.createRequestProject(req)
	if !ok {
		return nil
	}
	switch payload.Kind {
	case uirequest.CreateKindShell:
		return m.applyCreateShellRequest(req, payload, project, key)
	case uirequest.CreateKindWorktree:
		return m.applyCreateWorktreeRequest(req, payload, project, key)
	default:
		return nil
	}
}

func (m *Model) createRequestProject(req uirequest.Request) (Project, string, bool) {
	if req.Origin.ProjectKey != "" {
		for _, project := range m.projects {
			key := projectKey(project)
			if key == req.Origin.ProjectKey || project.Name == req.Origin.ProjectKey || filepath.Base(project.Path) == req.Origin.ProjectKey {
				return project, key, true
			}
		}
		if _, ok := m.results[req.Origin.ProjectKey]; ok {
			return Project{Key: req.Origin.ProjectKey, Path: req.Origin.WorkDir, Name: req.Origin.ProjectKey}, req.Origin.ProjectKey, true
		}
	}
	if req.Origin.WorkDir != "" {
		want := workspaceinventory.CanonicalPath(req.Origin.WorkDir)
		for _, project := range m.projects {
			if workspaceinventory.CanonicalPath(project.Path) == want {
				return project, projectKey(project), true
			}
		}
	}
	if req.Origin.TmuxSession != "" {
		for key, result := range m.results {
			for _, ws := range result.Workspaces {
				if ws.TmuxName == req.Origin.TmuxSession {
					for _, project := range m.projects {
						if projectKey(project) == key {
							return project, key, true
						}
					}
					return Project{Key: key, Path: result.ProjectRoot, Name: result.ProjectName}, key, true
				}
			}
		}
	}
	if len(m.projects) == 1 {
		return m.projects[0], projectKey(m.projects[0]), true
	}
	return Project{}, "", false
}

func (m *Model) applyCreateShellRequest(req uirequest.Request, payload uirequest.CreatePayload, project Project, key string) tea.Cmd {
	if payload.Session == "" {
		return nil
	}
	name := payload.DisplayName
	if name == "" {
		name = payload.Session
	}
	ws := workspaceinventory.Workspace{
		ProjectKey:  key,
		ProjectName: project.Name,
		ProjectRoot: project.Path,
		Kind:        workspaceinventory.KindShell,
		Key:         payload.Session,
		Name:        name,
		Path:        project.Path,
		TmuxName:    payload.Session,
		Live:        true,
	}
	if ws.Path == "" {
		ws.Path = req.Origin.WorkDir
		ws.ProjectRoot = req.Origin.WorkDir
	}
	ws.ID = ws.ProjectKey + ":shell:" + ws.Key
	m.upsertCreateWorkspace(key, ws)
	if payload.ShouldFocus() {
		m.pendingCreatedTmux = payload.Session
		m.pendingCreatedPath = ""
		if !m.workspaces.SelectID(ws.ID) {
			m.honorPendingCreated()
		} else {
			m.clearPendingCreated()
		}
	}
	m.ackCreate(req, "shell:"+payload.Session)
	if project.Path != "" {
		return m.refreshProjectAfterMutation(project)
	}
	return nil
}

func (m *Model) applyCreateWorktreeRequest(req uirequest.Request, payload uirequest.CreatePayload, project Project, key string) tea.Cmd {
	if payload.Path == "" {
		return nil
	}
	path := workspaceinventory.CanonicalPath(payload.Path)
	name := payload.DisplayName
	if name == "" {
		name = filepath.Base(path)
	}
	ws := workspaceinventory.Workspace{
		ProjectKey:  key,
		ProjectName: project.Name,
		ProjectRoot: project.Path,
		Kind:        workspaceinventory.KindWorktree,
		Key:         path,
		Name:        name,
		Path:        path,
		Branch:      payload.Branch,
		TmuxName:    payload.Session,
	}
	ws.ID = ws.ProjectKey + ":worktree:" + ws.Key
	m.showIdleWorktrees = true
	m.upsertCreateWorkspace(key, ws)
	if payload.ShouldFocus() {
		m.pendingCreatedPath = path
		m.pendingCreatedTmux = ""
		if !m.workspaces.SelectID(ws.ID) {
			m.honorPendingCreated()
		} else {
			m.clearPendingCreated()
		}
	}
	surface := "worktree:" + path
	if payload.Session != "" {
		surface = "shell:" + payload.Session
	}
	m.ackCreate(req, surface)
	if project.Path != "" {
		return m.refreshProjectAfterMutation(project)
	}
	return nil
}

func (m *Model) upsertCreateWorkspace(key string, ws workspaceinventory.Workspace) {
	result := m.results[key]
	found := false
	for i, existing := range result.Workspaces {
		sameShell := ws.Kind == workspaceinventory.KindShell && existing.Kind == workspaceinventory.KindShell && existing.TmuxName == ws.TmuxName
		sameWorktree := ws.Kind == workspaceinventory.KindWorktree && existing.Kind == workspaceinventory.KindWorktree && existing.Path == ws.Path
		if !sameShell && !sameWorktree {
			continue
		}
		if ws.Name != "" {
			result.Workspaces[i].Name = ws.Name
		}
		if ws.TmuxName != "" {
			result.Workspaces[i].TmuxName = ws.TmuxName
		}
		if ws.Branch != "" {
			result.Workspaces[i].Branch = ws.Branch
		}
		if ws.Live {
			result.Workspaces[i].Live = true
		}
		found = true
		break
	}
	if !found {
		result.Workspaces = append(result.Workspaces, ws)
	}
	if m.results == nil {
		m.results = make(map[string]workspaceinventory.ProjectResult)
	}
	m.results[key] = result
	m.syncBoard()
}

func (m *Model) ackCreate(req uirequest.Request, surface string) {
	_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
		Instance: hostInstanceID(),
		Host:     uirequest.HostName(),
		PID:      os.Getpid(),
		Status:   uirequest.StatusOpened,
		Surface:  surface,
		At:       time.Now().UTC(),
	})
}

func (m *Model) applyWorktreeRenameRequest(req uirequest.Request) {
	if req.Target.Kind != uirequest.TargetKindWorktree || req.Origin.WorkDir == "" || req.Target.Value == "" {
		return
	}
	targetPath := workspaceinventory.CanonicalPath(req.Origin.WorkDir)
	changed := false
	for key, result := range m.results {
		resultChanged := false
		for i := range result.Workspaces {
			workspace := &result.Workspaces[i]
			if workspace.Kind == workspaceinventory.KindWorktree && workspace.Path == targetPath {
				workspace.Name = req.Target.Value
				resultChanged = true
			}
		}
		if resultChanged {
			m.results[key] = result
			changed = true
		}
	}
	if changed {
		m.syncBoard()
	}
}

func (m *Model) applyShellRenameRequest(req uirequest.Request) {
	if req.Target.Kind != uirequest.TargetKindShell || req.Origin.TmuxSession == "" || req.Target.Value == "" {
		return
	}
	changed := false
	for key, result := range m.results {
		resultChanged := false
		for i := range result.Workspaces {
			workspace := &result.Workspaces[i]
			if workspace.TmuxName == req.Origin.TmuxSession {
				workspace.Name = req.Target.Value
				resultChanged = true
			}
		}
		if resultChanged {
			m.results[key] = result
			changed = true
		}
	}
	if changed {
		m.syncBoard()
	}
}

// willRetargetPreviewPane reports whether opening kind would land in a pane
// that is already on screen rather than splitting a new one.
func (m *Model) willRetargetPreviewPane(kind panelayout.Kind) bool {
	plan, ok := panelayout.PlanOpen(m.preview.paneRoot, kind, m.lastPreviewBoxes())
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

	prevSplit := m.openSplit
	m.openSplit = pv.Options.Split
	defer func() { m.openSplit = prevSplit }()

	switch pv.Target.Kind {
	case uirequest.TargetKindFile:
		return m.openPreviewDocTarget(pv.Target)
	case uirequest.TargetKindIssue:
		return m.openPreviewIssue(pv.Target.Value)
	case uirequest.TargetKindDiff:
		root := ""
		if selected, ok := m.SelectedWorkspace(); ok {
			root = selected.Path
		}
		return m.openPreviewDiff(uirequest.DiffTarget(root, pv.Target.Value))
	case uirequest.TargetKindResource:
		ref, refusal := resourceview.ReferenceForLocator(m.resourceMatchers, pv.Target.Provider, pv.Target.Value)
		if refusal != "" {
			return nil
		}
		return m.OpenPreviewResource(ref)
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
