package app

import (
	"fmt"
	"strings"

	"image/color"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/issueview"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// Issue type icons and colors (matching td monitor).
var issueTypeIcons = map[string]string{
	"epic": "◆", "feature": "●", "bug": "✗", "task": "■", "chore": "○",
}
var issueTypeColors = map[string]color.Color{
	"epic": lipgloss.Color("212"), "feature": lipgloss.Color("42"), "bug": lipgloss.Color("196"), "task": lipgloss.Color("45"), "chore": lipgloss.Color("241"),
}

func formatSearchTypeIcon(t string) string {
	k := strings.ToLower(t)
	icon := issueTypeIcons[k]
	if icon == "" {
		icon = "?"
	}
	c, ok := issueTypeColors[k]
	if !ok {
		return icon
	}
	return lipgloss.NewStyle().Foreground(c).Render(icon)
}

func formatSearchPriority(p string) string {
	var s lipgloss.Style
	switch strings.ToUpper(p) {
	case "P0":
		s = lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Bold(true)
	case "P1":
		s = lipgloss.NewStyle().Foreground(lipgloss.Color("214"))
	case "P2":
		s = lipgloss.NewStyle().Foreground(lipgloss.Color("45"))
	default:
		s = lipgloss.NewStyle().Foreground(lipgloss.Color("241"))
	}
	return s.Render(p)
}

func formatSearchStatusTag(status string) string {
	switch strings.ToLower(status) {
	case "in_review":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("141")).Render("[REV]")
	case "open":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("42")).Render("[RDY]")
	case "blocked":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("196")).Render("[BLK]")
	case "in_progress":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("214")).Render("[WIP]")
	case "closed":
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("[CLS]")
	default:
		abbr := strings.ToUpper(status)
		if len(abbr) > 3 {
			abbr = abbr[:3]
		}
		return lipgloss.NewStyle().Foreground(lipgloss.Color("241")).Render("[" + abbr + "]")
	}
}

func (m *Model) renderIssueInputOverlay(content string) string {
	m.ensureIssueInputModal()
	if m.issueInputModal == nil {
		return content
	}
	if m.issueInputMouseHandler == nil {
		m.issueInputMouseHandler = mouse.NewHandler()
	}
	rendered := m.issueInputModal.Render(m.width, m.height, m.issueInputMouseHandler)
	return ui.OverlayModal(content, rendered, m.width, m.height)
}

// issueSearchResultPrefix is the hit-region ID prefix for clickable search results.
const issueSearchResultPrefix = "issue-search-"

