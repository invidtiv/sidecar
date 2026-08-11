package app

import (
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
	"github.com/marcus/sidecar/internal/version"
)

const changelogURL = "https://raw.githubusercontent.com/marcus/sidecar/main/CHANGELOG.md"

// changelogViewState holds mutable state shared between the model and the
// modal's Custom section closure. Using a heap-allocated struct avoids
// rebuilding the modal on every scroll event (bubbletea value semantics
// would otherwise make the closure capture a stale model pointer).
type changelogViewState struct {
	ScrollOffset    int
	RenderedLines   []string
	MaxVisibleLines int
}

// updateModalWidth returns the appropriate modal width based on screen size.
func (m *Model) updateModalWidth() int {
	modalW := 60
	maxW := m.width - 4
	if maxW < 20 {
		maxW = 20 // Absolute minimum for very small screens
	}
	if modalW > maxW {
		modalW = maxW
	}
	if modalW < 30 {
		modalW = 30
	}
	// Final cap: never exceed available space
	if modalW > maxW {
		modalW = maxW
	}
	return modalW
}

// renderUpdateModalOverlay renders the update modal as an overlay on top of background.
func (m *Model) renderUpdateModalOverlay(background string) string {
	// Render modal content based on state
	var modalContent string
	switch m.updateModalState {
	case UpdateModalPreview:
		modalContent = m.renderUpdatePreviewModal()
	case UpdateModalProgress:
		modalContent = m.renderUpdateProgressModal()
	case UpdateModalComplete:
		modalContent = m.renderUpdateCompleteModal()
	case UpdateModalError:
		modalContent = m.renderUpdateErrorModal()
	default:
		return background
	}

	return ui.OverlayModal(background, modalContent, m.width, m.height)
}

// clearUpdatePreviewModal drops the cached preview modal so it rebuilds from
// the current target list.
func (m *Model) clearUpdatePreviewModal() {
	m.updatePreviewModal = nil
	m.updatePreviewModalWidth = 0
}

// clearUpdateResultModals drops the cached completion/error modals. They render
// from immutable results, so they must be rebuilt when a batch settles.
func (m *Model) clearUpdateResultModals() {
	m.updateCompleteModal = nil
	m.updateCompleteModalWidth = 0
	m.updateErrorModal = nil
	m.updateErrorModalWidth = 0
}

// targetRow renders one product row for the preview: what changes, from which
// version to which, and how Sidecar would do it.
func targetRow(t version.Target) string {
	arrow := lipgloss.NewStyle().Foreground(styles.Success).Render(" → ")
	line := fmt.Sprintf("  %s %s%s%s", t.DisplayName, t.CurrentVersion, arrow, t.LatestVersion)
	if t.Install.Managed {
		return line + styles.Muted.Render("  · "+t.Install.Method.String())
	}
	return line + styles.Muted.Render("  · manual") +
		"\n" + styles.Muted.Render("      run: "+t.Install.ManualCommand)
}

// previewChromeLines estimates everything in the preview other than the
// release notes: borders, padding, the intro line, one or two lines per target
// row, the Tasks explanation, the section headers, and the button row.
func previewChromeLines(targets int, includesTasks bool) int {
	chrome := 16 + targets*2
	if includesTasks {
		chrome += 5
	}
	return chrome
}

