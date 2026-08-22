package app

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/scroll"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// --- helpers ---------------------------------------------------------------

func switcherClick(x, y int) tea.MouseClickMsg {
	return tea.MouseClickMsg{X: x, Y: y, Button: tea.MouseLeft}
}

// switcherDragMotion is left-button motion; button-less motion means a lost
// release to the mouse handler.
func switcherDragMotion(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func switcherBareMotion(x, y int) tea.MouseMotionMsg {
	return tea.MouseMotionMsg{X: x, Y: y}
}

func switcherRelease(x, y int) tea.MouseReleaseMsg {
	return tea.MouseReleaseMsg{X: x, Y: y, Button: tea.MouseLeft}
}

func barRegion(t *testing.T, h *mouse.Handler, id string) mouse.Region {
	t.Helper()
	for _, r := range h.HitMap.Regions() {
		if r.ID == id {
			return r
		}
	}
	t.Fatalf("no %q region registered", id)
	return mouse.Region{}
}

// sectionBarRegion returns the section-declared bar's region: sections
// register after the framework viewport bar, so the last match is theirs.
func sectionBarRegion(t *testing.T, h *mouse.Handler, id string) mouse.Region {
	t.Helper()
	for i := len(h.HitMap.Regions()) - 1; i >= 0; i-- {
		if r := h.HitMap.Regions()[i]; r.ID == id {
			return r
		}
	}
	t.Fatalf("no %q region registered", id)
	return mouse.Region{}
}

func manyProjects(n int) []config.ProjectConfig {
	projects := make([]config.ProjectConfig, n)
	for i := range projects {
		projects[i] = config.ProjectConfig{Name: fmt.Sprintf("proj-%02d", i), Path: fmt.Sprintf("/tmp/proj-%02d", i)}
	}
	return projects
}

func projectSwitcherModel(t *testing.T, n int) Model {
	t.Helper()
	m := routerTestModel(t, newRouterPlugin())
	m.cfg.Projects.List = manyProjects(n)
	m.ui.WorkDir = "/tmp/proj-00"
	m.showProjectSwitcher = true
	m.initProjectSwitcher()
	if m.activeModal() != ModalProjectSwitcher {
		t.Fatalf("activeModal() = %v, want ModalProjectSwitcher", m.activeModal())
	}
	m.renderProjectSwitcherOverlay("bg") // registers regions into the handler
	if m.projectSwitcherMouseHandler == nil {
		t.Fatal("switcher render produced no mouse handler")
	}
	return m
}

// --- project switcher -------------------------------------------------------

// TestProjectSwitcherScrollbarDragThroughActiveModalDispatch is the plan's open
// question proven at the app level: update.go routes every mouse event for an
// open modal through activeModal() into handleProjectSwitcherMouse, and the
// scrollbar regions the modal registered are reachable there — a full gesture
// driven exclusively through that entry point moves the list.
func TestProjectSwitcherScrollbarDragThroughActiveModalDispatch(t *testing.T) {
	m := projectSwitcherModel(t, 20)
	h := m.projectSwitcherMouseHandler
	track := sectionBarRegion(t, h, ui.RegionScrollbarTrack)
	thumb := sectionBarRegion(t, h, ui.RegionScrollbarThumb)

	params := ui.ScrollbarParams{
		TotalItems:   len(m.projectSwitcherFiltered),
		ScrollOffset: m.projectSwitcherScroll,
		VisibleItems: projectSwitcherMaxVisible,
		TrackHeight:  projectSwitcherMaxVisible,
	}

	grabY := thumb.Rect.Y + thumb.Rect.H/2
	if _, cmd := m.handleProjectSwitcherMouse(switcherClick(thumb.Rect.X, grabY)); cmd != nil {
		t.Fatal("thumb press produced an unexpected command")
	}
	if !h.IsDragging() || h.DragRegion() != ui.RegionScrollbarThumb {
		t.Fatalf("press did not start a thumb drag: dragging=%v region=%q", h.IsDragging(), h.DragRegion())
	}

	dragY := grabY + 4
	want := scroll.OffsetAtRow(params.TotalItems, params.VisibleItems, params.TrackHeight, dragY-track.Rect.Y-(grabY-track.Rect.Y))
	if _, _ = m.handleProjectSwitcherMouse(switcherDragMotion(thumb.Rect.X, dragY)); m.projectSwitcherScroll != want {
		t.Errorf("after drag to row %d: scroll = %d, want %d", dragY, m.projectSwitcherScroll, want)
	}

	// The gesture marks itself moved while it is live...
	if !m.projectSwitcherBar.moved {
		t.Error("a moving gesture must mark itself moved")
	}
	// ...and the cursor stays inside the visible window so Enter still acts on
	// a row the viewport shows.
	if m.projectSwitcherCursor < m.projectSwitcherScroll ||
		m.projectSwitcherCursor >= m.projectSwitcherScroll+projectSwitcherMaxVisible {
		t.Errorf("cursor %d outside window [%d,%d)", m.projectSwitcherCursor, m.projectSwitcherScroll, m.projectSwitcherScroll+projectSwitcherMaxVisible)
	}

	if _, _ = m.handleProjectSwitcherMouse(switcherRelease(0, 0)); h.IsDragging() {
		t.Error("drag live after release")
	}
	if m.projectSwitcherBar.active || m.projectSwitcherBar.moved {
		t.Error("release must settle the gesture and consume its moved flag")
	}
}

func TestProjectSwitcherTrackClickAnchorsAndContinues(t *testing.T) {
	m := projectSwitcherModel(t, 20)
	h := m.projectSwitcherMouseHandler
	track := sectionBarRegion(t, h, ui.RegionScrollbarTrack)
	thumb := sectionBarRegion(t, h, ui.RegionScrollbarThumb)

	clickRow := thumb.Rect.Y + thumb.Rect.H + 2
	m.handleProjectSwitcherMouse(switcherClick(track.Rect.X, clickRow))

	want := scroll.OffsetAtRow(len(m.projectSwitcherFiltered), projectSwitcherMaxVisible, projectSwitcherMaxVisible, clickRow-track.Rect.Y)
	if m.projectSwitcherScroll != want {
		t.Fatalf("track click must jump so the grabbed point anchors: scroll = %d, want %d", m.projectSwitcherScroll, want)
	}
	if !h.IsDragging() {
		t.Fatal("track click continues as a drag")
	}

	further := clickRow + 3
	m.handleProjectSwitcherMouse(switcherDragMotion(track.Rect.X, further))
	want = scroll.OffsetAtRow(len(m.projectSwitcherFiltered), projectSwitcherMaxVisible, projectSwitcherMaxVisible, further-track.Rect.Y)
	if m.projectSwitcherScroll != want {
		t.Errorf("post-jump drag: scroll = %d, want %d", m.projectSwitcherScroll, want)
	}
}

func TestSwitcherBarInertWhenContentFits(t *testing.T) {
	m := projectSwitcherModel(t, 5) // five projects fit the eight-row window
	for _, r := range m.projectSwitcherMouseHandler.HitMap.Regions() {
		if r.ID == ui.RegionScrollbarThumb || r.ID == ui.RegionScrollbarTrack {
			t.Fatalf("fitting content registered %q", r.ID)
		}
	}
	scrollBefore := m.projectSwitcherScroll
	body := barRegion(t, m.projectSwitcherMouseHandler, "modal-body")
	m.projectSwitcherModal.HandleMouse(
		switcherClick(body.Rect.X+body.Rect.W-1, body.Rect.Y+1), m.projectSwitcherMouseHandler)
	if m.projectSwitcherScroll != scrollBefore {
		t.Errorf("click in the bare column scrolled %d -> %d", scrollBefore, m.projectSwitcherScroll)
	}
	if m.projectSwitcherMouseHandler.IsDragging() {
		t.Error("click where no bar exists started a drag")
	}
}

// --- rule 8 guard -----------------------------------------------------------

// TestOpeningSubModalCancelsPreexistingBarDrag covers the modal guard from the
// switcher's side: a drag started on the bar goes dead when another surface
// takes the pointer (project-add opens over the switcher); the stray motions
// and release that follow move nothing, and the gesture settles cleanly.
func TestOpeningSubModalCancelsPreexistingBarDrag(t *testing.T) {
	m := projectSwitcherModel(t, 20)
	h := m.projectSwitcherMouseHandler
	thumb := barRegion(t, h, ui.RegionScrollbarThumb)

	m.handleProjectSwitcherMouse(switcherClick(thumb.Rect.X, thumb.Rect.Y+1))
	m.handleProjectSwitcherMouse(switcherDragMotion(thumb.Rect.X, thumb.Rect.Y+3))
	scrolledTo := m.projectSwitcherScroll
	if scrolledTo == 0 {
		t.Fatal("precondition: drag moved the list")
	}

	// Opening the add-project sub-flow routes the pointer elsewhere (the same
	// boundary any modal-open crosses).
	m.initProjectAdd()

	// Stray events from the abandoned gesture arrive through the switcher's
	// old path; they must not advance the list or resurrect the drag.
	m.handleProjectSwitcherMouse(switcherRelease(thumb.Rect.X, thumb.Rect.Y+9))
	if m.projectSwitcherScroll != scrolledTo {
		t.Errorf("stray release moved scroll %d -> %d", scrolledTo, m.projectSwitcherScroll)
	}

	// Closing the sub-modal discards the dead gesture state entirely.
	m.clearProjectAddModal()
	if m.projectSwitcherBar.active {
		t.Error("bar gesture survived the modal boundary it should have died at")
	}
}

// TestClosingMidGestureSettlesCleanly proves the other half of the guard: a
// drag running when the modal closes ends with it — no live handler state and
// no anchor leaks past clear.
func TestClosingMidGestureSettlesCleanly(t *testing.T) {
	m := projectSwitcherModel(t, 20)
	h := m.projectSwitcherMouseHandler
	thumb := barRegion(t, h, ui.RegionScrollbarThumb)

	m.handleProjectSwitcherMouse(switcherClick(thumb.Rect.X, thumb.Rect.Y))
	if !h.IsDragging() {
		t.Fatal("precondition: drag live")
	}

	m.resetProjectSwitcher() // what Esc runs

	if h.IsDragging() {
		t.Error("handler drag survived the modal closing under it")
	}
	if m.projectSwitcherBar.active || m.projectSwitcherBar.grabDelta != 0 {
		t.Errorf("bar state leaked past close: %+v", m.projectSwitcherBar)
	}
}

// --- theme switcher ---------------------------------------------------------

// TestThemeSwitcherDragMovesSilentlyThenPreviewsOnce pins the "one gesture one
// recolour" precedent: selection follows the drag without previewing per
// motion event (nil command while the gesture is live); the single preview
// command appears at release.
func TestThemeSwitcherDragMovesSilentlyThenPreviewsOnce(t *testing.T) {
	m := routerTestModel(t, newRouterPlugin())
	m.ui.WorkDir = "/tmp/proj-00"
	m.showThemeSwitcher = true
	m.initThemeSwitcher()
	m.renderThemeSwitcherModal("bg")

	h := m.themeSwitcherMouseHandler
	thumb := sectionBarRegion(t, h, ui.RegionScrollbarThumb)
	startSel := m.themeSwitcherSelectedIdx
	before := styles.GetCurrentTheme().Name

	_, _ = m.handleThemeSwitcherMouse(switcherClick(thumb.Rect.X, thumb.Rect.Y))
	if got := styles.GetCurrentTheme().Name; got != before {
		t.Fatalf("thumb press recoloured: %q -> %q", before, got)
	}

	_, _ = m.handleThemeSwitcherMouse(switcherDragMotion(thumb.Rect.X, thumb.Rect.Y+6))
	if m.themeSwitcherSelectedIdx == startSel {
		t.Fatal("dragging down did not move the selection")
	}
	if got := styles.GetCurrentTheme().Name; got != before {
		t.Fatalf("drag motion recoloured mid-gesture: %q -> %q (one gesture one recolour)", before, got)
	}

	_, _ = m.handleThemeSwitcherMouse(switcherRelease(thumb.Rect.X, thumb.Rect.Y+6))
	if got := styles.GetCurrentTheme().Name; got == before {
		t.Errorf("release owed exactly one recolour, theme stayed %q", before)
	}
	if m.themeSwitcherBar.active || h.IsDragging() {
		t.Error("gesture not settled after release")
	}
}

// --- worktree switcher ------------------------------------------------------

func TestWorktreeSwitcherScrollbarDrag(t *testing.T) {
	orig := listWorktreesForSwitcher
	listWorktreesForSwitcher = func(string) []WorktreeInfo {
		wts := make([]WorktreeInfo, 12)
		for i := range wts {
			wts[i] = WorktreeInfo{Path: fmt.Sprintf("/tmp/repo/wt-%02d", i), Branch: fmt.Sprintf("wt-%02d", i), IsMain: i == 0}
		}
		return wts
	}
	defer func() { listWorktreesForSwitcher = orig }()

	m := routerTestModel(t, newRouterPlugin())
	m.ui.WorkDir = "/tmp/repo"
	m.showWorktreeSwitcher = true
	m.initWorktreeSwitcher()
	m.renderWorktreeSwitcherModal("bg")

	h := m.worktreeSwitcherMouseHandler
	track := sectionBarRegion(t, h, ui.RegionScrollbarTrack)
	thumb := sectionBarRegion(t, h, ui.RegionScrollbarThumb)

	grabY := thumb.Rect.Y + thumb.Rect.H/2
	m.handleWorktreeSwitcherMouse(switcherClick(thumb.Rect.X, grabY))
	if !h.IsDragging() {
		t.Fatal("worktree thumb press did not start a drag")
	}
	m.handleWorktreeSwitcherMouse(switcherDragMotion(thumb.Rect.X, track.Rect.Y+track.Rect.H-1))

	maxOff := len(m.worktreeSwitcherFiltered) - 8
	if m.worktreeSwitcherScroll != maxOff {
		t.Errorf("dragging to the bottom of the track: scroll = %d, want %d", m.worktreeSwitcherScroll, maxOff)
	}
	if m.worktreeSwitcherCursor < m.worktreeSwitcherScroll ||
		m.worktreeSwitcherCursor >= m.worktreeSwitcherScroll+8 {
		t.Errorf("cursor %d outside window [%d,%d)", m.worktreeSwitcherCursor, m.worktreeSwitcherScroll, m.worktreeSwitcherScroll+8)
	}

	m.handleWorktreeSwitcherMouse(switcherRelease(0, 0))
	if h.IsDragging() || m.worktreeSwitcherBar.active {
		t.Error("gesture not settled after release")
	}
}
