package notes

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/charmbracelet/x/cellbuf"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// calculatePaneWidths sets the list and editor pane widths.
func (p *Plugin) calculatePaneWidths() {
	available := p.width - dividerWidth
	if p.listWidth == 0 {
		p.listWidth = available * 30 / 100
	}

	// Clamp listWidth to valid bounds
	minWidth := 20
	maxWidth := available - 40
	if maxWidth < minWidth {
		maxWidth = minWidth
	}
	if p.listWidth < minWidth {
		p.listWidth = minWidth
	} else if p.listWidth > maxWidth {
		p.listWidth = maxWidth
	}
}

// renderView renders the full plugin view.
func (p *Plugin) renderView() string {
	if p.store == nil {
		return p.renderInitMessage()
	}
	if p.loading {
		return p.renderLoading()
	}
	if p.loadErr != nil {
		return p.renderError()
	}

	// Calculate layout dimensions
	contentHeight := p.height
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Register mouse regions for click detection
	p.registerMouseRegions()

	// Render two-pane layout
	return p.renderTwoPaneLayout(contentHeight)
}

// renderTwoPaneLayout renders the list and editor panes side by side.
func (p *Plugin) renderTwoPaneLayout(height int) string {
	p.calculatePaneWidths()

	// Pane height for panels (outer dimensions including borders)
	paneHeight := height
	if paneHeight < 4 {
		paneHeight = 4
	}

	// Inner content height (excluding borders)
	innerHeight := paneHeight - 2
	if innerHeight < 1 {
		innerHeight = 1
	}

	// Determine if panes are active
	listActive := p.activePane == PaneList && !p.searchMode
	editorActive := p.activePane == PaneEditor

	// Calculate editor width
	editorWidth := p.width - p.listWidth - dividerWidth

	// Render pane contents
	listContent := p.renderListPane(innerHeight)
	editorContent := p.renderEditorPane(innerHeight, editorWidth-4) // -4 for borders (2) and padding (2)

	// Apply panel styles
	leftPane := styles.RenderPanel(listContent, p.listWidth, paneHeight, listActive)
	rightPane := styles.RenderPanel(editorContent, editorWidth, paneHeight, editorActive)

	dragging := p.mouseHandler != nil && p.mouseHandler.IsDragging() && p.mouseHandler.DragRegion() == regionDivider
	divider := ui.RenderHandle(paneHeight, true, ui.HandleStateFrom(p.hoverDivider, dragging))

	// Join panes horizontally
	return lipgloss.JoinHorizontal(lipgloss.Top, leftPane, divider, rightPane)
}

// renderListPane renders the list pane content (without borders).
func (p *Plugin) renderListPane(height int) string {
	var sb strings.Builder

	// Get display notes (filtered or all)
	displayNotes := p.getDisplayNotes()
	noteCount := len(displayNotes)
	totalCount := len(p.notes)

	// Header: "Notes (filter)" with count
	sb.WriteString(styles.Title.Render("Notes"))

	// Show filter indicator
	filterLabel := p.viewFilter.String()
	sb.WriteString(styles.Muted.Render(" [" + filterLabel + "]"))

	// Show count
	if p.searchQuery != "" {
		sb.WriteString(styles.Muted.Render(fmt.Sprintf(" (%d/%d)", noteCount, totalCount)))
	} else {
		sb.WriteString(styles.Muted.Render(fmt.Sprintf(" (%d)", noteCount)))
	}
	sb.WriteString("\n")

	headerLines := 1

	// Search input line (if in search mode or has query)
	if p.searchMode || p.searchQuery != "" {
		sb.WriteString(p.renderSearchInput())
		sb.WriteString("\n")
		headerLines++
	}

	contentHeight := height - headerLines
	if contentHeight < 1 {
		contentHeight = 1
	}

	// Empty state
	if noteCount == 0 {
		if p.searchQuery != "" {
			// No matches for search - show create prompt
			sb.WriteString("\n")
			sb.WriteString(styles.Muted.Render("No matches"))
			sb.WriteString("\n\n")
			sb.WriteString(styles.Subtle.Render("Press "))
			sb.WriteString(styles.Code.Render("Enter"))
			sb.WriteString(styles.Subtle.Render(" to create"))
		} else {
			sb.WriteString("\n")
			sb.WriteString(styles.Muted.Render("No notes"))
			sb.WriteString("\n")
			sb.WriteString(styles.Subtle.Render("n=new"))
		}
		return sb.String()
	}

	// Calculate visible range with scroll offset
	p.ensureCursorVisibleForList(contentHeight, noteCount)
	start := p.scrollOff
	end := start + contentHeight
	if end > noteCount {
		end = noteCount
	}

	listInner := p.listWidth - paneChromeX
	if listInner < 1 {
		listInner = 1
	}
	bodyWidth := listInner - scrollbarWidth
	if bodyWidth < 10 {
		bodyWidth = 10
	}

	var body strings.Builder
	for i := start; i < end; i++ {
		note := displayNotes[i]
		isSelected := i == p.cursor
		body.WriteString(p.renderNoteRow(note, isSelected, bodyWidth))
		if i < end-1 {
			body.WriteString("\n")
		}
	}

	bar := ui.RenderScrollbar(ui.ScrollbarParams{
		TotalItems:   noteCount,
		ScrollOffset: p.scrollOff,
		VisibleItems: contentHeight,
		TrackHeight:  contentHeight,
	})
	sb.WriteString(attachScrollbar(body.String(), bar, bodyWidth, contentHeight))
	return sb.String()
}

