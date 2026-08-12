package features

import "testing"

func TestWorkspaceDocPanesIsKnownAndDefaultsOff(t *testing.T) {
	globalManager = nil
	if !IsKnownFeature(WorkspaceDocPanes.Name) {
		t.Fatalf("%s is not registered", WorkspaceDocPanes.Name)
	}
	if WorkspaceDocPanes.Default {
		t.Fatalf("%s must default off", WorkspaceDocPanes.Name)
	}
	if IsEnabled(WorkspaceDocPanes.Name) {
		t.Fatalf("%s unexpectedly enabled without configuration", WorkspaceDocPanes.Name)
	}
}
