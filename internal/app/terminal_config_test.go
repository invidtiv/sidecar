package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/tty"
)

func TestTerminalConfigFallsBackToDefaults(t *testing.T) {
	defaults := tty.DefaultConfig()
	for _, cfg := range []*config.Config{nil, {}} {
		got := TerminalConfig(cfg)
		if got.ExitKey != defaults.ExitKey || got.AttachKey != defaults.AttachKey ||
			got.CopyKey != defaults.CopyKey || got.PasteKey != defaults.PasteKey {
			t.Errorf("TerminalConfig(%v) = %+v, want the defaults", cfg, got)
		}
	}
}

func TestTerminalConfigDrivesTheChords(t *testing.T) {
	cfg := &config.Config{}
	cfg.Plugins.Workspace.InteractiveCopyKey = "ctrl+shift+c"
	cfg.Plugins.Workspace.InteractivePasteKey = "ctrl+shift+v"
	cfg.Plugins.Workspace.CopyOnSelect = true

	terminal := TerminalConfig(cfg)
	if !terminal.CopyOnSelect {
		t.Error("copy-on-select did not reach the terminal config")
	}
	if !terminal.IsCopyChord(tea.KeyPressMsg{Code: 'c', Mod: tea.ModCtrl | tea.ModShift}) {
		t.Error("the configured copy chord is not answered")
	}
	if !terminal.IsCopyChord(tea.KeyPressMsg{Code: 'c', Mod: tea.ModSuper}) {
		t.Error("the platform copy chord stopped being answered")
	}
	if !terminal.IsPasteChord(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl | tea.ModShift}) {
		t.Error("the configured paste chord is not answered")
	}
}
