package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
)

func TestIsModalViewMode(t *testing.T) {
	tests := []struct {
		mode    ViewMode
		isModal bool
	}{
		{ViewModeList, false},
		{ViewModeKanban, false},
		{ViewModeInteractive, false},
		{ViewModeCreate, true},
		{ViewModeTaskLink, true},
		{ViewModeMerge, true},
		{ViewModeAgentChoice, true},
		{ViewModeConfirmDelete, true},
		{ViewModeConfirmDeleteShell, true},
		{ViewModeCommitForMerge, true},
		{ViewModePromptPicker, true},
		{ViewModeTypeSelector, true},
		{ViewModeRenameShell, true},
		{ViewModeFilePicker, true},
	}

	for _, tt := range tests {
		p := &Plugin{viewMode: tt.mode}
		got := p.isModalViewMode()
		if got != tt.isModal {
			t.Errorf("isModalViewMode() for %d = %v, want %v", tt.mode, got, tt.isModal)
		}
	}
}

func TestIsBackgroundRegion(t *testing.T) {
	background := []string{
		regionSidebar, regionPreviewPane, regionPaneDivider,
		regionWorktreeItem, regionPreviewTab,
		regionCreateWorktreeButton, regionShellsPlusButton, regionWorkspacesPlusButton,
		regionKanbanCard, regionKanbanColumn, regionViewToggle,
	}
	for _, id := range background {
		if !isBackgroundRegion(id) {
			t.Errorf("isBackgroundRegion(%q) = false, want true", id)
		}
	}

	modal := []string{
		agentChoiceConfirmID, agentChoiceCancelID,
		deleteConfirmDeleteID, deleteConfirmCancelID,
		regionCreateBackdrop, regionCreateModalBody, regionCreateInput,
		mergeMethodListID, mergeCleanUpButtonID, mergeSkipButtonID, // Merge modal element IDs
		typeSelectorListID, typeSelectorConfirmID, typeSelectorCancelID, typeSelectorInputID, // Type selector modal element IDs
		regionPromptItem, regionPromptFilter,
	}
	for _, id := range modal {
		if isBackgroundRegion(id) {
			t.Errorf("isBackgroundRegion(%q) = true, want false", id)
		}
	}
}

func TestModalClickGuard(t *testing.T) {
	modalModes := []ViewMode{
		ViewModeCreate, ViewModeMerge, ViewModeAgentChoice,
		ViewModeConfirmDelete, ViewModeConfirmDeleteShell,
		ViewModeRenameShell, ViewModeTypeSelector,
		ViewModePromptPicker, ViewModeTaskLink,
		ViewModeCommitForMerge, ViewModeFilePicker,
	}
	backgroundRegions := []string{
		regionSidebar, regionPreviewPane, regionWorktreeItem,
		regionPaneDivider, regionPreviewTab,
	}

	for _, mode := range modalModes {
		for _, region := range backgroundRegions {
			p := &Plugin{
				viewMode:     mode,
				mouseHandler: mouse.NewHandler(),
			}
			action := mouse.MouseAction{
				Type:   mouse.ActionClick,
				Region: &mouse.Region{ID: region},
			}
			cmd := p.handleMouseClick(action)
			if cmd != nil {
				t.Errorf("handleMouseClick(mode=%d, region=%q) returned non-nil cmd", mode, region)
			}
		}
	}
}

func TestModalClickGuardAllowsModalRegions(t *testing.T) {
	// Merge modal now uses modal library which handles its own regions via handleMergeModalMouse
	// This test verifies that handleMouseClick returns nil for merge modal (since it's handled separately)
	p := &Plugin{
		viewMode:     ViewModeMerge,
		mouseHandler: mouse.NewHandler(),
	}
	action := mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: mergeCleanUpButtonID, Data: nil},
	}
	// handleMouseClick should not process merge modal clicks (handled by handleMergeModalMouse)
	cmd := p.handleMouseClick(action)
	if cmd != nil {
		t.Errorf("merge modal click via handleMouseClick returned unexpected cmd")
	}
}

func TestModalDoubleClickGuard(t *testing.T) {
	p := &Plugin{
		viewMode:     ViewModeMerge,
		mouseHandler: mouse.NewHandler(),
	}
	action := mouse.MouseAction{
		Type:   mouse.ActionDoubleClick,
		Region: &mouse.Region{ID: regionPreviewPane},
	}
	cmd := p.handleMouseDoubleClick(action)
	if cmd != nil {
		t.Error("handleMouseDoubleClick should return nil when modal is open")
	}
}

