package app

import (
	"strconv"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/keymap"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/scroll/scrolltest"
)

// --- helpers ---------------------------------------------------------------

func wheelAt(x, y int, down bool) tea.MouseWheelMsg {
	btn := tea.MouseWheelUp
	if down {
		btn = tea.MouseWheelDown
	}
	return tea.MouseWheelMsg{X: x, Y: y, Button: btn}
}

func longLines(n int) string {
	lines := make([]string, n)
	for i := range lines {
		lines[i] = "line " + strconv.Itoa(i)
	}
	return strings.Join(lines, "\n")
}

// modalBodyPoint returns a rendered point over modal-body that no control covers.
func modalBodyPoint(t *testing.T, h *mouse.Handler) (int, int) {
	t.Helper()
	for _, r := range h.HitMap.Regions() {
		if r.ID != "modal-body" {
			continue
		}
		x := r.Rect.X + r.Rect.W/2
		for y := r.Rect.Y; y < r.Rect.Y+r.Rect.H; y++ {
			if hit := h.HitMap.Test(x, y); hit != nil && hit.ID == "modal-body" {
				return x, y
			}
		}
	}
	t.Fatalf("no free modal-body point found")
	return 0, 0
}

// renderedModal builds and renders a scrollable declarative modal plus its handler.
func renderedModal(w, h int, content string) (*modal.Modal, *mouse.Handler) {
	md := modal.New("Test").AddSection(modal.Text(content))
	handler := mouse.NewHandler()
	md.Render(w, h, handler)
	return md, handler
}

func boundaryModel(t *testing.T) Model {
	t.Helper()
	return routerTestModel(t, &wheelBoundaryPlugin{atBoundary: false, lastY: -1})
}

// --- the ledger: one row per ModalKind -------------------------------------

