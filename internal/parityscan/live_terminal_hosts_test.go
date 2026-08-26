package parityscan

import "testing"

func TestLiveTerminalHostsKeepSharedLeafOwnership(t *testing.T) {
	project := StructFields(t, "../plugins/workspace/plugin.go", "Plugin")
	RequireFieldType(t, "project workspace", project, "terminalPanes", "*termpanes.Deck",
		"primaryTerminal", "primaryTerminalTarget", "termPanelVisible", "termPanelSession",
		"termPanelPaneID", "termPanelOutput", "termPanelScroll", "termPanelFreeze",
		"termPanelFreezeDoc", "termPanelFocused", "TargetTermPanel")

	global := StructFields(t, "../overview/preview.go", "previewState")
	RequireFieldType(t, "global Sessions", global, "terminalPanes", "*termpanes.Deck",
		"terminal", "terminalTarget", "buffer", "offset", "freeze", "history",
		"selection", "pointer", "wheel", "termBar", "linkState", "rowAnalyzer", "interactive")
}
