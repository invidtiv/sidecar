package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCommaAndPeriodDoNotHideThePaneTree(t *testing.T) {
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, false)
	if !p.paneTreeShowing() {
		t.Fatal("premise: tree is showing")
	}
	p.activePane = PanePreview
	p.handleListKeys(tea.KeyPressMsg{Code: ',', Text: ","})
	p.handleListKeys(tea.KeyPressMsg{Code: '.', Text: "."})
	if !p.paneTreeShowing() || p.paneRoot == nil {
		t.Fatal(",/. hid the pane tree")
	}
}

func TestWorkspaceModalsBlockGlobalKeys(t *testing.T) {
	if !(&Plugin{viewMode: ViewModeAgentChoice}).BlocksGlobalKeys() {
		t.Fatal("agent-choice modal does not block global keys")
	}
	if (&Plugin{viewMode: ViewModeList}).BlocksGlobalKeys() {
		t.Fatal("workspace list should allow global keys")
	}
}
