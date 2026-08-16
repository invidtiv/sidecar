package workspace

import (
	"time"

	"github.com/marcus/sidecar/internal/termpreview"
	"github.com/marcus/sidecar/internal/tty"
)

// Every embedded terminal in the preview pane sits in the same vertical stack:
// the panel's top border, one header row, then the terminal viewport down to the
// bottom of the pane. That arithmetic used to be reimplemented with bare 1/2/3
// literals in the render path, the native cursor path, the term-panel hit
// regions, the selection hit test and the pane sizer, all of which had to agree
// by hand — which is exactly why geometry drift here was unfalsifiable
// (td-73fa86). Naming the rows once makes it testable.
const (
	// previewBorderRows is the panel's top border, above all preview content.
	previewBorderRows = 1

	// terminalHeaderRows is the single row every embedded terminal reserves for
	// its identity chips and its hints. It is one row for every surface — shell,
	// worktree and both terminal-panel children — so a terminal always begins on
	// the row immediately below it. The shared presentation layer names the same
	// row, and both consumers read it from there.
	terminalHeaderRows = termpreview.HeaderRows

	// termPanelDividerRows / termPanelDividerCols size the rule drawn between
	// the primary terminal and the terminal panel.
	termPanelDividerRows = 1
	termPanelDividerCols = 1

	// previewContentInset is the columns the panel border and padding consume on
	// the left of the preview pane.
	previewContentInset = panelOverhead / 2

	// sidebarMinWidth / previewMinWidth are the split's floors: the sidebar never
	// shrinks past sidebarMinWidth and never grows so far that the preview pane
	// drops below previewMinWidth.
	sidebarMinWidth = 15
	previewMinWidth = 40
)

// previewSplit is the horizontal split of the plugin's viewport into sidebar,
// divider and preview panel, in plugin-local columns. Seven call sites used to
// recompute this; they now all read it from previewSplitFor, which is itself a
// thin binding of the plugin's chrome constants to the shared split.
type previewSplit = termpreview.Split

// previewSplitFor computes the split for an explicit viewport width. The render
// path passes the width it was handed; everything else uses previewSplit.
//
// The arithmetic moved to internal/termpreview so the global Workspaces browser
// can place its own list and preview with the same rules — the same floors, the
// same divider, the same clamp order — instead of growing a parallel one.
func (p *Plugin) previewSplitFor(width int) previewSplit {
	return termpreview.SplitFor(width, termpreview.SplitConfig{
		SidebarVisible: p.sidebarVisible,
		SidebarPercent: p.sidebarWidth,
		DividerWidth:   dividerWidth,
		PanelOverhead:  panelOverhead,
		ContentInset:   previewContentInset,
		SidebarMin:     sidebarMinWidth,
		PreviewMin:     previewMinWidth,
	})
}

// previewSplit computes the split for the plugin's current width.
func (p *Plugin) previewSplit() previewSplit {
	return p.previewSplitFor(p.width)
}

// previewFlashActive reports whether the attach flash is up. It tints the panel
// border and adds a hint to the terminal header's right region; it never adds a
// row, so no geometry depends on it.
func (p *Plugin) previewFlashActive() bool {
	if !fullTmuxAttachEnabled() {
		return false
	}
	return !p.flashPreviewTime.IsZero() && time.Since(p.flashPreviewTime) < flashDuration
}

// previewContentY is the plugin-local row of the preview pane's first content
// row. Terminal surfaces begin immediately under the panel border, whatever the
// selection kind: their header is the terminal's own first row.
func (p *Plugin) previewContentY() int {
	return previewBorderRows
}

// terminalHeaderRow composes the one row above an embedded terminal: identity
// chips on the left, hints right-aligned on the right. Both the row and the
// chip placement it renders from live in internal/termpreview, so the global
// Workspaces preview draws a header with the same rules — including the
// whole-chip drop — rather than a lookalike.
func terminalHeaderRow(chips []string, hints string, width, hintFloor int, truncate func(string, int) string) string {
	return termpreview.HeaderRow(chips, hints, width, hintFloor, truncate)
}

// headerChipPlacement is where one chip landed on a header row.
type headerChipPlacement = termpreview.ChipPlacement

// layoutHeaderChips places chips left to right in the columns a header row can
// give them, dropping whole chips rather than clipping one. It is the single
// authority on which chips a row drew and where: terminalHeaderRow renders from
// it and the tab hit regions are registered from it, so a chip dropped for want
// of columns cannot keep a live click target — which it did, on top of the
// interactive exit hint that took its columns.
func layoutHeaderChips(chips []string, width, hintFloor int) []headerChipPlacement {
	return termpreview.LayoutChips(chips, width, hintFloor)
}

