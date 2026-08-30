package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func TestGlobalSessionsAttentionOriginUsesSelectedSurface(t *testing.T) {
	m := catalogModel(t)
	m.catalog["s2"] = workspaceinventory.Workspace{
		ID: "s2", ProjectKey: "/repos/sidecar", ProjectName: "sidecar", Path: "/repos/sidecar",
		Kind: workspaceinventory.KindShell, TmuxName: "sidecar-sh-1",
	}
	m.workspaces.SelectID("s2")
	m.preview.visible = true
	origin, ok := m.AttentionOrigin()
	if !ok || origin.TmuxSession != "sidecar-sh-1" || origin.ProjectKey != "sidecar" || origin.WorkDir != "/repos/sidecar" {
		t.Fatalf("global origin = %+v, %v", origin, ok)
	}
	m.preview.visible = false
	if _, ok := m.AttentionOrigin(); ok {
		t.Fatal("hidden Sessions surface exposed a visible origin")
	}
}
