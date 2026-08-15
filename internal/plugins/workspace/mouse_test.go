package workspace

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
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
		{ViewModeTypeSelector, true},
		{ViewModeRenameShell, true},
		{ViewModeRenameWorktree, true},
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
		createSubmitID, createCancelID,
		mergeMethodListID, mergeCleanUpButtonID, mergeSkipButtonID, // Merge modal element IDs
		typeSelectorListID, typeSelectorConfirmID, typeSelectorCancelID, typeSelectorInputID, // Type selector modal element IDs
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
		ViewModeRenameShell, ViewModeRenameWorktree, ViewModeTypeSelector,
		ViewModeTaskLink,
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

// A plain click makes the terminal live on release. Deferring activation keeps
// the viewport stable long enough for the same gesture to become a selection.
func TestPreviewPaneClickEntersInteractiveMode(t *testing.T) {
	p := newPreviewClickTestPlugin()

	if cmd := p.handleMouseClick(previewClickAction(false, false)); cmd != nil {
		t.Fatal("mouse-down should only prepare the terminal gesture")
	}
	if p.viewMode == ViewModeInteractive {
		t.Fatal("terminal entered interactive mode before the click was released")
	}
	if cmd := p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane}); cmd == nil {
		t.Fatal("click release should return a cmd from entering interactive mode")
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

func TestPreviewPaneImmediateDragSelectsWithoutActivatingOrJumping(t *testing.T) {
	p := newPreviewClickTestPlugin()
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row\n", 50))
	renderedStart := p.terminalSelectionViewportLayout().Start
	if renderedStart == 0 {
		t.Fatal("test needs enough scrollback for the live viewport to start after row zero")
	}

	p.handleMouseClick(previewClickAction(false, false))
	p.handleMouseDrag(mouse.MouseAction{
		Type:        mouse.ActionDrag,
		DragStartID: regionPreviewPane,
		X:           66,
		Y:           8,
		Region:      previewClickAction(false, false).Region,
	})
	p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane})

	if p.viewMode == ViewModeInteractive || p.interactiveState != nil {
		t.Fatal("a click-drag selection should not activate the terminal")
	}
	if !p.selection.HasSelection() {
		t.Fatal("immediate click-drag did not create a selection")
	}
	if p.previewFreeze.Start() != renderedStart {
		t.Fatalf("selection froze viewport at %d, want rendered live start %d",
			p.previewFreeze.Start(), renderedStart)
	}
}

func TestLostReleaseFinishesTerminalSelection(t *testing.T) {
	p := newPreviewClickTestPlugin()
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row\n", 50))
	p.handleMouseClick(previewClickAction(false, false))
	p.handleMouseDrag(mouse.MouseAction{
		Type: mouse.ActionDrag, X: 66, Y: 8,
		Region: previewClickAction(false, false).Region,
	})
	if !p.selection.Active {
		t.Fatal("test setup did not start a selection")
	}

	p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: 66, Y: 8, Button: tea.MouseNone}))
	if p.selection.Active {
		t.Fatal("lost release left the selection gesture active")
	}
	if !p.selection.HasSelection() {
		t.Fatal("lost release discarded the completed selection")
	}
}

func TestLostReleaseBeforeMotionClearsAnchorWithoutActivating(t *testing.T) {
	p := newPreviewClickTestPlugin()
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row\n", 50))
	p.handleMouseClick(previewClickAction(false, false))
	if !p.selection.Anchor.Valid() {
		t.Fatal("test setup did not prepare a selection anchor")
	}

	p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: 60, Y: 6, Button: tea.MouseNone}))
	if p.selection.Anchor.Valid() || p.selection.Active {
		t.Fatal("lost release before motion left stale selection state")
	}
	if p.viewMode == ViewModeInteractive || p.interactiveState != nil {
		t.Fatal("lost release activated the terminal")
	}
}

func TestDividerDragEndIsNotHijackedByOldTerminalSelection(t *testing.T) {
	p := newPreviewClickTestPlugin()
	p.selection.SelectRange(
		ui.SelectionPoint{Line: 1, Col: 0},
		ui.SelectionPoint{Line: 2, Col: 4},
		false,
	)
	p.lastDragRegion = regionPaneDivider

	p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPaneDivider})
	if !p.selection.HasSelection() {
		t.Fatal("unrelated divider release consumed the terminal selection")
	}
	if p.viewMode == ViewModeInteractive {
		t.Fatal("unrelated divider release activated the terminal")
	}
}

