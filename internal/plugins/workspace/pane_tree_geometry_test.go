package workspace

import (
	"testing"

	"github.com/marcus/sidecar/internal/config"
	"github.com/marcus/sidecar/internal/features"
	"github.com/marcus/sidecar/internal/plugin"
)

func enableSingleTerminalTree(p *Plugin) {
	p.paneRoot = &PaneNode{ID: 1, Kind: PaneTerminal}
	p.paneFocus = 1
	p.paneNextID = 2
}

// showTermPanel puts the terminal panel up as a Shell leaf of the pane tree,
// split on axis with the primary terminal taking primaryRatio percent. It is
// the tests' only way in, because the leaf and the flag are reconciled in one
// place and a test that set only one of them would be testing a state the
// plugin cannot reach.
func showTermPanel(t *testing.T, p *Plugin, axis SplitAxis, primaryRatio int) {
	t.Helper()
	if p.paneRoot == nil {
		enableSingleTerminalTree(p)
	}
	p.shellSplitAxis, p.shellSplitRatio = axis, primaryRatio
	p.termPanelVisible = true
	if !p.syncShellLeaf() {
		t.Fatalf("shell split did not fit at %dx%d", p.width, p.height)
	}
}

func configureTerminalPanel(t *testing.T, p *Plugin, axis SplitAxis) {
	t.Helper()
	showTermPanel(t, p, axis, 60)
	p.termPanelOutput = markerBuffer("PANEL", 4)
}

// A lone terminal leaf still renders exactly the legacy preview. The terminal
// panel is no longer part of this comparison: it is a leaf of the tree now, so
// a plugin with no tree has no panel to compare against.
func TestSingleTerminalPaneTreePreservesLegacyRenderBytes(t *testing.T) {
	for _, sidebarVisible := range []bool{true, false} {
		t.Run(map[bool]string{true: "sidebar visible", false: "sidebar hidden"}[sidebarVisible], func(t *testing.T) {
			legacy := surfacePlugin(false)
			legacy.sidebarVisible = sidebarVisible
			withTree := surfacePlugin(false)
			withTree.sidebarVisible = sidebarVisible
			enableSingleTerminalTree(withTree)

			legacyRender := legacy.renderListView(legacy.width, legacy.height)
			treeRender := withTree.renderListView(withTree.width, withTree.height)
			if treeRender != legacyRender {
				t.Fatalf("single-terminal tree changed rendered bytes\nlegacy:\n%s\nwith tree:\n%s", legacyRender, treeRender)
			}
		})
	}
}

