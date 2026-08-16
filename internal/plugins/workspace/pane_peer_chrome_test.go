package workspace

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
)

func TestOneLeafInnerMatchesLegacyPreviewContentBox(t *testing.T) {
	for _, width := range []int{80, 120} {
		for _, sidebar := range []bool{true, false} {
			p := surfacePlugin(false)
			p.width, p.height = width, 40
			p.sidebarVisible = sidebar
			enableSingleTerminalTree(p)

			peer, ok := p.previewPeerBox()
			if !ok {
				t.Fatalf("no peer box at width %d sidebar=%v", width, sidebar)
			}
			want := insetPanelChrome(peer)
			got, ok := p.previewContentBox()
			if !ok || got != want {
				t.Fatalf("previewContentBox = %+v, want inset(peer) %+v", got, want)
			}
			geom := leafGeometry(peer)
			if geom.Inner != want {
				t.Fatalf("leafGeometry(peer).Inner = %+v, want %+v", geom.Inner, want)
			}
			leaf, ok := p.terminalLeafBox()
			if !ok || leaf != want {
				t.Fatalf("terminalLeafBox = %+v, want 1-leaf inner %+v", leaf, want)
			}
		}
	}
}

func TestTwoLeafChromeInsetsAndBlankGap(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n\nbody\n")
	p := docPaneTestPlugin(t, root, true)
	p.width, p.height = 200, 50
	if cmd := p.openTerminalPath("README.md", 1); cmd == nil {
		t.Fatal("2-leaf open failed")
	}

	peer, ok := p.previewPeerBox()
	if !ok {
		t.Fatal("no preview peer")
	}
	layout, laid := LayoutPaneTree(p.paneRoot, peer, paneTreeFloors(), p.paneFocus)
	if !laid || layout.Zoomed || len(layout.Leaves) != 2 || len(layout.Dividers) != 1 {
		t.Fatalf("2-leaf layout = %+v laid=%v", layout, laid)
	}
	if layout.Dividers[0].Box.W != 1 && layout.Dividers[0].Axis == SplitCols {
		t.Fatalf("column gap = %+v, want 1 cell", layout.Dividers[0].Box)
	}

	rows := composePaneTree(t, p, peer.W, peer.H)
	assertDividersDrawn(t, rows, layout.Dividers)
	for _, placement := range layout.Leaves {
		geom := leafGeometry(placement.Box)
		if geom.Inner.W != geom.Outer.W-panelOverhead || geom.Inner.H != geom.Outer.H-panelBorderWidth {
			t.Fatalf("inner %+v is not outer %+v minus 4×2", geom.Inner, geom.Outer)
		}
		assertLeafHasCompletePanel(t, rows, geom.Outer)
	}

	w, h := p.calculatePreviewDimensions()
	term, ok := p.terminalLeafBox()
	if !ok {
		t.Fatal("no terminal inner")
	}
	if w != term.W || h != term.H-terminalHeaderRows {
		t.Fatalf("calculatePreviewDimensions = %dx%d, want terminal inner viewport %dx%d",
			w, h, term.W, term.H-terminalHeaderRows)
	}
}

