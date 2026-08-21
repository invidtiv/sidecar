package workspace

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/features"
	boardkanban "github.com/marcus/sidecar/internal/kanban"
	"github.com/marcus/sidecar/internal/mouse"
)

func TestFullAttachIsOffByDefault(t *testing.T) {
	if fullTmuxAttachEnabled() {
		t.Fatal("tmux_full_attach must default off")
	}
	p := surfacePlugin(false)
	var attached string
	p.attachSession = func(sessionName, _ string) tea.Cmd {
		attached = sessionName
		return nil
	}

	if cmd := p.AttachToSession(p.worktrees[0]); cmd != nil {
		t.Fatal("AttachToSession ran with tmux_full_attach off")
	}
	if cmd := p.ensureShellAndAttach(p.shells[0]); cmd != nil || attached != "" {
		t.Fatalf("ensureShellAndAttach ran with tmux_full_attach off (attached=%q)", attached)
	}
	if cmd := p.attachFromInteractive(); cmd != nil {
		t.Fatal("attachFromInteractive ran with tmux_full_attach off")
	}
	if cmd := p.handleListKeys(tea.KeyPressMsg{Code: 't', Text: "t"}); cmd != nil || attached != "" {
		t.Fatal("t attached with tmux_full_attach off")
	}

	cmd := p.handleMouseDoubleClick(mouse.MouseAction{
		Type:   mouse.ActionDoubleClick,
		Region: &mouse.Region{ID: regionWorktreeItem, Data: 0},
	})
	if cmd != nil || p.attachedSession != "" {
		t.Fatalf("double-click attached (cmd=%v session=%q)", cmd, p.attachedSession)
	}
}

func TestFullAttachFlagRestoresAttachJourneys(t *testing.T) {
	enableWorkspaceFeature(t, features.TmuxFullAttach.Name)
	p := surfacePlugin(true)
	var attached string
	p.attachSession = func(sessionName, _ string) tea.Cmd {
		attached = sessionName
		return nil
	}

	_ = p.ensureShellAndAttach(p.shells[0])
	if attached != "shell-session" {
		t.Fatalf("ensureShellAndAttach attached %q, want shell-session", attached)
	}

	attached = ""
	_ = p.handleListKeys(tea.KeyPressMsg{Code: 't', Text: "t"})
	if attached != "shell-session" {
		t.Fatalf("t attached %q, want shell-session", attached)
	}

	if cmd := p.AttachToSession(p.worktrees[0]); cmd == nil {
		t.Fatal("AttachToSession returned nil with tmux_full_attach on")
	}
}

func TestWatchedHeaderDropsAttachCopyByDefault(t *testing.T) {
	p := surfacePlugin(false)
	view := ansi.Strip(p.renderOutputContent(120, 20))
	for _, forbidden := range []string{
		"t to attach", "E for interactive", "to detach", "Enter or double-click to attach",
	} {
		if strings.Contains(view, forbidden) {
			t.Fatalf("watched header still contains %q:\n%s", forbidden, view)
		}
	}
}

func TestWatchedHeaderRestoresAttachCopyWhenEnabled(t *testing.T) {
	enableWorkspaceFeature(t, features.TmuxFullAttach.Name)
	p := surfacePlugin(false)
	view := ansi.Strip(p.renderOutputContent(160, 20))
	for _, want := range []string{"t to attach", "E for interactive", "to detach"} {
		if !strings.Contains(view, want) {
			t.Fatalf("watched header missing %q:\n%s", want, view)
		}
	}
}

func TestInteractiveHeaderIsExitOnlyByDefault(t *testing.T) {
	p := surfacePlugin(false)
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, TermPanel: false}
	view := ansi.Strip(p.renderOutputContent(160, 20))
	if !strings.Contains(view, "INTERACTIVE") {
		t.Fatalf("interactive header missing INTERACTIVE:\n%s", view)
	}
	if !strings.Contains(view, "exit") {
		t.Fatalf("interactive header missing exit key:\n%s", view)
	}
	if strings.Contains(view, "attach") {
		t.Fatalf("interactive header still advertises attach:\n%s", view)
	}
}