// renderEditorPane renders the editor pane content (without borders).
func (p *Plugin) renderEditorPane(height, width int) string {
	// Render inline editor if active
	if p.inlineEditMode && p.inlineEditor != nil {
		return p.renderInlineEditorContent(height)
	}

	// No note selected - show placeholder
	if p.editorNote == nil {
		return p.renderEditorPlaceholder(height)
	}

	var sb strings.Builder

	l := p.editorLayout()
	sb.WriteString(p.renderEditorStatusHeader(l.innerWidth))
	sb.WriteString("\n")

	bar := p.editorScrollbar(l)
	if p.previewMode {
		body := p.renderViewSurface(l.contentHeight)
		sb.WriteString(attachScrollbar(body, bar, l.wrapColumn, l.contentHeight))
	} else {
		p.editorTextarea.SetWidth(l.wrapColumn)
		p.editorTextarea.SetHeight(l.contentHeight)
		body := p.editorTextarea.View()
		if p.selection.HasSelection() {
			body = p.overlaySelectionOnEditor(body)
		}
		sb.WriteString(attachScrollbar(body, bar, l.wrapColumn, l.contentHeight))
	}

	return sb.String()
}

// renderViewSurface draws the current mapped view (glamour or wrapped raw)
// with no gutter and no '~' filler. Height is the content-row count from
// editorLayout. Visual rows are already wrapped to wrapColumn.
func (p *Plugin) renderViewSurface(height int) string {
	p.ensureViewSurface()
	p.clampPreviewScroll()

	lines := p.viewSurface.Lines
	if len(lines) == 0 {
		lines = []string{""}
	}

	start := p.previewScrollOff
	if start < 0 {
		start = 0
	}
	end := start + height
	if end > len(lines) {
		end = len(lines)
	}

	var sb strings.Builder
	for i := start; i < end; i++ {
		line := lines[i]
		if p.selection.HasSelection() && p.selection.IsLineSelected(i) {
			startCol, endCol := p.selection.GetLineSelectionCols(i)
			line = ui.InjectCharacterRangeBackground(line, startCol, endCol)
			sb.WriteString(line)
		} else if p.markdownView {
			sb.WriteString(line)
		} else {
			sb.WriteString(styles.Body.Render(line))
		}
		if i < end-1 {
			sb.WriteString("\n")
		}
	}
	return sb.String()
}

// renderPreviewContent draws the current view surface. Tests that want a
// specific wrap/render mode should set markdownView / previewLines first.
func (p *Plugin) renderPreviewContent(height, width int) string {
	_ = width
	return p.renderViewSurface(height)
}

// truncatePreviewLine cuts to wrapWidth cells without splitting a rune.
func truncatePreviewLine(line string, wrapWidth int) string {
	if wrapWidth < 1 {
		return ""
	}
	if ansi.StringWidth(line) <= wrapWidth {
		return line
	}
	return ansi.Truncate(line, wrapWidth, ">")
}

