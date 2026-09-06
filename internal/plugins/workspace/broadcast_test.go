package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/broadcastmodal"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
)

func TestProjectBroadcastFlagHidesKeyAndCommand(t *testing.T) {
	p := docPaneTestPlugin(t, t.TempDir(), false)
	p.activePane = PaneSidebar
	p.sidebarVisible = true

	disableWorkspaceFeature(t, features.AgentControl.Name)
	p.handleKeyPress(moveKey('B'))
	if p.broadcast != nil {
		t.Fatal("agent_control off opened the broadcast modal")
	}
	if hasBroadcastCommand(p.Commands(), broadcastmodal.CommandID) {
		t.Fatal("agent_control off still advertises Broadcast")
	}

	enableWorkspaceFeature(t, features.AgentControl.Name)
	if !hasBroadcastCommand(p.Commands(), broadcastmodal.CommandID) {
		t.Fatal("enabled agent_control did not advertise Broadcast")
	}
	p.handleKeyPress(moveKey('B'))
	if p.broadcast == nil {
		t.Fatal("enabled agent_control did not open the broadcast modal")
	}
}

func TestProjectBroadcastBDoesNotOpenWhenFilterFocused(t *testing.T) {
	p := filterPlugin(t)
	enableWorkspaceFeature(t, features.AgentControl.Name)
	p.handleKeyPress(key("/"))
	if !p.filterFocused() || p.FocusContext() != "workspace-filter" {
		t.Fatal("fixture did not focus the list filter")
	}
	p.handleKeyPress(moveKey('B'))
	if p.broadcast != nil {
		t.Fatal("B opened the broadcast modal while the filter owned the keyboard")
	}
	if p.listFilter.Query() != "B" {
		t.Fatalf("filter query = %q, want B", p.listFilter.Query())
	}
}

func TestProjectBroadcastLateSentDoesNotCloseAReopenedModal(t *testing.T) {
	p := docPaneTestPlugin(t, t.TempDir(), false)
	p.activePane = PaneSidebar
	first := broadcastmodal.New("demo", t.TempDir(), broadcastmodal.ScopeThisProject, false)
	p.broadcast = first
	second := broadcastmodal.New("demo", t.TempDir(), broadcastmodal.ScopeThisProject, false)
	p.broadcast = second
	p.applyBroadcastSent(broadcastmodal.SentMsg{Host: first})
	if p.broadcast != second {
		t.Fatal("a late SentMsg closed a broadcast modal it did not open")
	}
	p.applyBroadcastSent(broadcastmodal.SentMsg{Host: second})
	if p.broadcast != nil {
		t.Fatal("matching SentMsg left the modal open")
	}
}

func TestProjectBroadcastBDoesNotOpenFromPreview(t *testing.T) {
	p := docPaneTestPlugin(t, t.TempDir(), false)
	enableWorkspaceFeature(t, features.AgentControl.Name)
	p.activePane = PanePreview
	p.handleKeyPress(moveKey('B'))
	if p.broadcast != nil {
		t.Fatal("B on the preview opened the broadcast modal")
	}
}

func TestProjectBroadcastModalOwnsKeys(t *testing.T) {
	p := docPaneTestPlugin(t, t.TempDir(), false)
	p.activePane = PaneSidebar
	enableWorkspaceFeature(t, features.AgentControl.Name)
	p.handleKeyPress(moveKey('B'))
	if p.broadcast == nil {
		t.Fatal("B did not open the modal")
	}
	if !p.ConsumesTextInput() || !p.BlocksGlobalKeys() {
		t.Fatal("open broadcast modal must own typed keys")
	}
	p.broadcast.Ensure(80)
	p.handleKeyPress(tea.KeyPressMsg{Code: tea.KeyEscape})
	if p.broadcast != nil {
		t.Fatal("esc did not close the broadcast modal")
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