func TestTerminalSizingAndSurfaceGeometryUseTerminalLeafBox(t *testing.T) {
	tests := []struct {
		name           string
		sidebarVisible bool
		panelVisible   bool
		panelAxis      SplitAxis
	}{
		{name: "sidebar visible panel absent", sidebarVisible: true},
		{name: "sidebar hidden panel absent", sidebarVisible: false},
		{name: "sidebar visible panel right", sidebarVisible: true, panelVisible: true, panelAxis: SplitCols},
		{name: "sidebar hidden panel right", sidebarVisible: false, panelVisible: true, panelAxis: SplitCols},
		{name: "sidebar visible panel bottom", sidebarVisible: true, panelVisible: true, panelAxis: SplitRows},
		{name: "sidebar hidden panel bottom", sidebarVisible: false, panelVisible: true, panelAxis: SplitRows},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := surfacePlugin(false)
			p.sidebarVisible = tc.sidebarVisible
			enableSingleTerminalTree(p)
			if tc.panelVisible {
				configureTerminalPanel(t, p, tc.panelAxis)
			}

			leaf, ok := p.terminalLeafBox()
			if !ok {
				t.Fatal("terminal leaf was not placed")
			}
			content, ok := p.previewContentBox()
			if !ok {
				t.Fatal("preview content box is not placeable")
			}
			if !tc.panelVisible && leaf != content {
				t.Fatalf("single terminal leaf = %+v, want preview content box %+v", leaf, content)
			}
			if tc.panelVisible && (leaf.W > content.W && leaf.H > content.H) {
				t.Fatalf("terminal leaf %+v did not give the panel room inside %+v", leaf, content)
			}
			previewW, previewH := p.calculatePreviewDimensions()
			if previewW != leaf.W || previewH != leaf.H-terminalHeaderRows {
				t.Fatalf("preview dimensions = %dx%d, want leaf viewport %dx%d", previewW, previewH, leaf.W, leaf.H-terminalHeaderRows)
			}

			primary := p.terminalSurfaceGeometry(false)
			if !primary.OK || primary.X != leaf.X || primary.HeaderY != leaf.Y || primary.Y != leaf.Y+terminalHeaderRows {
				t.Fatalf("primary surface = %+v, want origin from leaf %+v", primary, leaf)
			}
			agentW, agentH := p.calculateAgentPaneDimensions()
			if primary.Width != agentW || primary.Height != agentH {
				t.Fatalf("primary surface size = %dx%d, agent sizing helper = %dx%d", primary.Width, primary.Height, agentW, agentH)
			}

			panel := p.terminalSurfaceGeometry(true)
			if !tc.panelVisible {
				if panel.OK {
					t.Fatalf("hidden terminal panel unexpectedly placed: %+v", panel)
				}
				return
			}
			panelW, panelH, panelOK := p.calculateTermPanelDimensions()
			if !panel.OK || !panelOK || panel.Width != panelW || panel.Height != panelH {
				t.Fatalf("panel surface = %+v, sizing helper = %dx%d ok=%v", panel, panelW, panelH, panelOK)
			}
			// The panel is its own leaf, so it sits past the terminal leaf's
			// chrome and the tree divider — the frame's arithmetic, not a walk
			// of this test's own. What has to hold is that it is beyond the
			// terminal leaf on the split's axis and nowhere near it on the other.
			if tc.panelAxis == SplitCols {
				if panel.X <= leaf.X+leaf.W || panel.HeaderY != leaf.Y {
					t.Fatalf("right panel surface = %+v, leaf = %+v, agent width = %d", panel, leaf, agentW)
				}
			} else if panel.X != leaf.X || panel.HeaderY <= leaf.Y+leaf.H {
				t.Fatalf("bottom panel surface = %+v, leaf = %+v, agent height = %d", panel, leaf, agentH)
			}
		})
	}
}

func TestInitRebuildsFeatureGatedPaneTreeState(t *testing.T) {
	cfg := config.Default()
	cfg.Features.Flags[features.WorkspaceDocPanes.Name] = true
	features.Init(cfg)
	t.Cleanup(func() { features.Init(config.Default()) })

	p := New()
	p.paneRoot = &PaneNode{ID: 99, Kind: PaneDoc, ContentID: 7}
	p.paneFocus = 99
	p.paneNextID = 100
	if err := p.Init(&plugin.Context{Config: cfg, Epoch: 1}); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if p.paneRoot == nil || p.paneRoot.Split != nil || p.paneRoot.Kind != PaneTerminal || p.paneRoot.ID != 1 {
		t.Fatalf("Init pane root = %+v, want fresh terminal leaf 1", p.paneRoot)
	}
	if p.paneFocus != 1 || p.paneNextID != 2 {
		t.Fatalf("Init pane identity state = focus %d next %d, want 1/2", p.paneFocus, p.paneNextID)
	}

	cfg.Features.Flags[features.WorkspaceDocPanes.Name] = false
	cfg.Features.Flags[features.WorkspaceTerminalPanel.Name] = false
	features.Init(cfg)
	if err := p.Init(&plugin.Context{Config: cfg, Epoch: 2}); err != nil {
		t.Fatalf("disabled Init: %v", err)
	}
	if p.paneRoot != nil || p.paneFocus != 0 || p.paneNextID != 1 {
		t.Fatalf("disabled Init retained pane state: root=%+v focus=%d next=%d", p.paneRoot, p.paneFocus, p.paneNextID)
	}
}