func (m *Model) ensureIssueInputModal() {
	modalW := 80
	if modalW > m.width-4 {
		modalW = m.width - 4
	}
	if modalW < 20 {
		modalW = 20
	}
	if m.issueInputModal != nil && m.issueInputModalWidth == modalW {
		return
	}
	m.issueInputModalWidth = modalW

	// Build footer hint string (always visible outside viewport)
	var hintBuf strings.Builder
	hasResults := len(m.issueSearchResults) > 0
	if hasResults {
		hintBuf.WriteString(styles.KeyHint.Render("enter"))
		hintBuf.WriteString(styles.Muted.Render(" open  "))
		hintBuf.WriteString(styles.KeyHint.Render("↑↓"))
		hintBuf.WriteString(styles.Muted.Render(" select  "))
		hintBuf.WriteString(styles.KeyHint.Render("tab"))
		hintBuf.WriteString(styles.Muted.Render(" fill  "))
	}
	if m.issueSearchIncludeClosed {
		hintBuf.WriteString(styles.KeyHint.Render("^x"))
		hintBuf.WriteString(styles.Muted.Render(" hide closed  "))
	} else {
		hintBuf.WriteString(styles.KeyHint.Render("^x"))
		hintBuf.WriteString(styles.Muted.Render(" show closed  "))
	}
	if hasResults {
		hintBuf.WriteString(styles.KeyHint.Render("esc"))
		hintBuf.WriteString(styles.Muted.Render(" cancel"))
	}

	b := modal.New("Open Issue",
		modal.WithWidth(modalW),
		modal.WithHints(false),
		modal.WithCustomFooter(hintBuf.String()),
	).
		AddSection(modal.Input("issue-id", &m.issueInputInput))

	// Status line — always present to avoid layout jumps
	if m.issueSearchLoading {
		b = b.AddSection(modal.Text(styles.Muted.Render("Searching...")))
	} else if len(m.issueSearchResults) > 0 {
		countStr := fmt.Sprintf("%d results", len(m.issueSearchResults))
		if !m.issueSearchIncludeClosed {
			countStr += " (excluding closed)"
		}
		b = b.AddSection(modal.Text(styles.Muted.Render(countStr)))
	} else {
		b = b.AddSection(modal.Text(styles.Muted.Render(" ")))
	}

	// Search results dropdown — viewport window over all results
	const maxVisible = 10
	const minResultLines = 5
	if len(m.issueSearchResults) > 0 {
		searchResults := m.issueSearchResults
		searchCursor := m.issueSearchCursor
		searchScrollOffset := m.issueSearchScrollOffset
		b = b.AddSection(modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
			var sb strings.Builder
			total := len(searchResults)
			endIdx := searchScrollOffset + maxVisible
			if endIdx > total {
				endIdx = total
			}
			visibleCount := endIdx - searchScrollOffset
			focusables := make([]modal.FocusableInfo, 0, visibleCount)
			for i := searchScrollOffset; i < endIdx; i++ {
				r := searchResults[i]
				tag := formatSearchStatusTag(r.Status)
				icon := formatSearchTypeIcon(r.Type)
				pri := formatSearchPriority(r.Priority)
				idStr := styles.Muted.Render(r.ID)
				prefix := fmt.Sprintf(" %s %s %s %s ", tag, icon, idStr, pri)
				title := r.Title
				titleWidth := contentWidth - lipgloss.Width(prefix)
				if titleWidth < 10 {
					titleWidth = 10
				}
				if len(title) > titleWidth {
					title = title[:titleWidth-3] + "..."
				}
				line := prefix + title
				itemID := fmt.Sprintf("%s%d", issueSearchResultPrefix, i)
				isHovered := itemID == hoverID
				if i == searchCursor || isHovered {
					sb.WriteString(styles.ListItemSelected.Render(line))
				} else {
					sb.WriteString(styles.ListItemNormal.Render(line))
				}
				if i < endIdx-1 {
					sb.WriteString("\n")
				}
				focusables = append(focusables, modal.FocusableInfo{
					ID:      itemID,
					OffsetX: 0,
					OffsetY: i - searchScrollOffset,
					Width:   contentWidth,
					Height:  1,
				})
			}
			// Pad with empty lines to maintain minimum height
			for i := visibleCount; i < minResultLines; i++ {
				sb.WriteString("\n")
			}
			return modal.RenderedSection{Content: sb.String(), Focusables: focusables}
		}, nil))
	} else {
		// Reserve space for results even when empty
		b = b.AddSection(modal.Custom(func(contentWidth int, _, _ string) modal.RenderedSection {
			var sb strings.Builder
			for i := 0; i < minResultLines; i++ {
				if i > 0 {
					sb.WriteString("\n")
				}
			}
			return modal.RenderedSection{Content: sb.String()}
		}, nil))
	}

	if hasResults {
		b = b.AddSection(modal.Spacer())
		b = b.AddSection(modal.Buttons(
			modal.Btn(" Open ", "open", modal.BtnPrimary()),
			modal.Btn(" Cancel ", "cancel"),
		))
	}

	m.issueInputModal = b
}

func (m *Model) renderIssuePreviewOverlay(content string) string {
	m.ensureIssuePreviewModal()
	if m.issuePreviewModal == nil {
		return content
	}
	if m.issuePreviewMouseHandler == nil {
		m.issuePreviewMouseHandler = mouse.NewHandler()
	}
	rendered := m.issuePreviewModal.Render(m.width, m.height, m.issuePreviewMouseHandler)
	return ui.OverlayModal(content, rendered, m.width, m.height)
}

const issueViewFocusID = "issue-view"

