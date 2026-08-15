package filefind

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/modal"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/scroll"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/ui"
)

const (
	// MaxMatches caps how many fuzzy matches the finder keeps for a query.
	MaxMatches = 50

	// RegionItem is the prefix of the hit region ID registered for each
	// visible row; the row's index into Matches follows it (see ItemID and
	// ParseItemID).
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
	// truncated is set when the query matched more files than the cap keeps.
	truncated bool
	// seenRows is the most rows the list has wanted since this finder was
	// opened; the box sizes itself to it (see modal.ListRows).
	seenRows int

	width, height int
	fill          bool
	// preferredWidth overrides PreferredWidth when a host has a reason to place
	// the box a little differently; 0 means the default.
	preferredWidth int

	modal      *modal.Modal
	modalWidth int
	modalFill  bool
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
	f.seenRows = 0
	if f.modal != nil {
		f.modal.Reset()
	}
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
	f.truncated = false
	f.seenRows = 0
}

// Refilter recomputes the matches from the current query and file list, keeping
// the cursor in range. Hosts that apply scan results to a shared cache
// themselves call this afterwards; Update does it for them.
func (f *Finder) Refilter() {
	// One over the cap, so the list can say it is a list of the best fifty
	// rather than of everything that matched. A capped set presented as the
	// whole answer is the same wrong answer the project search used to give.
	matches := FuzzyFilter(f.Cache.Files, f.query, MaxMatches+1)
	f.truncated = len(matches) > MaxMatches
	if f.truncated {
		matches = matches[:MaxMatches]
	}
	f.matches = matches
	if len(matches) > f.seenRows {
		f.seenRows = len(matches)
	}

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
	f.ensureModal()

	// Motion only updates hover, which the modal owns.
	if _, ok := msg.(tea.MouseMotionMsg); ok {
		if f.modal != nil {
			f.modal.HandleMouse(msg, handler)
		}
		return Result{}, nil
	}

	action := handler.HandleMouse(msg)

	switch action.Type {
	case mouse.ActionClick:
		if action.Region == nil {
			return Result{}, nil
		}
		switch action.Region.ID {
		case "modal-backdrop":
			// Clicking away dismisses the finder, as it does the search.
			f.Reset()
			return Result{Outcome: OutcomeCancelled}, nil
		case "modal-body":
			return Result{}, nil
		}
		if idx, ok := ParseItemID(action.Region.ID); ok {
			f.SetCursor(idx)
		}
		return Result{}, nil

	case mouse.ActionDoubleClick:
		if action.Region != nil {
			if idx, ok := ParseItemID(action.Region.ID); ok {
				f.SetCursor(idx)
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

// RenderMatch renders a single match row: the path fitted to maxWidth with its
// matched characters highlighted.
func RenderMatch(match Match, maxWidth int) string {
	path, ranges := elideMatch(match, maxWidth)
	return HighlightMatch(path, ranges)
}

// elideMatch fits a match's path into maxWidth cells, keeping the parts that
// tell one row from another (see ui.ElidePath) and carrying the match ranges
// across onto whatever text survived, so a narrow row still shows why it
// matched.
//
// A list draws its rows through elideMatches instead: a path fitted on its own
// cannot know that the row above it came out the same.
func elideMatch(match Match, maxWidth int) (string, []MatchRange) {
	if maxWidth < 1 {
		return "", nil
	}
	ranges := significantRanges(match.Path, match.MatchRanges)
	if ansi.StringWidth(match.Path) <= maxWidth {
		return match.Path, ranges
	}

	elided, spans := ui.ElidePath(match.Path, maxWidth)
	return elided, mapRanges(ranges, spans)
}

// fittedMatch is one row's path, cut to the row's budget, with its highlights
// moved onto whatever text survived the cut.
type fittedMatch struct {
	text   string
	ranges []MatchRange
}

// elideMatches fits a whole window of rows at once. Eliding the list rather
// than each path in it is what keeps two different files from arriving as the
// same row — the failure this list has produced three times — because the
// repair for a collision is by definition something no single row can see.
func elideMatches(matches []Match, maxWidth int) []fittedMatch {
	out := make([]fittedMatch, len(matches))
	if len(matches) == 0 {
		return out
	}
	if maxWidth < 1 {
		return out
	}
	paths := make([]string, len(matches))
	for i, match := range matches {
		paths[i] = match.Path
	}
	texts, spans := ui.ElidePathSet(paths, maxWidth)
	for i, match := range matches {
		ranges := significantRanges(match.Path, match.MatchRanges)
		if texts[i] == match.Path {
			out[i] = fittedMatch{text: texts[i], ranges: ranges}
			continue
		}
		out[i] = fittedMatch{text: texts[i], ranges: mapRanges(ranges, spans[i])}
	}
	return out
}

// mapRanges translates match ranges onto elided text, dropping the ones whose
// characters did not survive.
func mapRanges(ranges []MatchRange, spans []ui.Span) []MatchRange {
	if len(ranges) == 0 || len(spans) == 0 {
		return nil
	}
	out := make([]MatchRange, 0, len(ranges))
	for _, r := range ranges {
		if start, end, ok := ui.MapSpans(spans, r.Start, r.End); ok {
			out = append(out, MatchRange{Start: start, End: end})
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// significantRanges drops the highlights a reader would not recognise as part
// of the match. Subsequence matching lights up any character in the right
// order, so a query like "wd" paints the "w" in "website" and the "d" in "docs"
// as though they were the match. A run of two or more characters reads as
// intentional, and so does a single character that starts a path segment or a
// word — which is exactly what the scorer already rewards. Everything else is
// noise scattered across the row.
func significantRanges(path string, ranges []MatchRange) []MatchRange {
	if len(ranges) == 0 {
		return nil
	}
	out := make([]MatchRange, 0, len(ranges))
	for _, r := range ranges {
		if r.Start < 0 || r.End > len(path) || r.End <= r.Start {
			continue
		}
		if r.End-r.Start >= 2 || atWordStart(path, r.Start) {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// atWordStart reports whether the byte at idx begins a path segment or a word
// inside one, including a camelCase hump.
func atWordStart(path string, idx int) bool {
	if idx <= 0 {
		return true
	}
	prev := rune(path[idx-1])
	if isWordSeparator(prev) {
		return true
	}
	return unicode.IsUpper(rune(path[idx])) && unicode.IsLower(prev)
}

// HighlightMatch applies the fuzzy-match highlight style to the matched
// character ranges of text, leaving the rest of it alone.
func HighlightMatch(text string, ranges []MatchRange) string {
	return highlightRanges(text, ranges, nil, styleFn(styles.FuzzyMatchChar))
}

// styleFn adapts a lipgloss style to the renderer highlightRanges takes.
func styleFn(style lipgloss.Style) func(string) string {
	return func(s string) string { return style.Render(s) }
}

// highlightRanges renders text with hl applied to the given byte ranges and
// base applied to everything else. A nil base leaves the surrounding text
// unstyled, which is what a caller compositing into an already-styled line
// wants; a non-nil one re-applies the row style around each highlight so the
// highlight's reset does not punch a hole in a selected row's background.
func highlightRanges(text string, ranges []MatchRange, base, hl func(string) string) string {
	paint := func(s string) string {
		if s == "" || base == nil {
			return s
		}
		return base(s)
	}

	if len(ranges) == 0 {
		return paint(text)
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

		if r.Start > lastEnd {
			result.WriteString(paint(text[lastEnd:r.Start]))
		}

		result.WriteString(hl(text[r.Start:r.End]))
		lastEnd = r.End
	}

	if lastEnd < len(text) {
		result.WriteString(paint(text[lastEnd:]))
	}

	return result.String()
}

// WheelAtBoundary reports whether a wheel event is certainly a no-op for the
// finder. The wheel moves the match cursor wherever the pointer is, so only the
// cursor bounds matter. A scan still in flight can add matches, so it is never
// bounded.
//
// True means "certain no-op"; false means the cursor can move, or the answer is
// unknown. It performs no scans and mutates nothing.
func (f *Finder) WheelAtBoundary(msg tea.MouseWheelMsg) bool {
	if f == nil || (f.Cache != nil && f.Cache.Scanning) {
		return false
	}
	// Mirrors the ±3 HandleMouse applies to the cursor.
	delta := 3
	switch msg.Button {
	case tea.MouseWheelUp:
		delta = -3
	case tea.MouseWheelDown:
	default:
		// Horizontal and shift wheel are outside the vertical contract.
		return false
	}
	return (scroll.Bounds{Position: f.cursor, Maximum: len(f.matches) - 1}).AtBoundary(delta)
}
