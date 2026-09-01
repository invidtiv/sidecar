package app

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/overview"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/state"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/uirequest"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func landingTestModel(t *testing.T) Model {
	t.Helper()
	if err := state.InitWithDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	cfg := config.Default()
	cfg.Features.Flags[features.CrossProjectOverview.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	reg := plugin.NewRegistry(&plugin.Context{})
	if err := reg.Register(&recordingInitPlugin{id: workspacePluginID}); err != nil {
		t.Fatal(err)
	}
	m := New(reg, keymap.NewRegistry(), cfg, "", "/tmp/one", "/tmp/one", workspacePluginID)
	m.testHostCatalog = []overview.HostCatalogEntry{{
		ID: "aerie",
		Projects: []overview.HostCatalogProject{{
			Key: "/home/me/sidecar",
			Workspaces: []workspaceinventory.Workspace{{
				Kind: workspaceinventory.KindShell, Name: "Claude", TmuxName: "sidecar-claude", Live: true,
			}},
		}},
	}}
	m.boundDestination = Destination{HostID: "aerie", ProjectKey: "/home/me/sidecar"}
	m.scope = ScopeProject
	return m
}

func relayedOpenReq(session string) uirequest.Request {
	return uirequest.Request{
		Action: uirequest.ActionOpen,
		Origin: uirequest.Origin{HostID: "aerie", TmuxSession: session, ProjectKey: "/home/me/sidecar"},
		Target: uirequest.Target{Kind: uirequest.TargetKindFile, Value: "twin.txt"},
	}
}

func TestUIRequestLandingBoundWorkspaceWhenProjectIsTheScreen(t *testing.T) {
	m := landingTestModel(t)
	original := tty.SessionOwner
	t.Cleanup(func() { tty.SessionOwner = original })
	tty.SessionOwner = func(string) string { return tty.InstanceID() }

	if got := m.uiRequestLanding(relayedOpenReq("sidecar-claude")); got != uiRequestLandingBoundWorkspace {
		t.Fatalf("landing = %v, want bound workspace", got)
	}
}

func TestUIRequestLandingNoneWhenDifferentLocalProject(t *testing.T) {
	m := landingTestModel(t)
	m.boundDestination = Destination{}
	original := tty.SessionOwner
	t.Cleanup(func() { tty.SessionOwner = original })
	tty.SessionOwner = func(string) string { return tty.InstanceID() }

	if got := m.uiRequestLanding(relayedOpenReq("sidecar-claude")); got != uiRequestLandingNone {
		t.Fatalf("landing = %v, want none for a local project", got)
	}
}

func TestUIRequestLandingNoneWhenLeaseIsForeign(t *testing.T) {
	m := landingTestModel(t)
	original := tty.SessionOwner
	t.Cleanup(func() { tty.SessionOwner = original })
	tty.SessionOwner = func(string) string { return "host-tui-1" }

	if got := m.uiRequestLanding(relayedOpenReq("sidecar-claude")); got != uiRequestLandingNone {
		t.Fatalf("landing = %v, want none when this instance does not hold the lease", got)
	}
}

func TestUIRequestLandingNoneWhenSessionsVisibleButRowNotSelected(t *testing.T) {
	m := landingTestModel(t)
	m.scope = ScopeGlobal
	m.globalTab = GlobalSessions
	if m.overview == nil {
		t.Fatal("expected overview")
	}
	original := tty.SessionOwner
	t.Cleanup(func() { tty.SessionOwner = original })
	tty.SessionOwner = func(string) string { return tty.InstanceID() }

	if got := m.uiRequestLanding(relayedOpenReq("sidecar-claude")); got != uiRequestLandingNone {
		t.Fatalf("landing = %v, want none when Sessions is on screen but the row is not selected", got)
	}
}

func TestUIRequestLandingLocalRequestIsNone(t *testing.T) {
	m := landingTestModel(t)
	req := relayedOpenReq("sidecar-claude")
	req.Origin.HostID = ""
	if got := m.uiRequestLanding(req); got != uiRequestLandingNone {
		t.Fatalf("local request landing = %v, want none (existing dual-receive path)", got)
	}
}
