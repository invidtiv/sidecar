package palette

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// keyColumnWidth is the fixed width for the key column to ensure clean alignment.
const (
	keyColumnWidth  = 10
	nameColumnWidth = 24
)

// scopeSection renders the context selector / mode indicator.
func (m *Model) scopeSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		var sb strings.Builder
		activeStyle := lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
		mutedStyle := styles.Muted

		ctxName := m.activeContext
		if ctxName == "" {
			ctxName = "global"
		}

		currentLabel := fmt.Sprintf("Context: %s", ctxName)
		allLabel := "All Contexts"

		if m.showAllContexts {
			sb.WriteString(mutedStyle.Render(currentLabel))
			sb.WriteString(mutedStyle.Render("  │  "))
			sb.WriteString(activeStyle.Render(allLabel))
		} else {
			sb.WriteString(activeStyle.Render(currentLabel))
			sb.WriteString(mutedStyle.Render("  │  "))
			sb.WriteString(mutedStyle.Render(allLabel))
		}

		// Trailing hint
		hintStr := styles.Muted.Render("tab to toggle")
		usedWidth := lipgloss.Width(sb.String())
		hintWidth := lipgloss.Width(hintStr)
		gap := contentWidth - usedWidth - hintWidth
		if gap > 1 {
			sb.WriteString(strings.Repeat(" ", gap))
			sb.WriteString(hintStr)
		} else {
			sb.WriteString("  ")
			sb.WriteString(hintStr)
		}

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// countSection renders the command count info.
func (m *Model) countSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		var text string
		if m.textInput.Value() != "" {
			text = fmt.Sprintf("%d of %d commands", len(m.filtered), len(m.allEntries))
		} else if len(m.allEntries) > 0 {
			text = fmt.Sprintf("%d commands", len(m.filtered))
		}
		if text == "" {
			return modal.RenderedSection{Content: ""}
		}
		return modal.RenderedSection{Content: styles.Muted.Render(text)}
	}, nil)
}

// listWindow is the visible slice of the filtered entries: how many rows the
// section draws and at which entry it starts, derived from the cursor exactly
// as the renderer has always drawn it. The renderer and the scrollbar's
// pointer handlers both read it, so the bar can never disagree with the list.
func (m *Model) listWindow() (visibleCount, scrollOffset int) {
	if len(m.filtered) == 0 {
		return 0, 0
	}
	maxVisible := 10
	if m.maxVisible > 0 && m.maxVisible < maxVisible {
		maxVisible = m.maxVisible
	}
	visibleCount = min(maxVisible, len(m.filtered))

	selectedIdx := m.cursor
	if selectedIdx < 0 {
		selectedIdx = 0
	}
	if selectedIdx >= len(m.filtered) {
		selectedIdx = len(m.filtered) - 1
	}

	scrollOffset = m.offset
	if selectedIdx < scrollOffset {
		scrollOffset = selectedIdx
	}
	if selectedIdx >= scrollOffset+visibleCount {
		scrollOffset = selectedIdx - visibleCount + 1
	}
	if scrollOffset > len(m.filtered)-visibleCount {
		scrollOffset = len(m.filtered) - visibleCount
	}
	if scrollOffset < 0 {
		scrollOffset = 0
	}
	return visibleCount, scrollOffset
}

// listSection renders the command list with scrollbar, keyboard and mouse hit targets.
func (m *Model) listSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		entries := m.filtered

		if len(entries) == 0 {
			return modal.RenderedSection{Content: styles.Muted.Render("No matching commands")}
		}

		visibleCount, scrollOffset := m.listWindow()
		m.offset = scrollOffset
		selectedIdx := max(0, min(m.cursor, len(entries)-1))

		rowWidth := max(10, contentWidth-2) // Reserve 2 cols for space + scrollbar
		lines := make([]string, 0, visibleCount)
		focusables := make([]modal.FocusableInfo, 0, visibleCount+2)

		for i := 0; i < visibleCount; i++ {
			entryIdx := scrollOffset + i
			entry := entries[entryIdx]
			isSelected := entryIdx == selectedIdx
			itemID := fmt.Sprintf("%s%d", paletteItemPrefix, entryIdx)
			isHovered := itemID == hoverID

			line := m.renderEntry(entry, isSelected || isHovered, rowWidth)
			lineWidth := lipgloss.Width(line)
			if lineWidth < rowWidth {
				line += strings.Repeat(" ", rowWidth-lineWidth)
			}
			lines = append(lines, line)

			focusables = append(focusables, modal.FocusableInfo{
				ID:      itemID,
				OffsetX: 0,
				OffsetY: i,
				Width:   contentWidth,
				Height:  1,
			})
		}

		state := ui.HandleStateFrom(
			hoverID == ui.RegionScrollbarThumb || hoverID == ui.RegionScrollbarTrack,
			m.scrollbarDragActive())
		scrollbar, barGeom := ui.RenderScrollbarWithState(ui.ScrollbarParams{
			TotalItems:   len(entries),
			ScrollOffset: scrollOffset,
			VisibleItems: visibleCount,
			TrackHeight:  visibleCount,
		}, ui.ScrollbarStyle{Thumb: state, Track: state})

		// The bar's thumb and track are appended after the entry rows they
		// overlap — modal hit regions resolve reverse-scan order, so the last
		// registration on a cell wins. When everything fits there is no thumb:
		// the spacer column stays inert under the pointer.
		if barGeom.HasThumb {
			barX := rowWidth + 1 // the single spacer column between rows and bar
			focusables = append(focusables,
				modal.FocusableInfo{
					ID:        ui.RegionScrollbarTrack,
					OffsetX:   barX,
					OffsetY:   0,
					Width:     1,
					Height:    visibleCount,
					MouseOnly: true,
				},
				modal.FocusableInfo{
					ID:        ui.RegionScrollbarThumb,
					OffsetX:   barX,
					OffsetY:   barGeom.ThumbRect.Min.Y,
					Width:     1,
					Height:    max(1, barGeom.ThumbRect.Dy()),
					MouseOnly: true,
				},
			)
		}

		bodyContent := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(lines, "\n")+" ", scrollbar)

		return modal.RenderedSection{
			Content:    bodyContent,
			Focusables: focusables,
		}
	}, nil)
}

