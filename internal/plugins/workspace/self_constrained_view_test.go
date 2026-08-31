package workspace

import (
	"testing"

	"charm.land/lipgloss/v2"
)

func TestWorkspaceSelfConstrainedViewMatchesFormerAppWrapper(t *testing.T) {
	tests := []struct {
		name   string
		width  int
		height int
		setup  func(*Plugin)
	}{
		{name: "active split", width: 220, height: 58},
		{name: "narrow active split", width: 100, height: 30},
		{name: "sidebar hidden", width: 160, height: 40, setup: func(p *Plugin) { p.sidebarVisible = false }},
		{name: "kanban", width: 120, height: 32, setup: func(p *Plugin) { p.viewMode = ViewModeKanban }},
		{name: "create overlay", width: 120, height: 36, setup: func(p *Plugin) { p.viewMode = ViewModeCreate }},
	}
	boundaryModes := []struct {
		name string
		mode ViewMode
	}{
		{name: "list", mode: ViewModeList},
		{name: "kanban", mode: ViewModeKanban},
		{name: "create", mode: ViewModeCreate},
		{name: "task link", mode: ViewModeTaskLink},
		{name: "merge", mode: ViewModeMerge},
		{name: "agent choice", mode: ViewModeAgentChoice},
		{name: "confirm delete", mode: ViewModeConfirmDelete},
		{name: "confirm shell delete", mode: ViewModeConfirmDeleteShell},
		{name: "confirm split close", mode: ViewModeConfirmCloseSplit},
		{name: "commit for merge", mode: ViewModeCommitForMerge},
		{name: "rename shell", mode: ViewModeRenameShell},
		{name: "rename worktree", mode: ViewModeRenameWorktree},
		{name: "file picker", mode: ViewModeFilePicker},
		{name: "interactive", mode: ViewModeInteractive},
		{name: "fetch PR", mode: ViewModeFetchPR},
		{name: "agent config", mode: ViewModeAgentConfig},
	}
	for _, boundary := range boundaryModes {
		mode := boundary.mode
		tests = append(tests, struct {
			name   string
			width  int
			height int
			setup  func(*Plugin)
		}{
			name: "accepted boundary/" + boundary.name, width: 3, height: 4,
			setup: func(p *Plugin) { p.viewMode = mode },
		})
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, terminal := prepareProjectActiveSessionRoot(t)
			p := newProjectActiveSessionFixture(t, root, terminal).p
			if tt.setup != nil {
				tt.setup(p)
			}

			raw := p.View(tt.width, tt.height)
			if !p.ViewIsSelfConstrained() {
				t.Fatalf("Workspace declined dimensions %dx%d", tt.width, tt.height)
			}
			wrapped := lipgloss.NewStyle().Width(tt.width).Height(tt.height).MaxHeight(tt.height).Render(raw)
			if raw != wrapped {
				t.Fatalf("Workspace output differs from former app wrapper: raw=%d bytes wrapped=%d bytes", len(raw), len(wrapped))
			}
		})
	}
}

func TestWorkspaceDeclinesSelfConstrainedViewBelowDimensionFloor(t *testing.T) {
	tests := []struct {
		name          string
		width, height int
	}{
		{name: "height 1", width: 8, height: 1},
		{name: "height 2", width: 8, height: 2},
		{name: "height 3", width: 8, height: 3},
		{name: "width 0", width: 0, height: 4},
		{name: "width 1", width: 1, height: 4},
		{name: "width 2", width: 2, height: 4},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, terminal := prepareProjectActiveSessionRoot(t)
			p := newProjectActiveSessionFixture(t, root, terminal).p
			_ = p.View(tt.width, tt.height)
			if p.ViewIsSelfConstrained() {
				t.Fatalf("Workspace claimed undersized dimensions %dx%d", tt.width, tt.height)
			}
		})
	}
}
