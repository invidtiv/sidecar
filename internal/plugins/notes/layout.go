package notes

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/ui"
)

// editorLayout is the one geometry contract for the built-in note body.
// Preview and textarea consume the same numbers so entering edit changes
// the cursor, not the frame.
type editorLayout struct {
	wrapColumn    int // cells of note text
	leftMargin    int // reserved columns before text (0: no gutter)
	contentHeight int // body rows under the status header
	scrollbarCol  int // 0-based column of the scrollbar in the inner pane
	statusRow     int // 0-based inner-pane row of the status header
	innerWidth    int // full inner width of the editor pane
}

const (
	paneChromeX      = 4 // left/right border + padding
	paneChromeY      = 2 // top/bottom border
	editorStatusRows = 1
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
	contentHeight := innerHeight - editorStatusRows
	if contentHeight < 1 {
		contentHeight = 1
	}

	leftMargin := 0
	scrollbarCol := innerWidth - scrollbarWidth
	if scrollbarCol < 0 {
		scrollbarCol = 0
	}
	wrapColumn := innerWidth - leftMargin - scrollbarWidth
	if wrapColumn < 1 {
		wrapColumn = 1
	}

	return editorLayout{
		wrapColumn:    wrapColumn,
		leftMargin:    leftMargin,
		contentHeight: contentHeight,
		scrollbarCol:  scrollbarCol,
		statusRow:     statusRow,
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

// trackTextareaScroll keeps previewScrollOff in the same source-line
// viewport editorLayout describes, so mouse regions and the scrollbar
// stay honest while the textarea has focus.
func (p *Plugin) trackTextareaScroll() {
	if p.width == 0 || p.height == 0 {
		return
	}
	l := p.editorLayout()
	height := l.contentHeight
	if height < 1 {
		height = 1
	}
	cursorLine := p.editorTextarea.Line()
	if cursorLine < p.previewScrollOff {
		p.previewScrollOff = cursorLine
	}
	if cursorLine >= p.previewScrollOff+height {
		p.previewScrollOff = cursorLine - height + 1
	}
	if p.previewScrollOff < 0 {
		p.previewScrollOff = 0
	}
}

// editorScrollbar draws the shared body scrollbar from the same
// source-line viewport preview and edit use.
func (p *Plugin) editorScrollbar(l editorLayout) string {
	visible := l.contentHeight
	if visible < 1 {
		visible = 1
	}
	total := len(p.previewLines)
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
