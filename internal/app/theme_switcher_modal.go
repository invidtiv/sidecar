package app

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/theme"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	themeSwitcherFilterID   = "theme-switcher-filter"
	themeSwitcherItemPrefix = "theme-switcher-item-"

	// themeSwitcherMaxVisible is how many rows the theme window shows; both
	// the render and the scrollbar gesture math derive from it.
	themeSwitcherMaxVisible = 12
)

// themeEntry is one theme in the unified list. The list, the filter, and the
// swatch colors live in internal/theme so this modal and Configuration's theme
// picker show the same library rather than two hand-maintained copies of it.
type themeEntry = theme.Entry

// buildUnifiedThemeList returns all themes: built-in first, then community.
func buildUnifiedThemeList() []themeEntry { return theme.List() }

// filterThemeEntries filters entries by case-insensitive substring match on Name.
// Separators are included only when unfiltered; they are excluded when a query is active.
func filterThemeEntries(entries []themeEntry, query string) []themeEntry {
	return theme.Filter(entries, query)
}

// themeSwitcherItemID returns the ID for a theme item at the given index.
func themeSwitcherItemID(idx int) string {
	return fmt.Sprintf("%s%d", themeSwitcherItemPrefix, idx)
}

// ensureThemeSwitcherModal builds/rebuilds the theme switcher modal.
func (m *Model) ensureThemeSwitcherModal() {
	modalW := 72
	if modalW > m.width-4 {
		modalW = m.width - 4
	}
	if modalW < 30 {
		modalW = 30
	}

	// Only rebuild if modal doesn't exist or width changed
	if m.themeSwitcherModal != nil && m.themeSwitcherModalWidth == modalW {
		return
	}
	m.themeSwitcherModalWidth = modalW

	m.themeSwitcherModal = modal.New("Switch Theme",
		modal.WithWidth(modalW),
		modal.WithHints(false),
	).
		AddSection(modal.When(m.themeSwitcherHasProject, m.themeSwitcherScopeSection())).
		AddSection(modal.Input(themeSwitcherFilterID, &m.themeSwitcherInput, modal.WithSubmitOnEnter(false))).
		AddSection(m.themeSwitcherCountSection()).
		AddSection(modal.Spacer()).
		AddSection(m.themeSwitcherListSection()).
		AddSection(modal.Spacer()).
		AddSection(m.themeSwitcherHintsSection())
}

// themeSwitcherHasProject returns true if the current project is in the project list.
func (m *Model) themeSwitcherHasProject() bool {
	return m.currentProjectConfig() != nil
}

// themeSwitcherCountSection renders the theme count info.
func (m *Model) themeSwitcherCountSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		all := buildUnifiedThemeList()
		allCount := 0
		for _, e := range all {
			if !e.IsSeparator {
				allCount++
			}
		}
		filteredCount := 0
		for _, e := range m.themeSwitcherFiltered {
			if !e.IsSeparator {
				filteredCount++
			}
		}

		var text string
		if m.themeSwitcherInput.Value() != "" {
			text = fmt.Sprintf("%d of %d themes", filteredCount, allCount)
		}

		if text == "" {
			return modal.RenderedSection{Content: ""}
		}
		return modal.RenderedSection{Content: styles.Muted.Render(text)}
	}, nil)
}

