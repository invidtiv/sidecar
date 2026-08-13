package features

import "testing"

func TestWorkspaceDocPanesIsKnownAndDefaultsOn(t *testing.T) {
	globalManager = nil
	if !IsKnownFeature(WorkspaceDocPanes.Name) {
		t.Fatalf("%s is not registered", WorkspaceDocPanes.Name)
	}
	if !WorkspaceDocPanes.Default {
		t.Fatalf("%s must default on", WorkspaceDocPanes.Name)
	}
	if !IsEnabled(WorkspaceDocPanes.Name) {
		t.Fatalf("%s unexpectedly disabled without configuration", WorkspaceDocPanes.Name)
	}
}
