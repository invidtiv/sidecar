package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/plugin"
)

func TestProjectWorkspaceAttentionOriginUsesSelectedSurface(t *testing.T) {
	p := &Plugin{
		focused:          true,
		ctx:              &plugin.Context{WorkDir: "/repos/sidecar"},
		shellSelected:    true,
		selectedShellIdx: 0,
		shells: []*ShellSession{{
			Name: "Agent", TmuxName: "sidecar-sh-sidecar-1", WorkDir: "/repos/sidecar/feature",
		}},
	}
	origin, ok := p.AttentionOrigin()
	if !ok || origin.TmuxSession != "sidecar-sh-sidecar-1" || origin.ProjectKey != "sidecar" || origin.WorkDir != "/repos/sidecar/feature" {
		t.Fatalf("shell origin = %+v, %v", origin, ok)
	}

	p.shellSelected = false
	p.selectedIdx = 0
	p.worktrees = []*Worktree{{
		Key: "feature", Name: "feature", Path: "/repos/sidecar/feature",
		Agent: &Agent{TmuxSession: "sidecar-ws-feature"},
	}}
	origin, ok = p.AttentionOrigin()
	if !ok || origin.TmuxSession != "sidecar-ws-feature" || origin.WorkDir != "/repos/sidecar/feature" {
		t.Fatalf("worktree origin = %+v, %v", origin, ok)
	}

	p.focused = false
	if _, ok := p.AttentionOrigin(); ok {
		t.Fatal("hidden project workspace exposed a visible origin")
	}
}
