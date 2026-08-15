package filefind

import (
	"fmt"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	// MaxMatches caps how many fuzzy matches the finder keeps for a query.
	MaxMatches = 50

	// RegionItem is the hit region ID registered for each visible row; the
	// region's Data is the row's index into Matches.
	RegionItem = "quick-open"
)

// Outcome is what a key or mouse event asked the host to do. Everything the
// finder can do to itself it has already done by the time it returns; the
// outcome names only what it cannot decide, because that belongs to whatever
// surface is hosting it.
type Outcome int

const (
	// OutcomeNone: the event was consumed (or ignored); the host has nothing
	// to do.
	OutcomeNone Outcome = iota
	// OutcomeCancelled: the user dismissed the finder.
	OutcomeCancelled
	// OutcomeOpen: the user chose a file. The host decides what "open" means.
	OutcomeOpen
)

// Result carries an Outcome plus what the host needs to act on it.
type Result struct {
	Outcome Outcome
	// Path is relative to the finder's root.
	Path string
	// Line is the 1-based line to land on, or 0 for the top of the file. The
	// finder never selects a line itself; the field exists so hosts can treat
	// it and projectsearch.Result the same way.
	Line int
	// NewTab is set when the user asked for the file beside what they are
	// already looking at rather than in place.
	NewTab bool
}

// Finder is the "open a file by typing part of its name" surface: the file
// list, the query, the matches, the cursor, and the rendering and input
// handling for all of it. It renders at whatever size it is given, so the same
// type serves a full screen and a single pane.
type Finder struct {
	// Cache is the project file list the finder filters. It is a pointer
	// because a host may share one list with other surfaces (the Files plugin
	// filters the same cache for its tree search); pass nil to Open a finder
	// that owns its own.
	Cache *Cache

	root  string
	epoch uint64

	query   string
	matches []Match
	cursor  int
}

// NewFinder creates a finder over cache, rooted at root. A nil cache gives the
// finder one of its own. epoch is stamped on the scans it issues, so a scan for
// a root the host has since switched away from is dropped on arrival.
func NewFinder(cache *Cache, root string, epoch uint64) *Finder {
	if cache == nil {
		cache = &Cache{}
	}
	return &Finder{Cache: cache, root: root, epoch: epoch}
}

// SetRoot points the finder at a different project. The cache is the host's to
// reset, since it may be shared.
func (f *Finder) SetRoot(root string, epoch uint64) {
	f.root, f.epoch = root, epoch
}

// Root is the directory the finder searches.
func (f *Finder) Root() string { return f.root }

// Query is the text typed so far.
func (f *Finder) Query() string { return f.query }

// Matches are the current fuzzy matches, best first.
func (f *Finder) Matches() []Match { return f.matches }

// Cursor is the index into Matches of the highlighted row.
func (f *Finder) Cursor() int { return f.cursor }

// Open clears the query and starts a background scan if the file list is
// missing or stale, returning the command that runs it.
func (f *Finder) Open() tea.Cmd {
	cmd := f.Cache.Ensure(f.root, f.epoch)
	f.query = ""
	f.cursor = 0
	f.Refilter()
	return cmd
}

// SetQuery replaces the typed text and recomputes the matches, for a host that
// seeds the finder from something other than typing.
func (f *Finder) SetQuery(query string) {
	f.query = query
	f.Refilter()
}

// SetCursor moves the highlighted row, clamped to the matches.
func (f *Finder) SetCursor(idx int) {
	if idx < 0 {
		idx = 0
	}
	if idx >= len(f.matches) {
		idx = len(f.matches) - 1
	}
	if idx < 0 {
		idx = 0
	}
	f.cursor = idx
}

// Reset drops the query, matches, and cursor. The file list survives, since it
// describes the project rather than this use of the finder.
func (f *Finder) Reset() {
	f.query = ""
	f.matches = nil
	f.cursor = 0
}