// modalKindLedger asserts every ModalKind has an explicit, tested answer. Each
// row opens exactly one overlay and reports the point the wheel lands on.
func TestActiveModalWheelAtBoundaryLedger(t *testing.T) {
	type want struct {
		up, down bool
	}
	tests := []struct {
		name string
		// setup opens the overlay and returns the pointer position to use.
		setup func(t *testing.T, m *Model) (int, int)
		want  want
	}{
		{
			name: "palette cursor at the top of the filtered list",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showPalette = true
				return openPalette(t, m)
			},
			want: want{up: true, down: false},
		},
		{
			name: "palette wheel outside the modal is absorbed",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showPalette = true
				openPalette(t, m)
				return 0, 0
			},
			want: want{up: true, down: true},
		},
		{
			name: "help absorbs every wheel event",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showHelp = true
				return 10, 10
			},
			want: want{up: true, down: true},
		},
		{
			name: "update preview at top",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.updateModalState = UpdateModalPreview
				md, h := renderedModal(m.width, m.height, longLines(200))
				m.updatePreviewModal, m.updatePreviewMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: false},
		},
		{
			name: "update preview at bottom",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.updateModalState = UpdateModalPreview
				md, h := renderedModal(m.width, m.height, longLines(200))
				md.ScrollToBottom()
				md.Render(m.width, m.height, h)
				m.updatePreviewModal, m.updatePreviewMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: false, down: true},
		},
		{
			name: "update preview mid-content is movable both ways",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.updateModalState = UpdateModalPreview
				md, h := renderedModal(m.width, m.height, longLines(200))
				md.ScrollBy(3)
				md.Render(m.width, m.height, h)
				m.updatePreviewModal, m.updatePreviewMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: false, down: false},
		},
		{
			name: "update preview with short content is bounded both ways",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.updateModalState = UpdateModalPreview
				md, h := renderedModal(m.width, m.height, "all done")
				m.updatePreviewModal, m.updatePreviewMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: true},
		},
		{
			name: "update preview before its first render is unknown",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.updateModalState = UpdateModalPreview
				return 10, 10
			},
			want: want{up: false, down: false},
		},
		{
			name: "update complete dialog",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.updateModalState = UpdateModalComplete
				md, h := renderedModal(m.width, m.height, "restart required")
				m.updateCompleteModal, m.updateCompleteMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: true},
		},
		{
			name: "update error dialog",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.updateModalState = UpdateModalError
				md, h := renderedModal(m.width, m.height, "it failed")
				m.updateErrorModal, m.updateErrorMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: true},
		},
		{
			name: "diagnostics at top",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showDiagnostics = true
				md, h := renderedModal(m.width, m.height, longLines(200))
				m.diagnosticsModal, m.diagnosticsMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: false},
		},
		{
			name: "diagnostics backdrop is absorbed",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showDiagnostics = true
				md, h := renderedModal(m.width, m.height, longLines(200))
				m.diagnosticsModal, m.diagnosticsMouseHandler = md, h
				return 0, 0
			},
			want: want{up: true, down: true},
		},
		{
			name: "quit confirm is bounded in both directions",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showQuitConfirm = true
				md, h := renderedModal(m.width, m.height, "Quit sidecar?")
				m.quitModal, m.quitMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: true},
		},
		{
			name: "project switcher cursor at top",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showProjectSwitcher = true
				m.projectSwitcherFiltered = make([]projectSwitcherDestination, 5)
				m.projectSwitcherCursor = 0
				return 10, 10
			},
			want: want{up: true, down: false},
		},
		{
			name: "project switcher cursor at bottom",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showProjectSwitcher = true
				m.projectSwitcherFiltered = make([]projectSwitcherDestination, 5)
				m.projectSwitcherCursor = 4
				m.projectSwitcherScroll = 0
				return 10, 10
			},
			want: want{up: false, down: true},
		},
		{
			name: "project switcher cursor in the middle",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showProjectSwitcher = true
				m.projectSwitcherFiltered = make([]projectSwitcherDestination, 5)
				m.projectSwitcherCursor = 2
				return 10, 10
			},
			want: want{up: false, down: false},
		},
		{
			name: "project switcher with an empty filter result",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showProjectSwitcher = true
				m.projectSwitcherFiltered = nil
				m.projectSwitcherCursor = 0
				return 10, 10
			},
			want: want{up: true, down: true},
		},
		{
			name: "project switcher with a stale list offset is movable",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showProjectSwitcher = true
				m.projectSwitcherFiltered = make([]projectSwitcherDestination, 5)
				m.projectSwitcherCursor = 0
				m.projectSwitcherScroll = 3 // ensureCursorVisible would move it
				return 10, 10
			},
			want: want{up: false, down: false},
		},
		{
			name: "project add sub-flow answers from its own modal",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showProjectSwitcher = true
				m.projectAddMode = true
				md, h := renderedModal(m.width, m.height, "Name / Path")
				m.projectAddModal, m.projectAddMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: true},
		},
		{
			name: "project add theme picker has no mouse handling",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showProjectSwitcher = true
				m.projectAddMode = true
				m.projectAddThemeMode = true
				return 10, 10
			},
			want: want{up: true, down: true},
		},
		{
			name: "worktree switcher body at top",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showWorktreeSwitcher = true
				md, h := renderedModal(m.width, m.height, longLines(200))
				m.worktreeSwitcherModal, m.worktreeSwitcherMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: false},
		},
		{
			name: "worktree switcher with a short list",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showWorktreeSwitcher = true
				md, h := renderedModal(m.width, m.height, "main")
				m.worktreeSwitcherModal, m.worktreeSwitcherMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: true},
		},
		{
			name: "theme switcher body at top",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showThemeSwitcher = true
				md, h := renderedModal(m.width, m.height, longLines(200))
				m.themeSwitcherModal, m.themeSwitcherMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: false},
		},
		{
			name: "theme switcher with a filtered-empty list",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showThemeSwitcher = true
				m.themeSwitcherFiltered = nil
				md, h := renderedModal(m.width, m.height, "no themes match")
				m.themeSwitcherModal, m.themeSwitcherMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: true},
		},
		{
			name: "open in picker",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showOpenIn = true
				md, h := renderedModal(m.width, m.height, "Finder\nVS Code")
				m.openInModal, m.openInMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: true},
		},
		{
			name: "issue lookup with results that overflow",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showIssueInput = true
				md, h := renderedModal(m.width, m.height, longLines(200))
				m.issueInputModal, m.issueInputMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: false},
		},
		{
			name: "issue lookup with no results",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showIssueInput = true
				m.issueSearchResults = nil
				md, h := renderedModal(m.width, m.height, "No matches")
				m.issueInputModal, m.issueInputMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: true},
		},
		{
			name: "issue lookup rebuilt on a keystroke is unknown",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showIssueInput = true
				m.issueInputModal = nil
				return 10, 10
			},
			want: want{up: false, down: false},
		},
		{
			name: "issue preview empty card is bounded both ways",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showIssuePreview = true
				view := issueview.New(nil)
				view.SetSize(60, 20)
				m.issuePreviewView = view
				return 10, 10
			},
			want: want{up: true, down: true},
		},
		{
			name: "issue preview long card at top",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showIssuePreview = true
				m.issuePreviewView = longIssueView()
				return 10, 10
			},
			want: want{up: true, down: false},
		},
		{
			name: "issue preview long card at bottom",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showIssuePreview = true
				view := longIssueView()
				view.Scroll(100000)
				m.issuePreviewView = view
				return 10, 10
			},
			want: want{up: false, down: true},
		},
		{
			name: "issue preview with data but no card yet is unknown",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showIssuePreview = true
				m.issuePreviewData = &issueview.Data{ID: "td-1"}
				m.issuePreviewView = nil
				return 10, 10
			},
			want: want{up: false, down: false},
		},
		{
			name: "issue preview before any card falls back to the modal body",
			setup: func(t *testing.T, m *Model) (int, int) {
				m.showIssuePreview = true
				m.issuePreviewView = nil
				md, h := renderedModal(m.width, m.height, "Loading...")
				m.issuePreviewModal, m.issuePreviewMouseHandler = md, h
				return modalBodyPoint(t, h)
			},
			want: want{up: true, down: true},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := boundaryModel(t)
			x, y := tt.setup(t, &m)
			if m.activeModal() == ModalNone {
				t.Fatal("setup opened no modal")
			}
			if got := m.wheelAtBoundary(wheelAt(x, y, false)); got != tt.want.up {
				t.Errorf("wheel up boundary = %v, want %v", got, tt.want.up)
			}
			if got := m.wheelAtBoundary(wheelAt(x, y, true)); got != tt.want.down {
				t.Errorf("wheel down boundary = %v, want %v", got, tt.want.down)
			}
		})
	}
}

