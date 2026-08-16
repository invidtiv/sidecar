package overview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/paneframe"
	"github.com/marcus/sidecar/internal/panelayout"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/workspaceinventory"
)

// The global Workspaces browser draws panes the way the project workspace does.
// That is not a coincidence to be re-checked by eye on each surface: both drive
// internal/paneframe, and these tests measure the properties that would break
// first if one of them grew a private path again.

// TestGlobalOneLeafInnerMatchesTheInsetPeer is the invariant every geometry
// consumer on this surface depends on: the lone terminal's content box is the
// peer minus exactly one panel's chrome.
func TestGlobalOneLeafInnerMatchesTheInsetPeer(t *testing.T) {
	for _, width := range []int{90, 120} {
		for _, sidebar := range []bool{true, false} {
			m := linkPreviewModel(t, workspaceinventory.KindWorktree)
			m.sidebarVisible = sidebar
			m.WorkspacesView(width, previewTall)

			peer, ok := m.previewPeerBox()
			if !ok {
				t.Fatalf("no peer box at width %d sidebar=%v", width, sidebar)
			}
			want := paneframe.Inset(peer)
			got, ok := m.previewBox()
			if !ok || got != want {
				t.Fatalf("previewBox = %+v, want inset(peer) %+v", got, want)
			}
			term, ok := m.previewTerminalBox()
			if !ok || term != want {
				t.Fatalf("previewTerminalBox = %+v, want 1-leaf inner %+v", term, want)
			}
		}
	}
}

// TestGlobalTwoLeafLeavesWearTheirOwnBorders is the visible half of parity: with
// a second pane open the single outer frame dissolves and each leaf draws a
// complete perimeter of its own, separated by the shared one-cell drag handle.
func TestGlobalTwoLeafLeavesWearTheirOwnBorders(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	if m.preview.doc == nil {
		t.Fatal("document pane did not open")
	}
	m.WorkspacesView(previewWide, previewTall)

	peer, ok := m.previewPeerBox()
	if !ok {
		t.Fatal("no preview peer")
	}
	layout, laid := m.layoutPreviewPanes(peer)
	if !laid || layout.Zoomed || len(layout.Leaves) != 2 || len(layout.Dividers) != 1 {
		t.Fatalf("2-leaf layout = %+v laid=%v", layout, laid)
	}

	rows := strings.Split(ansi.Strip(m.WorkspacesView(previewWide, previewTall)), "\n")
	for _, placement := range layout.Leaves {
		geom := paneframe.Geometry(placement.Box)
		if geom.Inner.W != geom.Outer.W-paneframe.Overhead || geom.Inner.H != geom.Outer.H-paneframe.BorderWidth {
			t.Fatalf("inner %+v is not outer %+v minus %d×%d",
				geom.Inner, geom.Outer, paneframe.Overhead, paneframe.BorderWidth)
		}
		assertGlobalLeafHasCompletePanel(t, rows, geom.Outer)
	}
}

// assertGlobalLeafHasCompletePanel checks the four corners of one leaf's OUTER
// box are box-drawing characters. A leaf sharing an outer frame with its
// neighbour — the arrangement this work removed — has no corners of its own.
func assertGlobalLeafHasCompletePanel(t *testing.T, rows []string, outer paneframe.Box) {
	t.Helper()
	corners := []struct {
		x, y int
		name string
	}{
		{outer.X, outer.Y, "top-left"},
		{outer.X + outer.W - 1, outer.Y, "top-right"},
		{outer.X, outer.Y + outer.H - 1, "bottom-left"},
		{outer.X + outer.W - 1, outer.Y + outer.H - 1, "bottom-right"},
	}
	for _, corner := range corners {
		if corner.y < 0 || corner.y >= len(rows) {
			t.Fatalf("%s corner of %+v is off screen", corner.name, outer)
		}
		runes := []rune(rows[corner.y])
		if corner.x < 0 || corner.x >= len(runes) {
			t.Fatalf("%s corner of %+v is off the row", corner.name, outer)
		}
		if !strings.ContainsRune("╭╮╰╯┌┐└┘╔╗╚╝", runes[corner.x]) {
			t.Fatalf("%s corner of leaf %+v is %q, not a panel corner", corner.name, outer, runes[corner.x])
		}
	}
}

// TestGlobalLeafChromeReadsFocusLikeTheProjectSurface pins the border states to
// the shared frame's, so "which pane looks focused" cannot answer differently
// here than in the project workspace.
func TestGlobalLeafChromeReadsFocusLikeTheProjectSurface(t *testing.T) {
	m := linkPreviewModel(t, workspaceinventory.KindWorktree)
	run(t, m, m.openPreviewDoc(mustPreviewSpan(t, m, previewNeedleAction(t, m, "README.md"))))
	doc := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Document)
	term := panelayout.FirstOfKind(m.preview.paneRoot, panelayout.Terminal)
	if doc == nil || term == nil {
		t.Fatalf("tree lacks a doc or terminal leaf: %+v", m.preview.paneRoot)
	}

	m.preview.focus = focusPreview
	m.preview.paneFocus = doc.ID
	host := paneHost{m}
	if got := host.Chrome(doc); got != paneframe.ChromeActive {
		t.Fatalf("focused doc leaf chrome = %v, want active", got)
	}
	if got := host.Chrome(term); got != paneframe.ChromeIdle {
		t.Fatalf("unfocused terminal leaf chrome = %v, want idle", got)
	}
	if got := paneframe.WrapLeaf("body", paneframe.Box{W: 40, H: 10}, host.Chrome(doc)); got != styles.RenderPanel("body", 40, 10, true) {
		t.Fatal("the focused leaf did not draw the shared active panel")
	}
	if got := paneframe.WrapLeaf("body", paneframe.Box{W: 40, H: 10}, host.Chrome(term)); got != styles.RenderPanel("body", 40, 10, false) {
		t.Fatal("the unfocused leaf did not draw the shared muted panel")
	}
}