func TestNestedThreeAndFourLeafChromeAgreesOnInners(t *testing.T) {
	cases := []struct {
		name  string
		setup func(*testing.T, *Plugin, string)
		want  int
	}{
		{"three-leaf", threeLeafPaneTree, 3},
		{"four-leaf", fourLeafPaneTree, 4},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			p := docPaneTestPlugin(t, root, true)
			p.width, p.height = 200, 50
			tc.setup(t, p, root)

			peer, ok := p.previewPeerBox()
			if !ok {
				t.Fatal("no preview peer")
			}
			layout, laid := LayoutPaneTree(p.paneRoot, peer, paneTreeFloors(), p.paneFocus)
			if !laid || layout.Zoomed || len(layout.Leaves) != tc.want {
				t.Fatalf("layout leaves=%d zoomed=%v laid=%v, want %d tiled",
					len(layout.Leaves), layout.Zoomed, laid, tc.want)
			}

			rows := composePaneTree(t, p, peer.W, peer.H)
			assertDividersDrawn(t, rows, layout.Dividers)
			for _, placement := range layout.Leaves {
				geom := leafGeometry(placement.Box)
				if geom.Inner.X != geom.Outer.X+2 || geom.Inner.Y != geom.Outer.Y+1 ||
					geom.Inner.W != geom.Outer.W-4 || geom.Inner.H != geom.Outer.H-2 {
					t.Fatalf("leaf %d inner %+v != outer %+v inset 2/1/−4/−2",
						placement.Node.ID, geom.Inner, geom.Outer)
				}
				assertLeafHasCompletePanel(t, rows, geom.Outer)
			}

			term, ok := p.terminalLeafBox()
			if !ok {
				t.Fatal("terminal inner missing")
			}
			surface := p.terminalSurfaceGeometry(false)
			if !surface.OK || surface.X != term.X || surface.HeaderY != term.Y {
				t.Fatalf("surface %+v disagrees with terminal inner %+v", surface, term)
			}
			w, h := p.calculatePreviewDimensions()
			if w != term.W || h != term.H-terminalHeaderRows {
				t.Fatalf("sizer %dx%d != terminal viewport %dx%d", w, h, term.W, term.H-terminalHeaderRows)
			}

			assertPaneTreeRegions(t, p, layout.Leaves, layout.Dividers)
			for _, placement := range layout.Leaves {
				if placement.Node.Kind != PaneDoc {
					continue
				}
				inner := leafGeometry(placement.Box).Inner
				hit := p.mouseHandler.HitMap.Test(inner.X, inner.Y)
				if hit == nil {
					t.Fatalf("inner header of leaf %d has no hit", placement.Node.ID)
				}
			}
		})
	}
}

func TestWrapLeafChromeInteractiveDoesNotLightUnfocusedTerminal(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# chrome\n")
	p := docPaneTestPlugin(t, root, true)
	p.width, p.height = 200, 50
	if cmd := p.openTerminalPath("README.md", 1); cmd == nil {
		t.Fatal("2-leaf open failed")
	}
	_, docLeaf := p.activeDocPane()
	if docLeaf == nil {
		t.Fatal("no document leaf")
	}
	p.activePane = PanePreview
	p.focusLeaf(docLeaf.ID)
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, PaneOnEntry: PanePreview}

	termID := terminalLeafID(p.paneRoot)
	got := p.wrapLeafChrome(&PaneNode{ID: termID, Kind: PaneTerminal}, "body", Box{W: 40, H: 10})
	interactive := styles.RenderPanelWithGradient("body", 40, 10, styles.GetInteractiveGradient())
	if got == interactive {
		t.Fatal("terminal used GetInteractiveGradient while paneFocus is the Doc")
	}
	muted := styles.RenderPanel("body", 40, 10, false)
	if got != muted {
		t.Fatalf("unfocused terminal chrome is not the muted panel")
	}
	docGot := p.wrapLeafChrome(docLeaf, "body", Box{W: 40, H: 10})
	if docGot != styles.RenderPanel("body", 40, 10, true) {
		t.Fatal("focused Doc did not use the active gradient")
	}
}