// Refilter recomputes the matches from the current query and file list, keeping
// the cursor in range. Hosts that apply scan results to a shared cache
// themselves call this afterwards; Update does it for them.
func (f *Finder) Refilter() {
	f.matches = FuzzyFilter(f.Cache.Files, f.query, MaxMatches)

	// Reset cursor if out of bounds
	if f.cursor >= len(f.matches) {
		if len(f.matches) > 0 {
			f.cursor = len(f.matches) - 1
		} else {
			f.cursor = 0
		}
	}
}

// Update handles the finder's own async traffic: a landed file scan. Scans for
// a different epoch, and directory scans (which belong to path auto-complete,
// not to the finder), are ignored.
func (f *Finder) Update(msg tea.Msg) tea.Cmd {
	scanned, ok := msg.(ScannedMsg)
	if !ok || scanned.Dirs || scanned.Epoch != f.epoch {
		return nil
	}
	f.Cache.Apply(scanned)
	f.Refilter()
	return nil
}

// HandleKey processes a keypress.
func (f *Finder) HandleKey(msg tea.KeyPressMsg) (Result, tea.Cmd) {
	key := msg.String()
	text := ui.PrintableKeyText(msg)

	switch key {
	case "esc":
		f.Reset()
		return Result{Outcome: OutcomeCancelled}, nil

	case "enter":
		if len(f.matches) > 0 && f.cursor < len(f.matches) {
			return f.selectMatch(false), nil
		}

	case "up", "ctrl+p":
		if f.cursor > 0 {
			f.cursor--
		}

	case "down", "ctrl+n":
		if f.cursor < len(f.matches)-1 {
			f.cursor++
		}

	case "backspace":
		if len(f.query) > 0 {
			runes := []rune(f.query)
			f.query = string(runes[:len(runes)-1])
			f.Refilter()
		}

	default:
		// Append printable characters
		if text != "" {
			f.query += text
			f.Refilter()
		}
	}

	return Result{}, nil
}

// HandleMouse processes a mouse event against the regions the last View
// registered on handler.
func (f *Finder) HandleMouse(msg tea.MouseMsg, handler *mouse.Handler) (Result, tea.Cmd) {
	action := handler.HandleMouse(msg)

	switch action.Type {
	case mouse.ActionClick:
		if action.Region != nil && action.Region.ID == RegionItem {
			if idx, ok := action.Region.Data.(int); ok {
				f.cursor = idx
			}
		}
		return Result{}, nil

	case mouse.ActionDoubleClick:
		if action.Region != nil && action.Region.ID == RegionItem {
			if idx, ok := action.Region.Data.(int); ok {
				f.cursor = idx
				return f.selectMatch(false), nil
			}
		}
		return Result{}, nil

	case mouse.ActionScrollUp, mouse.ActionScrollDown:
		// Scroll the match list
		delta := 3
		if action.Type == mouse.ActionScrollUp {
			delta = -3
		}
		f.cursor += delta
		if f.cursor < 0 {
			f.cursor = 0
		} else if f.cursor >= len(f.matches) {
			f.cursor = len(f.matches) - 1
		}
		return Result{}, nil
	}

	return Result{}, nil
}

// selectMatch resolves the cursor to a path and clears the finder, matching
// what the host sees when the user picks a file.
func (f *Finder) selectMatch(newTab bool) Result {
	if len(f.matches) == 0 || f.cursor >= len(f.matches) {
		return Result{}
	}

	path := f.matches[f.cursor].Path
	f.Reset()
	return Result{Outcome: OutcomeOpen, Path: path, NewTab: newTab}
}

