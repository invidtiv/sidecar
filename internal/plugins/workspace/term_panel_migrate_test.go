package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/state"
)

// Both legacy layouts and both ratio directions, asserted as a table: bottom is
// a Rows split and right is a Columns one, and the panel's own percentage
// becomes the PRIMARY terminal's ratio. Reading that complement backwards is an
// invisible upgrade bug — every user's panel arrives on the other side of its
// divider, or takes 70% of the pane it used to take 30% of — so it is pinned
// here rather than trusted to the reader.
func TestTermPanelSplitShapeConvertsBothLayoutsAndBothRatioDirections(t *testing.T) {
	for _, tc := range []struct {
		name      string
		prefs     termPanelPrefs
		wantAxis  SplitAxis
		wantRatio int
	}{
		{"bottom, panel small", termPanelPrefs{Visible: true, Side: "bottom", Size: 30}, SplitRows, 70},
		{"bottom, panel large", termPanelPrefs{Visible: true, Side: "bottom", Size: 70}, SplitRows, 30},
		{"right, panel small", termPanelPrefs{Visible: true, Side: "right", Size: 30}, SplitCols, 70},
		{"right, panel large", termPanelPrefs{Visible: true, Side: "right", Size: 70}, SplitCols, 30},
		{"unset side reads as bottom", termPanelPrefs{Visible: true, Size: 40}, SplitRows, 60},
		{"unset size takes the old default", termPanelPrefs{Visible: true, Side: "right"}, SplitCols, 50},
		{"beyond the clamp", termPanelPrefs{Visible: true, Side: "bottom", Size: 99}, SplitRows, paneMinRatio},
	} {
		t.Run(tc.name, func(t *testing.T) {
			axis, ratio := termPanelSplitShape(tc.prefs)
			if axis != tc.wantAxis || ratio != tc.wantRatio {
				t.Fatalf("shape = %v/%d, want %v/%d", axis, ratio, tc.wantAxis, tc.wantRatio)
			}
		})
	}
}

// The conversion splices the panel in as a Shell leaf beside the terminal leaf,
// wherever in the tree that leaf sits, and leaves everything else alone.
func TestMigrateTermPanelLayoutSplicesTheShellLeaf(t *testing.T) {
	base := func() *state.PaneLayoutJSON {
		return &state.PaneLayoutJSON{Root: "/repo", Surface: "shell:one", Split: &state.PaneSplitJSON{
			Axis: "cols", Ratio: 60,
			A: &state.PaneLayoutJSON{Kind: contentKindTerminal},
			B: &state.PaneLayoutJSON{Kind: contentKindDoc},
		}}
	}

	got := migrateTermPanelIntoLayout(base(), termPanelPrefs{Visible: true, Side: "right", Size: 30})
	if got.Root != "/repo" || got.Surface != "shell:one" {
		t.Fatalf("migration lost the layout's identity: %+v", got)
	}
	if got.Split == nil || got.Split.Axis != "cols" || got.Split.Ratio != 60 {
		t.Fatalf("migration disturbed the existing split: %+v", got.Split)
	}
	if got.Split.B.Kind != contentKindDoc {
		t.Fatalf("migration disturbed the document leaf: %+v", got.Split.B)
	}
	spliced := got.Split.A
	if spliced.Split == nil {
		t.Fatalf("terminal leaf was not split: %+v", spliced)
	}
	if spliced.Split.Axis != "cols" || spliced.Split.Ratio != 70 {
		t.Fatalf("spliced split = %s/%d, want cols/70", spliced.Split.Axis, spliced.Split.Ratio)
	}
	if spliced.Split.A.Kind != contentKindTerminal || spliced.Split.B.Kind != contentKindShell {
		t.Fatalf("spliced children = %q/%q, want terminal then shell",
			spliced.Split.A.Kind, spliced.Split.B.Kind)
	}

	// Nothing to convert leaves the layout exactly as it was.
	for _, prefs := range []termPanelPrefs{
		{Visible: false, Side: "right", Size: 30},
		{Visible: true, Side: "right", Size: 30},
	} {
		layout := base()
		if !prefs.Visible {
			if migrateTermPanelIntoLayout(layout, prefs) != layout {
				t.Fatal("a panel that was not up must not be spliced in")
			}
			continue
		}
		// A layout that already carries a shell leaf is already converted.
		layout.Split.B = &state.PaneLayoutJSON{Kind: contentKindShell}
		if migrateTermPanelIntoLayout(layout, prefs) != layout {
			t.Fatal("a layout that already has a shell leaf must not gain a second one")
		}
	}
}

// End to end: a converted layout restores as a real Shell leaf, with the panel
// flagged up and its shape remembered for the next toggle.
func TestConvertedLayoutRestoresAsAShellLeaf(t *testing.T) {
	stubTd(t)
	p := docPaneTestPlugin(t, t.TempDir(), true)
	p.sidebarVisible = false
	selRoot, surface, ok := p.selectedTerminalSurface()
	if !ok {
		t.Fatal("fixture has no selected surface")
	}

	layout := migrateTermPanelIntoLayout(
		&state.PaneLayoutJSON{Root: selRoot, Surface: surface, Kind: contentKindTerminal},
		termPanelPrefs{Visible: true, Side: "right", Size: 40},
	)
	p.restorePaneLayout(layout)

	if !p.shellLeafVisible() {
		t.Fatal("a restored shell leaf must turn the panel on")
	}
	leaf := p.shellLeaf()
	if leaf == nil {
		t.Fatal("restored layout has no shell leaf")
	}
	split := FindPane(p.paneRoot, p.shellSplitID())
	if split.Split.Axis != SplitCols || split.Split.Ratio != 60 {
		t.Fatalf("restored split = %v/%d, want cols/60", split.Split.Axis, split.Split.Ratio)
	}
	if p.shellSplitRatio != 60 || p.shellSplitAxis != SplitCols {
		t.Fatalf("remembered shape = %v/%d, want cols/60", p.shellSplitAxis, p.shellSplitRatio)
	}
}