func TestLostDividerReleaseDoesNotFinishTerminalSelection(t *testing.T) {
	p := newPreviewClickTestPlugin()
	p.selection.PrepareDragMode(1, 0, mouse.Rect{X: 40, Y: 2, W: 60, H: 20}, false)
	p.selection.HandleDrag(2, 4)
	if !p.selection.Active {
		t.Fatal("test setup did not create an active terminal selection")
	}
	p.mouseHandler.StartDrag(40, 2, regionPaneDivider, 40)

	p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: 45, Y: 2, Button: tea.MouseNone}))
	if !p.selection.Active {
		t.Fatal("lost divider release finished an unrelated terminal selection")
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
				// A window already back in scrollback, so a notch towards the
				// live edge has somewhere to move it.
				p.previewScroll = 5

				p.handleMouseScroll(mouse.MouseAction{Delta: 1, X: pos.x})
				gotSidebar := p.selectedShellIdx == 1
				// A notch the preview took moves its window one row towards the
				// live bottom; one the sidebar took resets it to the live edge
				// with the selection, which is not the same answer.
				gotPreview := p.previewScroll == 4
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

// Trackpads twitch. A gesture that never leaves its anchor cell is the click the
// user meant, not a one-cell selection that silently lands on the clipboard and
// swallows the activation.
func TestClickJitterWithinOneCellStillActivatesTerminal(t *testing.T) {
	p := newPreviewClickTestPlugin()
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row\n", 50))

	p.handleMouseClick(previewClickAction(false, false))
	p.handleMouseDrag(mouse.MouseAction{
		Type: mouse.ActionDrag, X: 60, Y: 6, DragStartID: regionPreviewPane,
		Region: previewClickAction(false, false).Region,
	})
	cmd := p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane})

	if p.selection.HasSelection() {
		t.Errorf("jittered click left a %+v..%+v selection", p.selection.Start, p.selection.End)
	}
	if cmd == nil || p.viewMode != ViewModeInteractive {
		t.Fatalf("jittered click did not activate the terminal (viewMode=%v)", p.viewMode)
	}
}

// One cell of real movement is still a selection, and must not activate.
func TestOneCellDragSelectsWithoutActivating(t *testing.T) {
	p := newPreviewClickTestPlugin()
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row\n", 50))

	p.handleMouseClick(previewClickAction(false, false))
	p.handleMouseDrag(mouse.MouseAction{
		Type: mouse.ActionDrag, X: 61, Y: 6, DragStartID: regionPreviewPane,
		Region: previewClickAction(false, false).Region,
	})
	p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane})

	if !p.selection.HasSelection() {
		t.Fatal("a one-cell drag produced no selection")
	}
	if p.viewMode == ViewModeInteractive {
		t.Error("a one-cell drag selection activated the terminal")
	}
}

// The whole double-click-drag gesture, driven through the shared mouse handler:
// the word gesture has to arm drag tracking itself, or the motion that follows
// arrives as hover and the release as a fresh click on a live terminal.
func TestDoubleClickDragRoundTripSelectsWords(t *testing.T) {
	p := newPreviewClickTestPlugin()
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row here\n", 200))
	p.mouseHandler.HitMap.Add(regionPreviewPane, mouse.Rect{X: 40, Y: 2, W: 60, H: 20}, nil)

	p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: 53, Y: 6, Button: tea.MouseLeft}))
	p.handleMouse(tea.MouseClickMsg(tea.Mouse{X: 53, Y: 6, Button: tea.MouseLeft}))
	line := p.selection.Start.Line
	if p.selection.Start.Col != 11 || p.selection.End.Col != 18 {
		t.Fatalf("double-click selected %+v..%+v, want the whole word terminal",
			p.selection.Start, p.selection.End)
	}
	if !p.mouseHandler.IsDragging() {
		t.Fatal("double-click did not arm drag tracking")
	}

	p.handleMouse(tea.MouseMotionMsg(tea.Mouse{X: 67, Y: 6, Button: tea.MouseLeft}))
	if p.selection.End != (ui.SelectionPoint{Line: line, Col: 27}) {
		t.Errorf("held motion after a double-click ended at %+v, want col 27", p.selection.End)
	}

	p.handleMouse(tea.MouseReleaseMsg(tea.Mouse{X: 67, Y: 6, Button: tea.MouseLeft}))
	if !p.selection.HasSelection() || p.selection.Active {
		t.Errorf("release left the word selection at %+v..%+v (active=%v)",
			p.selection.Start, p.selection.End, p.selection.Active)
	}
	if p.viewMode == ViewModeInteractive {
		t.Error("finishing a word drag activated the terminal")
	}
}

