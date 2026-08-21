package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/features"
)

// shellLeafTestPlugin is a drawn plugin with the terminal panel up as a Shell
// leaf on the given axis.
func shellLeafTestPlugin(t *testing.T, axis SplitAxis) *Plugin {
	t.Helper()
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = false
	showTermPanel(t, p, axis, 50)
	p.View(p.width, p.height)
	if _, drawn := p.shellLeafBox(); !drawn {
		t.Fatal("fixture failed to draw the shell leaf")
	}
	return p
}

// The panel is a leaf of the pane tree, beside the primary terminal, on the
// axis the toggle opened it at — and it is gone from the tree the moment the
// panel is off. Never a leaf without a panel, never a panel without a leaf.
func TestShellLeafIsAPaneOfTheTree(t *testing.T) {
	for _, tc := range []struct {
		name string
		axis SplitAxis
	}{
		{"bottom stacks", SplitRows},
		{"right splits columns", SplitCols},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := shellLeafTestPlugin(t, tc.axis)
			leaf := p.shellLeaf()
			if leaf == nil {
				t.Fatal("a visible panel must be a leaf of the tree")
			}
			split := FindPane(p.paneRoot, p.shellSplitID())
			if split == nil || split.Split == nil {
				t.Fatalf("shell leaf %d has no split above it", leaf.ID)
			}
			if split.Split.Axis != tc.axis {
				t.Fatalf("axis = %v, want %v", split.Split.Axis, tc.axis)
			}
			if split.Split.B != leaf {
				t.Fatal("the panel must be the split's SECOND child, so the ratio is the primary terminal's")
			}
			if p.shellSplitIsColumns() != (tc.axis == SplitCols) {
				t.Fatalf("shellSplitIsColumns = %v on axis %v", p.shellSplitIsColumns(), tc.axis)
			}
			if !supportedPaneTree(p.paneRoot) {
				t.Fatal("a tree holding a shell leaf must be supported")
			}

			p.termPanelVisible = false
			p.syncShellLeaf()
			if p.shellLeaf() != nil {
				t.Fatal("a hidden panel must leave no leaf behind")
			}
			if _, ok := p.terminalSlotBox(true); ok {
				t.Fatal("a hidden panel must have no leaf box")
			}
		})
	}
}

// Geometry equivalence: every terminal surface is placed at its own leaf's box,
// from the one layout the frame drew, rather than by an offset walk of its own.
func TestShellLeafBoxesPlaceBothTerminals(t *testing.T) {
	for _, tc := range []struct {
		name string
		axis SplitAxis
	}{
		{"bottom", SplitRows},
		{"right", SplitCols},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := shellLeafTestPlugin(t, tc.axis)
			primary, ok := p.terminalSlotBox(false)
			if !ok {
				t.Fatal("the primary terminal must always have a box")
			}
			shell, ok := p.terminalSlotBox(true)
			if !ok {
				t.Fatal("a drawn panel must have a box")
			}

			if tc.axis == SplitCols {
				if shell.X <= primary.X+primary.W {
					t.Fatalf("shell box %+v does not sit past %+v", shell, primary)
				}
			} else if shell.Y <= primary.Y+primary.H {
				t.Fatalf("shell box %+v does not sit below %+v", shell, primary)
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
				wantW, wantH := terminalSlotSize(surface.box)
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
			if wantW, wantH := terminalSlotSize(shell); !ok || panelW != wantW || panelH != wantH {
				t.Fatalf("panel dimensions = %dx%d (ok=%v), want the shell leaf's %dx%d", panelW, panelH, ok, wantW, wantH)
			}
			agentW, agentH := p.calculateAgentPaneDimensions()
			if wantW, wantH := terminalSlotSize(primary); agentW != wantW || agentH != wantH {
				t.Fatalf("agent dimensions = %dx%d, want the primary leaf's %dx%d", agentW, agentH, wantW, wantH)
			}
		})
	}
}

// ctrl+t is a toggle of that leaf, and the shape it opens at is the shape the
// last one was left in — including a divider the user dragged.
func TestToggleTermPanelOpensAtTheRememberedShape(t *testing.T) {
	enableWorkspaceFeature(t, features.WorkspaceTerminalPanel.Name)
	p := shellLeafTestPlugin(t, SplitCols)
	SetRatio(p.paneRoot, p.shellSplitID(), 70)
	p.rememberShellSplit()

	p.toggleTermPanel() // the fixture is already open; this closes it
	if p.termPanelVisible || p.shellLeaf() != nil {
		t.Fatal("toggle did not close the panel")
	}

	p.toggleTermPanel()
	if !p.termPanelVisible || p.shellLeaf() == nil {
		t.Fatal("toggle did not reopen the panel")
	}
	split := FindPane(p.paneRoot, p.shellSplitID())
	if split.Split.Axis != SplitCols || split.Split.Ratio != 70 {
		t.Fatalf("reopened at %v/%d, want the remembered cols/70", split.Split.Axis, split.Split.Ratio)
	}
}

// With no panel the primary terminal owns the whole terminal leaf, which is the
// box the single-terminal journey has always used.
func TestPrimarySlotOwnsTheWholeLeafWithoutAPanel(t *testing.T) {
	stubTd(t)
	root := t.TempDir()
	p := docPaneTestPlugin(t, root, true)
	p.sidebarVisible = false
	p.View(p.width, p.height)

	width, height := p.calculatePreviewDimensions()
	box, ok := p.terminalSlotBox(false)
	if !ok {
		t.Fatal("the primary terminal must always have a box")
	}
	if w, h := terminalSlotSize(box); w != width || h != height {
		t.Fatalf("primary terminal sized %dx%d, want the preview's %dx%d", w, h, width, height)
	}
}
