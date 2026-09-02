package app

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/uirequest"
)

// handleSwitchProjectRequest processes an ActionSwitchProject UI request, switching
// the TUI to the requested project and writing an acknowledgement.
func (m *Model) handleSwitchProjectRequest(req uirequest.Request) tea.Cmd {
	target := strings.TrimSpace(req.Target.Value)
	if target == "" {
		_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
			Instance: uirequest.InstanceID("app"),
			Host:     uirequest.HostName(),
			PID:      os.Getpid(),
			Status:   uirequest.StatusDeclined,
			Reason:   "no target project specified",
			Surface:  "workspace",
		})
		return nil
	}

	cfg, err := config.Load()
	if err != nil && m.cfg != nil {
		cfg = m.cfg
	} else if err != nil {
		_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
			Instance: uirequest.InstanceID("app"),
			Host:     uirequest.HostName(),
			PID:      os.Getpid(),
			Status:   uirequest.StatusDeclined,
			Reason:   "cannot load configuration: " + err.Error(),
			Surface:  "workspace",
		})
		return nil
	}
	m.cfg = cfg

	proj, found := findConfiguredProject(cfg.Projects.List, target)
	if !found {
		_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
			Instance: uirequest.InstanceID("app"),
			Host:     uirequest.HostName(),
			PID:      os.Getpid(),
			Status:   uirequest.StatusDeclined,
			Reason:   fmt.Sprintf("unknown project %q", target),
			Surface:  "workspace",
		})
		return nil
	}

	targetPath := config.ExpandPath(proj.Path)
	info, err := os.Stat(targetPath)
	if err != nil || !info.IsDir() {
		_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
			Instance: uirequest.InstanceID("app"),
			Host:     uirequest.HostName(),
			PID:      os.Getpid(),
			Status:   uirequest.StatusDeclined,
			Reason:   fmt.Sprintf("project path does not exist: %s", targetPath),
			Surface:  "workspace",
		})
		return nil
	}

	normTarget, _ := normalizePath(targetPath)
	normCurrent := ""
	if m.ui != nil {
		normCurrent, _ = normalizePath(m.ui.WorkDir)
	}

	if !m.inGlobalScope() && normTarget != "" && normTarget == normCurrent {
		_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
			Instance: uirequest.InstanceID("app"),
			Host:     uirequest.HostName(),
			PID:      os.Getpid(),
			Status:   uirequest.StatusUnchanged,
			Reason:   fmt.Sprintf("already showing project %s", proj.Name),
			Surface:  "workspace",
		})
		return nil
	}

	if m.inGlobalScope() && normTarget != "" && normTarget == normCurrent {
		exitCmd := m.exitOverview()
		_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
			Instance: uirequest.InstanceID("app"),
			Host:     uirequest.HostName(),
			PID:      os.Getpid(),
			Status:   uirequest.StatusOpened,
			Surface:  "workspace",
		})
		return exitCmd
	}

	if m.inGlobalScope() {
		m.leaveOverview(false)
		m.updateContext()
	}

	switchCmd := m.switchProjectWithSelection(targetPath, nil, nil, true)
	_ = uirequest.WriteAck(config.StateDir(), req.ID, req.Action, uirequest.Ack{
		Instance: uirequest.InstanceID("app"),
		Host:     uirequest.HostName(),
		PID:      os.Getpid(),
		Status:   uirequest.StatusOpened,
		Surface:  "workspace",
	})
	return switchCmd
}

func findConfiguredProject(list []config.ProjectConfig, target string) (config.ProjectConfig, bool) {
	target = strings.TrimSpace(target)
	if target == "" {
		return config.ProjectConfig{}, false
	}
	expandedTarget := config.ExpandPath(target)
	normTarget, _ := normalizePath(expandedTarget)

	// 1. Exact name match (case-insensitive)
	for _, p := range list {
		if strings.EqualFold(strings.TrimSpace(p.Name), target) {
			return p, true
		}
	}
	// 2. Canonical path match
	for _, p := range list {
		pExpanded := config.ExpandPath(p.Path)
		pNorm, _ := normalizePath(pExpanded)
		if (normTarget != "" && pNorm == normTarget) || pExpanded == expandedTarget {
			return p, true
		}
	}
	// 3. Basename match
	for _, p := range list {
		if filepath.Base(filepath.Clean(p.Path)) == target || filepath.Base(filepath.Clean(config.ExpandPath(p.Path))) == target {
			return p, true
		}
	}
	return config.ProjectConfig{}, false
}
