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

func configureTerminalPanel(p *Plugin, layout TermPanelLayout) {
	p.termPanelVisible = true
	p.termPanelLayout = layout
	p.termPanelSize = 40
	p.termPanelOutput = markerBuffer("PANEL", 4)
}

func TestSingleTerminalPaneTreePreservesLegacyRenderBytes(t *testing.T) {
	tests := []struct {
		name           string
		sidebarVisible bool
		panelVisible   bool
		panelLayout    TermPanelLayout
	}{
		{name: "sidebar visible panel absent", sidebarVisible: true},
		{name: "sidebar hidden panel absent", sidebarVisible: false},
		{name: "sidebar visible panel right", sidebarVisible: true, panelVisible: true, panelLayout: TermPanelRight},
		{name: "sidebar hidden panel right", sidebarVisible: false, panelVisible: true, panelLayout: TermPanelRight},
		{name: "sidebar visible panel bottom", sidebarVisible: true, panelVisible: true, panelLayout: TermPanelBottom},
		{name: "sidebar hidden panel bottom", sidebarVisible: false, panelVisible: true, panelLayout: TermPanelBottom},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			legacy := surfacePlugin(false)
			legacy.sidebarVisible = tc.sidebarVisible
			withTree := surfacePlugin(false)
			withTree.sidebarVisible = tc.sidebarVisible
			enableSingleTerminalTree(withTree)
			if tc.panelVisible {
				configureTerminalPanel(legacy, tc.panelLayout)
				configureTerminalPanel(withTree, tc.panelLayout)
			}

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
		panelLayout    TermPanelLayout
	}{
		{name: "sidebar visible panel absent", sidebarVisible: true},
		{name: "sidebar hidden panel absent", sidebarVisible: false},
		{name: "sidebar visible panel right", sidebarVisible: true, panelVisible: true, panelLayout: TermPanelRight},
		{name: "sidebar hidden panel right", sidebarVisible: false, panelVisible: true, panelLayout: TermPanelRight},
		{name: "sidebar visible panel bottom", sidebarVisible: true, panelVisible: true, panelLayout: TermPanelBottom},
		{name: "sidebar hidden panel bottom", sidebarVisible: false, panelVisible: true, panelLayout: TermPanelBottom},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			p := surfacePlugin(false)
			p.sidebarVisible = tc.sidebarVisible
			enableSingleTerminalTree(p)
			if tc.panelVisible {
				configureTerminalPanel(p, tc.panelLayout)
			}

			leaf, ok := p.terminalLeafBox()
			if !ok {
				t.Fatal("terminal leaf was not placed")
			}
			content, ok := p.previewContentBox()
			if !ok || leaf != content {
				t.Fatalf("single terminal leaf = %+v, want preview content box %+v", leaf, content)
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
			if tc.panelLayout == TermPanelRight {
				if panel.X != leaf.X+agentW+termPanelDividerCols || panel.HeaderY != leaf.Y {
					t.Fatalf("right panel surface = %+v, leaf = %+v, agent width = %d", panel, leaf, agentW)
				}
			} else if panel.X != leaf.X || panel.HeaderY != leaf.Y+terminalHeaderRows+agentH+termPanelDividerRows {
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
	features.Init(cfg)
	if err := p.Init(&plugin.Context{Config: cfg, Epoch: 2}); err != nil {
		t.Fatalf("disabled Init: %v", err)
	}
	if p.paneRoot != nil || p.paneFocus != 0 || p.paneNextID != 1 {
		t.Fatalf("disabled Init retained pane state: root=%+v focus=%d next=%d", p.paneRoot, p.paneFocus, p.paneNextID)
	}
}
