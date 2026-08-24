package filebrowser

import (
	tea "charm.land/bubbletea/v2"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/plugin"
)

const (
	filesTreeFocusID    = "tree"
	filesPreviewFocusID = "preview"
)

var (
	_ plugin.PaneFocusProvider   = (*Plugin)(nil)
	_ plugin.ContentLinkProvider = (*Plugin)(nil)
)

// PaneFocusStops projects the browser's visible panes in their rendered order.
func (p *Plugin) PaneFocusStops() []plugin.PaneFocusStop {
	stops := make([]plugin.PaneFocusStop, 0, 2)
	if p.treeVisible {
		stops = append(stops, plugin.PaneFocusStop{ID: filesTreeFocusID})
	}
	if p.previewPaneOnScreen() {
		stops = append(stops, plugin.PaneFocusStop{ID: filesPreviewFocusID})
	}
	return stops
}

// PaneFocus returns the stable ID for the browser's existing pane focus.
func (p *Plugin) PaneFocus() string {
	if p.activePane == PanePreview {
		return filesPreviewFocusID
	}
	return filesTreeFocusID
}

// SetPaneFocus directly selects a visible browser pane.
func (p *Plugin) SetPaneFocus(id string) tea.Cmd {
	switch id {
	case filesTreeFocusID:
		if p.treeVisible {
			p.activePane = PaneTree
		}
	case filesPreviewFocusID:
		if p.previewPaneOnScreen() {
			p.activePane = PanePreview
		}
	}
	return nil
}

// SetPaneFocusActive lets the outer deck mute Files' inner active border while
// a passive leaf owns focus. The managed bit preserves the browser's historical
// behavior until a host opts into this capability.
func (p *Plugin) SetPaneFocusActive(active bool) {
	p.paneFocusManaged = true
	p.paneFocusActive = active
}

func (p *Plugin) innerPaneFocusActive() bool {
	return !p.paneFocusManaged || p.paneFocusActive
}

// ContentLinkSurfaces exposes only a loaded preview whose rows can be mapped
// exactly to the rendered frame. Interactive modes and placeholder states
// deliberately opt out for the frame.
func (p *Plugin) ContentLinkSurfaces() []contentlink.Surface {
	if !p.contentLinksSafe() {
		return nil
	}
	rect := p.previewTextRect()
	if rect.W <= 0 || rect.H <= 0 {
		return nil
	}
	return []contentlink.Surface{{
		ID:          filesPreviewFocusID,
		Rect:        rect,
		WorkDir:     p.ctx.WorkDir,
		ProjectRoot: p.ctx.ProjectRoot,
		Kinds: contentlink.NewKindSet(
			contentlink.KindFile,
			contentlink.KindIssue,
			contentlink.KindDiff,
			contentlink.KindResource,
			contentlink.KindURL,
			contentlink.KindInternal,
		),
		ReadOnly: true,
	}}
}

func (p *Plugin) contentLinksSafe() bool {
	if p.ctx == nil || p.width <= 0 || p.height <= 0 || p.previewWidth <= 0 || p.previewFile == "" {
		return false
	}
	if p.edit.Active || p.searchMode || p.contentSearchMode || p.quickOpenMode || p.projectSearchMode ||
		p.infoMode || p.blameMode || p.fileOpMode != FileOpNone || p.lineJumpMode {
		return false
	}
	if p.isImage || p.isBinary || p.previewError != nil || len(p.previewLines) == 0 {
		return false
	}
	// Rendered Markdown is scanned as what was drawn: rows are Glamour's output
	// rows and columns are their visual columns, exactly as Notes and docview
	// already treat their own Glamour frames. No source-row mapping is needed —
	// recognition is column-based on an already-rendered frame. A render mode
	// that has not produced output yet still opts out, because the rows on
	// screen are then a transient state the exported geometry does not describe.
	if p.markdownRenderMode && p.isMarkdownFile() && len(p.markdownRendered) == 0 {
		return false
	}
	return p.activePreviewLoaded()
}

func (p *Plugin) activePreviewLoaded() bool {
	return p.activeTab >= 0 && p.activeTab < len(p.tabs) &&
		p.tabs[p.activeTab].Path == p.previewFile && p.tabs[p.activeTab].Loaded
}

// previewTextRect is the canonical source-preview rectangle. Its X and width
// share the renderer's panel border, padding, gutter, and line-width math; its
// height counts the actual source rows rendered after scrolling and wrapping.
func (p *Plugin) previewTextRect() mouse.Rect {
	gutterWidth, lineWidth := p.previewTextWidths()
	rows := p.previewRenderedRows(lineWidth)
	previewX := 0
	if p.treeVisible {
		previewX = p.treeWidth + dividerWidth
	}
	return mouse.Rect{
		X: previewX + 2 + gutterWidth, // border + panel padding + gutter
		Y: p.inputBarHeight() + 3,     // border + two rendered header rows
		W: lineWidth,
		H: rows,
	}
}

// previewContentWidth is the inner cell width the preview rows are rendered into,
// excluding borders, panel padding, and the scrollbar column.
func (p *Plugin) previewContentWidth() int {
	w := p.previewWidth - 4 - 1
	if w < 1 {
		w = 1
	}
	return w
}

func (p *Plugin) previewTextWidths() (gutterWidth, lineWidth int) {
	gutterWidth = p.previewGutter().Width()
	lineWidth = p.previewContentWidth() - gutterWidth
	if lineWidth < 10 {
		lineWidth = 10
	}
	return gutterWidth, lineWidth
}

func (p *Plugin) previewRenderedRows(lineWidth int) int {
	limit := p.previewSourceRowCapacity()
	if p.isTruncated && limit > 1 {
		// The final visible row belongs to the truncation notice, not the source.
		limit--
	}
	// Bound the walk by the slice actually being drawn, not by the source lines.
	// In render mode previewDisplayLines is Glamour's output, which is longer or
	// shorter than the source; counting source rows while reading rendered ones
	// is what lands a link a row off (or clips the last rows of a long render).
	display := p.previewDisplayLines()
	rows := 0
	for i := p.previewScroll; i < len(display) && rows < limit; i++ {
		lineRows := 1
		if p.previewWrapEnabled {
			lineRows = len(p.wrapPreviewLine(display[i], lineWidth))
			if lineRows < 1 {
				lineRows = 1
			}
		}
		rows += lineRows
	}
	if rows > limit {
		rows = limit
	}
	return rows
}

// previewSourceRowCapacity is the panel height left after two borders and the
// preview's two rendered header rows. Both rendering and exported geometry
// read it so wrapping and truncation cannot expose rows the frame clipped.
func (p *Plugin) previewSourceRowCapacity() int {
	rows := p.paneHeight() - 4
	if rows < 1 {
		return 1
	}
	return rows
}

// previewDisplayLines is the one accessor for "the lines the preview is
// currently drawing" — rendered Glamour rows in render mode, highlighted or raw
// source rows otherwise. Exported geometry (previewRenderedRows) and the gutter
// (previewGutter) both read it through previewRenderLines, so they cannot
// disagree about what is on screen.
func (p *Plugin) previewDisplayLines() []string {
	lines, _ := p.previewRenderLines()
	return lines
}
