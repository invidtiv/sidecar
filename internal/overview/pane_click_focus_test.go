package overview

import (
	"fmt"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/workspacediff"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// td-43db92. The focus ring and the keyboard target had diverged on this
// surface: a press in the terminal leaf never moved paneFocus, and a press out
// of it while the pane was live moved the ring without taking the keys back.
// Focus is now answered from the leaf's BOX in internal/paneframe, so these are
// the global browser's half of a property the project workspace holds too
// (internal/plugins/workspace/pane_click_focus_test.go).

const (
	focusWide, focusTall = 200, 44
)

// interactiveLinkModel is the link fixture with a pane the preview can actually
// be handed the keyboard for, which is what the second half of this bug needs.
func interactiveLinkModel(t *testing.T) *Model {
	t.Helper()
	terminal := newFakeTerminal("see README.md then review td-196c42\n")
	original := newPreviewTerminal
	newPreviewTerminal = func(config tty.Config, hooks tty.Hooks) previewTerminal {
		terminal.config, terminal.hooks = config, hooks
		return terminal
	}
	t.Cleanup(func() { newPreviewTerminal = original })
	return linkPreviewModel(t, workspaceinventory.KindWorktree)
}

// everyKindPreviewTree opens one leaf of every kind beside the terminal, so a
// kind that stops being click-to-focusable fails here rather than in a bug
// report. A kind added later has to be added here as well.
func everyKindPreviewTree(t *testing.T, m *Model) {
	t.Helper()
	run(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	run(t, m, m.openPreviewIssue("td-1111aa"))
	run(t, m, m.openPreviewDiff(workspacediff.WorkingTreeTarget()))
	m.WorkspacesView(focusWide, focusTall)
	for _, kind := range []panelayout.Kind{
		panelayout.Terminal, panelayout.Document, panelayout.Issue, panelayout.Diff,
	} {
		if panelayout.FirstOfKind(m.preview.paneRoot, kind) == nil {
			t.Fatalf("the tree has no %s leaf: %#v", kindName(kind), m.preview.paneRoot)
		}
	}
}

// kindName names a leaf kind for a subtest, since panelayout.Kind is an int.
func kindName(kind panelayout.Kind) string {
	switch kind {
	case panelayout.Terminal:
		return "terminal"
	case panelayout.Document:
		return "document"
	case panelayout.Issue:
		return "issue"
	case panelayout.Diff:
		return "diff"
	default:
		return fmt.Sprint(int(kind))
	}
}

// previewClickPoints is the middle of every placed leaf's box, keyed by kind.
func previewClickPoints(t *testing.T, m *Model) map[panelayout.Kind][2]int {
	t.Helper()
	layout, ok := paneHost{m}.Layout()
	if !ok || len(layout.Leaves) != 4 {
		t.Fatalf("layout = %+v ok=%v, want four placed leaves", layout, ok)
	}
	points := make(map[panelayout.Kind][2]int, len(layout.Leaves))
	for _, placement := range layout.Leaves {
		box := placement.Box
		points[placement.Node.Kind] = [2]int{box.X + box.W/2, box.Y + box.H/2}
	}
	return points
}

// With the preview holding the keyboard, a press inside a leaf moves the ring
// to that leaf, whatever kind it is. The terminal is the case that was broken:
// it owns no click-to-focus region, because its presses belong to the live pane
// and are forwarded to tmux.
func TestGlobalClickInsideAnyLeafFocusesThatLeaf(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	everyKindPreviewTree(t, m)
	points := previewClickPoints(t, m)

	if !m.PreviewFocused() {
		t.Fatal("premise: the preview holds the keyboard after opening panes into it")
	}
	order := []panelayout.Kind{
		panelayout.Terminal, panelayout.Document, panelayout.Terminal,
		panelayout.Issue, panelayout.Diff, panelayout.Terminal,
	}
	for step, kind := range order {
		leaf := panelayout.FirstOfKind(m.preview.paneRoot, kind)
		point := points[kind]
		run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: point[0], Y: point[1], Button: tea.MouseLeft}))
		if m.preview.paneFocus != leaf.ID {
			t.Fatalf("step %d: click at %v inside the %s leaf %d left focus on leaf %d",
				step, point, kindName(kind), leaf.ID, m.preview.paneFocus)
		}
		run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: point[0], Y: point[1], Button: tea.MouseLeft}))
		m.WorkspacesView(focusWide, focusTall)
	}
}

