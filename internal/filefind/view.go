package filefind

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

// The finder presents itself through internal/modal, exactly as
// projectsearch.Search does: same border, same title row, same result list,
// same scrollbar, same stats line. The two surfaces are one component in two
// modes, so anything that is not genuinely different content (the finder has
// no option chips and no file/match hierarchy) is deliberately identical.
//
// Going through the modal is also what makes clicks land: the modal registers
// each row's hit region from the same layout pass that draws it, so the two
// cannot disagree. The hand-rolled version registered rows at fixed screen
// coordinates while the caller drew the box centred, and every click on a
// vertically-centred finder hit the wrong row.

// ItemID is the modal element ID of the row at idx into Matches.
func ItemID(idx int) string {
	return fmt.Sprintf("%s-%d", RegionItem, idx)
}

// ParseItemID reports whether id names a result row, and which one.
func ParseItemID(id string) (int, bool) {
	rest, ok := strings.CutPrefix(id, RegionItem+"-")
	if !ok {
		return 0, false
	}
	idx, err := strconv.Atoi(rest)
	if err != nil {
		return 0, false
	}
	return idx, true
}

// SetSize records the surface the finder will be rendered at. View does this
// too; call it directly when input may arrive before the first render.
func (f *Finder) SetSize(width, height int) {
	if f.width == width && f.height == height {
		return
	}
	f.width, f.height = width, height
	// The modal caches its layout by width; a resize invalidates it.
	f.clearModal()
}

// SetFill switches the finder between the two placements its hosts need. Off
// (the default) it draws a box sized to its own content, which a host centres
// on a screen or in a roomy pane with the surface behind it dimmed. On, it draws
// a box that is exactly the surface it was given, for a pane with no room to
// show anything useful behind the modal.
func (f *Finder) SetFill(fill bool) {
	if f.fill == fill {
		return
	}
	f.fill = fill
	f.clearModal()
}

// SetPreferredWidth overrides how wide the box likes to be when it is sizing
// itself to its content. A host sets it when it knows something about where the
// box will land that the surface cannot: the Files plugin keeps the border off
// the column its pane divider occupies, so the box does not read as welded to
// the frame it is floating over. Zero restores the default.
func (f *Finder) SetPreferredWidth(width int) {
	if f.preferredWidth == width {
		return
	}
	f.preferredWidth = width
	f.clearModal()
}

// View renders the finder at the given size and registers its hit regions on
// handler. The result is the modal box alone; the caller composites it over its
// own background (ui.OverlayModal for a screen, panemodal for a pane).
func (f *Finder) View(width, height int, handler *mouse.Handler) string {
	f.SetSize(width, height)
	f.ensureModal()
	if f.modal == nil {
		return ""
	}
	return f.modal.Render(f.width, f.height, handler)
}

// ensureModal builds/rebuilds the modal for the current width.
func (f *Finder) ensureModal() {
	modalW := f.modalWidthForView()
	if f.modal != nil && f.modalWidth == modalW && f.modalFill == f.fill {
		return
	}
	f.modalWidth = modalW

	opts := []modal.Option{modal.WithWidth(modalW), modal.WithHints(false)}
	if f.fill {
		// No margin: the box is the surface, so there is nothing to keep clear
		// around it.
		opts = append(opts, modal.WithMargin(0, 0))
	}

	f.modal = modal.New("", opts...).
		AddSection(f.headerSection()).
		AddSection(modal.When(f.hasScanError, f.errorSection())).
		AddSection(modal.When(f.hasScanError, modal.Spacer())).
		AddSection(f.resultsSection()).
		AddSection(modal.When(f.hasStats, modal.Spacer())).
		AddSection(modal.When(f.hasStats, f.statsSection()))
	f.modalFill = f.fill
}

func (f *Finder) clearModal() {
	f.modal = nil
	f.modalWidth = 0
}

// modalWidthForView mirrors projectsearch.Search.modalWidthForView, with the
// finder's narrower preferred width: rows are paths, not source lines. Filling,
// the box is the surface exactly.
func (f *Finder) modalWidthForView() int {
	if f.fill {
		return maxInt(f.width, 1)
	}
	modalW := f.preferredWidth
	if modalW <= 0 {
		modalW = PreferredWidth
	}
	maxWidth := modal.ContentBoxWidth(f.width)
	if modalW > maxWidth {
		modalW = maxWidth
	}
	minWidth := 30
	if maxWidth < minWidth {
		minWidth = maxWidth
	}
	if modalW < minWidth {
		modalW = minWidth
	}
	return modalW
}

// PreferredWidth is how wide the finder's box likes to be when the surface has
// room to spare.
const PreferredWidth = 80

func (f *Finder) hasScanError() bool { return f.Cache != nil && f.Cache.ErrText != "" }