// overlaySelectionOnEditor paints the current exclusive source selection
// onto the textarea surface. Visual rows are remapped through the same
// wrap policy the textarea uses; syntax spans stay deferred.
func (p *Plugin) overlaySelectionOnEditor(view string) string {
	if !p.hasEditSelection() {
		return view
	}
	start, end := orderSrc(srcFromPoint(p.selection.Start), srcFromPoint(p.selection.End))
	raw := markdown.MapWrappedSource(p.editorTextarea.Value(), p.editorLayout().wrapColumn)
	return overlayExclusiveOnView(view, raw, start, end, p.editorTextarea.Value(), p.editorTextarea.ScrollYOffset())
}

// wrapEditorLine wraps a single line to width using plain-text breakpoints,
// preserving ANSI styling on the wrapped segments.
func (p *Plugin) wrapEditorLine(line string, width int) []string {
	if width < 1 {
		return []string{""}
	}

	expanded := ui.ExpandTabs(line, 8)
	plain := ansi.Strip(expanded)

	// If line fits, return as-is
	if ansi.StringWidth(plain) <= width {
		return []string{expanded}
	}

	wrappedPlain := cellbuf.Wrap(plain, width, "")
	plainSegments := strings.Split(wrappedPlain, "\n")

	wrapped := make([]string, 0, len(plainSegments))
	offset := 0
	for _, seg := range plainSegments {
		segWidth := ansi.StringWidth(seg)
		if segWidth == 0 {
			wrapped = append(wrapped, "")
			continue
		}
		slice := ansi.TruncateLeft(expanded, offset, "")
		slice = ansi.Truncate(slice, segWidth, "")
		wrapped = append(wrapped, slice)
		offset += segWidth
	}

	return wrapped
}

// renderEditorStatusHeader renders the persistent status header line.
// Left: save state indicator, Right: created/updated timestamps. Timestamp
// detail degrades before the actionable save state when the pane narrows.
func (p *Plugin) renderEditorStatusHeader(width int) string {
	if p.editorNote == nil {
		return ""
	}
	if width <= 0 {
		return ""
	}

	// Left side: save state + optional preview indicator
	var leftText string
	if p.editorDirty {
		leftText = "Unsaved*"
	} else {
		leftText = "Saved"
	}
	if p.previewMode {
		if p.markdownView {
			leftText += " [md]"
		} else {
			leftText += " [raw]"
		}
	}

	leftText = ansi.Truncate(leftText, width, "...")
	leftPart := styles.Muted.Render(leftText)
	if p.editorDirty {
		leftPart = styles.StatusModified.Render(leftText)
	}
	leftWidth := lipgloss.Width(leftPart)

	createdStr := p.editorNote.CreatedAt.Format("Jan 2, 2006")
	updatedStr := p.editorNote.UpdatedAt.Format("Jan 2, 2006")
	rightCandidates := []string{
		fmt.Sprintf("Created: %s | Updated: %s", createdStr, updatedStr),
		fmt.Sprintf("Updated: %s", updatedStr),
	}
	for _, rightText := range rightCandidates {
		rightPart := styles.Muted.Render(rightText)
		rightWidth := lipgloss.Width(rightPart)
		if leftWidth+1+rightWidth > width {
			continue
		}
		return leftPart + strings.Repeat(" ", width-leftWidth-rightWidth) + rightPart
	}

	// Save state is actionable; retain it when timestamp metadata cannot fit.
	return padToWidth(leftPart, width)
}

// renderEditorPlaceholder shows when no note is selected.
func (p *Plugin) renderEditorPlaceholder(height int) string {
	var sb strings.Builder
	sb.WriteString(styles.Muted.Render("No note selected"))
	sb.WriteString("\n\n")
	sb.WriteString(styles.Subtle.Render("Select a note from the list"))
	sb.WriteString("\n")
	sb.WriteString(styles.Subtle.Render("or press "))
	sb.WriteString(styles.Code.Render("n"))
	sb.WriteString(styles.Subtle.Render(" to create new"))
	return sb.String()
}

