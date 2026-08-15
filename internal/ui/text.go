package ui

import (
	"os"
	"strings"

	"github.com/mattn/go-runewidth"
)

// Span records that out[Dst:Dst+(SrcEnd-SrcStart)] is a verbatim copy of
// src[SrcStart:SrcEnd]. ElidePath returns one per surviving run so a caller
// holding byte ranges into the original string (fuzzy-match highlights) can
// translate them onto the elided text instead of throwing them away.
type Span struct {
	SrcStart, SrcEnd, Dst int
}

// ElidePath fits a path into width cells while keeping the parts that tell one
// row from another. Rows in a result list usually share a leading prefix, so
// the outermost directories are the least informative thing on the line and are
// spent first — but only as far as the budget demands:
//
//	.claude/skills/create-modal/SKILL.md at 30  ->  .c…/sk…s/create-modal/SKILL.md
//	.claude/skills/create-modal/SKILL.md at 22  ->  .c…/s/cre…dal/SKILL.md
//
// Degrading gradually is the whole point. Spending a directory outright when a
// single cell was needed is how a list of siblings turns into a page of
// identical rows: an all-or-nothing fallback that missed its budget by one cell
// used to throw the entire head away, and `.c` versus `.a` is routinely the only
// thing that differs between two rows.
//
// The order of sacrifice is: shorten outermost directories, then collapse the
// middle ones into a single "…", then — and only then — cut into the filename,
// from its front, so the end of the name and its extension survive. The
// leading segment and the filename are what a reader recognises a row by, so
// they are the last things to go, never the first.
func ElidePath(path string, width int) (string, []Span) {
	if width <= 0 {
		return "", nil
	}
	if runewidth.StringWidth(path) <= width {
		return path, []Span{{SrcStart: 0, SrcEnd: len(path), Dst: 0}}
	}
	if !strings.Contains(path, "/") {
		return truncateStartSpans(path, width)
	}

	segs := splitSegments(path)
	shortenDirs(segs, width)
	if segsWidth(segs) > width {
		segs = collapseMiddle(segs, width)
	}
	if segsWidth(segs) > width {
		if out, spans, ok := keepFilename(segs, width); ok {
			return out, spans
		}
		return truncateStartSpans(path, width)
	}
	out, spans := renderSegments(segs)
	return out, spans
}

// segment is one path component on its way through an elision: where it came
// from, what it currently renders as, and how many bytes of that rendering are
// still a verbatim prefix of the original (which is what a Span reports).
type segment struct {
	srcStart, srcEnd int
	src              string // the segment as it was in the path
	text             string
	keepHead         int // bytes at the front of text that are a verbatim head of the source
	keepTail         int // bytes at the back of text that are a verbatim tail of the source
}

func splitSegments(path string) []*segment {
	var segs []*segment
	start := 0
	for i := 0; i <= len(path); i++ {
		if i == len(path) || path[i] == '/' {
			segs = append(segs, &segment{
				srcStart: start,
				srcEnd:   i,
				src:      path[start:i],
				text:     path[start:i],
				keepHead: i - start,
			})
			start = i + 1
		}
	}
	return segs
}

func segsWidth(segs []*segment) int {
	w := 0
	for i, s := range segs {
		if i > 0 {
			w++ // the separator
		}
		w += runewidth.StringWidth(s.text)
	}
	return w
}

func renderSegments(segs []*segment) (string, []Span) {
	var out strings.Builder
	var spans []Span
	pos := 0
	for i, s := range segs {
		if i > 0 {
			out.WriteString("/")
			pos++
		}
		if s.keepHead > 0 {
			spans = append(spans, Span{SrcStart: s.srcStart, SrcEnd: s.srcStart + s.keepHead, Dst: pos})
		}
		if s.keepTail > 0 {
			// The verbatim run sits after the ellipsis inside the segment.
			spans = append(spans, Span{
				SrcStart: s.srcEnd - s.keepTail,
				SrcEnd:   s.srcEnd,
				Dst:      pos + len(s.text) - s.keepTail,
			})
		}
		out.WriteString(s.text)
		pos += len(s.text)
	}
	return out.String(), spans
}

