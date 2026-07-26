package workspace

import (
	"os/exec"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/app"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/plugin"
)

// newAutoShellPlugin builds a focused plugin with autoCreateShell set as given.
func newAutoShellPlugin(t *testing.T, enabled bool, defaultAgent string) *Plugin {
	t.Helper()
	cfg := config.Default()
	cfg.Plugins.Workspace.AutoCreateShell = enabled
	cfg.Plugins.Workspace.DefaultAgentType = defaultAgent
	return &Plugin{
		focused: true,
		shells:  []*ShellSession{},
		ctx: &plugin.Context{
			WorkDir:     t.TempDir(),
			ProjectRoot: t.TempDir(),
			Config:      cfg,
		},
	}
}

func TestResolveShellAgentType_NoConfigDefaultsToNone(t *testing.T) {
	p := newAutoShellPlugin(t, false, "")
	if got := p.resolveShellAgentType(); got != AgentNone {
		t.Errorf("resolveShellAgentType() = %q, want %q", got, AgentNone)
	}
}

func TestResolveShellAgentType_HonorsConfig(t *testing.T) {
	p := newAutoShellPlugin(t, false, string(AgentOpenCode))
	if got := p.resolveShellAgentType(); got != AgentOpenCode {
		t.Errorf("resolveShellAgentType() = %q, want %q", got, AgentOpenCode)
	}
}

func TestResolveShellAgentType_InvalidAgentFallsBackToNone(t *testing.T) {
	p := newAutoShellPlugin(t, false, "not-an-agent")
	if got := p.resolveShellAgentType(); got != AgentNone {
		t.Errorf("resolveShellAgentType() = %q, want %q", got, AgentNone)
	}
}

func TestMaybeAutoCreateShell_DisabledIsNoOp(t *testing.T) {
	p := newAutoShellPlugin(t, false, "")
	if cmd := p.maybeAutoCreateShell(); cmd != nil {
		t.Error("maybeAutoCreateShell() returned a command with autoCreateShell disabled")
	}
	if p.autoShellChecked {
		t.Error("autoShellChecked was consumed while the feature is disabled")
	}
}

func TestMaybeAutoCreateShell_UnfocusedDefersCheck(t *testing.T) {
	p := newAutoShellPlugin(t, true, "")
	p.focused = false

	if cmd := p.maybeAutoCreateShell(); cmd != nil {
		t.Error("maybeAutoCreateShell() returned a command while unfocused")
	}
	if p.autoShellChecked {
		t.Error("autoShellChecked was consumed while unfocused; the check must re-run on focus")
	}

	// Once focused, the deferred check runs.
	p.focused = true
	if cmd := p.maybeAutoCreateShell(); cmd == nil {
		t.Error("maybeAutoCreateShell() returned nil after gaining focus")
	}
}

func TestMaybeAutoCreateShell_SkipsWhenShellsExist(t *testing.T) {
	p := newAutoShellPlugin(t, true, "")
	p.shells = []*ShellSession{{Name: "Shell 1", TmuxName: "sidecar-shell-x-1"}}

	if cmd := p.maybeAutoCreateShell(); cmd != nil {
		t.Error("maybeAutoCreateShell() returned a command when a shell already exists")
	}
	if !p.autoShellChecked {
		t.Error("autoShellChecked should be consumed once the check runs while focused")
	}
}

func TestMaybeAutoCreateShell_RunsOnlyOnce(t *testing.T) {
	p := newAutoShellPlugin(t, true, "")

	if cmd := p.maybeAutoCreateShell(); cmd == nil {
		t.Fatal("maybeAutoCreateShell() returned nil on the first focused call")
	}
	if !p.autoShellChecked {
		t.Fatal("autoShellChecked was not consumed after running")
	}
	if cmd := p.maybeAutoCreateShell(); cmd != nil {
		t.Error("maybeAutoCreateShell() ran a second time")
	}
}

func TestCreateDefaultShell_DoesNotSkipPermissions(t *testing.T) {
	p := newAutoShellPlugin(t, true, string(AgentClaude))
	p.createDefaultShell(false)

	// The opts are consumed inside createShell; assert via the message it emits.
	msg := collectShellCreatedMsg(t, p, true)
	if msg.AgentType != AgentClaude {
		t.Errorf("AgentType = %q, want %q", msg.AgentType, AgentClaude)
	}
	if msg.SkipPerms {
		t.Error("SkipPerms = true; auto-created shells must not disable permission prompts")
	}
	if !msg.KeepSelection {
		t.Error("KeepSelection = false for an auto-created shell")
	}
}