// renderEntry renders a single palette entry line.
func (m Model) renderEntry(entry PaletteEntry, selected bool, maxWidth int) string {
	cursorStr := "  "
	if selected {
		cursorStr = lipgloss.NewStyle().Foreground(styles.Primary).Render("> ")
	}

	keyStr := ""
	if entry.Key != "" {
		keyStr = styles.KeyHint.Render(entry.Key)
	}
	keyWidth := lipgloss.Width(keyStr)
	if keyWidth < keyColumnWidth {
		keyStr += strings.Repeat(" ", keyColumnWidth-keyWidth)
	}

	nameHighlighted := m.highlightMatches(entry.Name, entry.MatchRanges)
	var nStyle lipgloss.Style
	if selected {
		nStyle = lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
	} else {
		nStyle = lipgloss.NewStyle().Foreground(styles.TextPrimary)
	}
	renderedName := nStyle.Render(nameHighlighted)
	nameDisplayWidth := lipgloss.Width(entry.Name)
	if nameDisplayWidth < nameColumnWidth {
		renderedName += strings.Repeat(" ", nameColumnWidth-nameDisplayWidth)
	}

	var desc string
	if entry.Description != "" && entry.Description != entry.Name {
		desc = entry.Description
	}
	if entry.ContextCount > 1 {
		if desc != "" {
			desc = fmt.Sprintf("%s (%d contexts)", desc, entry.ContextCount)
		} else {
			desc = fmt.Sprintf("(%d contexts)", entry.ContextCount)
		}
	}

	var descStr string
	if desc != "" {
		availDesc := maxWidth - (keyColumnWidth + nameColumnWidth + 4)
		if availDesc > 3 && lipgloss.Width(desc) > availDesc {
			desc = desc[:availDesc-3] + "..."
		}
		descStr = " " + lipgloss.NewStyle().Foreground(styles.TextSecondary).Render(desc)
	}

	return fmt.Sprintf("%s%s %s%s", cursorStr, keyStr, renderedName, descStr)
}

// hintsSection renders the footer hint line.
func (m *Model) hintsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		var sb strings.Builder
		if len(m.filtered) == 0 {
			sb.WriteString(styles.KeyHint.Render("esc"))
			sb.WriteString(styles.Muted.Render(" clear filter  "))
			sb.WriteString(styles.KeyHint.Render("?"))
			sb.WriteString(styles.Muted.Render(" close"))
		} else {
			sb.WriteString(styles.KeyHint.Render("enter"))
			sb.WriteString(styles.Muted.Render(" select  "))
			sb.WriteString(styles.KeyHint.Render("↑/↓"))
			sb.WriteString(styles.Muted.Render(" navigate  "))
			sb.WriteString(styles.KeyHint.Render("tab"))
			sb.WriteString(styles.Muted.Render(" toggle scope  "))
			sb.WriteString(styles.KeyHint.Render("esc"))
			sb.WriteString(styles.Muted.Render(" close"))
		}
		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// highlightMatches applies highlighting to matched characters.
func (m Model) highlightMatches(text string, ranges []MatchRange) string {
	if len(ranges) == 0 {
		return text
	}

	var result strings.Builder
	lastEnd := 0

	for _, r := range ranges {
		if r.Start > lastEnd && lastEnd < len(text) {
			result.WriteString(text[lastEnd:min(r.Start, len(text))])
		}
		if r.Start < len(text) && r.End <= len(text) && r.Start < r.End {
			highlighted := matchHighlight().Render(text[r.Start:r.End])
			result.WriteString(highlighted)
		}
		lastEnd = r.End
	}

	if lastEnd < len(text) {
		result.WriteString(text[lastEnd:])
	}

	return result.String()
}

