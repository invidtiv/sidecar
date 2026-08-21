package palette

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/styles"
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

// listSection renders the command list with keyboard and mouse hit targets.
func (m *Model) listSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		entries := m.filtered

		if len(entries) == 0 {
			return modal.RenderedSection{Content: styles.Muted.Render("No matching commands")}
		}

		maxVisible := 10
		if m.maxVisible > 0 && m.maxVisible < maxVisible {
			maxVisible = m.maxVisible
		}
		visibleCount := min(maxVisible, len(entries))

		selectedIdx := m.cursor
		if selectedIdx < 0 {
			selectedIdx = 0
		}
		if selectedIdx >= len(entries) {
			selectedIdx = len(entries) - 1
		}

		scrollOffset := m.offset
		if selectedIdx < scrollOffset {
			scrollOffset = selectedIdx
		}
		if selectedIdx >= scrollOffset+visibleCount {
			scrollOffset = selectedIdx - visibleCount + 1
		}
		if scrollOffset > len(entries)-visibleCount {
			scrollOffset = len(entries) - visibleCount
		}
		if scrollOffset < 0 {
			scrollOffset = 0
		}
		m.offset = scrollOffset

		var sb strings.Builder
		focusables := make([]modal.FocusableInfo, 0, visibleCount)
		lineOffset := 0

		if scrollOffset > 0 {
			sb.WriteString(styles.Muted.Render(fmt.Sprintf("  ↑ %d more above", scrollOffset)))
			sb.WriteString("\n")
			lineOffset++
		}

		for i := scrollOffset; i < scrollOffset+visibleCount && i < len(entries); i++ {
			entry := entries[i]
			isSelected := i == selectedIdx
			itemID := fmt.Sprintf("%s%d", paletteItemPrefix, i)
			isHovered := itemID == hoverID

			line := m.renderEntry(entry, isSelected || isHovered, contentWidth)
			sb.WriteString(line)
			sb.WriteString("\n")

			focusables = append(focusables, modal.FocusableInfo{
				ID:      itemID,
				OffsetX: 0,
				OffsetY: lineOffset + (i - scrollOffset),
				Width:   contentWidth,
				Height:  1,
			})
		}

		remaining := len(entries) - (scrollOffset + visibleCount)
		if remaining > 0 {
			sb.WriteString(styles.Muted.Render(fmt.Sprintf("  ↓ %d more below", remaining)))
		}

		return modal.RenderedSection{
			Content:    strings.TrimRight(sb.String(), "\n"),
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