func TestModalScrollGuard(t *testing.T) {
	p := &Plugin{
		viewMode:     ViewModeConfirmDelete,
		mouseHandler: mouse.NewHandler(),
	}
	action := mouse.MouseAction{
		Type:   mouse.ActionScrollDown,
		Region: &mouse.Region{ID: regionSidebar},
	}
	cmd := p.handleMouseScroll(action)
	if cmd != nil {
		t.Error("handleMouseScroll should return nil when modal is open with background region")
	}
}

func TestModalScrollGuardNilRegion(t *testing.T) {
	p := &Plugin{
		viewMode:     ViewModeConfirmDelete,
		mouseHandler: mouse.NewHandler(),
	}
	action := mouse.MouseAction{
		Type:   mouse.ActionScrollDown,
		Region: nil,
	}
	cmd := p.handleMouseScroll(action)
	if cmd != nil {
		t.Error("handleMouseScroll should return nil when modal is open with nil region")
	}
}

func TestModalDragGuard(t *testing.T) {
	p := &Plugin{
		viewMode:     ViewModeRenameShell,
		mouseHandler: mouse.NewHandler(),
		sidebarWidth: 40,
	}
	action := mouse.MouseAction{
		Type: mouse.ActionDrag,
		X:    50,
	}
	cmd := p.handleMouseDrag(action)
	if cmd != nil {
		t.Error("handleMouseDrag should return nil when modal is open")
	}
	if p.sidebarWidth != 40 {
		t.Errorf("sidebarWidth changed during modal drag: got %d, want 40", p.sidebarWidth)
	}
}

func TestModalHoverGuard(t *testing.T) {
	p := &Plugin{
		viewMode:     ViewModeTaskLink,
		mouseHandler: mouse.NewHandler(),
	}
	action := mouse.MouseAction{
		Type:   mouse.ActionHover,
		Region: &mouse.Region{ID: regionCreateWorktreeButton},
	}
	cmd := p.handleMouseHover(action)
	if cmd != nil {
		t.Error("handleMouseHover should return nil for background region when modal is open")
	}
	if p.hoverNewButton {
		t.Error("hoverNewButton should not be set when modal is open")
	}
}

// newPreviewClickTestPlugin builds a plugin with a selected shell whose agent is
// live enough for enterInteractiveMode to accept it.
func newPreviewClickTestPlugin() *Plugin {
	p := &Plugin{
		viewMode:      ViewModeList,
		mouseHandler:  mouse.NewHandler(),
		width:         100,
		height:        30,
		sidebarWidth:  40,
		previewTab:    PreviewTabOutput,
		shellSelected: true,
		shells: []*ShellSession{{
			TmuxName: "shell-1",
			Agent:    &Agent{OutputBuf: tty.NewOutputBuffer(100)},
		}},
	}
	p.selection.Clear()
	return p
}

func previewClickAction(shift, alt bool) mouse.MouseAction {
	return mouse.MouseAction{
		Type:  mouse.ActionClick,
		X:     60,
		Y:     6,
		Shift: shift,
		Alt:   alt,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: 40, Y: 2, W: 60, H: 20},
		},
	}
}

// A plain click on the terminal should make it live, so typing and wheel events
// reach the running app instead of scrolling sidecar's own capture buffer.
func TestPreviewPaneClickEntersInteractiveMode(t *testing.T) {
	p := newPreviewClickTestPlugin()

	if cmd := p.handleMouseClick(previewClickAction(false, false)); cmd == nil {
		t.Fatal("preview pane click should return a cmd from entering interactive mode")
	}
	if p.viewMode != ViewModeInteractive {
		t.Errorf("viewMode = %v, want ViewModeInteractive", p.viewMode)
	}
	if p.interactiveState == nil || !p.interactiveState.Active {
		t.Error("interactiveState should be active after clicking the preview pane")
	}
	if p.activePane != PanePreview {
		t.Errorf("activePane = %v, want PanePreview", p.activePane)
	}
}