// terminalSurface locates one embedded terminal inside the plugin's viewport
// and reports the size of its viewport. It is the shared surface type: the
// plugin's pane tree produces the leaf box, and termpreview.SurfaceIn turns a
// box into the header row plus the viewport under it.
type terminalSurface = termpreview.Surface

// leafGeom is one pane-tree leaf's chrome versus its content. Layout places
// Outer; tmux, the cursor, and content hits use Inner.
type leafGeom struct {
	Outer Box // plugin-local, includes border+padding
	Inner Box // plugin-local, content / tmux / cursor / content hits
}

// insetPanelChrome is the content box inside one RenderPanel: two columns of
// border+padding on each side, one border row on the top and bottom.
func insetPanelChrome(outer Box) Box {
	return Box{
		X: outer.X + previewContentInset,
		Y: outer.Y + previewBorderRows,
		W: outer.W - panelOverhead,
		H: outer.H - panelBorderWidth,
	}
}

func leafGeometry(outer Box) leafGeom {
	return leafGeom{Outer: outer, Inner: insetPanelChrome(outer)}
}

// previewPeerBox is the outer preview rectangle in plugin-local coordinates —
// the peer of the sidebar, including the chrome a lone terminal still spends
// once. Multi-leaf layout starts here so each leaf owns its own 4×2 inset
// instead of sharing one outer frame.
func (p *Plugin) previewPeerBox() (Box, bool) {
	if p.width <= 0 || p.height <= 0 {
		return Box{}, false
	}
	split := p.previewSplit()
	return Box{
		X: split.PreviewX,
		Y: 0,
		W: split.PreviewWidth,
		H: p.height,
	}, true
}

// previewLayoutBox is the pane-tree canvas for an explicit size. Production
// passes the live peer's width and height, which makes this previewPeerBox.
// Tests pass a smaller canvas; the origin is still the peer so placement.Box
// stays plugin-local OUTER.
func (p *Plugin) previewLayoutBox(width, height int) Box {
	if peer, ok := p.previewPeerBox(); ok {
		return Box{X: peer.X, Y: peer.Y, W: width, H: height}
	}
	return Box{W: width, H: height}
}

// previewContentBox is the 1-leaf inner: inset(previewPeerBox()). A lone
// terminal still matches today's preview inset. Multi-leaf has no single inner
// canvas — layout and sizers read previewPeerBox / leafGeometry instead.
func (p *Plugin) previewContentBox() (Box, bool) {
	peer, ok := p.previewPeerBox()
	if !ok {
		return Box{}, false
	}
	return insetPanelChrome(peer), true
}

// terminalLeafBox returns the terminal leaf's INNER box: header plus viewport,
// inside that leaf's own chrome. Tmux, the native cursor, and surface hits all
// read this. The optional terminal panel is nested inside this box and does
// not add pane-tree chrome.
//
// With the feature disabled paneRoot is nil, and the legacy terminal is the
// 1-leaf inner itself. This keeps the seam load-bearing without changing any
// rendered bytes in the single-terminal journey.
func (p *Plugin) terminalLeafBox() (Box, bool) {
	peer, ok := p.previewPeerBox()
	if !ok {
		return Box{}, false
	}
	if p.paneRoot == nil {
		return insetPanelChrome(peer), true
	}

	// A box too small for the tree is the layout's own answer — the focused leaf
	// alone in the whole box — so a terminal that is not the zoomed leaf has no
	// box here, exactly as it has no pixels in the split renderer.
	layout, ok := LayoutPaneTree(p.paneRoot, peer, paneTreeFloors(), p.paneFocus)
	if !ok {
		return Box{}, false
	}
	for _, placement := range layout.Leaves {
		if placement.Node != nil && placement.Node.Kind == PaneTerminal {
			return insetPanelChrome(placement.Box), true
		}
	}
	return Box{}, false
}

// leafGeometryFor returns the focused layout's outer/inner pair for one leaf.
func (p *Plugin) leafGeometryFor(leafID int) (leafGeom, bool) {
	peer, ok := p.previewPeerBox()
	if !ok || p.paneRoot == nil {
		return leafGeom{}, false
	}
	layout, laid := LayoutPaneTree(p.paneRoot, peer, paneTreeFloors(), p.paneFocus)
	if !laid {
		return leafGeom{}, false
	}
	for _, placement := range layout.Leaves {
		if placement.Node != nil && placement.Node.ID == leafID {
			return leafGeometry(placement.Box), true
		}
	}
	return leafGeom{}, false
}

