package notes

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/ui"
)

// editorLayout is the one geometry contract for the built-in note body.
// Preview and textarea consume the same numbers so entering edit changes
// the cursor, not the frame.
type editorLayout struct {
	wrapColumn    int // cells of note text
	leftMargin    int // reserved columns before note text
	rightMargin   int // reserved columns after the body scrollbar
	contentHeight int // body rows under the status header
	scrollbarCol  int // 0-based column of the scrollbar in the inner pane
	statusRow     int // 0-based inner-pane row of the status header
	contentRow    int // 0-based inner-pane row where note text begins
	innerWidth    int // full inner width of the editor pane
}

const (
	paneChromeX      = 4 // left/right border + padding
	paneChromeY      = 2 // top/bottom border
	editorStatusRows = 1
	editorTopRows    = 1
	editorBottomRows = 1
	editorSideCols   = 1
	scrollbarWidth   = 1
)

// editorLayout returns the built-in editor geometry for the current pane size.
// It does not depend on previewMode.
func (p *Plugin) editorLayout() editorLayout {
	p.calculatePaneWidths()
	paneHeight := p.height
	if paneHeight < 4 {
		paneHeight = 4
	}
	innerHeight := paneHeight - paneChromeY
	if innerHeight < 1 {
		innerHeight = 1
	}
	innerWidth := p.width - p.listWidth - dividerWidth - paneChromeX
	if innerWidth < 1 {
		innerWidth = 1
	}

	statusRow := 0
	contentHeight := innerHeight - editorStatusRows - editorTopRows - editorBottomRows
	if contentHeight < 1 {
		contentHeight = 1
	}

	leftMargin := editorSideCols
	rightMargin := editorSideCols
	scrollbarCol := innerWidth - rightMargin - scrollbarWidth
	if scrollbarCol < 0 {
		scrollbarCol = 0
	}
	wrapColumn := scrollbarCol - leftMargin
	if wrapColumn < 1 {
		wrapColumn = 1
	}

	return editorLayout{
		wrapColumn:    wrapColumn,
		leftMargin:    leftMargin,
		rightMargin:   rightMargin,
		contentHeight: contentHeight,
		scrollbarCol:  scrollbarCol,
		statusRow:     statusRow,
		contentRow:    statusRow + editorStatusRows + editorTopRows,
		innerWidth:    innerWidth,
	}
}

// previewViewport is the wrap-column / content-height pair both the
// preview renderer and the textarea consume via editorLayout.
func (p *Plugin) previewViewport() (height, width int) {
	l := p.editorLayout()
	return l.contentHeight, l.wrapColumn
}

// updateTextareaDimensions sizes the textarea from editorLayout.
func (p *Plugin) updateTextareaDimensions() {
	if p.width == 0 || p.height == 0 {
		return
	}
	l := p.editorLayout()
	p.editorTextarea.SetWidth(l.wrapColumn)
	p.editorTextarea.SetHeight(l.contentHeight)
}

// trackTextareaScroll mirrors the textarea's soft-wrapped visual-row offset so
// mouse mapping and the scrollbar use the same coordinate space as Bubbles.
func (p *Plugin) trackTextareaScroll() {
	if p.width == 0 || p.height == 0 {
		return
	}
	p.previewScrollOff = p.editorTextarea.ScrollYOffset()
}

// editorScrollbar draws the shared body scrollbar from the same
// source-line viewport preview and edit use.
func (p *Plugin) editorScrollbar(l editorLayout) string {
	visible := l.contentHeight
	if visible < 1 {
		visible = 1
	}
	var total int
	if p.previewMode {
		p.ensureViewSurface()
		total = len(p.viewSurface.Lines)
	} else {
		total = len(markdown.MapWrappedSource(p.editorTextarea.Value(), l.wrapColumn).Lines)
	}
	if total < 1 {
		total = 1
	}
	maxScroll := p.previewMaxScroll(visible, l.wrapColumn)
	totalItems := maxScroll + visible
	if totalItems < total {
		totalItems = total
	}
	return ui.RenderScrollbar(ui.ScrollbarParams{
		TotalItems:   totalItems,
		ScrollOffset: p.previewScrollOff,
		VisibleItems: visible,
		TrackHeight:  visible,
	})
}

// attachScrollbar pads body to height×bodyWidth and joins a one-column bar.
func attachScrollbar(body, bar string, bodyWidth, height int) string {
	if height < 1 {
		return body
	}
	if bodyWidth < 1 {
		bodyWidth = 1
	}
	lines := strings.Split(body, "\n")
	padded := make([]string, height)
	for i := 0; i < height; i++ {
		line := ""
		if i < len(lines) {
			line = lines[i]
		}
		padded[i] = padToWidth(line, bodyWidth)
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(padded, "\n"), bar)
}

// insetEditorBody adds the horizontal body padding from editorLayout without
// changing the shared preview/edit viewport. The scrollbar stays beside the
// note text and the right inset remains outside it.
func insetEditorBody(body, bar string, l editorLayout) string {
	core := attachScrollbar(body, bar, l.wrapColumn, l.contentHeight)
	lines := strings.Split(core, "\n")
	left := strings.Repeat(" ", l.leftMargin)
	right := strings.Repeat(" ", l.rightMargin)
	for i := range lines {
		lines[i] = left + lines[i] + right
	}
	return strings.Join(lines, "\n")
}

func padToWidth(line string, width int) string {
	w := ansi.StringWidth(line)
	if w > width {
		return ansi.Truncate(line, width, "")
	}
	if w < width {
		return line + strings.Repeat(" ", width-w)
	}
	return line
}