func matchHighlight() lipgloss.Style {
	return lipgloss.NewStyle().
		Foreground(styles.Primary).
		Bold(true).
		Underline(true)
}

// scrollbarDragActive reports that a live drag started on the results bar —
// from the thumb or from the track, both of which move the same window. It is
// derived from the shared mouse handler, so it clears itself the moment the
// gesture ends and nothing has to be reset by hand.
func (m Model) scrollbarDragActive() bool {
	if m.mouseHandler == nil {
		return false
	}
	switch m.mouseHandler.DragRegion() {
	case ui.RegionScrollbarThumb, ui.RegionScrollbarTrack:
		return true
	}
	return false
}

// handleScrollbarPointer answers the pointer for the results scrollbar after
// the modal framework has dispatched an event. Drags and hovers return ""
// from that dispatch, but they have already moved through the shared handler,
// whose state below says whether a gesture is live.
//
// The gesture follows the shared contract: press thumb → StartDrag anchored at
// the thumb top (the grab offset rides startValue); press track → jump-to-spot
// so the grabbed row becomes the anchor, then StartDrag from there; motion →
// offset = ui.OffsetAtRow(pointer Y minus the anchor), clamped inside
// OffsetAtRow itself; release anywhere settles, because offsets are ephemeral
// and the handler has already ended the drag before we see the event.
//
// Presses probe the bar regions directly rather than trusting the dispatch's
// action string: a second press inside the double-click window reports as
// ActionDoubleClick, which the modal framework drops on the floor.
func (m *Model) handleScrollbarPointer(msg tea.MouseMsg) bool {
	handler := m.mouseHandler
	if handler == nil {
		return false
	}

	if click, isClick := msg.(tea.MouseClickMsg); isClick && click.Mouse().Button == tea.MouseLeft {
		mi := click.Mouse()
		region := handler.HitMap.Test(mi.X, mi.Y)
		if region == nil {
			return false
		}
		switch region.ID {
		case ui.RegionScrollbarThumb:
			params, ok := m.listScrollbarParams()
			if !ok {
				return false
			}
			_, scrollOffset := m.listWindow()
			grab := mi.Y - ui.RowForOffset(params, scrollOffset)
			handler.StartDrag(mi.X, mi.Y, ui.RegionScrollbarThumb, grab)
			return true
		case ui.RegionScrollbarTrack:
			params, ok := m.listScrollbarParams()
			if !ok {
				return false
			}
			trackTop := region.Rect.Y
			m.setListWindow(ui.OffsetAtRow(params, mi.Y-trackTop))
			handler.StartDrag(mi.X, mi.Y, ui.RegionScrollbarTrack, trackTop)
			return true
		}
		return false
	}

	if !handler.IsDragging() || !m.scrollbarDragActive() {
		return false
	}
	motion, isMotion := msg.(tea.MouseMotionMsg)
	if !isMotion {
		return false
	}
	params, ok := m.listScrollbarParams()
	if !ok {
		return true
	}
	m.setListWindow(ui.OffsetAtRow(params, motion.Y-handler.DragStartValue()))
	return true
}

// listScrollbarParams is the ScrollbarParams the renderer and the pointer
// handlers must agree on: the same window listSection draws.
func (m *Model) listScrollbarParams() (ui.ScrollbarParams, bool) {
	visibleCount, scrollOffset := m.listWindow()
	if visibleCount <= 0 || len(m.filtered) <= visibleCount {
		return ui.ScrollbarParams{}, false
	}
	return ui.ScrollbarParams{
		TotalItems:   len(m.filtered),
		ScrollOffset: scrollOffset,
		VisibleItems: visibleCount,
		TrackHeight:  visibleCount,
	}, true
}

// setListWindow moves the visible window to offset, carrying the cursor's slot
// within the window along. The renderer re-derives the window around the
// cursor, so viewport and selection have to move together or the next frame
// would drag the bar straight back.
func (m *Model) setListWindow(offset int) {
	if len(m.filtered) == 0 {
		return
	}
	offset = max(0, min(offset, len(m.filtered)-1))
	visibleCount, prevOffset := m.listWindow()
	rel := 0
	if visibleCount > 0 {
		rel = max(0, min(m.cursor-prevOffset, visibleCount-1))
	}
	m.offset = offset
	m.cursor = max(0, min(offset+rel, len(m.filtered)-1))
	// Re-derive around the carried cursor exactly as the renderer will, so what
	// was asked for is what sticks.
	_, final := m.listWindow()
	m.offset = final
}
