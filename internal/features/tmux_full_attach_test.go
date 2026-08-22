package features

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
)

func TestTmuxFullAttach_DefaultOff(t *testing.T) {
	globalManager = nil
	if TmuxFullAttach.Default {
		t.Error("tmux_full_attach must ship disabled by default")
	}
	if IsEnabled(TmuxFullAttach.Name) {
		t.Error("tmux_full_attach should be disabled without config")
	}
	if !IsKnownFeature(TmuxFullAttach.Name) {
		t.Error("tmux_full_attach should be a registered feature")
	}
}

func TestTmuxFullAttach_ConfigEnables(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[TmuxFullAttach.Name] = true
	Init(cfg)
	t.Cleanup(func() { globalManager = nil })

	if !IsEnabled(TmuxFullAttach.Name) {
		t.Error("tmux_full_attach should be enabled by config")
	}

	SetOverride(TmuxFullAttach.Name, false)
	if IsEnabled(TmuxFullAttach.Name) {
		t.Error("CLI override should win over config")
	}
}

func TestWorkspaceTerminalPanel_DefaultOn(t *testing.T) {
	globalManager = nil
	if !WorkspaceTerminalPanel.Default {
		t.Error("workspace_terminal_panel must ship enabled by default")
	}
	if !IsEnabled(WorkspaceTerminalPanel.Name) {
		t.Error("workspace_terminal_panel should be enabled without config")
	}
	if !IsKnownFeature(WorkspaceTerminalPanel.Name) {
		t.Error("workspace_terminal_panel should be a registered feature")
	}
}

func TestWorkspaceTerminalPanel_ConfigEnables(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[WorkspaceTerminalPanel.Name] = true
	Init(cfg)
	t.Cleanup(func() { globalManager = nil })

	if !IsEnabled(WorkspaceTerminalPanel.Name) {
		t.Error("workspace_terminal_panel should be enabled by config")
	}

	SetOverride(WorkspaceTerminalPanel.Name, false)
	if IsEnabled(WorkspaceTerminalPanel.Name) {
		t.Error("CLI override should win over config")
	}
}

func TestStayInSidecarFlagsListedInAll(t *testing.T) {
	want := map[string]bool{
		TmuxFullAttach.Name:         false,
		WorkspaceTerminalPanel.Name: false,
	}
	for _, f := range ListAll() {
		if _, ok := want[f.Name]; ok {
			want[f.Name] = true
		}
	}
	for name, found := range want {
		if !found {
			t.Errorf("%s missing from ListAll", name)
		}
	}
}