// ensureUpdatePreviewModal builds the preview from the plan the confirmation
// would run, so the user sees every product that will change before choosing.
func (m *Model) ensureUpdatePreviewModal() {
	plan := version.SelectPlan(m.products)
	if len(plan) == 0 {
		return
	}
	modalW := m.updateModalWidth()
	if m.updatePreviewModal != nil && m.updatePreviewModalWidth == modalW {
		return
	}
	m.updatePreviewModalWidth = modalW
	contentW := modalW - 6 // borders + padding

	var rows []string
	includesSidecar, includesTasks, anyManaged := false, false, false
	for _, t := range plan {
		rows = append(rows, targetRow(t))
		switch t.Product {
		case version.ProductSidecar:
			includesSidecar = true
		case version.ProductTasks:
			includesTasks = true
		}
		if t.Install.Managed {
			anyManaged = true
		}
	}

	intro := "Updating will change:"

	mdl := modal.New("Update available",
		modal.WithWidth(modalW),
		modal.WithVariant(modal.VariantDefault),
		modal.WithPrimaryAction("update"),
	).
		AddSection(modal.Text(styles.Muted.Render(intro))).
		AddSection(modal.Text(strings.Join(rows, "\n")))

	if includesTasks {
		// Updating Sidecar refreshes its embedded Tasks plugin; updating Tasks
		// refreshes the standalone commands. They are different artifacts.
		mdl = mdl.AddSection(modal.Spacer()).
			AddSection(modal.Text(styles.Muted.Render(
				"Tasks here is the standalone tasks/tasks-tui/tasks-api commands.\nSidecar's embedded Tasks tab updates with Sidecar itself.")))
	}

	if includesSidecar {
		notes := m.updateNotes
		if notes == "" {
			notes = "No release notes available."
		}
		rendered := m.renderReleaseNotes(parseReleaseNotes(notes), contentW)
		lines := strings.Split(rendered, "\n")
		// Budget the notes against the terminal, not a fixed number: the rows
		// and the confirm buttons must stay on screen at any height.
		maxLines := m.height - previewChromeLines(len(plan), includesTasks)
		if maxLines > 12 {
			maxLines = 12
		}
		if maxLines < 2 {
			maxLines = 2
		}
		if len(lines) > maxLines {
			lines = lines[:maxLines]
			lines = append(lines, styles.Muted.Render("... (truncated)"))
		}
		mdl = mdl.AddSection(modal.Spacer()).
			AddSection(modal.Text(lipgloss.NewStyle().Bold(true).Render("What's New in Sidecar"))).
			AddSection(modal.Spacer()).
			AddSection(modal.Text(strings.Join(lines, "\n"))).
			AddSection(modal.Spacer()).
			AddSection(modal.Text(styles.Muted.Render("[c] View Full Changelog (Sidecar only)")))
	}

	buttons := []modal.ButtonDef{modal.Btn(" Close ", "cancel")}
	if anyManaged {
		buttons = []modal.ButtonDef{
			modal.Btn(" Update Now ", "update"),
			modal.Btn(" Later ", "cancel"),
		}
	}

	m.updatePreviewModal = mdl.
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(buttons...))
}

// renderUpdatePreviewModal renders the preview state shown before confirming.
func (m *Model) renderUpdatePreviewModal() string {
	m.ensureUpdatePreviewModal()
	if m.updatePreviewModal == nil {
		return ""
	}
	if m.updatePreviewMouseHandler == nil {
		m.updatePreviewMouseHandler = mouse.NewHandler()
	}
	return m.updatePreviewModal.Render(m.width, m.height, m.updatePreviewMouseHandler)
}

// parseReleaseNotes cleans up release notes by removing duplicate headers
// and excessive whitespace. The modal already shows "What's New" as a header,
// so we strip any leading "What's New" headers from the content.
func parseReleaseNotes(notes string) string {
	if notes == "" {
		return notes
	}

	// Patterns to strip from the beginning of release notes
	// Match: ## What's New, ### What's New, # What's New (case-insensitive)
	// Also match: # Release Notes, ## Release Notes
	headerPatterns := regexp.MustCompile(`(?im)^#+\s*(what'?s?\s*new|release\s*notes)\s*\n*`)

	result := notes

	// Strip leading whitespace and newlines first
	result = strings.TrimSpace(result)

	// Repeatedly strip matching headers from the beginning
	// (in case there are multiple, e.g., "## What's New\n### What's New\n")
	for {
		loc := headerPatterns.FindStringIndex(result)
		if loc == nil || loc[0] != 0 {
			break
		}
		result = result[loc[1]:]
		result = strings.TrimSpace(result)
	}

	// Collapse multiple consecutive newlines to at most 2
	multiNewlines := regexp.MustCompile(`\n{3,}`)
	result = multiNewlines.ReplaceAllString(result, "\n\n")

	// If parsing emptied the content, return original
	if strings.TrimSpace(result) == "" {
		return strings.TrimSpace(notes)
	}

	return result
}

