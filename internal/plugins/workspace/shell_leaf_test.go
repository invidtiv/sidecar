package workspace

import "testing"

// shellLeafTestPlugin is a drawn plugin with the terminal panel up in one of
// the two legacy layouts.
func shellLeafTestPlugin(t *testing.T, layout TermPanelLayout) *Plugin {
	t.Helper()
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = false
	p.termPanelVisible = true
	p.termPanelLayout = layout
	p.View(p.width, p.height)
	if _, _, fits := p.termPanelSplitBoxes(); !fits {
		t.Fatal("fixture failed to produce a split that fits")
	}
	return p
}

// The panel terminal is a Shell leaf of the terminal leaf's own sub-tree, and
// the primary terminal is the Primary leaf beside it. Without a panel there is
// no split and no Shell leaf at all — never an empty pane.
func TestShellPaneSubtreeShape(t *testing.T) {
	for _, tc := range []struct {
		name     string
		layout   TermPanelLayout
		wantAxis SplitAxis
	}{
		{"bottom stacks", TermPanelBottom, SplitRows},
		{"right splits columns", TermPanelRight, SplitCols},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := shellLeafTestPlugin(t, tc.layout)
			root := p.shellPaneSubtree()
			if root.Split == nil {
				t.Fatal("a visible panel must be a split")
			}
			if root.Split.Axis != tc.wantAxis {
				t.Fatalf("axis = %v, want %v", root.Split.Axis, tc.wantAxis)
			}
			if leaf := firstPaneLeafOfKind(root, PaneShell); leaf == nil || leaf.ID != shellSlotShell {
				t.Fatalf("shell leaf = %+v, want slot %d", leaf, shellSlotShell)
			}
			if leaf := firstPaneLeafOfKind(root, PaneTerminal); leaf == nil || leaf.ID != shellSlotPrimary {
				t.Fatalf("primary leaf = %+v, want slot %d", leaf, shellSlotPrimary)
			}
			if !supportedPaneTree(root) {
				t.Fatal("a tree holding a shell leaf must be supported")
			}

			p.termPanelVisible = false
			hidden := p.shellPaneSubtree()
			if hidden.Split != nil || hidden.Kind != PaneTerminal {
				t.Fatalf("hidden panel tree = %+v, want a lone terminal leaf", hidden)
			}
			if _, ok := p.shellSlotBox(true); ok {
				t.Fatal("a hidden panel must have no leaf box")
			}
		})
	}
}

// Geometry equivalence: every terminal surface is placed at its own leaf's box
// in both legacy layouts, rather than by an offset walk of its own. The two
// boxes tile the terminal leaf exactly, divider included.
func TestShellLeafBoxesPlaceBothTerminals(t *testing.T) {
	for _, tc := range []struct {
		name   string
		layout TermPanelLayout
	}{
		{"bottom", TermPanelBottom},
		{"right", TermPanelRight},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := shellLeafTestPlugin(t, tc.layout)
			boxes := p.shellPaneBoxes()
			primary, shell := boxes[shellSlotPrimary], boxes[shellSlotShell]

			outputBox, termBox, _ := p.termPanelSplitBoxes()
			width, height := p.calculatePreviewDimensions()
			container := termPanelContainerHeight(height)
			if tc.layout == TermPanelRight {
				if primary.W != outputBox || shell.W != termBox {
					t.Fatalf("column split = %d/%d, want %d/%d", primary.W, shell.W, outputBox, termBox)
				}
				if primary.H != container || shell.H != container {
					t.Fatalf("column heights = %d/%d, want %d", primary.H, shell.H, container)
				}
				if shell.X != primary.X+primary.W+termPanelDividerCols || shell.Y != primary.Y {
					t.Fatalf("shell box %+v does not sit past the divider of %+v", shell, primary)
				}
			} else {
				if primary.H != outputBox || shell.H != termBox {
					t.Fatalf("row split = %d/%d, want %d/%d", primary.H, shell.H, outputBox, termBox)
				}
				if primary.W != width || shell.W != width {
					t.Fatalf("row widths = %d/%d, want %d", primary.W, shell.W, width)
				}
				if shell.Y != primary.Y+primary.H+termPanelDividerRows || shell.X != primary.X {
					t.Fatalf("shell box %+v does not sit past the divider of %+v", shell, primary)
				}
			}

			for _, surface := range []struct {
				name      string
				termPanel bool
				box       Box
			}{
				{"primary terminal", false, primary},
				{"panel terminal", true, shell},
			} {
				geom := p.terminalSurfaceGeometry(surface.termPanel)
				if !geom.OK {
					t.Fatalf("%s has no geometry", surface.name)
				}
				wantW, wantH := shellSlotTerminalSize(surface.box)
				if geom.X != surface.box.X || geom.HeaderY != surface.box.Y {
					t.Fatalf("%s at (%d,%d), want its leaf box origin (%d,%d)",
						surface.name, geom.X, geom.HeaderY, surface.box.X, surface.box.Y)
				}
				if geom.Y != surface.box.Y+terminalHeaderRows {
					t.Fatalf("%s viewport starts at %d, want %d", surface.name, geom.Y, surface.box.Y+terminalHeaderRows)
				}
				if geom.Width != wantW || geom.Height != wantH {
					t.Fatalf("%s sized %dx%d, want %dx%d", surface.name, geom.Width, geom.Height, wantW, wantH)
				}
			}

			panelW, panelH, ok := p.calculateTermPanelDimensions()
			if wantW, wantH := shellSlotTerminalSize(shell); !ok || panelW != wantW || panelH != wantH {
				t.Fatalf("panel dimensions = %dx%d (ok=%v), want the shell leaf's %dx%d", panelW, panelH, ok, wantW, wantH)
			}
			agentW, agentH := p.calculateAgentPaneDimensions()
			if wantW, wantH := shellSlotTerminalSize(primary); agentW != wantW || agentH != wantH {
				t.Fatalf("agent dimensions = %dx%d, want the primary leaf's %dx%d", agentW, agentH, wantW, wantH)
			}
		})
	}
}

// With no panel drawn the primary terminal owns the whole terminal leaf, which
// is the box the single-terminal journey has always used.
func TestPrimarySlotOwnsTheWholeLeafWithoutAPanel(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = false
	p.View(p.width, p.height)

	width, height := p.calculatePreviewDimensions()
	box, ok := p.shellSlotBox(false)
	if !ok {
		t.Fatal("the primary terminal must always have a box")
	}
	if box.W != width || box.H != termPanelContainerHeight(height) {
		t.Fatalf("primary box = %dx%d, want %dx%d", box.W, box.H, width, termPanelContainerHeight(height))
	}
	if w, h := shellSlotTerminalSize(box); w != width || h != height {
		t.Fatalf("primary terminal sized %dx%d, want the preview's %dx%d", w, h, width, height)
	}
}