// shortenDirs shortens directory segments, outermost first, stopping the moment
// the path fits. A segment is abbreviated to its first character (two for a
// dotted name, so the dot does not become the whole segment) only when that is
// no more than the budget needs; when less would do, it keeps as much of its
// name as it can afford and marks the cut with an ellipsis.
func shortenDirs(segs []*segment, width int) {
	for i := 0; i < len(segs)-1; i++ {
		over := segsWidth(segs) - width
		if over <= 0 {
			return
		}
		seg := segs[i]
		full := runewidth.StringWidth(seg.text)
		abbr, keep := markAbbrev(seg.text, i == 0)
		if full-runewidth.StringWidth(abbr) <= over {
			// Even spent entirely it does not free enough; take it and move on.
			seg.text = abbr
			seg.keepHead, seg.keepTail = keep, 0
			continue
		}
		if head, tail, ok := trimSegment(seg.text, full-over); ok {
			seg.text = head + "…" + tail
			seg.keepHead, seg.keepTail = len(head), len(tail)
			return
		}
		seg.text = abbr
		seg.keepHead, seg.keepTail = keep, 0
	}
}

// abbreviateSegment is a directory reduced to the least that still names it.
func abbreviateSegment(seg string) string {
	n := 1
	if strings.HasPrefix(seg, ".") {
		n = 2
	}
	return leadingRunes(seg, n)
}

// markAbbrev is an abbreviated directory as it is drawn, plus how many of its
// bytes are still a verbatim head of the source. mark asks for the ellipsis
// that says letters were dropped.
//
// The leading segment always asks for it. `.claude` rendered as `.c` reads as a
// directory literally named `.c`, and this repo holds both `.claude` and
// `.codex`, so the bare abbreviation is not merely terse but false — while
// every other cut on the row is marked. It is also the segment a reader takes
// as a name: it is the row's identity, and it is a directory they know exists.
// One cell buys the difference between a path that is short and a path that
// lies.
//
// Interior directories do not, and that is a budget decision rather than an
// oversight: their cells are the ones that tell rows apart. Marking every
// segment cost `.claude/skills/create-modal/SKILL.md` and its siblings exactly
// the two cells that rendered them as `.c…/s…/c…l/SKILL.md` rather than as
// three copies of `.c…/s…/c…/SKILL.md`. A row that lies is worse than a row
// that is terse; a column of identical rows is worse than both.
func markAbbrev(seg string, mark bool) (text string, keepBytes int) {
	kept := abbreviateSegment(seg)
	if kept == seg || !mark {
		return kept, len(kept)
	}
	return kept + "…", len(kept)
}

// trimSegment cuts seg down to target cells by eliding its middle, returning
// the head and tail that survive. Both ends are kept because either end can be
// the discriminator: `create-modal` and `create-theme` are a head apart from
// nothing and a tail apart from each other, and a head-only cut renders both as
// "create-…". It reports false when target leaves too little for that to say
// anything.
func trimSegment(seg string, target int) (head, tail string, ok bool) {
	if target < 3 {
		return "", "", false
	}
	visible := target - 1 // the ellipsis costs a cell
	headWidth := (visible + 1) / 2
	// A head shorter than the plain abbreviation says less than the plain
	// abbreviation would, and costs a cell more: ".…e" for ".claude" spends the
	// dot on nothing, where ".c" keeps the letter that tells it from ".agents".
	if minHead := runewidth.StringWidth(abbreviateSegment(seg)); headWidth < minHead {
		if visible-minHead < 1 {
			return "", "", false
		}
		headWidth = minHead
	}
	runes := []rune(seg)

	used := 0
	headEnd := 0
	for headEnd < len(runes) {
		w := runewidth.RuneWidth(runes[headEnd])
		if used+w > headWidth {
			break
		}
		used += w
		headEnd++
	}
	tailWidth := visible - used

	used = 0
	tailStart := len(runes)
	for tailStart > headEnd {
		w := runewidth.RuneWidth(runes[tailStart-1])
		if used+w > tailWidth {
			break
		}
		used += w
		tailStart--
	}
	if headEnd == 0 || tailStart >= len(runes) {
		return "", "", false
	}
	return string(runes[:headEnd]), string(runes[tailStart:]), true
}

