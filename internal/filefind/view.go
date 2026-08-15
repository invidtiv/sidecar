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
	if f.modal != nil && f.modalWidth == modalW {
		return
	}
	f.modalWidth = modalW

	f.modal = modal.New("",
		modal.WithWidth(modalW),
		modal.WithHints(false),
	).
		AddSection(f.headerSection()).
		AddSection(modal.When(f.hasScanError, f.errorSection())).
		AddSection(modal.When(f.hasScanError, modal.Spacer())).
		AddSection(f.resultsSection()).
		AddSection(modal.When(f.hasStats, modal.Spacer())).
		AddSection(modal.When(f.hasStats, f.statsSection()))
}

func (f *Finder) clearModal() {
	f.modal = nil
	f.modalWidth = 0
}

// modalWidthForView mirrors projectsearch.Search.modalWidthForView, with the
// finder's narrower preferred width: rows are paths, not source lines.
func (f *Finder) modalWidthForView() int {
	modalW := 80
	maxWidth := f.width - 4
	if maxWidth < 1 {
		maxWidth = 1
	}
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

func (f *Finder) hasScanError() bool { return f.Cache != nil && f.Cache.ErrText != "" }

// hasStats reports whether the counts line is both meaningful and affordable.
// In a pane short enough that the line would cost the list its last row, the
// list wins.
func (f *Finder) hasStats() bool {
	if f.Cache == nil || (len(f.matches) == 0 && len(f.Cache.Files) == 0) {
		return false
	}
	return f.height-modalChromeHeight-f.overheadWithoutStats()-statsHeight >= 1
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

// maxVisible is how many rows the list gets: the modal's inner height less
// everything drawn around the list. Counting the overhead exactly is what keeps
// the box from overflowing into a scrollbar it does not need, and the search
// budgets its list the same way. It drops to a single row rather than to a
// floor, because a file pane can be shorter than a modal ever is on a screen.
func (f *Finder) maxVisible() int {
	overhead := f.overheadWithoutStats()
	if f.hasStats() {
		overhead += statsHeight
	}

	height := f.height - modalChromeHeight - overhead
	if height < 1 {
		height = 1
	}
	if height > 30 {
		height = 30
	}
	return height
}

// modalChromeHeight is what internal/modal spends on the box itself: border,
// padding, and the margin it leaves around the modal on screen.
const modalChromeHeight = 6

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
	return f.height-modalChromeHeight-titleHeight < 1
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
				return modal.RenderedSection{Content: padToMinHeight(styles.Muted.Render("Scanning files..."))}
			case f.query != "":
				return modal.RenderedSection{Content: padToMinHeight(styles.Muted.Render("No matches"))}
			default:
				return modal.RenderedSection{Content: padToMinHeight(styles.Muted.Render("Type to search files..."))}
			}
		}

		start, end := f.window(maxVisible)

		lines := make([]string, 0, maxVisible)
		focusables := make([]modal.FocusableInfo, 0, maxVisible)

		for i := start; i < end; i++ {
			itemID := ItemID(i)
			selected := i == f.cursor
			hovered := itemID == hoverID

			lines = append(lines, renderRow(f.matches[i], selected, hovered, contentWidth))
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

		position := ""
		if len(f.matches) > 0 {
			position = fmt.Sprintf("%d/%d  ", f.cursor+1, len(f.matches))
		}

		stats := fmt.Sprintf("%d files", len(f.Cache.Files))
		if f.Cache.Scanning {
			stats = "scanning..."
		}

		return modal.RenderedSection{Content: styles.Muted.Render(position + stats)}
	}, nil)
}

// renderRow renders one result row. Selection and hover paint the full width,
// with the fuzzy-matched characters kept legible inside the highlight, which is
// how projectsearch paints its rows.
func renderRow(match Match, selected, hovered bool, width int) string {
	marker := "  "
	if selected {
		marker = "> "
	}

	available := width - len(marker)
	if available < 1 {
		available = 1
	}

	path, ranges := elideMatch(match, available)

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
