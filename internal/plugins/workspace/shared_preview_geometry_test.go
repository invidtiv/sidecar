package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/termpreview"
)

// The shared presentation layer sits beneath the pane tree, not beside it. The
// tree stays the geometry authority: it decides where the terminal leaf is, and
// termpreview only says where the header row and the viewport sit inside the
// box it was handed. These tests are the falsifiable version of that claim —
// if the shared layer ever grows a second opinion about the preview box, the
// split, or the header row, one of them fails.
//
// Pane-tree placement itself (ratios, floors, dividers, doc leaves) is covered
// by panetree_test.go and doc_panes_test.go; what is checked here is only the
// agreement between those placements and the shared layer.

// paneTrees returns the trees whose terminal leaf must agree with the shared
// layer: the single terminal, and both axes of a terminal/doc split.
func paneTrees(t *testing.T) map[string]*PaneNode {
	t.Helper()
	return map[string]*PaneNode{
		"single terminal": {ID: 1, Kind: PaneTerminal},
		"terminal beside doc": {ID: 3, Split: &PaneSplit{
			Axis:  SplitCols,
			Ratio: 60,
			A:     &PaneNode{ID: 1, Kind: PaneTerminal},
			B:     &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 1},
		}},
		"terminal above doc": {ID: 3, Split: &PaneSplit{
			Axis:  SplitRows,
			Ratio: 55,
			A:     &PaneNode{ID: 1, Kind: PaneTerminal},
			B:     &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 1},
		}},
	}
}

func previewFloors() Floors {
	return Floors{
		Terminal: PaneFloor{Width: termPanelMinBoxCols, Height: termPanelMinBoxRows},
		Doc:      PaneFloor{Width: markdown.MinWidthForMarkdown, Height: termPanelMinBoxRows},
	}
}

func TestSharedTerminalPresentationAgreesWithLayoutPanes(t *testing.T) {
	sizes := []struct{ width, height int }{{120, 40}, {180, 50}, {96, 24}}

	for name, tree := range paneTrees(t) {
		for _, size := range sizes {
			for _, sidebar := range []bool{true, false} {
				t.Run(name, func(t *testing.T) {
					p := surfacePlugin(false)
					p.width, p.height = size.width, size.height
					p.sidebarVisible = sidebar
					p.paneRoot = tree
					p.paneFocus = 1

					content, ok := p.previewContentBox()
					if !ok {
						t.Fatalf("no preview content box at %dx%d", size.width, size.height)
					}
					leaves, _, fits := LayoutPanes(p.paneRoot, content, previewFloors())
					if !fits {
						t.Skip("tree does not fit this viewport; floors are panetree_test.go's subject")
					}
					var placed Box
					for _, placement := range leaves {
						if placement.Node != nil && placement.Node.Kind == PaneTerminal {
							placed = placement.Box
						}
					}

					// 1. The seam every sizer reads returns exactly what LayoutPanes
					// placed. Nothing beneath it recomputes the box.
					leaf, ok := p.terminalLeafBox()
					if !ok || leaf != placed {
						t.Fatalf("terminalLeafBox() = %+v ok=%v, LayoutPanes placed %+v", leaf, ok, placed)
					}

					// 2. The shared layer's surface is that box minus its header row,
					// and it is the same answer the plugin's own geometry reports.
					shared := termpreview.SurfaceIn(leaf)
					surface := p.terminalSurfaceGeometry(false)
					if !shared.OK || !surface.OK {
						t.Fatalf("surface not placed: shared=%+v plugin=%+v", shared, surface)
					}
					if surface.X != shared.X || surface.HeaderY != shared.HeaderY || surface.Y != shared.Y {
						t.Fatalf("plugin surface origin %+v disagrees with shared layer %+v", surface, shared)
					}

					// 3. The size tmux is asked for is the same viewport, so a pane is
					// never laid out against a box the tree did not give it.
					previewW, previewH := p.calculatePreviewDimensions()
					if previewW != shared.Width || previewH != shared.Height {
						t.Fatalf("calculatePreviewDimensions = %dx%d, shared viewport = %dx%d",
							previewW, previewH, shared.Width, shared.Height)
					}
				})
			}
		}
	}
}

func TestPreviewSplitIsTheSharedSplit(t *testing.T) {
	for _, sidebar := range []bool{true, false} {
		for _, percent := range []int{5, 40, 95} {
			for _, width := range []int{40, 80, 120, 240} {
				p := surfacePlugin(false)
				p.sidebarVisible = sidebar
				p.sidebarWidth = percent

				want := termpreview.SplitFor(width, termpreview.SplitConfig{
					SidebarVisible: sidebar,
					SidebarPercent: percent,
					DividerWidth:   dividerWidth,
					PanelOverhead:  panelOverhead,
					ContentInset:   previewContentInset,
					SidebarMin:     sidebarMinWidth,
					PreviewMin:     previewMinWidth,
				})
				if got := p.previewSplitFor(width); got != want {
					t.Fatalf("previewSplitFor(%d) sidebar=%v percent=%d = %+v, shared split = %+v",
						width, sidebar, percent, got, want)
				}
			}
		}
	}
}

func TestSharedHeaderRowIsThePluginHeaderRow(t *testing.T) {
	p := surfacePlugin(false)
	chips := []string{"Output", "Diff", "Task"}
	for _, width := range []int{0, 6, 12, 24, 60} {
		for _, floor := range []int{0, 10} {
			truncate := func(value string, limit int) string { return p.truncateCache.Truncate(value, limit, "") }
			want := termpreview.HeaderRow(chips, "t to attach", width, floor, truncate)
			if got := p.terminalHeader(chips, "t to attach", width, floor); got != want {
				t.Fatalf("terminalHeader(width=%d floor=%d) = %q, shared header = %q", width, floor, got, want)
			}
			if len(termpreview.LayoutChips(chips, width, floor)) != len(layoutHeaderChips(chips, width, floor)) {
				t.Fatalf("chip placement count disagreed at width %d", width)
			}
		}
	}
}