// previewMaxScroll returns the largest previewScrollOff that still fills the
// viewport. Both modes use soft-wrapped visual-row offsets.
func (p *Plugin) previewMaxScroll(viewHeight, viewWidth int) int {
	if viewHeight < 1 {
		return 0
	}
	if p.previewMode {
		p.ensureViewSurface()
		n := len(p.viewSurface.Lines)
		if n <= viewHeight {
			return 0
		}
		return n - viewHeight
	}
	raw := markdown.MapWrappedSource(p.editorTextarea.Value(), viewWidth)
	if len(raw.Lines) <= viewHeight {
		return 0
	}
	return len(raw.Lines) - viewHeight
}

// clampPreviewScroll keeps the view offset in range without moving it to
// follow the reading cursor. Paint and wheel must use this; snapping to
// the cursor here would undo a wheel that did not also move the cursor.
func (p *Plugin) clampPreviewScroll() {
	height, width := p.previewViewport()
	if p.previewScrollOff < 0 {
		p.previewScrollOff = 0
	}
	maxScroll := p.previewMaxScroll(height, width)
	if p.previewScrollOff > maxScroll {
		p.previewScrollOff = maxScroll
	}
}

// keepPreviewCursorInView moves the reading cursor into the current
// viewport. Wheel uses this so Enter/i/paste stay tied to what is on
// screen, without pulling the viewport back to an old cursor.
func (p *Plugin) keepPreviewCursorInView() {
	height, _ := p.previewViewport()
	if height < 1 {
		height = 1
	}
	n := len(p.previewLines)
	if p.previewMode {
		p.ensureViewSurface()
		n = len(p.viewSurface.Lines)
	}
	if n < 1 {
		p.previewCursorLine = 0
		return
	}
	if p.previewCursorLine < p.previewScrollOff {
		p.previewCursorLine = p.previewScrollOff
	}
	last := p.previewScrollOff + height - 1
	if last >= n {
		last = n - 1
	}
	if p.previewCursorLine > last {
		p.previewCursorLine = last
	}
	if p.previewCursorLine < 0 {
		p.previewCursorLine = 0
	}
}

// ensurePreviewCursorVisibleWithHeight adjusts preview scroll offset for given
// viewport dimensions.
func (p *Plugin) ensurePreviewCursorVisibleWithHeight(viewHeight, viewWidth int) {
	n := len(p.previewLines)
	if p.previewMode {
		p.ensureViewSurface()
		n = len(p.viewSurface.Lines)
	}
	if n == 0 {
		return
	}
	if p.previewCursorLine < 0 {
		p.previewCursorLine = 0
	}
	if p.previewCursorLine >= n {
		p.previewCursorLine = n - 1
	}
	if p.previewCursorLine < p.previewScrollOff {
		p.previewScrollOff = p.previewCursorLine
	}
	if p.previewCursorLine >= p.previewScrollOff+viewHeight {
		p.previewScrollOff = p.previewCursorLine - viewHeight + 1
	}
	if p.previewScrollOff < 0 {
		p.previewScrollOff = 0
	}
	maxScroll := p.previewMaxScroll(viewHeight, viewWidth)
	if p.previewScrollOff > maxScroll {
		p.previewScrollOff = maxScroll
	}
}

// renderInitMessage shows when td is not initialized.
func (p *Plugin) renderInitMessage() string {
	var sb strings.Builder
	sb.WriteString(styles.Title.Render("Notes"))
	sb.WriteString("\n\n")
	sb.WriteString(styles.Muted.Render("Notes plugin requires td initialization."))
	sb.WriteString("\n")
	sb.WriteString(styles.Code.Render("Run 'td init' in this project."))
	return sb.String()
}

// renderLoading shows a loading indicator.
func (p *Plugin) renderLoading() string {
	var sb strings.Builder
	sb.WriteString(styles.Title.Render("Notes"))
	sb.WriteString("\n\n")
	sb.WriteString(styles.Muted.Render("Loading notes..."))
	return sb.String()
}

// renderError shows an error message.
func (p *Plugin) renderError() string {
	var sb strings.Builder
	sb.WriteString(styles.Title.Render("Notes"))
	sb.WriteString("\n\n")
	sb.WriteString(styles.StatusDeleted.Render("Error: "))
	sb.WriteString(styles.Muted.Render(p.loadErr.Error()))
	return sb.String()
}

