package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

func nativeWorkspacePlugin() *Plugin {
	p := New()
	p.width = 100
	p.height = 30
	p.focused = true
	p.activePane = PanePreview
	p.viewMode = ViewModeInteractive
	p.previewTab = PreviewTabOutput
	p.sidebarVisible = false
	buffer := tty.NewOutputBuffer(outputBufferCap)
	buffer.Write("zero\none\ntwo")
	p.worktrees = []*Worktree{{
		Name: "worktree",
		Agent: &Agent{
			TmuxSession: "agent-session", TmuxPane: "%11", OutputBuf: buffer,
		},
	}}
	width, height := p.calculateAgentPaneDimensions()
	p.interactiveState = &InteractiveState{
		Active: true, TargetSession: "agent-session", TargetPane: "%11",
		CursorRow: 2, CursorCol: 4, CursorVisible: true,
		PaneHeight: height, PaneWidth: width,
	}
	return p
}

func TestWorkspaceNativeCursorFullPreviewAndSuppression(t *testing.T) {
	p := nativeWorkspacePlugin()
	cursor := p.Cursor()
	// Row 4: the panel border, the single header row, then cursor row 2.
	if cursor == nil || cursor.X != 6 || cursor.Y != 4 ||
		cursor.Shape != tea.CursorBlock || !cursor.Blink {
		t.Fatalf("Cursor() = %#v, want plugin-local (6,4)", cursor)
	}
	if mode := p.PreferredMouseMode(); mode != tea.MouseModeCellMotion {
		t.Fatalf("PreferredMouseMode() = %v, want cell motion", mode)
	}
	if rendered := p.View(p.width, p.height); strings.Contains(rendered, "█") {
		t.Fatalf("workspace painted a cursor while native cursor owns it: %q", rendered)
	}

	p.previewScroll = 1
	if cursor := p.Cursor(); cursor != nil {
		t.Fatalf("off-live Cursor() = %#v, want nil", cursor)
	}
	p.previewScroll = 0
	p.activePane = PaneSidebar
	if cursor := p.Cursor(); cursor != nil {
		t.Fatalf("sidebar-focused Cursor() = %#v, want nil", cursor)
	}
	if mode := p.PreferredMouseMode(); mode != tea.MouseModeAllMotion {
		t.Fatalf("sidebar mouse mode = %v, want all motion", mode)
	}
}

func TestWorkspaceNativeCursorTerminalPanelRightAndBottom(t *testing.T) {
	p := New()
	p.width = 120
	p.height = 40
	p.focused = true
	p.activePane = PanePreview
	p.viewMode = ViewModeInteractive
	p.previewTab = PreviewTabOutput
	p.sidebarVisible = true
	p.sidebarWidth = 40
	p.shellSelected = true
	p.selectedShellIdx = 0
	p.termPanelVisible = true
	p.termPanelSize = 50
	p.termPanelSession = "panel-session"
	p.termPanelPaneID = "%12"
	p.termPanelOutput = tty.NewOutputBuffer(outputBufferCap)
	p.termPanelOutput.Write("zero\none")
	p.shells = []*ShellSession{{
		Name: "Shell", TmuxName: "shell-session",
		Agent: &Agent{
			TmuxSession: "shell-session", TmuxPane: "%11",
			OutputBuf: tty.NewOutputBuffer(outputBufferCap),
		},
	}}
	p.interactiveState = &InteractiveState{
		Active: true, TermPanel: true, TargetSession: "panel-session", TargetPane: "%12",
		CursorRow: 1, CursorCol: 2, CursorVisible: true,
	}

	p.termPanelLayout = TermPanelRight
	width, height, _ := p.calculateTermPanelDimensions()
	p.interactiveState.PaneWidth = width
	p.interactiveState.PaneHeight = height
	if cursor := p.Cursor(); cursor == nil || cursor.X != 86 || cursor.Y != 3 {
		t.Fatalf("right-panel Cursor() = %#v, want (86,3)", cursor)
	}

	p.termPanelLayout = TermPanelBottom
	width, height, _ = p.calculateTermPanelDimensions()
	p.interactiveState.PaneWidth = width
	p.interactiveState.PaneHeight = height
	if cursor := p.Cursor(); cursor == nil || cursor.X != 52 || cursor.Y != 22 {
		t.Fatalf("bottom-panel Cursor() = %#v, want (52,22)", cursor)
	}

	p.termPanelScroll = 1
	if cursor := p.Cursor(); cursor != nil {
		t.Fatalf("scrolled panel Cursor() = %#v, want nil", cursor)
	}
}

func TestTerminalViewportNativeCursorDoesNotMutateContent(t *testing.T) {
	buffer := tty.NewOutputBuffer(20)
	buffer.Write("one\ntwo")
	in := terminalViewportInput{
		Buffer: buffer, Width: 10, Height: 2, Follow: true,
		Interactive: true, CursorRow: 1, CursorCol: 2, CursorVisible: true,
		PaneHeight: 2, PaneWidth: 10, NativeCursor: true,
	}
	result := renderTerminalViewport(in, ui.NewTruncateCache(20))
	if strings.Contains(result.Content, "█") {
		t.Fatalf("native viewport painted cursor: %q", result.Content)
	}
	if x, y, ok := terminalViewportCursorPosition(in); !ok || x != 2 || y != 1 {
		t.Fatalf("terminalViewportCursorPosition() = (%d,%d,%v)", x, y, ok)
	}

	// A pane taller than the viewport is clipped, and following anchors the
	// window on the cursor rather than on the pane's tail (td-73fa86), so a
	// cursor near the top of the pane stays visible on the first rendered row.
	buffer.Write("one\ntwo\nthree\nfour\nfive")
	in.CursorRow = 0
	in.PaneHeight = 5
	if x, y, ok := terminalViewportCursorPosition(in); !ok || x != 2 || y != 0 {
		t.Fatalf("clipped-pane terminalViewportCursorPosition() = (%d,%d,%v), want (2,0,true)", x, y, ok)
	}
}