// renderReleaseNotes renders markdown release notes.
func (m *Model) renderReleaseNotes(notes string, width int) string {
	// Try to use markdown renderer
	renderer, err := markdown.NewRenderer()
	if err != nil {
		return notes
	}

	lines := renderer.RenderContent(notes, width)
	return strings.Join(lines, "\n")
}

// centerText centers text within a given width.
func centerText(text string, width int) string {
	textWidth := lipgloss.Width(text)
	if textWidth >= width {
		return text
	}
	padding := (width - textWidth) / 2
	return strings.Repeat(" ", padding) + text
}

// dividerWidth keeps a full-width rule inside the modal's padded content box.
func dividerWidth(contentW int) int {
	if contentW <= 2 {
		return 1
	}
	return contentW - 2
}

// resultIcon renders the settled status of one target.
func resultIcon(status version.ResultStatus) string {
	switch status {
	case version.StatusUpdated:
		return lipgloss.NewStyle().Foreground(styles.Success).Render("✓")
	case version.StatusFailed:
		return lipgloss.NewStyle().Foreground(styles.Error).Render("✗")
	default:
		return lipgloss.NewStyle().Foreground(styles.TextMuted).Render("•")
	}
}

// resultLabel words a settled outcome for the user.
func resultLabel(r version.Result) string {
	switch r.Status {
	case version.StatusUpdated:
		return "updated to " + r.Version
	case version.StatusUpToDate:
		return "already current"
	case version.StatusManual:
		return "needs a manual update"
	default:
		return "failed"
	}
}

// renderUpdateProgressModal shows which product is being changed right now,
// plus the settled rows for targets that already finished.
func (m *Model) renderUpdateProgressModal() string {
	modalW := m.updateModalWidth()
	contentW := modalW - 4

	var sb strings.Builder

	title := lipgloss.NewStyle().Bold(true).Foreground(styles.Warning).Render("Updating")
	sb.WriteString(centerText(title, contentW))
	sb.WriteString("\n\n")

	for i, t := range m.updatePlan {
		switch {
		case i < len(m.updateResults):
			r := m.updateResults[i]
			fmt.Fprintf(&sb, "  %s %s %s\n", resultIcon(r.Status), t.DisplayName,
				lipgloss.NewStyle().Foreground(styles.TextMuted).Render(resultLabel(r)))
		case i == m.updateActiveIdx:
			action := "installing " + t.LatestVersion
			if t.Install.Managed {
				action += " via " + t.Install.Method.String()
			}
			fmt.Fprintf(&sb, "  %s %s %s\n",
				lipgloss.NewStyle().Foreground(styles.Warning).Render("●"),
				lipgloss.NewStyle().Bold(true).Render(t.DisplayName),
				lipgloss.NewStyle().Foreground(styles.TextMuted).Render(action))
		default:
			fmt.Fprintf(&sb, "  %s %s\n",
				lipgloss.NewStyle().Foreground(styles.TextMuted).Render("○"),
				lipgloss.NewStyle().Foreground(styles.TextMuted).Render(t.DisplayName))
		}
	}

	sb.WriteString("\n")

	elapsed := lipgloss.NewStyle().Foreground(styles.TextMuted).Render(
		fmt.Sprintf("Elapsed: %s", formatElapsed(m.getUpdateElapsed())))
	sb.WriteString(centerText(elapsed, contentW))
	sb.WriteString("\n\n")

	sb.WriteString(lipgloss.NewStyle().Foreground(styles.TextMuted).Render(strings.Repeat("─", dividerWidth(contentW))))
	sb.WriteString("\n\n")

	// The running package-manager subprocess is not cancellable, so do not
	// offer a cancel that would not stop it.
	hint := lipgloss.NewStyle().Foreground(styles.TextMuted).Render("Update in progress")
	sb.WriteString(centerText(hint, contentW))

	maxHeight := m.height - 4
	if maxHeight < 10 {
		maxHeight = 10
	}

	modalStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(styles.TextMuted).
		Padding(1, 2).
		Width(modalW).
		MaxHeight(maxHeight)

	return modalStyle.Render(sb.String())
}