// collapseMiddle replaces the directories between the outermost one and the
// file's own parent with a single "…", and drops the parent too when that is
// still not enough. The leading segment survives both steps: it is what
// distinguishes `.claude/...` from `.agents/...`, and a list where every row
// begins "…" is a list of rows that all look alike.
func collapseMiddle(segs []*segment, width int) []*segment {
	if len(segs) < 4 {
		return segs
	}
	head, parent, file := segs[0], segs[len(segs)-2], segs[len(segs)-1]
	marker := &segment{text: "…"}

	// Collapsing frees cells, so the parent can often go back to its full name:
	// "a/…/on/file.go" says where the file is, "a/…/o/file.go" does not.
	full := &segment{srcStart: parent.srcStart, srcEnd: parent.srcEnd, src: parent.src,
		text: parent.src, keepHead: len(parent.src)}

	for _, candidate := range [][]*segment{
		{head, marker, full, file},
		{head, marker, parent, file},
	} {
		if segsWidth(candidate) <= width {
			return candidate
		}
	}
	withoutParent := []*segment{head, marker, file}
	if segsWidth(withoutParent) < segsWidth(segs) {
		return withoutParent
	}
	return segs
}

// keepFilename is the last resort: the directories have nothing left to give,
// so everything above the file is spent on keeping the filename. As much of the
// leading directory as still fits is kept in front of it — ".c…/SKILL.md" says
// more than "…/SKILL.md" — and when the name itself will not fit either it is
// cut from its front, so the end of the name and its extension survive.
//
// Cutting the name here rather than falling back to the end of the whole path
// is what keeps a column reading as one thing. The fallback rendered
// "…nversations-plugin.md" — no directory at all, and a leading ellipsis — next
// to rows that still began with an abbreviated directory, so three different
// elisions shared one list. Every row now begins with a directory and ends with
// the end of the filename.
//
// It reports false only when the row is too narrow for both parts to say
// anything, which is where keeping the end of the path is genuinely the best
// there is.
func keepFilename(segs []*segment, width int) (string, []Span, bool) {
	file := segs[len(segs)-1]

	head, headKeep := markAbbrev(segs[0].src, true)
	budget := width - runewidth.StringWidth(head) - 1 // the separator
	fileWidth := runewidth.StringWidth(file.src)
	if head == "" || budget < 1 {
		return "", nil, false
	}
	if fileWidth > budget && budget < minFilenameCells {
		// The name would have to be cut down to nothing to make room for the
		// directory. At that point the row says more as the end of the path.
		return "", nil, false
	}

	name := &segment{srcStart: file.srcStart, srcEnd: file.srcEnd, src: file.src,
		text: file.src, keepHead: len(file.src)}
	if fileWidth > budget {
		cut := TruncateStart(file.src, budget)
		name.text = cut
		name.keepHead = 0
		name.keepTail = len(strings.TrimPrefix(cut, "…"))
	}

	kept := []*segment{
		{srcStart: segs[0].srcStart, srcEnd: segs[0].srcEnd, src: segs[0].src, text: head, keepHead: headKeep},
		name,
	}
	out, spans := renderSegments(kept)
	return out, spans, true
}

// minFilenameCells is how little of a cut filename is still worth the directory
// in front of it. A name that fits whole always keeps its directory; a name that
// must be cut gives the directory up below this, because three cells out of
// twelve is a quarter of the name spent on one letter of directory, and at that
// width the name is all the row has left.
const minFilenameCells = 12

// leadingRunes returns the first n runes of s (all of s when it is shorter).
func leadingRunes(s string, n int) string {
	count := 0
	for i := range s {
		if count == n {
			return s[:i]
		}
		count++
	}
	return s
}

// truncateStartSpans is TruncateStart with the mapping of what survived.
func truncateStartSpans(s string, width int) (string, []Span) {
	out := TruncateStart(s, width)
	kept := strings.TrimPrefix(out, "…")
	if kept == out {
		return out, []Span{{SrcStart: 0, SrcEnd: len(s), Dst: 0}}
	}
	if kept == "" {
		return out, nil
	}
	return out, []Span{{SrcStart: len(s) - len(kept), SrcEnd: len(s), Dst: len("…")}}
}

// MapSpans translates a byte range in the original string onto the elided one,
// reporting false when the range did not survive the elision.
func MapSpans(spans []Span, start, end int) (int, int, bool) {
	for _, sp := range spans {
		if start >= sp.SrcStart && end <= sp.SrcEnd {
			return sp.Dst + (start - sp.SrcStart), sp.Dst + (end - sp.SrcStart), true
		}
	}
	return 0, 0, false
}

