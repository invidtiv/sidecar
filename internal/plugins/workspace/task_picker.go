package workspace

import (
	"fmt"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/styles"
)

const (
	taskLinkFieldID    = "task-link-search"
	taskLinkItemPrefix = "task-link-item-"
)

// taskPickerSection is the task-search presentation for linking a task to an
// existing worktree. All geometry is derived from the modal's rendered content
// width so borders and hit regions cannot disagree at narrow terminal sizes.
func (p *Plugin) taskPickerSection(fieldID, itemPrefix, label string, maxVisible int) modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		maxVisible = min(maxVisible, taskPickerVisibleRows(p.height, fieldID == taskLinkFieldID))
		lines := []string{label}
		focusables := make([]modal.FocusableInfo, 0, maxVisible+1)
		lineY := 1

		inputFocused := focusID == fieldID || (focusID == "" && fieldID == taskLinkFieldID)
		if inputFocused {
			p.taskSearchInput.Focus()
		} else {
			p.taskSearchInput.Blur()
		}

		// inputStyle adds a one-cell border and one-cell horizontal padding on
		// each side. Never impose a minimum larger than the available content.
		// Bubbles textinput width is the editable viewport, while the surrounding
		// style adds border and padding. Leave two additional cells for the modal
		// layout's ANSI-safe padding so the right corners never wrap separately.
		inputWidth := max(1, contentWidth-6)
		p.taskSearchInput.SetWidth(inputWidth)
		style := inputStyle()
		if inputFocused {
			style = inputFocusedStyle()
		}

		rendered := style.Render(p.taskSearchInput.View())
		renderedLines := strings.Split(rendered, "\n")
		lines = append(lines, renderedLines...)
		focusables = append(focusables, modal.FocusableInfo{
			ID: fieldID, OffsetX: 0, OffsetY: lineY,
			Width: min(contentWidth, ansi.StringWidth(rendered)), Height: len(renderedLines),
		})
		lineY += len(renderedLines)

		if p.taskSearchLoading {
			lines = append(lines, dimText("  Loading tasks..."))
			return modal.RenderedSection{Content: strings.Join(lines, "\n"), Focusables: focusables}
		}
		if len(p.taskSearchFiltered) == 0 {
			message := "  No open tasks found"
			if p.taskSearchInput.Value() != "" {
				message = "  No matching tasks"
			}
			lines = append(lines, dimText(message))
			return modal.RenderedSection{Content: strings.Join(lines, "\n"), Focusables: focusables}
		}

		visible := max(1, min(maxVisible, len(p.taskSearchFiltered)))
		p.taskSearchScroll = ensureListSelectionVisible(p.taskSearchIdx, p.taskSearchScroll, visible, len(p.taskSearchFiltered))
		end := min(len(p.taskSearchFiltered), p.taskSearchScroll+visible)
		for i := p.taskSearchScroll; i < end; i++ {
			task := p.taskSearchFiltered[i]
			prefix := "  "
			if i == p.taskSearchIdx {
				prefix = "> "
			}
			idPart := task.ID + "  "
			titleWidth := max(0, contentWidth-ansi.StringWidth(prefix)-ansi.StringWidth(idPart))
			line := prefix + idPart + ansi.Truncate(task.Title, titleWidth, "…")
			if i == p.taskSearchIdx {
				line = lipgloss.NewStyle().Foreground(styles.Primary).Render(line)
			} else {
				line = dimText(line)
			}
			lines = append(lines, line)
			focusables = append(focusables, modal.FocusableInfo{
				ID: createIndexedID(itemPrefix, i), OffsetX: 0, OffsetY: lineY,
				Width: min(contentWidth, ansi.StringWidth(line)), Height: 1,
			})
			lineY++
		}
		if remaining := len(p.taskSearchFiltered) - end; remaining > 0 {
			lines = append(lines, dimText(fmt.Sprintf("  ↓ %d more", remaining)))
		} else if p.taskSearchScroll > 0 {
			lines = append(lines, dimText(fmt.Sprintf("  ↑ %d above", p.taskSearchScroll)))
		}

		return modal.RenderedSection{Content: strings.Join(lines, "\n"), Focusables: focusables}
	}, nil)
}

func ensureListSelectionVisible(cursor, scroll, visible, total int) int {
	if total <= 0 || visible <= 0 {
		return 0
	}
	cursor = max(0, min(cursor, total-1))
	maxScroll := max(0, total-visible)
	if cursor < scroll {
		scroll = cursor
	} else if cursor >= scroll+visible {
		scroll = cursor - visible + 1
	}
	return max(0, min(scroll, maxScroll))
}

func taskPickerVisibleRows(height int, standalone bool) int {
	reserved := 17
	if standalone {
		reserved = 12
	}
	return max(1, min(8, height-reserved))
}

func selectedTaskIndex(tasks []Task, id string) int {
	for i := range tasks {
		if tasks[i].ID == id {
			return i
		}
	}
	return -1
}

func taskPickerNavigationDelta(key string, inputFocused bool) (int, bool) {
	switch key {
	case "up", "ctrl+p":
		return -1, true
	case "down", "ctrl+n":
		return 1, true
	case "k":
		return -1, !inputFocused
	case "j":
		return 1, !inputFocused
	default:
		return 0, false
	}
}

func (p *Plugin) moveTaskPickerSelection(delta int, standalone, focusList bool, itemPrefix string) {
	if len(p.taskSearchFiltered) == 0 {
		return
	}
	p.taskSearchIdx = max(0, min(p.taskSearchIdx+delta, len(p.taskSearchFiltered)-1))
	p.taskSearchScroll = ensureListSelectionVisible(p.taskSearchIdx, p.taskSearchScroll, taskPickerVisibleRows(p.height, standalone), len(p.taskSearchFiltered))
	if focusList && p.taskLinkModal != nil {
		p.taskLinkModal.SetFocus(createIndexedID(itemPrefix, p.taskSearchIdx))
	}
}
