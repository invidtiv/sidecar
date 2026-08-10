package workspace

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

func TestCommaAndPeriodCyclePreviewTabs(t *testing.T) {
	p := &Plugin{
		activePane: PanePreview,
		previewTab: PreviewTabDiff,
	}

	p.handleListKeys(tea.KeyPressMsg{Code: ',', Text: ","})
	if p.previewTab != PreviewTabOutput {
		t.Fatalf("previewTab after comma = %v, want Output", p.previewTab)
	}
	p.handleListKeys(tea.KeyPressMsg{Code: '.', Text: "."})
	if p.previewTab != PreviewTabDiff {
		t.Fatalf("previewTab after period = %v, want Diff", p.previewTab)
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