// themeSwitcherListSection renders the theme list with selection and scrollbar.
func (m *Model) themeSwitcherListSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		themes := m.themeSwitcherFiltered

		if len(themes) == 0 {
			return modal.RenderedSection{Content: styles.Muted.Render("No matches")}
		}

		cursorStyle := lipgloss.NewStyle().Foreground(styles.Primary)
		nameNormalStyle := lipgloss.NewStyle().Foreground(styles.Secondary)
		nameSelectedStyle := lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)
		nameCurrentStyle := lipgloss.NewStyle().Foreground(styles.Success).Bold(true)

		maxVisible := themeSwitcherMaxVisible
		visibleCount := min(maxVisible, len(themes))

		selectedIdx := m.themeSwitcherSelectedIdx
		if selectedIdx < 0 {
			selectedIdx = 0
		}
		if selectedIdx >= len(themes) {
			selectedIdx = len(themes) - 1
		}

		scrollOffset := 0
		if selectedIdx >= maxVisible {
			scrollOffset = selectedIdx - maxVisible + 1
		}
		if scrollOffset > len(themes)-visibleCount {
			scrollOffset = len(themes) - visibleCount
		}
		if scrollOffset < 0 {
			scrollOffset = 0
		}

		rowWidth := max(10, contentWidth-2) // Reserve 2 cols for space + scrollbar
		lines := make([]string, 0, visibleCount)
		focusables := make([]modal.FocusableInfo, 0, visibleCount)

		for i := 0; i < visibleCount; i++ {
			entryIdx := scrollOffset + i
			entry := themes[entryIdx]

			// Render separator lines (non-selectable)
			if entry.IsSeparator {
				sepLine := styles.Muted.Render(fmt.Sprintf("  ── %s ──", entry.SeparatorText))
				lineWidth := lipgloss.Width(sepLine)
				if lineWidth < rowWidth {
					sepLine += strings.Repeat(" ", rowWidth-lineWidth)
				}
				lines = append(lines, sepLine)
				continue
			}

			isSelected := entryIdx == selectedIdx
			itemID := themeSwitcherItemID(entryIdx)
			isHovered := itemID == hoverID
			isCurrent := entry.IsBuiltIn == m.themeSwitcherOriginal.IsBuiltIn && entry.ThemeKey == m.themeSwitcherOriginal.ThemeKey

			var row strings.Builder
			if isSelected {
				row.WriteString(cursorStyle.Render("> "))
			} else {
				row.WriteString("  ")
			}

			// Color swatch for all themes
			if swatchColors := theme.Swatch(entry); len(swatchColors) > 0 {
				for _, sc := range swatchColors {
					row.WriteString(lipgloss.NewStyle().Background(lipgloss.Color(sc)).Render(" "))
				}
				row.WriteString(" ")
			}

			var nameStyle lipgloss.Style
			if isCurrent {
				nameStyle = nameCurrentStyle
			} else if isSelected || isHovered {
				nameStyle = nameSelectedStyle
			} else {
				nameStyle = nameNormalStyle
			}

			row.WriteString(nameStyle.Render(entry.Name))

			if isCurrent {
				row.WriteString(styles.Muted.Render(" (current)"))
			}

			line := row.String()
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

		barParams := ui.ScrollbarParams{
			TotalItems:   len(themes),
			ScrollOffset: scrollOffset,
			VisibleItems: visibleCount,
			TrackHeight:  visibleCount,
		}
		scrollbar, _ := ui.RenderScrollbarWithState(barParams, m.themeSwitcherBar.style(m.themeSwitcherMouseHandler))

		bodyContent := lipgloss.JoinHorizontal(lipgloss.Top, strings.Join(lines, "\n")+" ", scrollbar)

		// Declaring the bar lets the modal library place its hit regions and
		// route presses/drags back through this section's Update.
		return modal.RenderedSection{
			Content:    bodyContent,
			Focusables: focusables,
			Scrollbar: &modal.SectionScrollbar{
				TotalItems:   barParams.TotalItems,
				ScrollOffset: barParams.ScrollOffset,
				VisibleItems: barParams.VisibleItems,
				TrackHeight:  barParams.TrackHeight,
				LocalX:       rowWidth + 1,
			},
		}
	}, m.themeSwitcherListUpdate)
}