// Shift/alt clicks stay in read mode so drag-selection still works over apps
// that have mouse reporting enabled.
func TestPreviewPaneModifierClickStaysInReadMode(t *testing.T) {
	for _, tc := range []struct {
		name       string
		shift, alt bool
	}{
		{"shift", true, false},
		{"alt", false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := newPreviewClickTestPlugin()
			p.handleMouseClick(previewClickAction(tc.shift, tc.alt))
			if p.viewMode == ViewModeInteractive {
				t.Error("modifier click should not enter interactive mode")
			}
			if p.interactiveState != nil {
				t.Error("modifier click should not create interactive state")
			}
		})
	}
}

// Diff/Task tabs have no terminal behind them, so a click there must not attach.
func TestPreviewPaneClickOnNonTerminalTabDoesNotAttach(t *testing.T) {
	p := newPreviewClickTestPlugin()
	p.shellSelected = false
	p.previewTab = PreviewTabDiff
	p.worktrees = []*Worktree{{Name: "wt"}}

	p.handleMouseClick(previewClickAction(false, false))
	if p.viewMode == ViewModeInteractive {
		t.Error("clicking the diff tab should not enter interactive mode")
	}
}

func TestNonModalClickPassesThrough(t *testing.T) {
	p := &Plugin{
		viewMode:     ViewModeList,
		mouseHandler: mouse.NewHandler(),
		width:        100,
		sidebarWidth: 40,
	}
	action := mouse.MouseAction{
		Type:   mouse.ActionClick,
		Region: &mouse.Region{ID: regionSidebar},
	}
	_ = p.handleMouseClick(action)
	if p.activePane != PaneSidebar {
		t.Error("sidebar click in List mode should set activePane to PaneSidebar")
	}
}

type scrollFallbackPosition struct {
	x           int
	wantSidebar bool
}

func TestScrollFallbackUsesRenderedPreviewSplit(t *testing.T) {
	tests := []struct {
		name           string
		width          int
		sidebarWidth   int
		sidebarVisible bool
		positions      func(previewSplit) []scrollFallbackPosition
	}{
		{
			name: "normal width", width: 120, sidebarWidth: 40, sidebarVisible: true,
			positions: visibleSplitBoundaryPositions,
		},
		{
			name: "sidebar minimum clamp", width: 120, sidebarWidth: 1, sidebarVisible: true,
			positions: visibleSplitBoundaryPositions,
		},
		{
			name: "preview minimum clamp", width: 120, sidebarWidth: 95, sidebarVisible: true,
			positions: visibleSplitBoundaryPositions,
		},
		{
			name: "sidebar hidden", width: 120, sidebarWidth: 40, sidebarVisible: false,
			positions: func(split previewSplit) []scrollFallbackPosition {
				return []scrollFallbackPosition{{x: 0}, {x: split.PreviewWidth - 1}}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			geometry := surfacePlugin(true)
			geometry.width = tt.width
			geometry.sidebarWidth = tt.sidebarWidth
			geometry.sidebarVisible = tt.sidebarVisible
			split := geometry.previewSplit()

			for _, pos := range tt.positions(split) {
				p := surfacePlugin(true)
				p.width = tt.width
				p.sidebarWidth = tt.sidebarWidth
				p.sidebarVisible = tt.sidebarVisible
				p.shells = append(p.shells, &ShellSession{
					Name: "Second shell", TmuxName: "shell-session-2",
					Agent: &Agent{OutputBuf: markerBuffer("SECOND", 100)},
				})
				p.shells[0].Agent.OutputBuf = markerBuffer("FIRST", 100)
				p.autoScrollOutput = false

				p.handleMouseScroll(mouse.MouseAction{Delta: 1, X: pos.x})
				gotSidebar := p.selectedShellIdx == 1
				gotPreview := p.previewOffset > 0
				if gotSidebar != pos.wantSidebar || gotPreview == pos.wantSidebar {
					t.Fatalf("x=%d split=%+v: sidebar scrolled=%v preview scrolled=%v, want sidebar=%v",
						pos.x, split, gotSidebar, gotPreview, pos.wantSidebar)
				}
			}
		})
	}
}

func visibleSplitBoundaryPositions(split previewSplit) []scrollFallbackPosition {
	return []scrollFallbackPosition{
		{x: split.SidebarWidth - 1, wantSidebar: true},
		{x: split.SidebarWidth}, // divider belongs to the preview fallback
		{x: split.PreviewX},
	}
}