func TestFlashDoesNotRunWhenAttachIsOff(t *testing.T) {
	p := surfacePlugin(false)
	p.activePane = PanePreview
	p.handleListKeys(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if p.previewFlashActive() || !p.flashPreviewTime.IsZero() {
		t.Fatal("unhandled key flashed attach copy with tmux_full_attach off")
	}
}

func TestFlashRunsWhenAttachIsOn(t *testing.T) {
	enableWorkspaceFeature(t, features.TmuxFullAttach.Name)
	p := surfacePlugin(false)
	p.activePane = PanePreview
	p.handleListKeys(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if p.flashPreviewTime.IsZero() || !p.previewFlashActive() {
		t.Fatal("unhandled key did not flash attach copy with tmux_full_attach on")
	}
	p.flashPreviewTime = time.Now()
	header := ansi.Strip(p.renderOutputContent(160, 20))
	if !strings.Contains(header, "Enter or double-click to attach") {
		t.Fatalf("flash missing from header:\n%s", header)
	}
}

func TestCtrlKDoesNotKillShell(t *testing.T) {
	p := surfacePlugin(true)
	p.viewMode = ViewModeList
	cmd := p.handleListKeys(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl})
	if cmd != nil {
		t.Fatal("ctrl+k still killed the shell")
	}
	if p.viewMode != ViewModeList {
		t.Fatalf("view mode = %v, want list", p.viewMode)
	}
}

func TestDOpensShellDeleteConfirm(t *testing.T) {
	p := surfacePlugin(true)
	p.viewMode = ViewModeList
	p.shellSelected = true
	p.handleListKeys(keyPressFor("D"))
	if p.viewMode != ViewModeConfirmDeleteShell {
		t.Fatalf("view mode = %v, want shell delete confirm", p.viewMode)
	}
	if p.deleteConfirmShell == nil || p.deleteConfirmShell.TmuxName != "shell-session" {
		t.Fatalf("delete target = %#v", p.deleteConfirmShell)
	}
}

func TestLiveShellFooterSaysDeleteNotKill(t *testing.T) {
	p := surfacePlugin(true)
	p.viewMode = ViewModeList
	p.activePane = PaneSidebar
	p.shellSelected = true
	ids := commandIDs(p.Commands())
	if ids["kill-shell"] {
		t.Fatal("live shell footer still advertises kill-shell")
	}
	if !ids["delete-workspace"] {
		t.Fatalf("live shell footer missing Delete: %v", ids)
	}
	if ids["attach-shell"] {
		t.Fatal("live shell footer advertises attach while tmux_full_attach is off")
	}

	enableWorkspaceFeature(t, features.TmuxFullAttach.Name)
	ids = commandIDs(p.Commands())
	if !ids["attach-shell"] {
		t.Fatal("live shell footer hid attach with tmux_full_attach on")
	}
}

func TestAgentChoiceHidesAttachByDefault(t *testing.T) {
	p := surfacePlugin(false)
	p.agentChoiceWorktree = p.worktrees[0]
	items := p.agentChoiceItems()
	if len(items) != 1 || items[0].ID != "agent-choice-restart" {
		t.Fatalf("agent choice items = %+v, want restart only", items)
	}
	p.agentChoiceIdx = 0
	if cmd := p.executeAgentChoice(); cmd != nil {
		t.Fatal("idx 0 attached with attach hidden")
	}
	if p.viewMode != ViewModeAgentConfig {
		t.Fatalf("view mode = %v, want restart config", p.viewMode)
	}

	enableWorkspaceFeature(t, features.TmuxFullAttach.Name)
	p.agentChoiceWorktree = p.worktrees[0]
	items = p.agentChoiceItems()
	if len(items) != 2 || items[0].ID != "agent-choice-attach" {
		t.Fatalf("enabled agent choice items = %+v", items)
	}
}

func TestKanbanDoubleClickDoesNotAttachByDefault(t *testing.T) {
	p := &Plugin{
		mouseHandler: mouse.NewHandler(),
		worktrees: []*Worktree{
			{Name: "first", Status: StatusActive, Agent: &Agent{TmuxSession: "first-session"}},
			{Name: "target", Status: StatusWaiting, Agent: &Agent{TmuxSession: "target-session"}},
		},
	}
	p.syncKanbanComponent()
	action := mouse.MouseAction{Type: mouse.ActionDoubleClick, Region: &mouse.Region{
		ID: regionKanbanCard, Data: boardkanban.HitRegion{Kind: boardkanban.RegionCard, Column: 2, Row: 0, CardID: "worktree:target"},
	}}
	if cmd := p.handleMouseDoubleClick(action); cmd != nil {
		t.Fatal("kanban double-click attached with tmux_full_attach off")
	}
	if p.attachedSession != "" {
		t.Fatalf("attachedSession = %q", p.attachedSession)
	}
}

func TestTerminalPanelIsOffByDefault(t *testing.T) {
	if terminalPanelEnabled() {
		t.Fatal("workspace_terminal_panel must default off")
	}
	p := surfacePlugin(false)
	if cmd := p.handleListKeys(tea.KeyPressMsg{Code: 't', Mod: tea.ModCtrl}); cmd != nil || p.termPanelVisible {
		t.Fatal("ctrl+t toggled the terminal panel with the flag off")
	}
	if cmd := p.handleListKeys(tea.KeyPressMsg{Code: 't', Mod: tea.ModAlt}); cmd != nil {
		t.Fatal("alt+t switched layout with the flag off")
	}
	ids := commandIDs(p.Commands())
	if ids["toggle-terminal"] || ids["switch-terminal-layout"] {
		t.Fatalf("footer advertised terminal panel commands: %v", ids)
	}
	if restoreTermPanelVisible(true) {
		t.Fatal("a saved visible split was restored with the flag off")
	}
}

func TestTerminalPanelFlagRestoresSplit(t *testing.T) {
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	p := surfacePlugin(false)
	if !restoreTermPanelVisible(true) {
		t.Fatal("saved visible split was not restored with the flag on")
	}
	ids := commandIDs(p.Commands())
	if !ids["toggle-terminal"] {
		t.Fatalf("footer hid Term with the flag on: %v", ids)
	}
}

func TestWorkspaceTerminalAttachKeyEmptyByDefault(t *testing.T) {
	p := surfacePlugin(false)
	if got := p.getInteractiveAttachKey(); got != "" {
		t.Fatalf("AttachKey = %q, want empty so ctrl+] belongs to the pane", got)
	}
	model := p.newWorkspaceTerminal(workspaceTerminalPrimary)
	if model.Config.AttachKey != "" {
		t.Fatalf("terminal model AttachKey = %q, want empty", model.Config.AttachKey)
	}
	enableWorkspaceFeature(t, features.TmuxFullAttach.Name)
	if got := p.getInteractiveAttachKey(); got == "" {
		t.Fatal("AttachKey stayed empty with tmux_full_attach on")
	}
	model = p.newWorkspaceTerminal(workspaceTerminalPrimary)
	if model.Config.AttachKey == "" {
		t.Fatal("terminal model AttachKey stayed empty with tmux_full_attach on")
	}
}