// ShortRoot names a directory in width cells: home-relative if it is under the
// user's home, then the last couple of path segments, then the basename — and
// when even the basename is too long, the basename with its middle elided.
//
// The last resort is a truncation rather than nothing, because a label that
// disappears is worse than a label that is short: this very checkout is named
// `sidecar-files-panel-improvements`, 32 cells against a budget of 28, and the
// counts row simply had no root on it at any width. Both ends of the name are
// kept, for the reason trimSegment keeps both: a project's worktrees share a
// prefix and differ in the suffix.
//
// A search surface that does not say what it is searching is a surface that can
// answer "No matches found" about a directory the user is not looking at — the
// pane's root and the checkout on screen are routinely different in a global
// workspace.
func ShortRoot(root string, width int) string {
	if root == "" || width <= 0 {
		return ""
	}
	segs := strings.Split(strings.TrimSuffix(root, "/"), "/")
	base := segs[len(segs)-1]

	candidates := []string{homeRelative(root)}
	for n := 2; n <= 3 && n < len(segs); n++ {
		candidates = append(candidates, ".../"+strings.Join(segs[len(segs)-n:], "/"))
	}
	candidates = append(candidates, base)
	for _, c := range candidates {
		if c != "" && runewidth.StringWidth(c) <= width {
			return c
		}
	}
	if base == "" {
		return TruncateStart(root, width)
	}
	if head, tail, ok := trimSegment(base, width); ok {
		return head + "…" + tail
	}
	return TruncateStart(base, width)
}

// homeRelative rewrites a path under the user's home as ~/….
func homeRelative(path string) string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return path
	}
	if path == home {
		return "~"
	}
	if rest, ok := strings.CutPrefix(path, home+"/"); ok {
		return "~/" + rest
	}
	return path
}

// FitMessage picks the first of candidates that fits width, falling back to the
// last one truncated. Wording that fits is the cheapest way to keep a box the
// height it budgeted for: a message that wraps costs a row nobody reserved.
func FitMessage(width int, candidates ...string) string {
	if len(candidates) == 0 {
		return ""
	}
	for _, c := range candidates {
		if runewidth.StringWidth(c) <= width {
			return c
		}
	}
	return TruncateString(candidates[len(candidates)-1], width)
}

// JoinEnds lays left and right out on one row of width cells with at least one
// space between them, dropping right when it would not fit and truncating left
// when it does not fit on its own.
func JoinEnds(left, right string, width int) string {
	leftW := runewidth.StringWidth(left)
	rightW := runewidth.StringWidth(right)
	if right == "" || leftW+rightW+1 > width {
		if leftW > width {
			return TruncateString(left, width)
		}
		return left
	}
	return left + strings.Repeat(" ", width-leftW-rightW) + right
}

// TruncateAnchored fits s into width cells around the highlighted range. The
// match itself is never given up while it fits, and the cells left over go to
// the parts of the line that tell one row from another:
//
//   - Leading context is bought only when the match and a useful amount of
//     what follows it are already paid for (leadRoom). Below that the window
//     starts at the match, with no leading ellipsis: in a pane budget of a
//     dozen cells, "… " is two cells spent saying nothing, and it was two of
//     the cells that would have carried the end of the line.
//   - When what follows the match will not fit either, the middle of the
//     remainder is elided rather than its end. Eight rows of
//     "**File:** `internal/plugins/workspace/…`" differ only in their last few
//     characters; a window that runs forward from the match renders all eight
//     identically, while one that keeps the line's end tells them apart.
//
// Positions are rune indices, in and out.
func TruncateAnchored(s string, width int, hlStart, hlEnd int) (string, int, int) {
	runes := []rune(s)
	if width <= 0 {
		return "", 0, 0
	}
	if runewidth.StringWidth(s) <= width {
		return s, hlStart, hlEnd
	}

	hlStart = clampInt(hlStart, 0, len(runes))
	hlEnd = clampInt(hlEnd, hlStart, len(runes))

	matchWidth := runewidth.StringWidth(string(runes[hlStart:hlEnd]))
	lead := 0
	if spare := width - matchWidth; spare >= leadRoom {
		lead = minInt(6, spare/4)
	}

	start := hlStart
	used := 0
	for start > 0 && lead > 0 {
		w := runewidth.RuneWidth(runes[start-1])
		if used+w > lead {
			break
		}
		used += w
		start--
	}

	budget := width
	var out []rune
	if start < hlStart {
		// Only context that is actually there earns the ellipsis that marks it.
		out = append(out, '…')
		budget--
	} else {
		start = hlStart
	}
	// Rune index of the match within the output, which is what callers map
	// their highlight onto.
	newStart := len(out) + (hlStart - start)

	// The whole remainder fits: nothing else to decide.
	if runewidth.StringWidth(string(runes[start:])) <= budget {
		out = append(out, runes[start:]...)
		return string(out), newStart, newStart + (hlEnd - hlStart)
	}

	// Everything through the end of the match, which is the part that must
	// survive if anything does.
	head := runes[start:hlEnd]
	headWidth := runewidth.StringWidth(string(head))
	if headWidth+1 > budget {
		// Not even the match fits: keep as much of it as the cells allow.
		out = append(out, takeCells(head, budget-1)...)
		out = append(out, '…')
		end := len(out) - 1
		if end < newStart {
			end = newStart
		}
		return string(out), newStart, end
	}

	out = append(out, head...)
	end := len(out)
	out = append(out, '…')
	if tail := tailCells(runes[hlEnd:], budget-headWidth-1); len(tail) > 0 {
		out = append(out, tail...)
	}
	return string(out), newStart, end
}

