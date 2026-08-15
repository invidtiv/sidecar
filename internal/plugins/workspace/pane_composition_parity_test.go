package workspace

import (
	"fmt"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/ui"
)

// twoLeafJoinComposition is the renderer M0 deleted, kept here and nowhere else:
// exactly two leaves and one divider, placed by one of four hand-written
// lipgloss join orderings. M0 claims the composition the product ships renders
// the same bytes on the canvas, and the journey that reaches that composition
// cannot be driven headlessly — a doc leaf is born from a markdown path in live
// agent output — so the claim is measured against the old renderer here instead
// of asserted in a transcript that never reaches it.
func (p *Plugin) twoLeafJoinComposition(width, height int, leaves []Placement, dividers []Divider, origin Box) string {
	var terminal, document string
	for _, placement := range leaves {
		switch placement.Node.Kind {
		case PaneTerminal:
			terminal = p.renderPaneLeaf(placement, origin, false)
		case PaneDoc:
			document = p.renderPaneLeaf(placement, origin, false)
		}
	}
	if dividers[0].Axis == SplitRows {
		divider := renderPaneTreeDividerH(width, p.docFocused())
		if leaves[0].Node.Kind == PaneTerminal {
			return lipgloss.JoinVertical(lipgloss.Left, terminal, divider, document)
		}
		return lipgloss.JoinVertical(lipgloss.Left, document, divider, terminal)
	}
	divider := renderPaneTreeDividerV(height, p.docFocused())
	if leaves[0].Node.Kind == PaneTerminal {
		return lipgloss.JoinHorizontal(lipgloss.Top, terminal, divider, document)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, document, divider, terminal)
}

// canvasComposition is the shipped path's composition alone, without the hit
// regions it also registers: the two renderers are compared on the cells they
// produce.
func (p *Plugin) canvasComposition(width, height int, leaves []Placement, dividers []Divider, origin Box) string {
	canvas := ui.NewCanvas(width, height)
	for _, placement := range leaves {
		canvas.Blit(placement.Box, p.renderPaneLeaf(placement, origin, false))
	}
	for _, split := range dividers {
		canvas.Blit(split.Box, p.renderPaneTreeDivider(split))
	}
	return canvas.String()
}

// Both axes are measured because a rows-axis terminal-plus-document tree is
// restorable from persisted JSON (supportedPaneTree accepts it on either
// axis), and the vertical joins were the ordering the shipped journey exercised
// least.
func TestTwoLeafPaneCompositionMatchesTheJoinsItReplaced(t *testing.T) {
	states := map[string]struct {
		shell bool
		setup func(p *Plugin)
	}{
		"shell with a live agent":     {shell: true},
		"shell with no agent":         {shell: true, setup: func(p *Plugin) { p.shells[0].Agent = nil }},
		"workspace with a live agent": {},
		"workspace with no agent":     {setup: func(p *Plugin) { p.worktrees[0].Agent = nil }},
		"workspace with an orphan tree": {setup: func(p *Plugin) {
			p.worktrees[0].Agent = nil
			p.worktrees[0].IsOrphaned = true
		}},
	}
	sizes := [][2]int{{140, 36}, {120, 24}, {100, 30}, {90, 20}}
	measured := 0
	for name, st := range states {
		t.Run(name, func(t *testing.T) {
			for _, size := range sizes {
				for _, axis := range []SplitAxis{SplitCols, SplitRows} {
					for _, terminalFirst := range []bool{true, false} {
						for _, focusDoc := range []bool{true, false} {
							width, height := size[0], size[1]
							root := t.TempDir()
							p := docPaneTestPlugin(t, root, st.shell)
							if st.setup != nil {
								st.setup(p)
							}
							p.width, p.height = width, height
							p.docs = make(map[int]*docPane)
							compositorDocLeaf(t, p, root, 2, "one.md", "# one\n\nbody\n")

							terminal := &PaneNode{ID: 1, Kind: PaneTerminal}
							document := &PaneNode{ID: 2, Kind: PaneDoc, ContentID: 2}
							first, second := terminal, document
							if !terminalFirst {
								first, second = document, terminal
							}
							p.paneRoot = &PaneNode{ID: 9, Split: &PaneSplit{Axis: axis, Ratio: 50, A: first, B: second}}
							p.paneNextID = 10
							p.paneFocus = terminal.ID
							if focusDoc {
								p.paneFocus = document.ID
							}

							leaves, dividers, fits := LayoutPanes(p.paneRoot, Box{W: width, H: height}, paneTreeFloors())
							if !fits || len(leaves) != 2 || len(dividers) != 1 {
								continue
							}
							origin, _ := p.previewContentBox()
							measured++
							where := fmt.Sprintf("%dx%d axis=%v terminalFirst=%v focusDoc=%v",
								width, height, axis, terminalFirst, focusDoc)
							if got, want := p.canvasComposition(width, height, leaves, dividers, origin),
								p.twoLeafJoinComposition(width, height, leaves, dividers, origin); got != want {
								t.Fatalf("%s: the canvas composed different bytes than the joins it replaced\ngot:\n%s\nwant:\n%s",
									where, got, want)
							}
						}
					}
				}
			}
		})
	}
	if measured == 0 {
		t.Fatal("no configuration was measured; the comparison proved nothing")
	}
}