// renderSearchInput renders the search input line.
func (p *Plugin) renderSearchInput() string {
	var sb strings.Builder

	// Prefix: "/" to indicate search mode
	sb.WriteString(styles.Muted.Render("/"))

	// Query text
	sb.WriteString(styles.Body.Render(p.searchQuery))

	// Cursor (only when in search mode)
	if p.searchMode {
		sb.WriteString(styles.ListCursor.Render("_"))
	}

	return sb.String()
}

// Note status icon constants
const (
	iconArchived = "\u25cb" // White circle for archived
	iconDeleted  = "\u00d7" // Multiplication sign (x) for deleted
)

// renderNoteRow renders a single note row.
// Active notes show just the title; archived/deleted notes show icon + title.
func (p *Plugin) renderNoteRow(note Note, selected bool, maxWidth int) string {
	var prefix strings.Builder

	// Status icon only for archived/deleted notes (no placeholder for active)
	hasStatusIcon := note.DeletedAt != nil || note.Archived
	if note.DeletedAt != nil {
		prefix.WriteString(styles.StatusDeletedNote.Render(iconDeleted))
		prefix.WriteString(" ")
	} else if note.Archived {
		prefix.WriteString(styles.StatusArchived.Render(iconArchived))
		prefix.WriteString(" ")
	}

	// Cursor indicator
	if selected {
		prefix.WriteString(styles.ListCursor.Render("> "))
	} else {
		prefix.WriteString("  ")
	}

	// Pin badge
	if note.Pinned {
		prefix.WriteString(styles.StatusModified.Render("* "))
	}

	prefixStr := prefix.String()
	prefixLen := lipgloss.Width(prefixStr)

	// Calculate available width for title
	titleWidth := maxWidth - prefixLen
	if titleWidth < 10 {
		titleWidth = 10
	}

	// Get title (first line of content, or "untitled" if empty)
	title := note.Title
	if title == "" {
		// Use first line of content as title
		lines := strings.SplitN(note.Content, "\n", 2)
		if len(lines) > 0 && strings.TrimSpace(lines[0]) != "" {
			title = strings.TrimSpace(lines[0])
		} else {
			title = "untitled"
		}
	}

	// Truncate by terminal cells; byte slicing can split Unicode titles.
	if ansi.StringWidth(title) > maxTitleLength {
		title = ansi.Truncate(title, maxTitleLength, "...")
	}

	if ansi.StringWidth(title) > titleWidth {
		title = ansi.Truncate(title, titleWidth, "...")
	}

	// Style based on selection
	if selected {
		// For selected rows, use full-width background highlight
		var plainRow string
		if hasStatusIcon {
			if note.DeletedAt != nil {
				plainRow = iconDeleted + " "
			} else if note.Archived {
				plainRow = iconArchived + " "
			}
		}
		plainRow += "  " // cursor space
		if note.Pinned {
			plainRow += "* "
		}
		plainRow += title

		plainRow = padToWidth(plainRow, maxWidth)
		return styles.ListItemSelected.Render(plainRow)
	}

	// Regular row with styled components
	return prefixStr + styles.Body.Render(title)
}

// ensureCursorVisibleForList adjusts scrollOff for a list of given size.
func (p *Plugin) ensureCursorVisibleForList(viewHeight, listSize int) {
	// Clamp cursor to valid range
	if p.cursor < 0 {
		p.cursor = 0
	}
	if listSize > 0 && p.cursor >= listSize {
		p.cursor = listSize - 1
	}

	// Adjust scroll offset to keep cursor in view
	if p.cursor < p.scrollOff {
		p.scrollOff = p.cursor
	}
	if p.cursor >= p.scrollOff+viewHeight {
		p.scrollOff = p.cursor - viewHeight + 1
	}

	// Clamp scroll offset
	if p.scrollOff < 0 {
		p.scrollOff = 0
	}
	maxScroll := listSize - viewHeight
	if maxScroll < 0 {
		maxScroll = 0
	}
	if p.scrollOff > maxScroll {
		p.scrollOff = maxScroll
	}
}