// getUpdateElapsed returns the elapsed time since update started.
func (m *Model) getUpdateElapsed() time.Duration {
	if m.updateStartTime.IsZero() {
		return 0
	}
	return time.Since(m.updateStartTime)
}

// formatElapsed formats a duration as M:SS.
func formatElapsed(d time.Duration) string {
	minutes := int(d.Minutes())
	seconds := int(d.Seconds()) % 60
	return fmt.Sprintf("%d:%02d", minutes, seconds)
}

// ensureUpdateCompleteModal renders the settled results. It reads only the
// immutable result list, so its copy cannot be invalidated by later discovery.
func (m *Model) ensureUpdateCompleteModal() {
	modalW := m.updateModalWidth()
	if m.updateCompleteModal != nil && m.updateCompleteModalWidth == modalW {
		return
	}
	m.updateCompleteModalWidth = modalW

	var rows []string
	for _, r := range m.updateResults {
		row := fmt.Sprintf("  %s %s %s", resultIcon(r.Status), r.Target.DisplayName,
			styles.Muted.Render(resultLabel(r)))
		if r.Status == version.StatusManual && r.Target.Install.ManualCommand != "" {
			row += "\n" + styles.Muted.Render("      run: "+r.Target.Install.ManualCommand)
		}
		rows = append(rows, row)
	}
	if len(rows) == 0 {
		rows = append(rows, styles.Muted.Render("  Nothing to update."))
	}

	restartRequired := version.RestartRequired(m.updateResults)
	primary := "cancel"
	if restartRequired {
		primary = "quit"
	}

	mdl := modal.New("Update Complete",
		modal.WithWidth(modalW),
		modal.WithVariant(modal.VariantInfo),
		modal.WithPrimaryAction(primary),
	).
		AddSection(modal.Text(strings.Join(rows, "\n"))).
		AddSection(modal.Spacer())

	var buttons []modal.ButtonDef
	if restartRequired {
		mdl = mdl.
			AddSection(modal.Text(styles.Muted.Render("Restart sidecar to use the new version."))).
			AddSection(modal.Text(styles.Muted.Render("Tip: Press q to quit, then run 'sidecar' again."))).
			AddSection(modal.Spacer())
		buttons = []modal.ButtonDef{
			modal.Btn(" Quit & Restart ", "quit"),
			modal.Btn(" Later ", "cancel"),
		}
	} else {
		mdl = mdl.
			AddSection(modal.Text(styles.Muted.Render("Sidecar itself did not change; no restart needed."))).
			AddSection(modal.Spacer())
		buttons = []modal.ButtonDef{modal.Btn(" Close ", "cancel")}
	}

	m.updateCompleteModal = mdl.AddSection(modal.Buttons(buttons...))
}

// renderUpdateCompleteModal renders the completion state.
func (m *Model) renderUpdateCompleteModal() string {
	m.ensureUpdateCompleteModal()
	if m.updateCompleteModal == nil {
		return ""
	}
	if m.updateCompleteMouseHandler == nil {
		m.updateCompleteMouseHandler = mouse.NewHandler()
	}
	return m.updateCompleteModal.Render(m.width, m.height, m.updateCompleteMouseHandler)
}

