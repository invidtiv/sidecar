package projectsearch

import (
	"fmt"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/docview"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	ToggleRegexID = "project-search-toggle-regex"
	ToggleCaseID  = "project-search-toggle-case"
	ToggleWordID  = "project-search-toggle-word"
	OpenActionID  = "project-search-open"

	filePrefix  = "project-search-file-"
	matchPrefix = "project-search-match-"
)

func fileID(fileIdx int) string {
	return fmt.Sprintf("%s%d", filePrefix, fileIdx)
}

func matchID(fileIdx, matchIdx int) string {
	return fmt.Sprintf("%s%d-%d", matchPrefix, fileIdx, matchIdx)
}

// ParseFileID reports whether id names a file header row, and which one.
func ParseFileID(id string) (int, bool) {
	if !strings.HasPrefix(id, filePrefix) {
		return 0, false
	}

	idx, err := strconv.Atoi(strings.TrimPrefix(id, filePrefix))
	if err != nil {
		return 0, false
	}
	return idx, true
}

// ParseMatchID reports whether id names a match row, and which one.
func ParseMatchID(id string) (int, int, bool) {
	if !strings.HasPrefix(id, matchPrefix) {
		return 0, 0, false
	}

	rest := strings.TrimPrefix(id, matchPrefix)
	parts := strings.Split(rest, "-")
	if len(parts) != 2 {
		return 0, 0, false
	}

	fileIdx, err := strconv.Atoi(parts[0])
	if err != nil {
		return 0, 0, false
	}

	matchIdx, err := strconv.Atoi(parts[1])
	if err != nil {
		return 0, 0, false
	}

	return fileIdx, matchIdx, true
}

// renderModal renders the modal at the search's current size, registering hit
// regions on handler.
func (s *Search) renderModal(handler *mouse.Handler) string {
	s.ensureModal()
	if s.modal == nil {
		return ""
	}
	return s.modal.Render(s.width, s.height, handler)
}

// ensureModal builds/rebuilds the modal for the current width.
func (s *Search) ensureModal() {
	if s.State == nil {
		return
	}

	modalW := s.modalWidthForView()
	if s.modal != nil && s.modalWidth == modalW && s.modalFill == s.fill {
		return
	}
	s.modalWidth = modalW

	opts := []modal.Option{
		modal.WithWidth(modalW),
		modal.WithPrimaryAction(OpenActionID),
		modal.WithHints(false),
	}
	if s.fill {
		// No margin: the box is the surface, so there is nothing to keep clear
		// around it.
		opts = append(opts, modal.WithMargin(0, 0))
	}

	s.modal = modal.New("", opts...).
		AddSection(s.headerSection()).
		AddSection(s.optionsSection()).
		AddSection(modal.Spacer()).
		AddSection(s.resultsSection()).
		AddSection(modal.When(s.hasStats, modal.Spacer())).
		AddSection(modal.When(s.hasStats, s.statsSection()))
	s.modalFill = s.fill
}

func (s *Search) modalWidthForView() int {
	if s.fill {
		return maxInt(s.width, 1)
	}
	modalW := PreferredWidth
	maxWidth := s.width - 2*modal.DefaultMarginX
	if maxWidth < 1 {
		maxWidth = 1
	}
	if modalW > maxWidth {
		modalW = maxWidth
	}
	minWidth := 40
	if maxWidth < minWidth {
		minWidth = maxWidth
	}
	if modalW < minWidth {
		modalW = minWidth
	}
	return modalW
}

// PreferredWidth is how wide the search's box likes to be when the surface has
// room to spare. It is wider than the finder's because its rows are source
// lines rather than paths.
const PreferredWidth = 120

func (s *Search) clearModal() {
	s.modal = nil
	s.modalWidth = 0
}

func (s *Search) hasResults() bool {
	return s.State != nil && len(s.State.Results) > 0
}

// hasStats reports whether the counts line is affordable. It does not ask
// whether there is anything to count: a line that comes and goes with the
// results makes the whole box change height as the user types, which is exactly
// what the padding below is there to prevent.
func (s *Search) hasStats() bool {
	return s.height-s.chromeHeight()-searchOverheadWithoutStats-statsHeight >= 1
}

func (s *Search) headerSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if s.State == nil {
			return modal.RenderedSection{}
		}
		return modal.RenderedSection{Content: s.renderHeader(contentWidth)}
	}, nil)
}