// View renders the finder at the given size and registers its hit regions on
// handler. The result is the modal box alone; the caller composites it over its
// own background (ui.OverlayModal for a screen, panemodal for a pane).
func (f *Finder) View(width, height int, handler *mouse.Handler) string {
	// Modal dimensions
	modalWidth := width - 4
	if modalWidth > 80 {
		modalWidth = 80
	}
	if modalWidth < 30 {
		modalWidth = 30
	}

	// Calculate max visible items based on available height
	// Leave room for: header (2 lines), footer (2 lines), border (2 lines), some padding
	maxListHeight := height - 8
	if maxListHeight < 5 {
		maxListHeight = 5
	}
	if maxListHeight > 20 {
		maxListHeight = 20
	}

	var sb strings.Builder

	// Header with search input
	cursor := "█"
	header := fmt.Sprintf("Quick Open: %s%s", f.query, cursor)
	sb.WriteString(styles.ModalTitle.Render(header))
	sb.WriteString("\n\n")

	// Error message if scan was limited
	if f.Cache.ErrText != "" {
		sb.WriteString(styles.Muted.Render("⚠ " + f.Cache.ErrText))
		sb.WriteString("\n")
	}

	// Calculate modal position for hit region registration
	hPad := (width - modalWidth - 4) / 2
	if hPad < 0 {
		hPad = 0
	}
	modalX := hPad + 1  // +1 for modal border
	modalItemY := 2 + 3 // paddingTop(2) + border(1) + header(2)
	if f.Cache.ErrText != "" {
		modalItemY++ // Extra line for error message
	}

	if len(f.matches) == 0 {
		switch {
		case f.Cache.Scanning:
			sb.WriteString(styles.Muted.Render("Scanning files..."))
		case f.query != "":
			sb.WriteString(styles.Muted.Render("No matches"))
		default:
			sb.WriteString(styles.Muted.Render("Type to search files..."))
		}
	} else {
		// Determine visible range (scroll if cursor out of view)
		listHeight := maxListHeight
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

		for i := start; i < end; i++ {
			match := f.matches[i]
			isSelected := i == f.cursor

			// Register hit region for this row
			itemY := modalItemY + (i - start)
			if handler != nil {
				handler.HitMap.AddRect(RegionItem, modalX, itemY, modalWidth-2, 1, i)
			}

			// Build the display line with highlighted match chars
			line := RenderMatch(match, modalWidth-4)

			if isSelected {
				sb.WriteString(styles.QuickOpenItemSelected.Render("> " + line))
			} else {
				sb.WriteString(styles.QuickOpenItem.Render("  " + line))
			}

			if i < end-1 {
				sb.WriteString("\n")
			}
		}
	}

	// Footer with match count
	if f.Cache.Scanning {
		fmt.Fprintf(&sb, "\n\n%s", styles.Muted.Render("(scanning...)"))
	} else if len(f.matches) > 0 {
		fmt.Fprintf(&sb, "\n\n%s", styles.Muted.Render(fmt.Sprintf("(%d/%d)", f.cursor+1, len(f.matches))))
	} else if len(f.Cache.Files) > 0 {
		fmt.Fprintf(&sb, "\n\n%s", styles.Muted.Render(fmt.Sprintf("(%d files)", len(f.Cache.Files))))
	}

	// Wrap in modal box (centering is the caller's job)
	return styles.ModalBox.
		Width(modalWidth).
		Render(sb.String())
}

// RenderMatch renders a single match row with its matched characters
// highlighted, truncated to maxWidth.
func RenderMatch(match Match, maxWidth int) string {
	path := match.Path

	// Truncate path if too long
	if len(path) > maxWidth {
		path = "..." + path[len(path)-maxWidth+3:]
		// Can't highlight properly after truncation, just return
		return path
	}

	// Apply match highlighting
	if len(match.MatchRanges) > 0 {
		return HighlightMatch(path, match.MatchRanges)
	}

	return path
}

// HighlightMatch applies the fuzzy-match highlight style to the matched
// character ranges of text, leaving the rest of it alone.
func HighlightMatch(text string, ranges []MatchRange) string {
	if len(ranges) == 0 {
		return text
	}

	var result strings.Builder
	lastEnd := 0

	for _, r := range ranges {
		if r.Start > len(text) || r.End > len(text) {
			continue
		}
		if r.Start < lastEnd {
			continue // Skip overlapping
		}

		// Add text before match
		if r.Start > lastEnd {
			result.WriteString(text[lastEnd:r.Start])
		}

		// Add highlighted match
		result.WriteString(styles.FuzzyMatchChar.Render(text[r.Start:r.End]))
		lastEnd = r.End
	}

	// Add remaining text
	if lastEnd < len(text) {
		result.WriteString(text[lastEnd:])
	}

	return result.String()
}