// hasStats reports whether the counts line is affordable. It does not ask
// whether there is anything to count: a line that comes and goes with the
// results makes the whole box change height as the user types.
func (f *Finder) hasStats() bool {
	return f.height-f.chromeHeight()-f.overheadWithoutStats()-statsHeight >= 1
}

// overheadWithoutStats is everything above the list.
func (f *Finder) overheadWithoutStats() int {
	overhead := titleHeight
	if f.compact() {
		overhead--
	}
	if f.hasScanError() {
		overhead += 2 // warning line + blank line
	}
	return overhead
}

// statsHeight is the counts line plus the blank line above it.
const statsHeight = 2

// maxVisible is how many rows the list gets. Filling, that is everything the
// box has left after the rows drawn around the list, so the modal ends up
// exactly the size of the surface. Otherwise it is a preferred row count that
// does not depend on how many matches there are — the box must not breathe as
// the user types — clamped to what the surface can hold. Counting the overhead
// exactly is what keeps the box from overflowing into a scrollbar it does not
// need, and the search budgets its list the same way.
func (f *Finder) maxVisible() int {
	overhead := f.overheadWithoutStats()
	if f.hasStats() {
		overhead += statsHeight
	}

	available := f.height - f.chromeHeight() - overhead
	if available < 1 {
		available = 1
	}
	if f.fill {
		return available
	}
	return minInt(modal.ListRowsFor(f.height, f.seenRows, f.currentRows()), available)
}

// currentRows is how many rows the list is showing, in the terms
// modal.ListRowsFor asks for: a finder that has not been given a query, or
// whose scan is still running, has not reached a dead end and asks for the
// ordinary floor rather than for nothing.
func (f *Finder) currentRows() int {
	if len(f.matches) > 0 {
		return len(f.matches)
	}
	if f.query == "" || (f.Cache != nil && f.Cache.Scanning) {
		return modal.MinListRows
	}
	return 0
}

// chromeHeight is what the box costs on this surface: border and padding, plus
// the margin the modal keeps clear above and below itself unless it is filling.
func (f *Finder) chromeHeight() int {
	if f.fill {
		return modal.ChromeHeight
	}
	return modal.ChromeHeight + 2*modal.DefaultMarginY
}

func (f *Finder) headerSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		return modal.RenderedSection{Content: f.renderHeader(contentWidth)}
	}, nil)
}

// renderHeader renders the query input row.
func (f *Finder) renderHeader(width int) string {
	prefix := "Quick Open: "
	cursor := "█"

	available := width - ansi.StringWidth(prefix) - 1
	if available < 0 {
		available = 0
	}
	query := f.query
	if ansi.StringWidth(query) > available {
		query = ui.TruncateStart(query, available)
	}

	style := styles.ModalTitle
	if f.compact() {
		// In a pane too short for the title's usual breathing room, the row of
		// results is worth more than the blank line under the query.
		style = style.MarginBottom(0)
	}
	return style.Render(prefix + query + cursor)
}

// compact reports whether the box is too short to afford the blank line the
// title style normally leaves under itself.
func (f *Finder) compact() bool {
	return f.height-f.chromeHeight()-titleHeight < 1
}

// titleHeight is the query row plus the blank line styles.ModalTitle leaves
// under it.
const titleHeight = 2

func (f *Finder) errorSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if !f.hasScanError() {
			return modal.RenderedSection{}
		}
		text := "⚠ " + f.Cache.ErrText
		if ansi.StringWidth(text) > contentWidth {
			text = ui.TruncateString(text, contentWidth)
		}
		return modal.RenderedSection{Content: styles.Muted.Render(text)}
	}, nil)
}

func (f *Finder) resultsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		maxVisible := f.maxVisible()

		// Pad to a stable height so the box does not resize as results land.
		// A single space rather than "" keeps the line from being trimmed.
		padToMinHeight := func(content string) string {
			lines := strings.Split(content, "\n")
			for len(lines) < maxVisible {
				lines = append(lines, " ")
			}
			return strings.Join(lines, "\n")
		}

		if len(f.matches) == 0 {
			switch {
			case f.Cache != nil && f.Cache.Scanning:
				return modal.RenderedSection{Content: padToMinHeight(styles.Muted.Render(
					ui.FitMessage(contentWidth, "Scanning files...", "Scanning...")))}
			case f.query != "":
				return modal.RenderedSection{Content: padToMinHeight(styles.Muted.Render("No matches"))}
			default:
				return modal.RenderedSection{Content: padToMinHeight(styles.Muted.Render(
					ui.FitMessage(contentWidth, "Type to search files...", "Type to search...")))}
			}
		}

		start, end := f.window(maxVisible)

		lines := make([]string, 0, maxVisible)
		focusables := make([]modal.FocusableInfo, 0, maxVisible)

		// The rows are elided as one list rather than one at a time: two
		// different files must never render as the same row, and no per-row
		// elision can promise that (see ui.ElidePathSet). The budget is the
		// same for every row, so the whole visible window is one set.
		fitted := elideMatches(f.matches[start:end], contentWidth-markerWidth)

		for i := start; i < end; i++ {
			itemID := ItemID(i)
			selected := i == f.cursor
			hovered := itemID == hoverID

			lines = append(lines, renderRow(fitted[i-start], selected, hovered, contentWidth))
			focusables = append(focusables, modal.FocusableInfo{
				ID:      itemID,
				OffsetX: 0,
				OffsetY: i - start,
				Width:   contentWidth, // Full width for hover detection
				Height:  1,
			})
		}

		for len(lines) < maxVisible {
			lines = append(lines, " ")
		}

		return modal.RenderedSection{
			Content:    strings.Join(lines, "\n"),
			Focusables: focusables,
		}
	}, nil)
}