func (s *Search) optionsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		state := s.State
		if state == nil {
			return modal.RenderedSection{}
		}

		type option struct {
			id     string
			label  string
			active bool
		}

		opts := []option{
			{id: ToggleRegexID, label: ".*", active: state.UseRegex},
			{id: ToggleCaseID, label: "Aa", active: state.CaseSensitive},
			{id: ToggleWordID, label: `\\b`, active: state.WholeWord},
		}

		var sb strings.Builder
		focusables := make([]modal.FocusableInfo, 0, len(opts))
		x := 0

		for i, opt := range opts {
			if i > 0 {
				sb.WriteString(" ")
				x++
			}

			style := styles.BarChip
			if opt.active || opt.id == focusID || opt.id == hoverID {
				style = styles.BarChipActive
			}

			rendered := style.Render(opt.label)
			sb.WriteString(rendered)

			width := ansi.StringWidth(rendered)
			focusables = append(focusables, modal.FocusableInfo{
				ID:      opt.id,
				OffsetX: x,
				OffsetY: 0,
				Width:   width,
				Height:  1,
			})
			x += width
		}

		return modal.RenderedSection{
			Content:    sb.String(),
			Focusables: focusables,
		}
	}, optionsUpdate)
}

func optionsUpdate(msg tea.Msg, focusID string) (string, tea.Cmd) {
	keyMsg, ok := msg.(tea.KeyPressMsg)
	if !ok {
		return "", nil
	}

	if focusID != ToggleRegexID && focusID != ToggleCaseID && focusID != ToggleWordID {
		return "", nil
	}

	// Note: Space is NOT handled here - it should always add to the search query.
	// Options can be toggled via Enter, mouse click, or alt+r/c/w shortcuts.
	switch keyMsg.String() {
	case "enter":
		return focusID, nil
	}

	return "", nil
}

func (s *Search) resultsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		state := s.State
		if state == nil {
			return modal.RenderedSection{}
		}

		maxVisible := s.maxVisible()

		// Helper to pad content to minimum height so modal doesn't jump in size
		// Uses " " instead of "" so lines aren't trimmed by measureHeight
		padToMinHeight := func(content string) string {
			lines := strings.Split(content, "\n")
			for len(lines) < maxVisible {
				lines = append(lines, " ")
			}
			return strings.Join(lines, "\n")
		}

		if state.IsSearching {
			return modal.RenderedSection{Content: padToMinHeight(styles.Muted.Render("Searching..."))}
		}
		if state.Error != "" {
			return modal.RenderedSection{Content: padToMinHeight(styles.StatusDeleted.Render(state.Error))}
		}
		if len(state.Results) == 0 {
			if state.Query != "" {
				return modal.RenderedSection{Content: padToMinHeight(styles.Muted.Render("No matches found"))}
			}
			return modal.RenderedSection{Content: padToMinHeight(styles.Muted.Render("Type to search project files..."))}
		}

		flatLen := state.FlatLen()
		if flatLen == 0 {
			return modal.RenderedSection{Content: padToMinHeight(styles.Muted.Render("No matches found"))}
		}

		if state.Cursor >= state.ScrollOffset+maxVisible {
			state.ScrollOffset = state.Cursor - maxVisible + 1
		}
		if state.Cursor < state.ScrollOffset {
			state.ScrollOffset = state.Cursor
		}
		if state.ScrollOffset < 0 {
			state.ScrollOffset = 0
		}

		gutter := matchGutter(state.Results)

		var lines []string
		focusables := make([]modal.FocusableInfo, 0, maxVisible)
		flatIdx := 0
		lineY := 0

		for fi, file := range state.Results {
			if flatIdx >= state.ScrollOffset && len(lines) < maxVisible {
				itemID := fileID(fi)
				selected := flatIdx == state.Cursor
				hovered := itemID == hoverID
				line := renderFileHeader(file, selected, hovered, contentWidth)

				lines = append(lines, line)
				focusables = append(focusables, modal.FocusableInfo{
					ID:      itemID,
					OffsetX: 0,
					OffsetY: lineY,
					Width:   contentWidth, // Full width for hover detection
					Height:  1,
				})
				lineY++
			}
			flatIdx++

			if !file.Collapsed {
				for mi, match := range file.Matches {
					if flatIdx >= state.ScrollOffset && len(lines) < maxVisible {
						itemID := matchID(fi, mi)
						selected := flatIdx == state.Cursor
						hovered := itemID == hoverID
						line := renderMatchLine(match, selected, hovered, contentWidth, gutter)

						lines = append(lines, line)
						focusables = append(focusables, modal.FocusableInfo{
							ID:      itemID,
							OffsetX: 0,
							OffsetY: lineY,
							Width:   contentWidth, // Full width for hover detection
							Height:  1,
						})
						lineY++
					}
					flatIdx++
					if len(lines) >= maxVisible {
						break
					}
				}
			}

			if len(lines) >= maxVisible {
				break
			}
		}

		// Pad to minimum height so modal doesn't jump in size
		// Uses " " instead of "" so lines aren't trimmed by measureHeight
		for len(lines) < maxVisible {
			lines = append(lines, " ")
		}
		content := strings.Join(lines, "\n")

		return modal.RenderedSection{
			Content:    content,
			Focusables: focusables,
		}
	}, nil)
}