// openPalette opens the command palette with enough entries for the cursor to
// be movable, and returns a point inside the modal. Palette geometry is
// computed arithmetically rather than from a hit map, so no render is needed.
func openPalette(t *testing.T, m *Model) (int, int) {
	t.Helper()
	keymap.RegisterDefaults(m.keymap)
	m.palette.SetSize(m.width, m.height)
	m.palette.Open(m.keymap, m.surfacePlugins(), m.activeContext, "project")
	if got := len(m.palette.Filtered()); got < 2 {
		t.Fatalf("palette has %d entries, need at least 2 for a movable cursor", got)
	}
	return m.width / 2, m.height / 2
}

func longIssueView() *issueview.Model {
	view := issueview.New(nil)
	view.SetData(&issueview.Data{
		ID:          "td-longone",
		Title:       "A long issue",
		Description: longLines(200),
	})
	view.SetSize(60, 10)
	return view
}

// TestEveryModalKindHasALedgerRow keeps the ledger honest: a new ModalKind must
// arrive with a boundary policy and a row above.
func TestEveryModalKindHasALedgerRow(t *testing.T) {
	covered := map[ModalKind]string{
		ModalPalette:          "palette cursor at the top of the filtered list",
		ModalHelp:             "help absorbs every wheel event",
		ModalUpdate:           "update preview at top",
		ModalDiagnostics:      "diagnostics at top",
		ModalQuitConfirm:      "quit confirm is bounded in both directions",
		ModalProjectSwitcher:  "project switcher cursor at top",
		ModalWorktreeSwitcher: "worktree switcher body at top",
		ModalThemeSwitcher:    "theme switcher body at top",
		ModalOpenIn:           "open in picker",
		ModalIssueInput:       "issue lookup with results that overflow",
		ModalIssuePreview:     "issue preview long card at top",
	}
	for kind := ModalPalette; kind <= ModalIssuePreview; kind++ {
		if _, ok := covered[kind]; !ok {
			t.Errorf("ModalKind %d has no boundary ledger row", kind)
		}
	}
}

