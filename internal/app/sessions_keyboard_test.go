package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// The Sessions surface is not a plugin, so it cannot report keyboard ownership
// through plugin.TextInputConsumer the way the project workspace does. The app
// asks it directly instead, and it must ask the surface rather than trust the
// context string: a collection pane taking a query reports the
// global-workspaces-resource context, which is not in the text-input list, and
// a host that consulted only the list would go on running and advertising the
// tab digits at a user who is typing them.
//
// The twin assertions over the pane itself live in internal/overview
// (TestSessionsSurfaceReportsPaneKeyboardOwnership) and in
// internal/plugins/workspace.
func TestTheHostAsksTheSessionsSurfaceWhoHasTheKeyboard(t *testing.T) {
	m, _ := scopeBaselineModel(t, "git")

	// Enter the global space on Sessions and focus the filter.
	updated, _ := m.Update(tea.KeyPressMsg{Code: 'k', Text: "K", Mod: tea.ModShift})
	m = asAppModel(t, updated)
	if !m.globalWorkspacesVisible() {
		t.Fatal("K did not land on the Sessions tab")
	}
	updated, _ = m.Update(tea.KeyPressMsg{Code: '/', Text: "/"})
	m = asAppModel(t, updated)
	if !m.overview.WorkspacesFilterFocused() {
		t.Fatal("`/` did not focus the Sessions filter")
	}

	// The context deliberately says something the text-input list does not
	// contain, which is the collection pane's situation exactly.
	m.activeContext = "global-workspaces"
	if !m.consumesTextInput() {
		t.Fatal("the host did not ask the Sessions surface who has the keyboard; " +
			"a typed key would run a global shortcut instead")
	}
}