func (s *Search) statsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		state := s.State
		if state == nil || len(state.Results) == 0 {
			// The row is still drawn: its height is part of the box's, and a
			// line that appears with the first result makes the box jump.
			return modal.RenderedSection{Content: " "}
		}

		position := ""
		flatLen := state.FlatLen()
		if flatLen > 0 {
			position = fmt.Sprintf("%d/%d  ", state.Cursor+1, flatLen)
		}
		stats := fmt.Sprintf("%d matches in %d files", state.TotalMatches(), state.FileCount())

		return modal.RenderedSection{Content: styles.Muted.Render(position + stats)}
	}, nil)
}

// maxVisible is how many rows the results list gets: the modal's inner height
// less everything drawn around the list. Counting the overhead exactly is what
// keeps the box from overflowing into a scrollbar it does not need, and the
// file finder budgets its list the same way. It drops to a single row rather
// than to a floor, because a file pane can be shorter than a modal ever is on
// a screen.
func (s *Search) maxVisible() int {
	overhead := searchOverheadWithoutStats
	if s.hasStats() {
		overhead += statsHeight
	}

	available := s.height - s.chromeHeight() - overhead
	if available < 1 {
		available = 1
	}
	if s.fill {
		return available
	}
	return minInt(modal.PreferredListRows(s.height), available)
}

// searchOverheadWithoutStats is everything drawn above the list: the title row
// plus the blank line its style leaves, the options row, and the blank line
// above the list.
const searchOverheadWithoutStats = 4

// statsHeight is the counts line plus the blank line above it.
const statsHeight = 2