// --- nested precedence -----------------------------------------------------

func TestChangelogTakesPrecedenceOverTheUpdateDialog(t *testing.T) {
	m := boundaryModel(t)
	m.updateModalState = UpdateModalPreview
	// The dialog underneath would answer "bounded both ways".
	md, h := renderedModal(m.width, m.height, "short")
	m.updatePreviewModal, m.updatePreviewMouseHandler = md, h

	m.changelogVisible = true
	m.changelogScrollState = &changelogViewState{
		RenderedLines:   strings.Split(longLines(100), "\n"),
		MaxVisibleLines: 10,
	}

	// At the top: up bounded, down movable — the changelog, not the dialog.
	if !m.wheelAtBoundary(wheelAt(10, 10, false)) {
		t.Error("expected the changelog top to be bounded upward")
	}
	if m.wheelAtBoundary(wheelAt(10, 10, true)) {
		t.Error("expected the changelog to be movable downward, not the dialog's bounded answer")
	}

	// At the bottom: down bounded, the reverse event passes.
	m.changelogScrollOffset = 90
	if !m.wheelAtBoundary(wheelAt(10, 10, true)) {
		t.Error("expected the changelog bottom to be bounded downward")
	}
	if m.wheelAtBoundary(wheelAt(10, 10, false)) {
		t.Error("expected the reverse event to pass at the changelog bottom")
	}

	// Closing the changelog hands the answer back to the dialog underneath.
	m.changelogVisible = false
	if !m.wheelAtBoundary(wheelAt(modalBodyPointOf(t, h))) {
		t.Error("expected the short update dialog to answer bounded once the changelog closed")
	}
}

// modalBodyPointOf adapts modalBodyPoint for the wheelAt(x, y, down) signature.
func modalBodyPointOf(t *testing.T, h *mouse.Handler) (int, int, bool) {
	t.Helper()
	x, y := modalBodyPoint(t, h)
	return x, y, true
}

func TestChangelogWithoutRenderedStateIsUnknown(t *testing.T) {
	m := boundaryModel(t)
	m.updateModalState = UpdateModalPreview
	m.changelogVisible = true
	if m.wheelAtBoundary(wheelAt(10, 10, true)) || m.wheelAtBoundary(wheelAt(10, 10, false)) {
		t.Error("expected an unbuilt changelog to be unknown in both directions")
	}
}

func TestProjectAddTakesPrecedenceOverTheSwitcherCursor(t *testing.T) {
	m := boundaryModel(t)
	m.showProjectSwitcher = true
	// A mid-list cursor would answer "movable" for the switcher.
	m.projectSwitcherFiltered = make([]projectSwitcherDestination, 5)
	m.projectSwitcherCursor = 2

	m.projectAddMode = true
	md, h := renderedModal(m.width, m.height, "Name / Path")
	m.projectAddModal, m.projectAddMouseHandler = md, h
	x, y := modalBodyPoint(t, h)
	if !m.wheelAtBoundary(wheelAt(x, y, true)) {
		t.Error("expected the nested project-add modal to answer, not the switcher cursor")
	}
}

// --- no-op tails must do no work ------------------------------------------