// Focusing a leaf means the KEYBOARD, not just the ring. The click-away rule
// that hands the arrow keys back to the list named doc and issue regions
// explicitly, so a press on a diff — a leaf kind added after it — moved the ring
// and then immediately gave the keyboard to the sidebar: the diff drew idle,
// the list drew focused, and j/k moved rows. Tab-cycling was unaffected, which
// is why this only ever looked like a mouse bug.
func TestGlobalClickInsideALeafKeepsTheKeyboardOnThePreview(t *testing.T) {
	stubPreviewTd(t)
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	everyKindPreviewTree(t, m)
	points := previewClickPoints(t, m)

	for _, kind := range []panelayout.Kind{panelayout.Document, panelayout.Issue, panelayout.Diff} {
		t.Run(kindName(kind), func(t *testing.T) {
			run(t, m, m.focusList())
			m.WorkspacesView(focusWide, focusTall)
			leaf := panelayout.FirstOfKind(m.preview.paneRoot, kind)
			point := points[kind]
			run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: point[0], Y: point[1], Button: tea.MouseLeft}))
			run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: point[0], Y: point[1], Button: tea.MouseLeft}))

			if m.preview.paneFocus != leaf.ID {
				t.Fatalf("click at %v left the ring on leaf %d, want the %s leaf %d",
					point, m.preview.paneFocus, kindName(kind), leaf.ID)
			}
			if !m.PreviewFocused() {
				t.Fatalf("click at %v drew the ring on the %s leaf but left the keyboard on the list",
					point, kindName(kind))
			}
			got := paneHost{m}.Chrome(leaf)
			if got != paneframe.ChromeActive {
				t.Fatalf("the clicked %s leaf draws %v, want active chrome", kindName(kind), got)
			}
			m.WorkspacesView(focusWide, focusTall)
		})
	}
}

// The other half of the divergence: a click out of a live pane has to take the
// keyboard with it. The ring moving while the pane kept receiving keys is what
// put a shortcut into the agent's CLI.
func TestGlobalClickOutOfALivePaneTakesTheKeyboardWithIt(t *testing.T) {
	stubPreviewTd(t)
	m := interactiveLinkModel(t)
	everyKindPreviewTree(t, m)
	points := previewClickPoints(t, m)

	for _, kind := range []panelayout.Kind{panelayout.Document, panelayout.Issue, panelayout.Diff} {
		t.Run(kindName(kind), func(t *testing.T) {
			run(t, m, m.enterPreviewInteractive())
			if !m.PreviewInteractive() {
				t.Skip("this fixture has no live pane to type into")
			}
			// Typing into the pane is the terminal leaf holding focus, whatever
			// the ring said before the handover.
			term := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal)
			if m.preview.paneFocus != term.ID {
				t.Fatalf("typing started with the ring on leaf %d, not the terminal %d",
					m.preview.paneFocus, term.ID)
			}

			leaf := panelayout.FirstOfKind(m.preview.paneRoot, kind)
			point := points[kind]
			run(t, m, m.WorkspacesMouse(tea.MouseClickMsg{X: point[0], Y: point[1], Button: tea.MouseLeft}))
			run(t, m, m.WorkspacesMouse(tea.MouseReleaseMsg{X: point[0], Y: point[1], Button: tea.MouseLeft}))

			if m.preview.paneFocus != leaf.ID {
				t.Fatalf("click at %v left focus on leaf %d, want the %v leaf %d",
					point, m.preview.paneFocus, kind, leaf.ID)
			}
			if m.PreviewInteractive() {
				t.Fatalf("the click drew the ring on the %s leaf but left the keys in the pane", kindName(kind))
			}
			m.WorkspacesView(focusWide, focusTall)
		})
	}
}

// With the sidebar hidden the preview OWNS the chrome while the list still owns
// the keyboard, so a terminal leaf that took the ring's leaf without taking the
// keys would draw an active border over a pane that receives nothing — j and k
// would move the list behind it. The ring is drawn from previewOwnsChrome, so
// that is what SetFocus defers on, not PreviewFocused.
//
// A drag-select is the gesture that reaches this state: it is the one press on
// the terminal that deliberately does NOT end up typing.
func TestGlobalDragSelectWithTheSidebarHiddenDoesNotLightAnUnfocusedRing(t *testing.T) {
	m, _, _ := interactiveModel(t)
	m.WorkspacesView(previewWide, previewTall)
	run(t, m, m.toggleWorkspaceSidebar())
	m.WorkspacesView(previewWide, previewTall)
	if m.sidebarVisible {
		t.Fatal("premise: the sidebar is hidden")
	}
	if !m.previewOwnsChrome() {
		t.Fatal("premise: a hidden sidebar leaves the preview drawing the focused chrome")
	}

	x, y := previewAt(t, m)
	pointerDown(t, m, x, y)
	dragTo(t, m, x+6, y)
	release(t, m, x+6, y)

	if m.PreviewInteractive() {
		t.Fatal("a drag activated the pane, so the selection it made is unreachable")
	}
	term := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal)
	if term == nil {
		t.Fatal("no terminal leaf")
	}
	// Whatever the ring says, it has to agree with the keyboard: either the
	// preview took both, or it took neither.
	ringOnTerminal := m.previewOwnsChrome() && m.preview.paneFocus == term.ID
	if ringOnTerminal && !m.PreviewFocused() {
		t.Fatalf("the terminal leaf draws %v while the list holds the keyboard",
			paneHost{m}.Chrome(term))
	}
}