// themeSwitcherListUpdate handles key events for the theme list. Scrollbar
// gestures on the declared bar are answered by themeSwitcherBarEvent in the
// switcher's mouse handler.
func (m *Model) themeSwitcherListUpdate(msg tea.Msg, focusID string) (string, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return "", nil
	}

	themes := m.themeSwitcherFiltered
	if len(themes) == 0 {
		return "", nil
	}

	switch keyMsg.String() {
	case "up", "k", "ctrl+p":
		if m.themeSwitcherSelectedIdx > 0 {
			m.themeSwitcherSelectedIdx--
			// Skip separators
			for m.themeSwitcherSelectedIdx > 0 && themes[m.themeSwitcherSelectedIdx].IsSeparator {
				m.themeSwitcherSelectedIdx--
			}
			m.themeSwitcherModalWidth = 0
			if m.themeSwitcherSelectedIdx < len(themes) && !themes[m.themeSwitcherSelectedIdx].IsSeparator {
				return "", m.previewThemeEntry(themes[m.themeSwitcherSelectedIdx])
			}
		}
		return "", nil

	case "down", "j", "ctrl+n":
		if m.themeSwitcherSelectedIdx < len(themes)-1 {
			m.themeSwitcherSelectedIdx++
			// Skip separators
			for m.themeSwitcherSelectedIdx < len(themes)-1 && themes[m.themeSwitcherSelectedIdx].IsSeparator {
				m.themeSwitcherSelectedIdx++
			}
			m.themeSwitcherModalWidth = 0
			if m.themeSwitcherSelectedIdx < len(themes) && !themes[m.themeSwitcherSelectedIdx].IsSeparator {
				return "", m.previewThemeEntry(themes[m.themeSwitcherSelectedIdx])
			}
		}
		return "", nil

	case "enter":
		if m.themeSwitcherSelectedIdx >= 0 && m.themeSwitcherSelectedIdx < len(themes) && !themes[m.themeSwitcherSelectedIdx].IsSeparator {
			return "select", nil
		}
		return "", nil
	}

	return "", nil
}

// themeSwitcherScopeSection renders the scope selector.
func (m *Model) themeSwitcherScopeSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		var sb strings.Builder

		activeStyle := lipgloss.NewStyle().Foreground(styles.Primary).Bold(true)

		scopeGlobal := "Global"
		scopeProject := "This project"
		if m.themeSwitcherScope == "project" {
			sb.WriteString(styles.Muted.Render(scopeGlobal))
			sb.WriteString(styles.Muted.Render("  │  "))
			sb.WriteString(activeStyle.Render(scopeProject))
		} else {
			sb.WriteString(activeStyle.Render(scopeGlobal))
			sb.WriteString(styles.Muted.Render("  │  "))
			sb.WriteString(styles.Muted.Render(scopeProject))
		}

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// themeSwitcherHintsSection renders the help text.
func (m *Model) themeSwitcherHintsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		var sb strings.Builder

		if len(m.themeSwitcherFiltered) == 0 {
			sb.WriteString(styles.KeyHint.Render("esc"))
			sb.WriteString(styles.Muted.Render(" clear filter  "))
			sb.WriteString(styles.KeyHint.Render("#"))
			sb.WriteString(styles.Muted.Render(" close"))
		} else {
			sb.WriteString(styles.KeyHint.Render("enter"))
			sb.WriteString(styles.Muted.Render(" select  "))
			sb.WriteString(styles.KeyHint.Render("↑/↓"))
			sb.WriteString(styles.Muted.Render(" navigate"))
			if m.currentProjectConfig() != nil {
				sb.WriteString(styles.Muted.Render("  "))
				sb.WriteString(styles.KeyHint.Render("←/→"))
				sb.WriteString(styles.Muted.Render(" scope"))
			}
			sb.WriteString(styles.Muted.Render("  "))
			sb.WriteString(styles.KeyHint.Render("esc"))
			sb.WriteString(styles.Muted.Render(" cancel"))
		}

		return modal.RenderedSection{Content: sb.String()}
	}, nil)
}

// renderThemeSwitcherModal renders the theme switcher modal using the modal library.
func (m *Model) renderThemeSwitcherModal(content string) string {
	m.ensureThemeSwitcherModal()
	if m.themeSwitcherModal == nil {
		return content
	}

	if m.themeSwitcherMouseHandler == nil {
		m.themeSwitcherMouseHandler = mouse.NewHandler()
	}
	modalContent := m.themeSwitcherModal.Render(m.width, m.height, m.themeSwitcherMouseHandler)
	return ui.OverlayModal(content, modalContent, m.width, m.height)
}