func TestBoundaryWheelOverPickersDoesNoPreviewWork(t *testing.T) {
	m := boundaryModel(t)
	m.showProjectSwitcher = true
	m.projectSwitcherFiltered = make([]projectSwitcherDestination, 3)
	m.projectSwitcherCursor = 0
	m.projectSwitcherScroll = 0
	md, h := renderedModal(m.width, m.height, "projects")
	m.projectSwitcherModal, m.projectSwitcherMouseHandler = md, h

	if got := FilterInput(m, wheelAt(10, 10, false)); got != nil {
		t.Fatalf("boundary wheel over the project switcher survived the filter as %T", got)
	}
	if m.projectSwitcherModal != md {
		t.Error("boundary query rebuilt or cleared the project switcher modal")
	}
	if m.projectSwitcherCursor != 0 || m.projectSwitcherScroll != 0 {
		t.Error("boundary query moved the project switcher cursor")
	}

	// Theme switcher: a boundary tail must not re-preview a theme, which means
	// it must never reach Update at all.
	tm := boundaryModel(t)
	tm.showThemeSwitcher = true
	themeModal, themeHandler := renderedModal(tm.width, tm.height, "one theme")
	tm.themeSwitcherModal, tm.themeSwitcherMouseHandler = themeModal, themeHandler
	tm.themeSwitcherSelectedIdx = 0
	x, y := modalBodyPoint(t, themeHandler)
	if got := FilterInput(tm, wheelAt(x, y, true)); got != nil {
		t.Fatalf("boundary wheel over the theme switcher survived the filter as %T", got)
	}
	if tm.themeSwitcherModal != themeModal || tm.themeSwitcherSelectedIdx != 0 {
		t.Error("boundary query mutated theme switcher state")
	}
}

func TestBoundaryWheelDoesNotSynchronizeChangelogState(t *testing.T) {
	m := boundaryModel(t)
	m.updateModalState = UpdateModalPreview
	m.changelogVisible = true
	state := &changelogViewState{
		RenderedLines:   strings.Split(longLines(100), "\n"),
		MaxVisibleLines: 10,
		ScrollOffset:    0,
	}
	m.changelogScrollState = state
	m.changelogScrollOffset = 0

	if got := FilterInput(m, wheelAt(10, 10, false)); got != nil {
		t.Fatalf("boundary wheel at the changelog top survived the filter as %T", got)
	}
	if m.changelogScrollOffset != 0 || state.ScrollOffset != 0 {
		t.Error("boundary query mutated changelog scroll state")
	}
}

// --- inertial tail and reverse --------------------------------------------

// TestInertialTailIsDroppedAndReversePasses runs the shared stress fixture
// through the real app filter for each overlay class that can sit at a
// boundary. No sleeps: the fixture drives the pre-update answer directly.
func TestInertialTailIsDroppedAndReversePasses(t *testing.T) {
	tests := []struct {
		name  string
		setup func(t *testing.T, m *Model) (x, y int, down bool)
	}{
		{
			name: "issue preview at the bottom of a long card",
			setup: func(t *testing.T, m *Model) (int, int, bool) {
				m.showIssuePreview = true
				view := longIssueView()
				view.Scroll(100000)
				m.issuePreviewView = view
				return 10, 10, true
			},
		},
		{
			name: "diagnostics modal body at the top",
			setup: func(t *testing.T, m *Model) (int, int, bool) {
				m.showDiagnostics = true
				md, h := renderedModal(m.width, m.height, longLines(200))
				m.diagnosticsModal, m.diagnosticsMouseHandler = md, h
				x, y := modalBodyPoint(t, h)
				return x, y, false
			},
		},
		{
			name: "project switcher cursor at the top",
			setup: func(t *testing.T, m *Model) (int, int, bool) {
				m.showProjectSwitcher = true
				m.projectSwitcherFiltered = make([]projectSwitcherDestination, 5)
				m.projectSwitcherCursor = 0
				return 10, 10, false
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			m := boundaryModel(t)
			x, y, down := tt.setup(t, &m)
			scrolltest.Run(t, scrolltest.Tail{
				Name: tt.name,
				X:    x,
				Y:    y,
				Down: down,
				Dropped: func(msg tea.MouseWheelMsg) bool {
					return FilterInput(m, msg) == nil
				},
			})
		})
	}
}

// --- routing ---------------------------------------------------------------

