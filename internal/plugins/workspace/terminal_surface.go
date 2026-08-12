package workspace

import (
	"strings"
	"time"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
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
	// the row immediately below it.
	terminalHeaderRows = 1

	// previewTabRows is the standalone tab row plus its blank spacer, which only
	// the non-terminal preview tabs (Diff, Task) still render: on the Output tab
	// the tab chips are the left region of the terminal's own header row.
	previewTabRows = 2

	// headerChipGap is the columns between two chips, and the minimum gap between
	// the header's left and right regions.
	headerChipGap = 1

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
// recompute this; they now all read it from previewSplitFor.
type previewSplit struct {
	SidebarWidth        int // outer width of the sidebar panel; 0 when hidden
	SidebarContentWidth int // sidebar width inside border + padding
	PreviewX            int // outer x of the preview panel
	PreviewWidth        int // outer width of the preview panel
	ContentX            int // x of the preview panel's first content column
	ContentWidth        int // preview width inside border + padding
}

// previewSplitFor computes the split for an explicit viewport width. The render
// path passes the width it was handed; everything else uses previewSplit.
func (p *Plugin) previewSplitFor(width int) previewSplit {
	if !p.sidebarVisible {
		return previewSplit{
			PreviewWidth: width,
			ContentX:     previewContentInset,
			ContentWidth: width - panelOverhead,
		}
	}

	// RenderPanel handles borders internally, so only the divider comes off the
	// available space here.
	available := width - dividerWidth
	sidebarW := (available * p.sidebarWidth) / 100
	if sidebarW < sidebarMinWidth {
		sidebarW = sidebarMinWidth
	}
	if sidebarW > available-previewMinWidth {
		sidebarW = available - previewMinWidth
	}
	previewW := available - sidebarW
	if previewW < previewMinWidth {
		previewW = previewMinWidth
	}
	previewX := sidebarW + dividerWidth
	return previewSplit{
		SidebarWidth:        sidebarW,
		SidebarContentWidth: sidebarW - panelOverhead,
		PreviewX:            previewX,
		PreviewWidth:        previewW,
		ContentX:            previewX + previewContentInset,
		ContentWidth:        previewW - panelOverhead,
	}
}

// previewSplit computes the split for the plugin's current width.
func (p *Plugin) previewSplit() previewSplit {
	return p.previewSplitFor(p.width)
}

// previewFlashActive reports whether the attach flash is up. It tints the panel
// border and adds a hint to the terminal header's right region; it never adds a
// row, so no geometry depends on it.
func (p *Plugin) previewFlashActive() bool {
	return !p.flashPreviewTime.IsZero() && time.Since(p.flashPreviewTime) < flashDuration
}

// previewContentY is the plugin-local row of the preview pane's first content
// row. Terminal surfaces begin immediately under the panel border, whatever the
// selection kind: their header is the terminal's own first row.
func (p *Plugin) previewContentY() int {
	return previewBorderRows
}

// terminalHeaderRow composes the one row above an embedded terminal: identity
// chips on the left, hints right-aligned on the right.
//
// Truncation is deliberately asymmetric. The chips say what the surface is and
// carry the only hit regions on the row, so they are kept whole — a chip either
// fits or is dropped entirely, never clipped mid-chip — while the hints are
// advisory and give way first. The result is always exactly one row and never
// wider than width, so the terminal below can never lose a row to overflow.
//
// hintFloor inverts that priority for the columns it names: the right region
// keeps at least that many, dropping chips to find them. It exists because the
// rule is backwards for interactive mode, where the chips are decoration and
// the key that gets the user back out is not.
//
// truncate is passed in rather than reached for so this stays a plain function
// over strings: the caller supplies the plugin's ANSI-aware truncation cache.
func terminalHeaderRow(chips []string, hints string, width, hintFloor int, truncate func(string, int) string) string {
	if width <= 0 {
		return ""
	}
	if hints == "" {
		hintFloor = 0
	}

	var left strings.Builder
	leftWidth := 0
	for i, placement := range layoutHeaderChips(chips, width, hintFloor) {
		if !placement.Drawn {
			continue
		}
		if leftWidth > 0 {
			left.WriteString(strings.Repeat(" ", headerChipGap))
		}
		left.WriteString(chips[i])
		leftWidth = placement.Col + placement.Width
	}

	if hints == "" {
		return left.String()
	}
	available := width - leftWidth - headerChipGap
	if available < 1 {
		return left.String()
	}
	hints = truncate(hints, available)
	gap := width - leftWidth - ansi.StringWidth(hints)
	if gap < headerChipGap {
		// truncate reported a narrower string than it produced; keep the row
		// exactly one row wide rather than risk a wrap.
		return left.String()
	}
	return left.String() + strings.Repeat(" ", gap) + hints
}

// headerChipPlacement is where one chip landed on a header row: Col is its
// first column, relative to the row's own first column, and Drawn is false for
// a chip the row had no columns for.
type headerChipPlacement struct {
	Col   int
	Width int
	Drawn bool
}

// layoutHeaderChips places chips left to right in the columns a header row can
// give them, dropping whole chips rather than clipping one. It is the single
// authority on which chips a row drew and where: terminalHeaderRow renders from
// it and the tab hit regions are registered from it, so a chip dropped for want
// of columns cannot keep a live click target — which it did, on top of the
// interactive exit hint that took its columns.
//
// hintFloor is the columns the row's right region has claimed; zero leaves the
// chips the whole width.
func layoutHeaderChips(chips []string, width, hintFloor int) []headerChipPlacement {
	placements := make([]headerChipPlacement, len(chips))
	if width <= 0 {
		return placements
	}
	budget := width
	if hintFloor > 0 {
		budget = max(width-hintFloor-headerChipGap, 0)
	}

	used := 0
	for i, chip := range chips {
		if chip == "" {
			continue
		}
		chipWidth := ansi.StringWidth(chip)
		col, cost := used, chipWidth
		if used > 0 {
			col += headerChipGap
			cost += headerChipGap
		}
		if used+cost > budget {
			break
		}
		placements[i] = headerChipPlacement{Col: col, Width: chipWidth, Drawn: true}
		used += cost
	}
	return placements
}

// terminalSurface locates one embedded terminal inside the plugin's viewport
// and reports the size of its viewport.
type terminalSurface struct {
	X int // plugin-local column of the terminal's first content column
	Y int // plugin-local row of the terminal's first content row
	// HeaderY is the row the surface's chips-and-hints header is drawn on, one
	// above Y.
	HeaderY int
	Width   int // terminal content columns
	Height  int // terminal content rows, header row excluded
	OK      bool
}

// previewContentBox is the preview panel's inner box in plugin-local
// coordinates. It includes the header row owned by each pane leaf and excludes
// the preview panel's border and padding. Pane-tree layout starts here so the
// tree, terminal sizers, renderers, cursor and hit testing share one authority.
func (p *Plugin) previewContentBox() (Box, bool) {
	if p.width <= 0 || p.height <= 0 {
		return Box{}, false
	}
	split := p.previewSplit()
	return Box{
		X: split.ContentX,
		Y: p.previewContentY(),
		W: split.ContentWidth,
		H: p.height - panelBorderWidth,
	}, true
}

// terminalLeafBox returns the pane-tree box hosting the primary terminal. The
// box includes its one-row header; the optional terminal panel is nested inside
// this leaf and therefore does not add pane-tree chrome.
//
// With the feature disabled paneRoot is nil, and the legacy terminal is the
// preview-content box itself. This keeps the seam load-bearing without changing
// any rendered bytes in the single-terminal journey.
func (p *Plugin) terminalLeafBox() (Box, bool) {
	content, ok := p.previewContentBox()
	if !ok || p.paneRoot == nil {
		return content, ok
	}

	floors := Floors{
		Terminal: PaneFloor{Width: termPanelMinBoxCols, Height: termPanelMinBoxRows},
		Doc:      PaneFloor{Width: markdown.MinWidthForMarkdown, Height: termPanelMinBoxRows},
	}
	leaves, _, fits := LayoutPanes(p.paneRoot, content, floors)
	if !fits {
		// A tree that cannot satisfy every leaf floor renders only its focused
		// leaf in the full content box. Phase 1's only reachable tree is the
		// single terminal, but keeping the fallback here prevents narrow windows
		// from escaping the geometry authority when document leaves arrive.
		if focused := FindPane(p.paneRoot, p.paneFocus); focused != nil && focused.Split == nil && focused.Kind == PaneTerminal {
			return content, true
		}
		return Box{}, false
	}
	for _, placement := range leaves {
		if placement.Node != nil && placement.Node.Kind == PaneTerminal {
			return placement.Box, true
		}
	}
	return Box{}, false
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
	// a subdivision within this box, not extra pane-tree chrome.
	x := leaf.X
	headerY := leaf.Y

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
// selectionAnchored is a parameter rather than something read here so the two
// callers that mean something different by it can say so: the three live paths
// (render, cursor, hit test) all pass selectionTermPanel && anchor-valid, while
// tests exercise the branches directly.
func (p *Plugin) terminalScrollState(termPanel, selectionAnchored bool) (follow bool, offset int, offsetFromBottom bool) {
	if !termPanel {
		return p.autoScrollOutput, p.previewOffset, false
	}
	if selectionAnchored {
		return false, p.termPanelSelectionOffset, false
	}
	return p.termPanelScroll == 0, p.termPanelScroll, true
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