// ensureUpdateErrorModal renders a batch that settled with failures. Earlier
// successes are retained and each failed target gets its own manual command.
func (m *Model) ensureUpdateErrorModal() {
	modalW := m.updateModalWidth()
	if m.updateErrorModal != nil && m.updateErrorModalWidth == modalW {
		return
	}
	m.updateErrorModalWidth = modalW

	errorStyle := lipgloss.NewStyle().Foreground(styles.Error)

	var rows []string
	for _, r := range m.updateResults {
		row := fmt.Sprintf("  %s %s %s", resultIcon(r.Status), r.Target.DisplayName,
			styles.Muted.Render(resultLabel(r)))
		if r.Status == version.StatusFailed {
			if r.Err != nil {
				row += "\n" + errorStyle.Render("      "+truncateLine(r.Err.Error(), modalW-12))
			}
			if cmd := r.Target.Install.ManualCommand; cmd != "" {
				row += "\n" + styles.Muted.Render("      manual fix: "+cmd)
			}
		}
		if r.Status == version.StatusManual && r.Target.Install.ManualCommand != "" {
			row += "\n" + styles.Muted.Render("      run: "+r.Target.Install.ManualCommand)
		}
		rows = append(rows, row)
	}

	m.updateErrorModal = modal.New("Update Incomplete",
		modal.WithWidth(modalW),
		modal.WithVariant(modal.VariantDanger),
		modal.WithPrimaryAction("retry"),
	).
		AddSection(modal.Text(strings.Join(rows, "\n"))).
		AddSection(modal.Spacer()).
		AddSection(modal.Text(styles.Muted.Render("Retry runs only the failed products."))).
		AddSection(modal.Text(styles.Muted.Render("Report: github.com/marcus/sidecar/issues"))).
		AddSection(modal.Spacer()).
		AddSection(modal.Buttons(
			modal.Btn(" Retry ", "retry"),
			modal.Btn(" Close ", "cancel"),
		))
}

// truncateLine keeps long package-manager errors inside the modal width.
func truncateLine(s string, width int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if width < 10 {
		width = 10
	}
	if lipgloss.Width(s) <= width {
		return s
	}
	runes := []rune(s)
	if len(runes) > width-1 {
		runes = runes[:width-1]
	}
	return string(runes) + "…"
}

// renderUpdateErrorModal renders the error state.
func (m *Model) renderUpdateErrorModal() string {
	m.ensureUpdateErrorModal()
	if m.updateErrorModal == nil {
		return ""
	}
	if m.updateErrorMouseHandler == nil {
		m.updateErrorMouseHandler = mouse.NewHandler()
	}
	return m.updateErrorModal.Render(m.width, m.height, m.updateErrorMouseHandler)
}

// getChangelogModalWidth returns the width for the changelog modal.
func (m *Model) getChangelogModalWidth() int {
	modalW := m.updateModalWidth() + 10
	maxW := m.width - 4
	if modalW > maxW {
		modalW = maxW
	}
	if modalW < 30 {
		modalW = 30
	}
	return modalW
}