// window is the slice of Matches the list shows, scrolled to keep the cursor
// in view.
func (f *Finder) window(maxVisible int) (int, int) {
	listHeight := maxVisible
	if listHeight > len(f.matches) {
		listHeight = len(f.matches)
	}

	start := 0
	if f.cursor >= listHeight {
		start = f.cursor - listHeight + 1
	}
	end := start + listHeight
	if end > len(f.matches) {
		end = len(f.matches)
	}
	return start, end
}

func (f *Finder) statsSection() modal.Section {
	return modal.Custom(func(contentWidth int, focusID, hoverID string) modal.RenderedSection {
		if !f.hasStats() {
			return modal.RenderedSection{}
		}

		// The root is drawn opposite the counts, in every state: a finder rooted
		// at a pane's own directory is often not rooted where the user is
		// reading, and "No matches" about an unnamed directory is unanswerable.
		root := ui.ShortRoot(f.root, rootBudget(contentWidth))
		counts := f.countsText(contentWidth - ansi.StringWidth(root) - 1)
		if counts == "" && root == "" {
			// The row is still drawn: its height is part of the box's, and a
			// line that appears with the first result makes the box jump.
			return modal.RenderedSection{Content: " "}
		}

		return modal.RenderedSection{Content: styles.Muted.Render(ui.JoinEnds(counts, root, contentWidth))}
	}, nil)
}

// rootBudget is how much of the counts row the root may take: enough for a
// project name at any size, never so much that the counts vanish.
func rootBudget(contentWidth int) int {
	budget := contentWidth / 2
	if budget > 28 {
		budget = 28
	}
	return budget
}

// countsText says where the cursor is and how many files were found, in the
// longest phrasing that fits. A query that matched more files than the list
// keeps says so with a "+", so the row is never a claim that these are all of
// them.
func (f *Finder) countsText(width int) string {
	position := ""
	if len(f.matches) > 0 {
		total := strconv.Itoa(len(f.matches))
		if f.truncated {
			total += "+"
		}
		position = fmt.Sprintf("%d/%s  ", f.cursor+1, total)
	}

	stats := ""
	switch {
	case f.Cache == nil:
	case f.Cache.Scanning:
		stats = "scanning..."
	case len(f.Cache.Files) > 0:
		stats = fmt.Sprintf("%d files", len(f.Cache.Files))
	}

	for _, candidate := range []string{position + stats, position, stats} {
		if candidate != "" && ansi.StringWidth(candidate) <= width {
			return strings.TrimRight(candidate, " ")
		}
	}
	return ""
}

// markerWidth is the "> " gutter every row carries, selected or not, so the
// path budget does not change when the cursor moves.
const markerWidth = 2

// renderRow renders one already-fitted result row. Selection and hover paint
// the full width, with the fuzzy-matched characters kept legible inside the
// highlight, which is how projectsearch paints its rows.
func renderRow(row fittedMatch, selected, hovered bool, width int) string {
	marker := "  "
	if selected {
		marker = "> "
	}

	path, ranges := row.text, row.ranges

	if selected || hovered {
		line := marker + path
		if pad := width - ansi.StringWidth(line); pad > 0 {
			line += strings.Repeat(" ", pad)
		}
		return highlightRanges(line, offsetRanges(ranges, len(marker)),
			styleFn(styles.QuickOpenItemSelected), styleFn(styles.SearchMatchCurrent))
	}

	return highlightRanges(marker+path, offsetRanges(ranges, len(marker)),
		styleFn(styles.QuickOpenItem), styleFn(styles.FuzzyMatchChar))
}

func offsetRanges(ranges []MatchRange, by int) []MatchRange {
	if by == 0 || len(ranges) == 0 {
		return ranges
	}
	out := make([]MatchRange, 0, len(ranges))
	for _, r := range ranges {
		out = append(out, MatchRange{Start: r.Start + by, End: r.End + by})
	}
	return out
}