// leadRoom is how much room past the match a line must have before leading
// context is worth its ellipsis.
const leadRoom = 16

// takeCells returns the longest prefix of runes fitting width cells.
func takeCells(runes []rune, width int) []rune {
	used := 0
	for i, r := range runes {
		w := runewidth.RuneWidth(r)
		if used+w > width {
			return runes[:i]
		}
		used += w
	}
	return runes
}

// tailCells returns the longest suffix of runes fitting width cells.
func tailCells(runes []rune, width int) []rune {
	if width <= 0 {
		return nil
	}
	used := 0
	for i := len(runes) - 1; i >= 0; i-- {
		w := runewidth.RuneWidth(runes[i])
		if used+w > width {
			return runes[i+1:]
		}
		used += w
	}
	return runes
}

func clampInt(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

// TruncateString truncates a string to the given visual width.
// It handles multi-byte characters and full-width characters correctly.
// If the string is truncated, "..." is appended (and accounted for in width).
// Pre-condition: width should be at least 3.
func TruncateString(s string, width int) string {
	if width < 3 {
		// Fallback for very small width
		runes := []rune(s)
		if len(runes) > width {
			return string(runes[:width])
		}
		return s
	}

	if runewidth.StringWidth(s) <= width {
		return s
	}

	targetWidth := width - 3

	currentWidth := 0
	runes := []rune(s)
	for i, r := range runes {
		w := runewidth.RuneWidth(r)
		if currentWidth+w > targetWidth {
			return string(runes[:i]) + "..."
		}
		currentWidth += w
	}

	return s
}

// SafeByteSlice extracts a substring using byte positions, ensuring
// the slice boundaries fall on valid UTF-8 boundaries.
// Returns the substring or empty string if positions are invalid.
func SafeByteSlice(s string, byteStart, byteEnd int) string {
	if byteStart < 0 {
		byteStart = 0
	}
	if byteEnd > len(s) {
		byteEnd = len(s)
	}
	if byteStart >= byteEnd || byteStart >= len(s) {
		return ""
	}

	// Convert to runes and find boundaries
	runes := []rune(s)
	bytePos := 0
	runeStart := 0
	runeEnd := len(runes)

	for i, r := range runes {
		if bytePos <= byteStart && bytePos+len(string(r)) > byteStart {
			runeStart = i
		}
		if bytePos < byteEnd {
			runeEnd = i + 1
		}
		bytePos += len(string(r))
		if bytePos >= byteEnd {
			break
		}
	}

	if runeStart >= runeEnd {
		return ""
	}
	return string(runes[runeStart:runeEnd])
}

// BytePosToRunePos converts a byte position to a rune position.
func BytePosToRunePos(s string, bytePos int) int {
	if bytePos <= 0 {
		return 0
	}
	if bytePos >= len(s) {
		return len([]rune(s))
	}

	pos := 0
	for i := range s {
		if i >= bytePos {
			return pos
		}
		pos++
	}
	return pos
}

// TruncateStart truncates the start of the string if it exceeds width.
// The suffix is kept, prefixed with "…" so a path keeps its filename end.
func TruncateStart(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if runewidth.StringWidth(s) <= width {
		return s
	}
	if width == 1 {
		return "…"
	}

	targetWidth := width - 1
	runes := []rune(s)
	currentWidth := 0
	for i := len(runes) - 1; i >= 0; i-- {
		w := runewidth.RuneWidth(runes[i])
		if currentWidth+w > targetWidth {
			return "…" + string(runes[i+1:])
		}
		currentWidth += w
	}
	return s
}
