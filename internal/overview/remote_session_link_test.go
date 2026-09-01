package overview

import (
	"testing"

	"github.com/marcus/sidecar/internal/hostproto"
	"github.com/marcus/sidecar/internal/targetactivation"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

const twinSessionName = "sidecar-sh-twin-1"

// sessionTwinModel is two catalog rows that share a tmux session name, one
// local and one on mac-mini, plus a second remote row to click from and a
// different-host twin that must never be selected.
func sessionTwinModel(t *testing.T) (m *Model, localID, remoteID, remoteSrcID, otherHostID string) {
	t.Helper()
	m, _ = previewModel(t)
	bindShowingRemoteHost(m, hostproto.VerbCapabilities{ContentReadV1: true})
	local := workspaceinventory.Workspace{
		ID: "local-twin", ProjectKey: "sidecar", ProjectName: "sidecar",
		Kind: workspaceinventory.KindShell, Name: "local twin",
		TmuxName: twinSessionName, Live: true, PaneID: "%lt", Path: "/tmp/sidecar-alpha",
	}
	remote := workspaceinventory.Workspace{
		ID: "mac-mini\x1fremote-twin", HostID: "mac-mini", ProjectKey: "sidecar", ProjectName: "sidecar",
		Kind: workspaceinventory.KindShell, Name: "remote twin",
		TmuxName: twinSessionName, Live: true, PaneID: "%rt", Path: "/home/me/api",
	}
	remoteSrc := workspaceinventory.Workspace{
		ID: "mac-mini\x1fremote-src", HostID: "mac-mini", ProjectKey: "sidecar", ProjectName: "sidecar",
		Kind: workspaceinventory.KindShell, Name: "remote src",
		TmuxName: "sidecar-sh-src-1", Live: true, PaneID: "%rs", Path: "/home/me/api",
	}
	other := workspaceinventory.Workspace{
		ID: "book\x1fbook-twin", HostID: "book", ProjectKey: "sidecar", ProjectName: "sidecar",
		Kind: workspaceinventory.KindShell, Name: "book twin",
		TmuxName: twinSessionName, Live: true, PaneID: "%bt", Path: "/home/me/api",
	}
	result := m.results["sidecar"]
	result.Workspaces = append(result.Workspaces, local, remote, remoteSrc, other)
	m.results["sidecar"] = result
	m.syncBoard()
	run(t, m, m.SetWorkspacesVisible(true))
	m.WorkspacesView(previewWide, previewTall)
	for _, id := range []string{local.ID, remote.ID, remoteSrc.ID, other.ID} {
		if _, ok := m.catalog[id]; !ok {
			t.Fatalf("fixture catalog missing %q", id)
		}
	}
	return m, local.ID, remote.ID, remoteSrc.ID, other.ID
}

func selectSessionSource(t *testing.T, m *Model, id string) {
	t.Helper()
	if !m.workspaces.SelectID(id) {
		t.Fatalf("could not select %q", id)
	}
	run(t, m, m.previewSync())
	putPreviewLine(t, m, "see "+twinSessionName+" in the log")
	m.WorkspacesView(previewWide, previewTall)
	m.PrepareTerminalLinks()
	deliverPreviewLinkResults(t, m, m.terminalLinks.TakeCmd())
	m.PrepareTerminalLinks()
	m.WorkspacesView(previewWide, previewTall)
}

func requireSelectedSession(t *testing.T, m *Model, wantID, wantHost string) {
	t.Helper()
	ws, ok := m.SelectedWorkspace()
	if !ok {
		t.Fatal("no selected workspace after session attach")
	}
	if ws.ID != wantID {
		t.Fatalf("selected id = %q host=%q, want %q host=%q", ws.ID, ws.HostID, wantID, wantHost)
	}
	if ws.HostID != wantHost {
		t.Fatalf("selected host = %q, want %q (id=%q)", ws.HostID, wantHost, ws.ID)
	}
	if ws.TmuxName != twinSessionName {
		t.Fatalf("selected tmux = %q, want %q", ws.TmuxName, twinSessionName)
	}
}

func TestSessionLinkAttachesMatchingHostRowOnly(t *testing.T) {
	for _, tc := range []struct {
		name     string
		from     func(localID, remoteID, remoteSrcID, otherHostID string) string
		want     func(localID, remoteID, remoteSrcID, otherHostID string) string
		wantHost string
	}{
		{
			name:     "local pane matches local twin",
			from:     func(localID, _, _, _ string) string { return "a" },
			want:     func(localID, _, _, _ string) string { return localID },
			wantHost: "",
		},
		{
			name:     "remote pane matches remote twin",
			from:     func(_, _, remoteSrcID, _ string) string { return remoteSrcID },
			want:     func(_, remoteID, _, _ string) string { return remoteID },
			wantHost: "mac-mini",
		},
	} {
		t.Run(tc.name+"/activatePreviewPlan", func(t *testing.T) {
			m, localID, remoteID, remoteSrcID, otherID := sessionTwinModel(t)
			from := tc.from(localID, remoteID, remoteSrcID, otherID)
			want := tc.want(localID, remoteID, remoteSrcID, otherID)
			selectSessionSource(t, m, from)
			cmd, handled := m.activatePreviewPlan(targetactivation.Plan{
				Kind: targetactivation.PlanAttachSession, Session: twinSessionName,
			})
			if !handled || cmd == nil {
				t.Fatalf("activatePreviewPlan handled=%v cmd=%v", handled, cmd != nil)
			}
			run(t, m, cmd)
			requireSelectedSession(t, m, want, tc.wantHost)
			if m.workspaces.SelectedID() == otherID {
				t.Fatal("attached a different-host twin")
			}
			if tc.wantHost == "" && m.workspaces.SelectedID() == remoteID {
				t.Fatal("local click attached the remote twin")
			}
			if tc.wantHost != "" && m.workspaces.SelectedID() == localID {
				t.Fatal("remote click attached the local twin")
			}
		})
		t.Run(tc.name+"/activatePreviewLinkAt", func(t *testing.T) {
			m, localID, remoteID, remoteSrcID, otherID := sessionTwinModel(t)
			from := tc.from(localID, remoteID, remoteSrcID, otherID)
			want := tc.want(localID, remoteID, remoteSrcID, otherID)
			selectSessionSource(t, m, from)
			cmd, claimed := m.activatePreviewLinkAt(previewNeedleAction(t, m, twinSessionName), false)
			if !claimed || cmd == nil {
				t.Fatalf("activatePreviewLinkAt claimed=%v cmd=%v", claimed, cmd != nil)
			}
			run(t, m, cmd)
			requireSelectedSession(t, m, want, tc.wantHost)
			if m.workspaces.SelectedID() == otherID {
				t.Fatal("attached a different-host twin")
			}
			if tc.wantHost == "" && m.workspaces.SelectedID() == remoteID {
				t.Fatal("local click attached the remote twin")
			}
			if tc.wantHost != "" && m.workspaces.SelectedID() == localID {
				t.Fatal("remote click attached the local twin")
			}
		})
	}
}

func TestSessionLinkDoesNotCrossHostWhenNameExistsOnlyOnTheOtherSide(t *testing.T) {
	m, localID, remoteID, remoteSrcID, _ := sessionTwinModel(t)

	selectSessionSource(t, m, "a")
	cmd, handled := m.activatePreviewPlan(targetactivation.Plan{
		Kind: targetactivation.PlanAttachSession, Session: "sidecar-sh-src-1",
	})
	if handled || cmd != nil {
		t.Fatalf("local pane attached a remote-only session: handled=%v cmd=%v", handled, cmd != nil)
	}
	if got := m.workspaces.SelectedID(); got != "a" {
		t.Fatalf("local miss changed selection to %q", got)
	}

	selectSessionSource(t, m, remoteSrcID)
	cmd, handled = m.activatePreviewPlan(targetactivation.Plan{
		Kind: targetactivation.PlanAttachSession, Session: "sc-alpha",
	})
	if handled || cmd != nil {
		t.Fatalf("remote pane attached a local-only session: handled=%v cmd=%v", handled, cmd != nil)
	}
	if got := m.workspaces.SelectedID(); got != remoteSrcID {
		t.Fatalf("remote miss changed selection to %q (local=%q remoteTwin=%q)", got, localID, remoteID)
	}
}
