package overview

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/broadcastmodal"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

func enableGlobalBroadcast(t *testing.T) {
	t.Helper()
	features.Init(config.Default())
	features.SetOverride(features.AgentControl.Name, true)
	t.Cleanup(func() { features.Init(config.Default()) })
}

func disableGlobalBroadcast(t *testing.T) {
	t.Helper()
	features.Init(config.Default())
	features.SetOverride(features.AgentControl.Name, false)
	t.Cleanup(func() { features.Init(config.Default()) })
}

func TestGlobalBroadcastFlagHidesKeyAndCommand(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.focusList())

	disableGlobalBroadcast(t)
	if handled, _ := m.WorkspacesKey(globalMoveKey('B')); handled && m.broadcast != nil {
		t.Fatal("agent_control off opened the broadcast modal")
	}
	if hasBroadcastCommand(m.Commands(), broadcastmodal.CommandID) {
		t.Fatal("agent_control off still advertises Broadcast")
	}

	enableGlobalBroadcast(t)
	if !hasBroadcastCommand(m.Commands(), broadcastmodal.CommandID) {
		t.Fatal("enabled agent_control did not advertise Broadcast")
	}
	if handled, _ := m.WorkspacesKey(globalMoveKey('B')); !handled || m.broadcast == nil {
		t.Fatal("enabled agent_control did not open the broadcast modal")
	}
}

func TestGlobalBroadcastModalOwnsKeys(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.focusList())
	enableGlobalBroadcast(t)
	if handled, _ := m.WorkspacesKey(globalMoveKey('B')); !handled || m.broadcast == nil {
		t.Fatal("B did not open the modal")
	}
	if !m.WorkspacesConsumesTextInput() || !m.WorkspacesBlocksGlobalKeys() {
		t.Fatal("open broadcast modal must own typed keys")
	}
	m.WorkspacesKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	if m.broadcast != nil {
		t.Fatal("esc did not close the broadcast modal")
	}
}

func TestGlobalBroadcastLateSentDoesNotCloseAReopenedModal(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	first := broadcastmodal.New(broadcastmodal.SurfaceSessions, "", t.TempDir(), broadcastmodal.ScopeAllProjects, false)
	m.broadcast = first
	second := broadcastmodal.New(broadcastmodal.SurfaceSessions, "", t.TempDir(), broadcastmodal.ScopeAllProjects, false)
	m.broadcast = second
	m.applyBroadcastSent(broadcastmodal.SentMsg{Host: first})
	if m.broadcast != second {
		t.Fatal("a late SentMsg closed a broadcast modal it did not open")
	}
}

func TestBroadcastPlanMessagesAreAsync(t *testing.T) {
	if !IsAsyncMessage(broadcastmodal.PlannedMsg{}) {
		t.Fatal("PlannedMsg must be async so Sessions receives the plan")
	}
	if !IsAsyncMessage(broadcastmodal.SentMsg{}) {
		t.Fatal("SentMsg must be async so Sessions receives the send result")
	}
	if !IsSharedBroadcastMessage(broadcastmodal.PlannedMsg{}) || !IsSharedBroadcastMessage(broadcastmodal.SentMsg{}) {
		t.Fatal("plan/send must stay shared so the project workspace still receives them")
	}
}

func TestGlobalBroadcastCarriesSelectedProject(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	enableGlobalBroadcast(t)
	run(t, m, m.focusList())
	if handled, _ := m.WorkspacesKey(globalMoveKey('B')); !handled || m.broadcast == nil {
		t.Fatal("B did not open the modal")
	}
	if m.broadcast.ProjectKey != "sidecar" {
		t.Fatalf("Sessions broadcast project = %q, want the selected workspace's project so this-project lists local agents", m.broadcast.ProjectKey)
	}
}

func TestGlobalBroadcastNotesRemoteAgents(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	m.catalog["remote"] = workspaceinventory.Workspace{ID: "remote", HostID: "mac-mini", Name: "remote-shell"}
	enableGlobalBroadcast(t)
	run(t, m, m.focusList())
	m.WorkspacesKey(globalMoveKey('B'))
	if m.broadcast == nil || !m.broadcast.RemoteNote {
		t.Fatalf("remote catalog row must note that remote agents are not recipients: %+v", m.broadcast)
	}
}

func hasBroadcastCommand(commands []plugin.Command, id string) bool {
	for _, command := range commands {
		if command.ID == id {
			return true
		}
	}
	return false
}

// Sessions gives up its modal when it stops being the visible surface: the app
// calls this on a global tab change and on the way back to a project, so a
// modal cannot reappear over a surface that never opened it (td-cd1706).
func TestGlobalBroadcastClosesWhenSessionsIsCovered(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.focusList())
	enableGlobalBroadcast(t)
	if handled, _ := m.WorkspacesKey(globalMoveKey('B')); !handled || m.broadcast == nil {
		t.Fatal("fixture did not open the broadcast modal")
	}
	m.CloseBroadcast()
	if m.BroadcastOpen() {
		t.Fatal("Sessions kept its broadcast modal open after being covered")
	}
}

// The mirror of the project workspace's rule: Sessions answers only for the
// results it asked for (td-cd1706).
func TestGlobalBroadcastIgnoresProjectResult(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	project := broadcastmodal.New(broadcastmodal.SurfaceProject, "demo", t.TempDir(), broadcastmodal.ScopeThisProject, false)
	if cmd := m.applyBroadcastSent(broadcastmodal.SentMsg{Host: project}); cmd != nil {
		t.Fatal("Sessions reported the project workspace's broadcast")
	}
	own := broadcastmodal.New(broadcastmodal.SurfaceSessions, "demo", t.TempDir(), broadcastmodal.ScopeAllProjects, false)
	if cmd := m.applyBroadcastSent(broadcastmodal.SentMsg{Host: own}); cmd == nil {
		t.Fatal("Sessions did not report its own broadcast")
	}
}
