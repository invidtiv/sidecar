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

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			root, terminal := prepareProjectActiveSessionRoot(t)
			p := newProjectActiveSessionFixture(t, root, terminal).p
			if tt.setup != nil {
				tt.setup(p)
			}

			raw := p.View(tt.width, tt.height)
			wrapped := lipgloss.NewStyle().Width(tt.width).Height(tt.height).MaxHeight(tt.height).Render(raw)
			if raw != wrapped {
				t.Fatalf("Workspace output differs from former app wrapper: raw=%d bytes wrapped=%d bytes", len(raw), len(wrapped))
			}
		})
	}
}