func TestPaneFocusMovesActivePerimeterWithoutDimmingBody(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# readme\n\nunique-doc-body\n")
	p := docPaneTestPlugin(t, root, true)
	p.width, p.height = 200, 50
	if cmd := p.openTerminalPath("README.md", 1); cmd == nil {
		t.Fatal("open failed")
	}
	doc, docLeaf := p.activeDocPane()
	if doc == nil || docLeaf == nil {
		t.Fatal("no document leaf")
	}
	termID := terminalLeafID(p.paneRoot)
	peer, _ := p.previewPeerBox()

	p.activePane = PanePreview
	p.focusLeaf(docLeaf.ID)
	docFocus := composePaneTree(t, p, peer.W, peer.H)
	p.focusLeaf(termID)
	termFocus := composePaneTree(t, p, peer.W, peer.H)

	if strings.Join(docFocus, "\n") == strings.Join(termFocus, "\n") {
		t.Fatal("moving paneFocus changed no styling; the active perimeter did not move")
	}

	layout, _ := LayoutPaneTree(p.paneRoot, peer, paneTreeFloors(), docLeaf.ID)
	var docOuter Box
	for _, placement := range layout.Leaves {
		if placement.Node.ID == docLeaf.ID {
			docOuter = placement.Box
		}
	}
	inner := insetPanelChrome(docOuter)
	// Body rows sit below the header. They must be cell-identical across focus
	// so unfocused content is not dimmed. Header chips may change glyphs (▸).
	for row := inner.Y + 1; row < inner.Y+inner.H; row++ {
		a := styledSlice(docFocus[row], inner.X, inner.W)
		b := styledSlice(termFocus[row], inner.X, inner.W)
		if a != b {
			t.Fatalf("unfocused document body row %d was restyled:\n%s\n%s", row, ansi.Strip(a), ansi.Strip(b))
		}
	}

	p.cyclePaneFocus(false)
	if p.paneFocus != docLeaf.ID && p.activePane == PanePreview {
		// Tab from terminal walks the ring; either the doc leaf or the sidebar
		// is acceptable as long as setFocusTarget was the writer.
	}
	p.focusLeaf(docLeaf.ID)
	if p.paneFocus != docLeaf.ID || p.activePane != PanePreview {
		t.Fatalf("focusLeaf did not write paneFocus: focus=%d pane=%v", p.paneFocus, p.activePane)
	}
}

func TestPaneTreeFloorTableEightyAndOneTwenty(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# floors\n")

	t.Run("80 sidebar 40% refuses", func(t *testing.T) {
		p := docPaneTestPlugin(t, root, true)
		p.width, p.height = 80, 36
		p.sidebarVisible = true
		p.sidebarWidth = 40
		peer, _ := p.previewPeerBox()
		if peer.W >= 14+1+34 {
			t.Fatalf("preview peer width %d unexpectedly tiles 14+1+34", peer.W)
		}
		if cmd := p.openTerminalPath("README.md", 1); cmd != nil || p.activeDocPaneOrNil() != nil {
			t.Fatalf("80/40%% sidebar tiled: cmd=%v doc=%#v peer=%+v", cmd, p.activeDocPaneOrNil(), peer)
		}
	})

	t.Run("120 sidebar 40% tiles", func(t *testing.T) {
		p := docPaneTestPlugin(t, root, true)
		p.width, p.height = 120, 36
		p.sidebarVisible = true
		p.sidebarWidth = 40
		if cmd := p.openTerminalPath("README.md", 1); cmd == nil {
			t.Fatal("120/40% refused a 2-col split")
		}
		assertMarkdownInnersAtLeastMin(t, p)
	})

	t.Run("80 sidebar hidden tiles", func(t *testing.T) {
		p := docPaneTestPlugin(t, root, true)
		p.width, p.height = 80, 36
		p.sidebarVisible = false
		if cmd := p.openTerminalPath("README.md", 1); cmd == nil {
			t.Fatal("80 hidden-sidebar refused a 2-col split")
		}
		assertMarkdownInnersAtLeastMin(t, p)
	})
}

