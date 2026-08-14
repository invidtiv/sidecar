package workspace

import "github.com/marcus/sidecar/internal/features"

func fullTmuxAttachEnabled() bool {
	return features.IsEnabled(features.TmuxFullAttach.Name)
}

func terminalPanelEnabled() bool {
	return features.IsEnabled(features.WorkspaceTerminalPanel.Name)
}

func restoreTermPanelVisible(saved bool) bool {
	return terminalPanelEnabled() && saved
}