// terminalSurfaceGeometry is the single source of truth for where a terminal
// surface is drawn and how big it is. termPanel selects the terminal panel
// child; false selects the primary agent/shell terminal (which occupies the
// whole preview when the panel is hidden).
//
// OK is false when there is nothing to place: no viewport yet, or the terminal
// panel was asked for while hidden. Callers that still need a size in that case
// fall back to calculatePreviewDimensions, which carries the term.GetSize
// fallback for a not-yet-sized plugin.
func (p *Plugin) terminalSurfaceGeometry(termPanel bool) terminalSurface {
	leaf, ok := p.terminalLeafBox()
	if !ok {
		return terminalSurface{}
	}

	// The leaf begins with the surface's own header. Any terminal-panel split is
	// a subdivision within this box, not extra pane-tree chrome. Where the header
	// and the viewport sit inside the box is the shared layer's answer, taken
	// from the box the pane tree placed — one derivation, two consumers.
	placed := termpreview.SurfaceIn(leaf)
	if !placed.OK {
		return terminalSurface{}
	}
	x := placed.X
	headerY := placed.HeaderY

	if termPanel {
		if !p.termPanelVisible {
			return terminalSurface{}
		}
		// A split too small to draw has no panel anywhere on screen, so there is
		// nothing to locate — reporting one would put it past the preview's right
		// edge and take the cursor and the mouse mapping with it.
		width, height, ok := p.calculateTermPanelDimensions()
		if !ok {
			return terminalSurface{}
		}
		if p.termPanelLayout == TermPanelRight {
			outputWidth, _ := p.calculateAgentPaneDimensions()
			x += outputWidth + termPanelDividerCols
		} else {
			// calculateAgentPaneDimensions reports the primary child's terminal
			// rows only, so step over its header row too before the divider.
			_, outputHeight := p.calculateAgentPaneDimensions()
			headerY += terminalHeaderRows + outputHeight + termPanelDividerRows
		}
		return terminalSurface{
			X: x, Y: headerY + terminalHeaderRows, HeaderY: headerY,
			Width: width, Height: height, OK: true,
		}
	}

	width, height := p.calculateAgentPaneDimensions()
	return terminalSurface{
		X: x, Y: headerY + terminalHeaderRows, HeaderY: headerY,
		Width: width, Height: height, OK: true,
	}
}

// terminalScrollState returns the buffer-window inputs the terminal viewport
// needs for a surface. The render path, the native cursor and the hit-test
// layout all have to agree on which window of the buffer is on screen, so they
// share this derivation.
//
// Both surfaces place their window from the live bottom: the scroll is the
// distance back from it and zero means live, so following is derived rather
// than tracked. A window a pointer gesture or a document freeze is holding still
// is placed from an absolute start instead, and that is the shared freeze's
// answer — neither surface carries a frozen flag of its own for the render path
// to translate.
func (p *Plugin) terminalScrollState(termPanel bool) (follow bool, offset int, offsetFromBottom bool) {
	if p.projectedTerminalBuffer(termPanel) != nil {
		return false, 0, false
	}
	freeze, scroll := &p.previewFreeze, p.previewScroll
	if termPanel {
		freeze, scroll = &p.termPanelFreeze, p.termPanelScroll
	}
	placement := tty.PlaceWindow(freeze, scroll)
	return placement.Follow, placement.Offset, placement.FromBottom
}

// interactiveDescribes reports whether the live interactive state is the one
// for this surface: interactive mode is up, still active, and pointed at this
// side of the split. Every path that consults the interactive state for
// geometry asks this, so a stale or inactive state cannot override the
// per-pane cache and a non-focused surface cannot be laid out at the focused
// pane's size.
func (p *Plugin) interactiveDescribes(termPanel bool) bool {
	return p.viewMode == ViewModeInteractive &&
		p.interactiveState != nil &&
		p.interactiveState.Active &&
		p.interactiveState.TermPanel == termPanel
}

// resolvedPaneGeometry returns the tmux pane size a surface should be laid out
// against: the observed per-pane geometry, preferring the fresher copy
// interactive mode carries (td-73fa86). interactive says whether the
// interactive state describes this surface — callers get it from
// interactiveDescribes.
func (p *Plugin) resolvedPaneGeometry(termPanel, interactive bool) (width, height int) {
	if interactive && p.interactiveState != nil {
		width, height = p.interactiveState.PaneWidth, p.interactiveState.PaneHeight
	}
	if width > 0 && height > 0 {
		return width, height
	}
	if geometry := p.paneGeometryFor(termPanel); geometry.known() {
		return geometry.Width, geometry.Height
	}
	return width, height
}