func (m *Model) ensureIssuePreviewView() *issueview.Model {
	if m.issuePreviewView == nil && m.issuePreviewData != nil {
		m.issuePreviewView = issueview.New(nil)
		m.issuePreviewView.SetData(m.issuePreviewData)
	}
	if m.issuePreviewView != nil && len(m.issuePreviewView.ActionHints()) == 0 {
		// The card's own ACTIONS row is the modal's only hint line: it already
		// varies with focused/active and with parent/sibling/child presence, so
		// the modal-wide keys are folded in here rather than duplicated in a
		// hand-rolled footer.
		m.issuePreviewView.SetActionHints([]issueview.ActionHint{
			{Key: "j/k", Label: "scroll"},
			{Key: "o", Label: "open"},
			{Key: "b", Label: "back"},
			{Key: "y", Label: "yank"},
			{Key: "esc", Label: "close"},
		})
	}
	return m.issuePreviewView
}

func issuePreviewViewportHeight(screenH int) int {
	// Match modal.desiredModalInnerHeight (screenH-6) minus the spacer and the
	// button row. The modal has no footer of its own — the card renders the
	// single hint row inside its own ACTIONS section.
	h := screenH - 11
	if h < 8 {
		h = 8
	}
	if h > 36 {
		h = 36
	}
	return h
}

func (m *Model) ensureIssuePreviewModal() {
	// Use 80% of terminal width so the issue is comfortable to read
	modalW := m.width * 4 / 5
	if modalW > m.width-4 {
		modalW = m.width - 4
	}
	if modalW < 30 {
		modalW = 30
	}

	cacheH := m.height
	if m.issuePreviewModal != nil && m.issuePreviewModalWidth == modalW && m.issuePreviewModalHeight == cacheH {
		return
	}
	m.issuePreviewModalWidth = modalW
	m.issuePreviewModalHeight = cacheH

	if m.issuePreviewLoading && (m.issuePreviewView == nil || m.issuePreviewView.Loading()) {
		m.issuePreviewModal = modal.New("Loading...",
			modal.WithWidth(modalW),
			modal.WithHints(false),
		).
			AddSection(modal.Text("Fetching issue data..."))
		return
	}

	if m.issuePreviewError != nil && (m.issuePreviewView == nil || m.issuePreviewView.Data() == nil) {
		m.issuePreviewModal = modal.New("Issue Not Found",
			modal.WithWidth(modalW),
			modal.WithVariant(modal.VariantDanger),
			modal.WithHints(false),
		).
			AddSection(modal.Text(m.issuePreviewError.Error())).
			AddSection(modal.Spacer()).
			AddSection(modal.Buttons(
				modal.Btn(" Close ", "cancel"),
			))
		return
	}

	view := m.ensureIssuePreviewView()
	if view == nil || (m.issuePreviewData == nil && view.Data() == nil && !view.Loading()) {
		m.issuePreviewModal = nil
		return
	}

	viewH := issuePreviewViewportHeight(m.height)
	b := modal.New("",
		modal.WithWidth(modalW),
		modal.WithHints(false),
	)

	b = b.AddSection(modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		v := m.ensureIssuePreviewView()
		if v == nil {
			return modal.RenderedSection{Content: " "}
		}
		v.SetFocused(focusID == issueViewFocusID)
		v.SetSize(contentWidth, viewH)
		return modal.RenderedSection{
			Content: v.View(),
			Focusables: []modal.FocusableInfo{{
				ID:      issueViewFocusID,
				OffsetX: 0,
				OffsetY: 0,
				Width:   contentWidth,
				Height:  viewH,
			}},
		}
	}, func(msg tea.Msg, focusID string) (string, tea.Cmd) {
		v := m.ensureIssuePreviewView()
		if v == nil {
			return "", nil
		}
		key, ok := msg.(tea.KeyPressMsg)
		if !ok {
			return "", nil
		}
		if key.String() == "enter" && !v.Active() {
			v.SetActive(true)
			v.SetFocused(true)
			return "", nil
		}
		if v.Active() {
			_, cmd := v.HandleKey(key)
			return "", cmd
		}
		return "", nil
	}))

	b = b.AddSection(modal.Spacer())
	b = b.AddSection(modal.Buttons(
		modal.Btn(" Open in TD ", "open-in-td", modal.BtnPrimary()),
		modal.Btn(" Back ", "back"),
		modal.Btn(" Close ", "cancel"),
	))

	m.issuePreviewModal = b
}
