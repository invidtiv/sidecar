package keymap_test

import (
	"testing"

	"github.com/marcus/sidecar/internal/keymap"
)

func TestPaneMoveModalIsBoundInBothWorkspaceListsAndEveryPaneContext(t *testing.T) {
	want := map[string]bool{
		"workspace-list": true, "workspace-preview": true, "workspace-doc": true, "workspace-issue": true,
		"workspace-note": true, "workspace-diff": true, "workspace-resource": true,
		"global-workspaces": true, "global-workspaces-doc": true, "global-workspaces-issue": true,
		"global-workspaces-note": true, "global-workspaces-diff": true, "global-workspaces-resource": true,
	}
	got := make(map[string]bool)
	for _, binding := range keymap.DefaultBindings() {
		if binding.Command != "move-pane" {
			continue
		}
		if binding.Key != "M" || binding.Feature != "pane_move" {
			t.Errorf("move-pane binding = %+v, want gated M", binding)
		}
		got[binding.Context] = true
	}
	if len(got) != len(want) {
		t.Fatalf("move-pane contexts = %v, want %v", got, want)
	}
	for context := range want {
		if !got[context] {
			t.Errorf("%s has no move-pane binding", context)
		}
	}
}

func TestPaneMoveLeavesInputAndPluginBrowseContextsUntouched(t *testing.T) {
	untouched := map[string]bool{
		"workspace-filter": true, "workspace-interactive": true,
		"workspace-doc-edit": true, "workspace-doc-search": true, "workspace-doc-find": true,
		"global-workspaces-filter": true, "global-workspaces-terminal": true,
		"global-workspaces-doc-search": true, "global-workspaces-doc-find": true,
		"file-browser-tree": true, "file-browser-preview": true, "git-status": true,
		"notes-list": true, "notes-editor": true, "notes-search": true,
	}
	for _, binding := range keymap.DefaultBindings() {
		if untouched[binding.Context] && binding.Command == "move-pane" {
			t.Errorf("input/plugin context gained pane move binding: %+v", binding)
		}
	}
}

func TestPaneMoveHasNoLegacyDirectMoveContext(t *testing.T) {
	for _, binding := range keymap.DefaultBindings() {
		if binding.Context == "pane-move" {
			t.Fatalf("legacy direct-move binding remains: %+v", binding)
		}
	}
}