// ensureChangelogModal creates/updates the changelog modal with caching.
// The modal is only rebuilt when width or height changes. Scroll offset changes
// are handled dynamically via the shared changelogScrollState pointer.
func (m *Model) ensureChangelogModal() {
	modalW := m.getChangelogModalWidth()
	contentW := modalW - 6 // borders + padding

	// Calculate max visible lines
	modalMaxHeight := m.height - 6
	if modalMaxHeight < 10 {
		modalMaxHeight = 10
	}
	maxContentLines := modalMaxHeight - 8
	if maxContentLines < 5 {
		maxContentLines = 5
	}

	// Check if we can reuse the cached modal
	// Rebuild only if width or max visible lines changed
	if m.changelogModal != nil &&
		m.changelogModalWidth == modalW &&
		m.changelogMaxVisibleLines == maxContentLines {
		return
	}

	m.changelogModalWidth = modalW
	m.changelogMaxVisibleLines = maxContentLines

	// Render changelog content and cache the lines
	content := m.updateChangelog
	if content == "" {
		content = "Loading changelog..."
	}
	renderedContent := m.renderReleaseNotes(content, contentW)
	m.changelogRenderedLines = strings.Split(renderedContent, "\n")

	// Initialize or update the shared scroll state
	if m.changelogScrollState == nil {
		m.changelogScrollState = &changelogViewState{}
	}
	m.changelogScrollState.RenderedLines = m.changelogRenderedLines
	m.changelogScrollState.MaxVisibleLines = maxContentLines

	// Capture shared pointer - survives bubbletea value copies
	state := m.changelogScrollState

	// Create a custom section that handles scrolling dynamically.
	// The closure reads from the shared state pointer so scroll changes
	// don't require rebuilding the modal.
	scrollSection := modal.Custom(func(cw int, focusID, hoverID string) modal.RenderedSection {
		lines := state.RenderedLines
		maxVisible := state.MaxVisibleLines

		// Apply scroll offset with clamping
		startLine := state.ScrollOffset
		maxStart := len(lines) - maxVisible
		if maxStart < 0 {
			maxStart = 0
		}
		if startLine > maxStart {
			startLine = maxStart
		}
		if startLine < 0 {
			startLine = 0
		}

		endLine := startLine + maxVisible
		if endLine > len(lines) {
			endLine = len(lines)
		}

		visibleLines := lines[startLine:endLine]
		visibleContent := strings.Join(visibleLines, "\n")

		// Add scroll indicator if needed
		if len(lines) > maxVisible {
			scrollInfo := styles.Muted.Render(fmt.Sprintf("Lines %d-%d of %d", startLine+1, endLine, len(lines)))
			visibleContent += "\n\n" + scrollInfo
		}

		return modal.RenderedSection{Content: visibleContent}
	}, nil)

	m.changelogModal = modal.New("Changelog",
		modal.WithWidth(modalW),
		modal.WithVariant(modal.VariantDefault),
		modal.WithHints(false), // We show custom hints
	).
		AddSection(scrollSection).
		AddSection(modal.Spacer()).
		AddSection(modal.Text(styles.Muted.Render("j/k scroll   Esc: close"))).
		AddSection(modal.Buttons(
			modal.Btn(" Close ", "cancel"),
		))
}

// clearChangelogModal clears the changelog modal cache.
func (m *Model) clearChangelogModal() {
	m.changelogModal = nil
	m.changelogModalWidth = 0
	m.changelogMouseHandler = nil
	m.changelogRenderedLines = nil
	m.changelogMaxVisibleLines = 0
	m.changelogScrollState = nil
}

// syncChangelogScroll updates the shared scroll state from the model field.
// Call this after modifying changelogScrollOffset instead of clearChangelogModal.
func (m *Model) syncChangelogScroll() {
	if m.changelogScrollState != nil {
		m.changelogScrollState.ScrollOffset = m.changelogScrollOffset
	}
}

// fetchChangelog fetches the CHANGELOG.md from GitHub.
func fetchChangelog() tea.Cmd {
	return func() tea.Msg {
		client := &http.Client{Timeout: 10 * time.Second}
		resp, err := client.Get(changelogURL)
		if err != nil {
			return ChangelogLoadedMsg{Err: err}
		}
		defer func() { _ = resp.Body.Close() }()

		if resp.StatusCode != http.StatusOK {
			return ChangelogLoadedMsg{Err: fmt.Errorf("HTTP %d", resp.StatusCode)}
		}

		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return ChangelogLoadedMsg{Err: err}
		}

		return ChangelogLoadedMsg{Content: string(body)}
	}
}

// renderChangelogOverlay renders the changelog as an overlay on the update preview modal.
func (m *Model) renderChangelogOverlay(background string) string {
	m.ensureChangelogModal()
	if m.changelogModal == nil {
		return background
	}
	if m.changelogMouseHandler == nil {
		m.changelogMouseHandler = mouse.NewHandler()
	}
	modalContent := m.changelogModal.Render(m.width, m.height, m.changelogMouseHandler)
	return ui.OverlayModal(background, modalContent, m.width, m.height)
}