// newMouseReportingTestPlugin puts a live terminal on screen whose app has
// enabled mouse tracking — Claude Code, grok — as the first synced frame does.
func newMouseReportingTestPlugin() *Plugin {
	p := newPreviewClickTestPlugin()
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row here\n", 50))
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{
		Active:        true,
		TargetSession: "shell-1",
		TargetPane:    "%1",
	}
	attachLiveTerminal(p, true)
	return p
}

// Forwarding the press on mouse-down meant a mouse-reporting app swallowed the
// gesture and the pane could never be selected again. Motion selects locally.
func TestMouseReportingPaneDragSelectsLocallyAndForwardsNothing(t *testing.T) {
	p := newMouseReportingTestPlugin()

	if cmd := p.handleMouseClick(previewClickAction(false, false)); cmd != nil {
		t.Fatal("mouse-down over a mouse-reporting pane forwarded before the gesture resolved")
	}
	p.handleMouseDrag(mouse.MouseAction{
		Type: mouse.ActionDrag, X: 66, Y: 8, DragStartID: regionPreviewPane,
		Region: previewClickAction(false, false).Region,
	})
	cmd := p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane})

	if !p.selection.HasSelection() {
		t.Fatal("drag over a mouse-reporting pane produced no selection")
	}
	if cmd != nil {
		t.Error("a completed selection still forwarded the click to the app")
	}
	if p.pointer.Resolution != tty.ClickNone {
		t.Errorf("pendingClickResolution = %v after a drag, want none", p.pointer.Resolution)
	}
}

// A click that never moves still belongs to the app.
func TestMouseReportingPaneClickWithoutMotionForwards(t *testing.T) {
	p := newMouseReportingTestPlugin()

	p.handleMouseClick(previewClickAction(false, false))
	if p.pointer.Resolution != tty.ClickForward {
		t.Fatalf("pendingClickResolution = %v, want forward", p.pointer.Resolution)
	}
	if cmd := p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane}); cmd == nil {
		t.Fatal("a click without motion did not forward to the app")
	}
	if p.selection.HasSelection() {
		t.Error("a forwarded click left a selection behind")
	}
	if p.pointer.Resolution != tty.ClickNone {
		t.Error("the forwarded click stayed pending")
	}
}

// The reported symptom: the first selection worked (the flag was still false
// before the first frame synced), every later one did nothing.
func TestSecondSelectionWorksOverMouseReportingPane(t *testing.T) {
	p := newMouseReportingTestPlugin()
	p.interactiveState.MouseReportingEnabled = false // pre-sync, as a fresh state is

	p.handleMouseClick(previewClickAction(false, false))
	p.handleMouseDrag(mouse.MouseAction{
		Type: mouse.ActionDrag, X: 66, Y: 8, DragStartID: regionPreviewPane,
		Region: previewClickAction(false, false).Region,
	})
	p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane})
	if !p.selection.HasSelection() {
		t.Fatal("the first selection did not work")
	}
	first := p.selection.End

	// syncTerminalModel copies the app's mouse tracking in from the frame.
	p.interactiveState.MouseReportingEnabled = true

	p.handleMouseClick(previewClickAction(false, false))
	p.handleMouseDrag(mouse.MouseAction{
		Type: mouse.ActionDrag, X: 70, Y: 9, DragStartID: regionPreviewPane,
		Region: previewClickAction(false, false).Region,
	})
	p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane})
	if !p.selection.HasSelection() {
		t.Fatal("the second selection over a mouse-reporting pane did nothing")
	}
	if p.selection.End == first {
		t.Errorf("the second selection did not move: still %+v", p.selection.End)
	}
}

func TestMouseReportingPaneDoubleClickStillSelectsWords(t *testing.T) {
	p := newMouseReportingTestPlugin()

	p.handleMouseClick(previewClickAction(false, false))
	p.handleMouseDoubleClick(terminalMultiClickAt(mouse.ActionDoubleClick, 11, 6))
	line := p.selection.Start.Line
	if p.selection.Start.Col != 11 || p.selection.End.Col != 18 {
		t.Fatalf("double-click over a mouse-reporting pane selected %+v..%+v, want the word terminal",
			p.selection.Start, p.selection.End)
	}
	if p.pointer.Resolution != tty.ClickNone {
		t.Error("the double-click left the app's click pending")
	}

	terminalDragTo(p, 25+42, 6)
	if p.selection.End != (ui.SelectionPoint{Line: line, Col: 27}) {
		t.Errorf("word drag over a mouse-reporting pane ended at %+v, want col 27", p.selection.End)
	}
	if cmd := p.handleMouseDragEnd(mouse.MouseAction{DragStartID: regionPreviewPane}); cmd != nil {
		t.Error("finishing a word drag forwarded the click to the app")
	}
}
