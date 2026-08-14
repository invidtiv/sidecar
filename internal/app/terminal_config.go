package app

import (
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/tty"
)

// TerminalConfig resolves the user's terminal-interaction settings once, so
// every surface that hosts a terminal answers the same chords. Surfaces override
// individual fields where they genuinely differ; nothing below re-reads config.
func TerminalConfig(cfg *config.Config) tty.Config {
	terminal := tty.DefaultConfig()
	if cfg != nil {
		workspace := cfg.Plugins.Workspace
		if workspace.InteractiveExitKey != "" {
			terminal.ExitKey = workspace.InteractiveExitKey
		}
		if workspace.InteractiveAttachKey != "" {
			terminal.AttachKey = workspace.InteractiveAttachKey
		}
		if workspace.InteractiveCopyKey != "" {
			terminal.CopyKey = workspace.InteractiveCopyKey
		}
		if workspace.InteractivePasteKey != "" {
			terminal.PasteKey = workspace.InteractivePasteKey
		}
		terminal.CopyOnSelect = workspace.CopyOnSelect
	}
	// Empty AttachKey is the pane's chord. tty.New treats empty as "use default",
	// so hosts that honour this resolution must assign the field after New.
	if !features.IsEnabled(features.TmuxFullAttach.Name) {
		terminal.AttachKey = ""
	}
	return terminal
}