// TestUpdate_PluginFocusedTriggersAutoCreate drives the real Update path rather
// than calling the helper directly, covering the trigger wiring.
func TestUpdate_PluginFocusedTriggersAutoCreate(t *testing.T) {
	if !isTmuxInstalled() {
		t.Skip("tmux not installed")
	}
	p := newAutoShellPlugin(t, true, "")

	_, cmd := p.Update(app.PluginFocusedMsg{})
	if cmd == nil {
		t.Fatal("Update(PluginFocusedMsg) returned no command")
	}
	if !p.autoShellChecked {
		t.Error("autoShellChecked was not consumed by the focused path")
	}

	// A second focus event must not create another shell.
	p.shells = append(p.shells, &ShellSession{Name: "Shell 1", TmuxName: "sidecar-shell-test-1"})
	before := len(p.shells)
	p.Update(app.PluginFocusedMsg{})
	if len(p.shells) != before {
		t.Errorf("len(shells) = %d after refocus, want %d", len(p.shells), before)
	}
}

// TestUpdate_UnfocusedPluginDoesNotAutoCreate guards the case where the user
// never opens the workspaces tab.
func TestUpdate_UnfocusedPluginDoesNotAutoCreate(t *testing.T) {
	p := newAutoShellPlugin(t, true, "")
	p.focused = false

	p.Update(app.PluginFocusedMsg{})
	if p.autoShellChecked {
		t.Error("autoShellChecked consumed while unfocused")
	}
}

// TestHandleListKeys_CtrlNCreatesShell covers the ctrl+n binding reaching the
// shell-creation path.
func TestHandleListKeys_CtrlNCreatesShell(t *testing.T) {
	if !isTmuxInstalled() {
		t.Skip("tmux not installed")
	}
	p := newAutoShellPlugin(t, false, "")
	p.viewMode = ViewModeList

	cmd := p.handleListKeys(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	if cmd == nil {
		t.Fatal("ctrl+n produced no command")
	}
	msg, ok := cmd().(ShellCreatedMsg)
	if !ok {
		t.Fatalf("ctrl+n produced %T, want ShellCreatedMsg", msg)
	}
	if msg.SessionName != "" {
		t.Cleanup(func() {
			_ = exec.Command("tmux", "kill-session", "-t", msg.SessionName).Run()
		})
	}
	if msg.Err != nil {
		t.Fatalf("shell creation failed: %v", msg.Err)
	}
	if msg.KeepSelection {
		t.Error("KeepSelection = true; a ctrl+n shell is user-requested and should be selected")
	}
	if msg.AgentType != AgentNone {
		t.Errorf("AgentType = %q, want %q with no configured default", msg.AgentType, AgentNone)
	}
}

func TestShellCreatedMsg_KeepSelectionLeavesSelectionAlone(t *testing.T) {
	t.Setenv("XDG_STATE_HOME", t.TempDir())

	tests := []struct {
		name              string
		keepSelection     bool
		wantShellSelected bool
		wantActivePane    FocusPane
	}{
		{"user-requested shell takes selection", false, true, PaneSidebar},
		{"auto-created shell leaves selection", true, false, PanePreview},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			p := newAutoShellPlugin(t, true, "")
			p.managedSessions = make(map[string]bool)
			p.shellPollGeneration = make(map[string]int)
			p.activePane = PanePreview
			p.shellSelected = false
			p.selectedIdx = 0

			p.Update(ShellCreatedMsg{
				SessionName:   "sidecar-shell-test-1",
				DisplayName:   "Shell 1",
				KeepSelection: tt.keepSelection,
			})

			if len(p.shells) != 1 {
				t.Fatalf("len(shells) = %d, want 1", len(p.shells))
			}
			if p.shellSelected != tt.wantShellSelected {
				t.Errorf("shellSelected = %v, want %v", p.shellSelected, tt.wantShellSelected)
			}
			if p.activePane != tt.wantActivePane {
				t.Errorf("activePane = %v, want %v", p.activePane, tt.wantActivePane)
			}
		})
	}
}

// collectShellCreatedMsg runs createDefaultShell and returns the resulting message.
// Skips the test when tmux is unavailable, since creation shells out to it.
func collectShellCreatedMsg(t *testing.T, p *Plugin, keepSelection bool) ShellCreatedMsg {
	t.Helper()
	if !isTmuxInstalled() {
		t.Skip("tmux not installed")
	}
	cmd := p.createDefaultShell(keepSelection)
	if cmd == nil {
		t.Fatal("createDefaultShell() returned nil")
	}
	msg, ok := cmd().(ShellCreatedMsg)
	if !ok {
		t.Fatalf("createDefaultShell() produced %T, want ShellCreatedMsg", msg)
	}
	if msg.SessionName != "" {
		t.Cleanup(func() {
			_ = exec.Command("tmux", "kill-session", "-t", msg.SessionName).Run()
		})
	}
	return msg
}
