package keymap_test

import (
	"testing"

	"github.com/marcus/sidecar/internal/keymap"
)

func TestBroadcastAgentsIsBoundOnlyInTheTwoListContexts(t *testing.T) {
	want := map[string]bool{
		"workspace-list":    true,
		"global-workspaces": true,
	}
	got := make(map[string]bool)
	for _, binding := range keymap.DefaultBindings() {
		if binding.Command != "broadcast-agents" {
			continue
		}
		if binding.Key != "B" || binding.Feature != "agent_control" {
			t.Errorf("broadcast-agents binding = %+v, want gated B", binding)
		}
		got[binding.Context] = true
	}
	if len(got) != len(want) {
		t.Fatalf("broadcast-agents contexts = %v, want %v", got, want)
	}
	for context := range want {
		if !got[context] {
			t.Errorf("%s has no broadcast-agents binding", context)
		}
	}
}

func TestBroadcastLeavesInputAndPaneContextsUntouched(t *testing.T) {
	untouched := map[string]bool{
		"workspace-filter": true, "workspace-interactive": true, "workspace-preview": true,
		"workspace-doc": true, "workspace-doc-edit": true, "workspace-doc-search": true, "workspace-doc-find": true,
		"global-workspaces-filter": true, "global-workspaces-terminal": true,
		"global-workspaces-doc": true, "global-workspaces-doc-search": true, "global-workspaces-doc-find": true,
		"file-browser-tree": true, "file-browser-preview": true, "git-status": true,
	}
	for _, binding := range keymap.DefaultBindings() {
		if untouched[binding.Context] && binding.Command == "broadcast-agents" {
			t.Errorf("input/pane context gained broadcast binding: %+v", binding)
		}
	}
}
