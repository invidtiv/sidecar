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