// chromeHeight is what the box costs on this surface: border and padding, plus
// the margin the modal keeps clear above and below itself unless it is filling.
func (s *Search) chromeHeight() int {
	if s.fill {
		return modal.ChromeHeight
	}
	return modal.ChromeHeight + 2*modal.DefaultMarginY
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func maxInt(a, b int) int {
	if a > b {
		return a
	}
	return b
}

// renderHeader renders the search input bar.
func (s *Search) renderHeader(width int) string {
	state := s.State
	// Show block cursor when input focused, thin cursor when results focused
	cursor := "█"
	if state.ResultsFocused {
		cursor = "▏"
	}

	prefix := "Search: "
	available := width - len(prefix) - 1
	if available < 0 {
		available = 0
	}

	query := state.Query
	if len(query) > available {
		query = ui.TruncateStart(query, available)
	}

	header := fmt.Sprintf("%s%s%s", prefix, query, cursor)
	return styles.ModalTitle.Render(header)
}

// renderFileHeader renders a file header line. The path is elided the way the
// finder's rows are — leading directories first, the parent and the filename
// last — so a narrow pane does not fill up with rows that all look alike.
func renderFileHeader(file SearchFileResult, selected, hovered bool, width int) string {
	icon := "▼ "
	if file.Collapsed {
		icon = "▶ "
	}

	matchCount := fmt.Sprintf(" (%d)", len(file.Matches))
	availableWidth := width - ansi.StringWidth(icon) - ansi.StringWidth(matchCount)
	if availableWidth < 1 {
		availableWidth = 1
	}

	path := file.Path
	if ansi.StringWidth(path) > availableWidth {
		path, _ = ui.ElidePath(path, availableWidth)
	}

	if selected || hovered {
		// Build plain text version for full-width highlight
		plainLine := icon + path + matchCount
		// Pad to full width
		if len(plainLine) < width {
			plainLine += strings.Repeat(" ", width-len(plainLine))
		}
		return styles.ListItemSelected.Render(plainLine)
	}

	return fmt.Sprintf("%s%s%s",
		styles.FileBrowserIcon.Render(icon),
		styles.FileBrowserDir.Render(path),
		styles.Muted.Render(matchCount),
	)
}

// matchGutter sizes the line-number column for a whole result set, so the
// column neither clips a five-digit line number nor changes width as the list
// scrolls. Ordinary results keep the historical four-digit column.
func matchGutter(results []SearchFileResult) docview.Gutter {
	maxLine := 1
	for _, file := range results {
		for _, m := range file.Matches {
			if m.LineNo > maxLine {
				maxLine = m.LineNo
			}
		}
	}
	return docview.NewGutter(maxLine).WithSeparator(": ")
}

// renderMatchLine renders a single match line. The window onto the line is
// anchored so the match and what follows it stay visible: a narrow pane gives
// up the leading context first. Centring the window on the match instead clips
// both sides down to the query itself, which renders every row as the same four
// characters.
func renderMatchLine(match SearchMatch, selected, hovered bool, width int, gutter docview.Gutter) string {
	indent := matchIndent(width)
	lineNum := gutter.Plain(match.LineNo)

	availableWidth := width - ansi.StringWidth(indent) - ansi.StringWidth(lineNum)
	if availableWidth < 1 {
		availableWidth = 1
	}

	// The source line's own indentation says nothing here and would be spent
	// before the match ever appeared.
	lineText := strings.TrimSpace(match.LineText)

	runeStart := ui.BytePosToRunePos(match.LineText, match.ColStart)
	runeEnd := ui.BytePosToRunePos(match.LineText, match.ColEnd)

	leadingSpaces := len(match.LineText) - len(strings.TrimLeft(match.LineText, " \t"))
	leadingRuneOffset := ui.BytePosToRunePos(match.LineText, leadingSpaces)
	runeStart -= leadingRuneOffset
	runeEnd -= leadingRuneOffset
	if runeStart < 0 {
		runeStart = 0
	}
	if runeEnd < runeStart {
		runeEnd = runeStart
	}

	lineText, hlStart, hlEnd := ui.TruncateAnchored(lineText, availableWidth, runeStart, runeEnd)

	if selected || hovered {
		// Build plain text for full-width highlight (keeps match visible within selection)
		plainLine := indent + lineNum + lineText
		// Pad to full width
		if pad := width - ansi.StringWidth(plainLine); pad > 0 {
			plainLine += strings.Repeat(" ", pad)
		}
		// Highlight the match within the plain text, in bytes.
		matchStart := len(indent) + len(lineNum) + runeToByte(lineText, hlStart)
		matchEnd := len(indent) + len(lineNum) + runeToByte(lineText, hlEnd)
		return highlightMatchInSelection(plainLine, matchStart, matchEnd)
	}

	highlightedLine := highlightMatchInLineRunes(lineText, hlStart, hlEnd)
	return fmt.Sprintf("%s%s%s",
		indent,
		gutter.Number(match.LineNo),
		highlightedLine,
	)
}

// matchIndent is how far a match row sits under its file header. A narrow pane
// spends two cells on the hierarchy rather than four; the row's content is
// worth more than the extra step.
func matchIndent(width int) string {
	if width < 60 {
		return "  "
	}
	return "    "
}

// runeToByte converts a rune index in s to a byte offset.
func runeToByte(s string, runeIdx int) int {
	if runeIdx <= 0 {
		return 0
	}
	count := 0
	for i := range s {
		if count == runeIdx {
			return i
		}
		count++
	}
	return len(s)
}

// highlightMatchInSelection applies selection style with embedded match highlight.
func highlightMatchInSelection(line string, matchStart, matchEnd int) string {
	if matchStart < 0 {
		matchStart = 0
	}
	if matchEnd > len(line) {
		matchEnd = len(line)
	}
	if matchStart >= matchEnd || matchStart >= len(line) {
		return styles.ListItemSelected.Render(line)
	}

	// Split the line and apply styles
	before := line[:matchStart]
	match := line[matchStart:matchEnd]
	after := line[matchEnd:]

	return styles.ListItemSelected.Render(before) +
		styles.SearchMatchCurrent.Render(match) +
		styles.ListItemSelected.Render(after)
}

// highlightMatchInLineRunes applies highlighting using rune positions (safe for Unicode).
func highlightMatchInLineRunes(lineText string, runeStart, runeEnd int) string {
	runes := []rune(lineText)

	if runeStart < 0 {
		runeStart = 0
	}
	if runeEnd > len(runes) {
		runeEnd = len(runes)
	}
	if runeStart >= runeEnd || runeStart >= len(runes) {
		return lineText
	}

	var result strings.Builder
	if runeStart > 0 {
		result.WriteString(string(runes[:runeStart]))
	}
	result.WriteString(styles.SearchMatchCurrent.Render(string(runes[runeStart:runeEnd])))
	if runeEnd < len(runes) {
		result.WriteString(string(runes[runeEnd:]))
	}

	return result.String()
}