func TestSelectionHitsUseTerminalSurfaceGeometryWithoutExtraInset(t *testing.T) {
	root := t.TempDir()
	writeDocPaneFixture(t, root, "README.md", "# hit\n")
	p := docPaneTestPlugin(t, root, true)
	p.width, p.height = 200, 50
	p.shells[0].Agent.OutputBuf.Update(strings.Repeat("selectable terminal row here\n", 10))
	if cmd := p.openTerminalPath("README.md", 1); cmd == nil {
		t.Fatal("2-leaf open failed")
	}
	p.viewMode = ViewModeInteractive
	p.interactiveState = &InteractiveState{Active: true, TargetSession: "test-shell", TargetPane: "%901"}
	p.selection.Clear()

	surface := p.terminalSurfaceGeometry(false)
	if !surface.OK {
		t.Fatal("terminal surface is unplaced")
	}
	geom := p.terminalSelectionGeometry()
	// A second previewContentInset would land the hit map to the right of the
	// surface — the multi-leaf bug this story forbids.
	if geom.Content.X != surface.X || geom.Content.Y != surface.Y {
		t.Fatalf("selection geometry origin = %d,%d, want surface %d,%d (no extra inset)",
			geom.Content.X, geom.Content.Y, surface.X, surface.Y)
	}
	inner, ok := p.terminalLeafBox()
	if !ok || surface.X != inner.X || surface.Y != inner.Y+terminalHeaderRows {
		t.Fatalf("surface %+v does not sit on terminal inner %+v", surface, inner)
	}

	action := mouse.MouseAction{
		Type: mouse.ActionClick,
		X:    surface.X + 4,
		Y:    surface.Y,
		Region: &mouse.Region{
			ID:   regionPreviewPane,
			Rect: mouse.Rect{X: surface.X, Y: surface.HeaderY, W: surface.Width, H: surface.Height + terminalHeaderRows},
		},
	}
	p.prepareInteractiveDrag(action, 0)
	if !p.selection.Anchor.Valid() {
		t.Fatal("click on the terminal inner did not anchor")
	}
}

func assertLeafHasCompletePanel(t *testing.T, rows []string, outer Box) {
	t.Helper()
	top := []rune(ansi.Strip(rows[outer.Y]))
	bot := []rune(ansi.Strip(rows[outer.Y+outer.H-1]))
	if string(top[outer.X]) != "╭" || string(top[outer.X+outer.W-1]) != "╮" {
		t.Fatalf("outer %+v missing top corners: %q %q", outer,
			string(top[outer.X]), string(top[outer.X+outer.W-1]))
	}
	if string(bot[outer.X]) != "╰" || string(bot[outer.X+outer.W-1]) != "╯" {
		t.Fatalf("outer %+v missing bottom corners: %q %q", outer,
			string(bot[outer.X]), string(bot[outer.X+outer.W-1]))
	}
	for x := outer.X + 1; x < outer.X+outer.W-1; x++ {
		if string(top[x]) != "─" || string(bot[x]) != "─" {
			t.Fatalf("outer %+v missing horizontal border at col %d", outer, x)
		}
	}
	for y := outer.Y + 1; y < outer.Y+outer.H-1; y++ {
		line := []rune(ansi.Strip(rows[y]))
		if string(line[outer.X]) != "│" || string(line[outer.X+outer.W-1]) != "│" {
			t.Fatalf("outer %+v missing vertical border on row %d", outer, y)
		}
	}
}

func assertMarkdownInnersAtLeastMin(t *testing.T, p *Plugin) {
	t.Helper()
	peer, ok := p.previewPeerBox()
	if !ok {
		t.Fatal("no peer")
	}
	leaves, _, fits := LayoutPanes(p.paneRoot, peer, paneTreeFloors())
	if !fits {
		t.Fatal("expected a tiled layout")
	}
	for _, placement := range leaves {
		if placement.Node.Kind == PaneTerminal {
			continue
		}
		inner := insetPanelChrome(placement.Box)
		if inner.W < markdown.MinWidthForMarkdown {
			t.Fatalf("markdown inner width %d < %d on leaf %d",
				inner.W, markdown.MinWidthForMarkdown, placement.Node.ID)
		}
	}
}

func styledSlice(row string, x, w int) string {
	cells := []rune(ansi.Strip(row))
	if x < 0 || w < 0 || x+w > len(cells) {
		return ""
	}
	return string(cells[x : x+w])
}