func TestModalWheelUsesScreenCoordinates(t *testing.T) {
	m := boundaryModel(t)
	m.showDiagnostics = true
	md, h := renderedModal(m.width, m.height, longLines(200))
	m.diagnosticsModal, m.diagnosticsMouseHandler = md, h
	x, y := modalBodyPoint(t, h)

	// The body at the top is bounded upward in screen coordinates.
	if !m.wheelAtBoundary(wheelAt(x, y, false)) {
		t.Fatal("expected the modal body to answer in untranslated screen coordinates")
	}
	// The same point shifted by the header lands elsewhere, proving the app does
	// not apply the plugin translation to modal overlays.
	if m.wheelAtBoundary(wheelAt(x, y, true)) {
		t.Fatal("precondition: the body should be movable downward")
	}
	if !m.wheelAtBoundary(wheelAt(0, 0, true)) {
		t.Error("expected the backdrop corner to be absorbed")
	}

	// A control's screen rectangle must answer "absorbed" at its untranslated
	// position: an app that shifted modal coordinates by the header would hit a
	// different row entirely.
	ctl := modal.New("Ctl").AddSection(modal.Buttons(modal.Btn(" OK ", "ok"))).AddSection(modal.Text(longLines(200)))
	ch := mouse.NewHandler()
	ctl.Render(m.width, m.height, ch)
	m.diagnosticsModal, m.diagnosticsMouseHandler = ctl, ch
	var bx, by int
	for _, r := range ch.HitMap.Regions() {
		if r.ID == "ok" {
			bx, by = r.Rect.X+r.Rect.W/2, r.Rect.Y+r.Rect.H/2
		}
	}
	if by == 0 {
		t.Fatal("button region not registered")
	}
	if !m.wheelAtBoundary(wheelAt(bx, by, true)) {
		t.Error("expected the button's screen rectangle to absorb the wheel")
	}
}

func TestModalWheelNeverConsultsTheCoveredPlugin(t *testing.T) {
	for _, tc := range []struct {
		name  string
		setup func(m *Model)
	}{
		{"help", func(m *Model) { m.showHelp = true }},
		{"palette", func(m *Model) { m.showPalette = true }},
		{"project switcher", func(m *Model) {
			m.showProjectSwitcher = true
			m.projectSwitcherFiltered = make([]projectSwitcherDestination, 3)
		}},
		{"issue preview", func(m *Model) {
			m.showIssuePreview = true
			m.issuePreviewView = longIssueView()
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := &wheelBoundaryPlugin{atBoundary: true, lastY: -1}
			m := routerTestModel(t, p)
			tc.setup(&m)
			m.wheelAtBoundary(wheelAt(10, 10, true))
			if p.lastY != -1 {
				t.Fatalf("the covered plugin was consulted under a modal (Y=%d)", p.lastY)
			}
		})
	}
}

func TestGlobalTasksBranchAnswersAndIsCoveredByModals(t *testing.T) {
	hosted := &wheelBoundaryPlugin{atBoundary: true, lastY: -1}
	m := routerTestModel(t, &wheelBoundaryPlugin{atBoundary: false, lastY: -1})
	m.globalTasks = &globalTasksHost{plugin: hosted}
	m.scope = ScopeGlobal
	m.globalTab = GlobalTasks
	if !m.globalTasksFocused() {
		t.Fatal("precondition: global Tasks should be the visible surface")
	}

	wheel := tea.MouseWheelMsg{X: 5, Y: headerHeight + 4, Button: tea.MouseWheelDown}
	if got := FilterInput(m, wheel); got != nil {
		t.Fatal("expected the global Tasks boundary answer to drop the event")
	}
	if hosted.lastY != 4 {
		t.Fatalf("global Tasks wheel Y = %d, want header-adjusted 4", hosted.lastY)
	}

	// An app modal covers the global surface exactly as it covers a plugin.
	hosted.lastY = -1
	m.showHelp = true
	if got := FilterInput(m, wheel); got != nil {
		t.Fatal("expected the help overlay to absorb the event")
	}
	if hosted.lastY != -1 {
		t.Fatalf("global Tasks was consulted under a modal (Y=%d)", hosted.lastY)
	}
}
