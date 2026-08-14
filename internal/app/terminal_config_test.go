package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/tty"
)

func TestTerminalConfigFallsBackToDefaults(t *testing.T) {
	features.Init(config.Default())
	t.Cleanup(func() { features.Init(config.Default()) })
	defaults := tty.DefaultConfig()
	for _, cfg := range []*config.Config{nil, {}} {
		got := TerminalConfig(cfg)
		if got.ExitKey != defaults.ExitKey || got.AttachKey != "" ||
			got.CopyKey != defaults.CopyKey || got.PasteKey != defaults.PasteKey {
			t.Errorf("TerminalConfig(%v) = %+v, want defaults with attach empty", cfg, got)
		}
	}
}

func TestTerminalConfigAttachKeyFollowsFullAttachFlag(t *testing.T) {
	cfg := config.Default()
	cfg.Plugins.Workspace.InteractiveAttachKey = "ctrl+g"
	cfg.Features.Flags[features.TmuxFullAttach.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })
	got := TerminalConfig(cfg)
	if got.AttachKey != "ctrl+g" {
		t.Fatalf("AttachKey = %q, want ctrl+g when tmux_full_attach is on", got.AttachKey)
	}
	features.SetOverride(features.TmuxFullAttach.Name, false)
	if got := TerminalConfig(cfg); got.AttachKey != "" {
		t.Fatalf("AttachKey = %q, want empty when tmux_full_attach is off", got.AttachKey)
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
