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
//	.claude/skills/create-modal/SKILL.md at 30  ->  .c…/s…/create-modal/SKILL.md
//	.claude/skills/create-modal/SKILL.md at 22  ->  .c…/s…/cre…al/SKILL.md
//
// Every cut is marked with an ellipsis, at every width and in every segment: a
// path that has been shortened must not read as a path that exists (see
// markAbbrev).
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
	e := elide(path, width, noPin)
	return e.text, e.spans
}

// pin asks an elision to keep at least keep runes of the segment at index idx,
// because those are the characters that tell this path from another one in the
// same list. A pinned segment is the last thing spent rather than the first:
// see ElidePathSet, which is the only thing that sets one.
type pin struct {
	idx  int
	keep int
}

// noPin is an elision with nothing to preserve beyond the usual rules.
var noPin = pin{idx: -1}

func (p pin) at(idx int) bool { return p.idx == idx && p.keep > 0 }

// elision is one path fitted to a width: the text, the map back onto the
// original, and the segments it was built from, which is what lets a caller
// holding a whole list compare two rows segment by segment.
type elision struct {
	text  string
	spans []Span
	segs  []*segment
	// structured is false for the last-resort truncation, which has no segment
	// structure left to reason about.
	structured bool
}

// elide is ElidePath with the pin, so the list-aware path and the single-path
// path cannot drift.
func elide(path string, width int, p pin) elision {
	if width <= 0 {
		return elision{}
	}
	if runewidth.StringWidth(path) <= width {
		return elision{text: path, spans: []Span{{SrcStart: 0, SrcEnd: len(path), Dst: 0}}, structured: true}
	}
	if !strings.Contains(path, "/") {
		text, spans := truncateStartSpans(path, width)
		return elision{text: text, spans: spans}
	}

	segs := splitSegments(path)
	if p.idx >= 0 && p.idx < len(segs) {
		segs[p.idx].pinKeep = p.keep
	}
	shortenDirs(segs, width, p)
	if segsWidth(segs) > width {
		segs = collapseMiddle(segs, width, p)
	}
	if segsWidth(segs) > width {
		// A pin outranks the leading directory even here. The rows being
		// repaired share their filename and their head; the pinned segment is
		// the only thing on the row that is not common to all of them, so it is
		// what the last cells are spent on — even at the price of cutting into
		// the name, which is otherwise the last thing to go.
		if p.idx > 0 && p.idx < len(segs)-1 {
			if out, spans, kept, ok := keepFilename([]*segment{segs[p.idx], segs[len(segs)-1]}, width, true); ok {
				return elision{text: out, spans: spans, segs: kept, structured: true}
			}
		}
		if out, spans, kept, ok := keepFilename(segs, width, false); ok {
			return elision{text: out, spans: spans, segs: kept, structured: true}
		}
		text, spans := truncateStartSpans(path, width)
		return elision{text: text, spans: spans}
	}
	out, spans := renderSegments(segs)
	return elision{text: out, spans: spans, segs: segs, structured: true}
}

// ElidePathSet fits every path in paths into width cells, as ElidePath does,
// with one thing ElidePath cannot have: the rest of the list.
//
// Two different files rendering as the same row is the failure this exists to
// prevent, and it is not a failure any single-path elision can rule out. Every
// budget eventually forces a choice between characters, and whichever
// characters a lone path picks may be exactly the ones its neighbour picked
// too: `internal/plugins/tasks/plugin_test.go` and
// `internal/plugins/tdmonitor/plugin_test.go` differ in one directory, at one
// character, and at 22 cells there is no room to show it unless something knows
// to look for it. The list knows.
//
// So the set is elided in three passes:
//
//  1. each path on its own, exactly as ElidePath would;
//  2. a directory abbreviated more than one way in the list is drawn the
//     shortest of those ways everywhere, so one directory reads as one thing;
//  3. any two rows that still render identically are re-elided with the
//     characters that tell them apart pinned, paid for by spending a directory
//     the colliding rows share.
//
// It is not a guarantee — two paths differing only inside a name longer than
// the whole budget cannot be told apart at that budget by anything — but it is
// a guarantee wherever the cells exist, and it fails by leaving rows equal
// rather than by inventing a path that does not exist.
func ElidePathSet(paths []string, width int) ([]string, [][]Span) {
	widths := make([]int, len(paths))
	for i := range widths {
		widths[i] = width
	}
	return ElidePathSetWidths(paths, widths)
}

// ElidePathSetWidths is ElidePathSet for a list whose rows do not all have the
// same budget — a result list with a per-row suffix, say. Rows with different
// budgets still have to be distinguishable from each other, so the collision
// pass spans the whole list either way.
func ElidePathSetWidths(paths []string, widths []int) ([]string, [][]Span) {
	if len(paths) == 0 {
		return nil, nil
	}
	elisions := make([]elision, len(paths))
	for i, path := range paths {
		w := 0
		if i < len(widths) {
			w = widths[i]
		}
		elisions[i] = elide(path, w, noPin)
	}
	harmoniseSegments(elisions)
	repairCollisions(paths, widths, elisions)

	texts := make([]string, len(paths))
	spans := make([][]Span, len(paths))
	for i, e := range elisions {
		texts[i], spans[i] = e.text, e.spans
	}
	return texts, spans
}

// harmoniseSegments makes one directory read as one thing down the list. A
// budget is spent per row, so the same directory could come back from pass one
// as `sk…ls`, `s…s`, and `s…` in three neighbouring rows — three renderings of
// `skills`, which reads as three directories. Where a segment was abbreviated
// more than one way, every abbreviation of it becomes the shortest one, which
// is the only choice guaranteed to fit every row that has to take it.
//
// A row showing the segment in full keeps it: the full name is not a rendering
// a reader can misread, and giving it up would spend legibility on symmetry.
func harmoniseSegments(elisions []elision) {
	type key struct {
		idx int
		src string
	}
	shortest := map[key]*segment{}
	for _, e := range elisions {
		if !e.structured {
			continue
		}
		for i, seg := range e.segs {
			if seg.src == "" || seg.text == seg.src || i == len(e.segs)-1 {
				continue
			}
			k := key{idx: i, src: seg.src}
			if cur, ok := shortest[k]; !ok ||
				runewidth.StringWidth(seg.text) < runewidth.StringWidth(cur.text) {
				copied := *seg
				shortest[k] = &copied
			}
		}
	}
	changed := false
	for _, e := range elisions {
		if !e.structured {
			continue
		}
		for i, seg := range e.segs {
			if seg.src == "" || seg.text == seg.src || i == len(e.segs)-1 {
				continue
			}
			short, ok := shortest[key{idx: i, src: seg.src}]
			if !ok || short.text == seg.text {
				continue
			}
			seg.text, seg.keepHead, seg.keepTail = short.text, short.keepHead, short.keepTail
			changed = true
		}
	}
	if !changed {
		return
	}
	for i := range elisions {
		if len(elisions[i].segs) > 0 {
			elisions[i].text, elisions[i].spans = renderSegments(elisions[i].segs)
		}
	}
}

// repairCollisions re-elides the rows that came back identical, pinning the
// characters that tell them apart. Rows whose paths are equal are not a
// collision — the same file listed twice reads the same on purpose.
func repairCollisions(paths []string, widths []int, elisions []elision) {
	groups := map[string][]int{}
	for i, e := range elisions {
		groups[e.text] = append(groups[e.text], i)
	}
	// Deterministic order: the map is only an index, the work is driven by the
	// list.
	done := map[string]bool{}
	for i, e := range elisions {
		if done[e.text] {
			continue
		}
		done[e.text] = true
		group := groups[e.text]
		if len(group) < 2 || i != group[0] {
			continue
		}
		distinct := map[string]bool{}
		for _, idx := range group {
			distinct[paths[idx]] = true
		}
		if len(distinct) < 2 {
			continue
		}
		repairGroup(paths, widths, elisions, group)
	}
}

// repairGroup re-elides one set of rows that render alike. It walks the
// directories from the one nearest the filename outwards — the nearest is the
// one a reader reads as "where this file lives" — and takes the first that
// actually differs across the group, pinning enough of it to separate the rows
// that share a prefix there.
func repairGroup(paths []string, widths []int, elisions []elision, group []int) {
	segsOf := make([][]string, len(group))
	depth := 0
	for i, idx := range group {
		segsOf[i] = strings.Split(paths[idx], "/")
		if len(segsOf[i]) > depth {
			depth = len(segsOf[i])
		}
	}
	for d := depth - 2; d >= 0; d-- {
		values := make([]string, 0, len(group))
		for _, segs := range segsOf {
			if d < len(segs)-1 {
				values = append(values, segs[d])
			}
		}
		if len(values) < 2 || !differ(values) {
			continue
		}
		candidates := make([]elision, len(group))
		texts := map[string]bool{}
		ok := true
		for i, idx := range group {
			if d >= len(segsOf[i])-1 {
				ok = false
				break
			}
			w := 0
			if idx < len(widths) {
				w = widths[idx]
			}
			candidates[i] = elide(paths[idx], w, pin{idx: d, keep: distinguishingPrefix(segsOf[i][d], values)})
			if texts[candidates[i].text] {
				ok = false
				break
			}
			texts[candidates[i].text] = true
		}
		if !ok {
			continue
		}
		for i, idx := range group {
			elisions[idx] = candidates[i]
		}
		return
	}
}

func differ(values []string) bool {
	for _, v := range values[1:] {
		if v != values[0] {
			return true
		}
	}
	return false
}

// distinguishingPrefix is the fewest leading runes of value that still tell it
// from every other name in others — one past the longest prefix it shares with
// any of them. It is asked per row rather than once for the group because the
// cheapest row should not pay for the dearest: among `tasks`, `git`, and
// `gitstatus`, two characters separate `tasks` from both while `git` needs
// four, and charging every row four is how a repair runs out of cells.
func distinguishingPrefix(value string, others []string) int {
	longest := 0
	for _, other := range others {
		if other == value {
			continue
		}
		if n := commonRunePrefix(value, other); n > longest {
			longest = n
		}
	}
	return longest + 1
}

func commonRunePrefix(a, b string) int {
	ar, br := []rune(a), []rune(b)
	n := 0
	for n < len(ar) && n < len(br) && ar[n] == br[n] {
		n++
	}
	return n
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
	// pinKeep is the fewest runes of src an abbreviation of this segment may
	// keep, because they are what tells this row from another in the same list.
	pinKeep int
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
//
// A pinned segment is skipped on the way down: its characters are the ones
// telling this row from its neighbours, so they are spent last (and, below,
// never at all — the surrounding directories go first).
func shortenDirs(segs []*segment, width int, p pin) {
	for i := 0; i < len(segs)-1; i++ {
		over := segsWidth(segs) - width
		if over <= 0 {
			return
		}
		seg := segs[i]
		full := runewidth.StringWidth(seg.text)
		abbr, keep := markAbbrev(seg)
		if full-runewidth.StringWidth(abbr) <= over {
			// Even spent entirely it does not free enough; take it and move on.
			seg.text = abbr
			seg.keepHead, seg.keepTail = keep, 0
			continue
		}
		if p.at(i) {
			// The pin's whole point is that this segment does not degrade
			// gracefully: half of it is not a discriminator.
			continue
		}
		if head, tail, ok := trimSegment(seg, full-over); ok {
			seg.text = head + "…" + tail
			seg.keepHead, seg.keepTail = len(head), len(tail)
			return
		}
		seg.text = abbr
		seg.keepHead, seg.keepTail = keep, 0
	}
}

// abbreviateSegment is a directory reduced to the least that still names it:
// its first character, two for a dotted name so the dot does not become the
// whole segment, and more when a pin says the extra characters are what tells
// this row from another.
func abbreviateSegment(seg *segment) string {
	n := 1
	if strings.HasPrefix(seg.src, ".") {
		n = 2
	}
	if seg.pinKeep > n {
		n = seg.pinKeep
	}
	return leadingRunes(seg.src, n)
}

// markAbbrev is an abbreviated directory as it is drawn, plus how many of its
// bytes are still a verbatim head of the source.
//
// Every abbreviation is marked, at every width. `.claude` rendered as `.c`
// reads as a directory literally named `.c`, and this repo holds both `.claude`
// and `.codex`; `internal/plugins/tasks` rendered as `i/p/t` reads as a path
// that exists and does not. The marker is the difference between a path that is
// short and a path that lies, and the narrowest budgets — where the segments
// collapse to a single letter — are exactly where the lie is most convincing.
//
// Marking costs a cell per abbreviated directory, and those cells used to be
// spent on telling rows apart instead. They no longer have to be: ElidePathSet
// sees the whole list, notices two rows that would render identically, and buys
// the discriminating characters back by spending a directory the rows share.
// One truthful row plus a list-wide repair beats a terse row that is wrong.
func markAbbrev(seg *segment) (text string, keepBytes int) {
	kept := abbreviateSegment(seg)
	if kept == seg.src {
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
func trimSegment(seg *segment, target int) (head, tail string, ok bool) {
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
	runes := []rune(seg.text)

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
func collapseMiddle(segs []*segment, width int, p pin) []*segment {
	if len(segs) < 4 {
		return segs
	}
	head, parent, file := segs[0], segs[len(segs)-2], segs[len(segs)-1]
	marker := &segment{text: "…"}

	// Collapsing frees cells, so the parent can often go back to its full name:
	// "a/…/on/file.go" says where the file is, "a/…/o/file.go" does not.
	full := &segment{srcStart: parent.srcStart, srcEnd: parent.srcEnd, src: parent.src,
		text: parent.src, keepHead: len(parent.src)}

	candidates := [][]*segment{
		{head, marker, full, file},
		{head, marker, parent, file},
	}
	// A pinned interior directory is kept through the collapse, and — when the
	// budget still will not stretch — kept in preference to the leading one.
	// The head is what the colliding rows have in common; the pin is the only
	// thing that is not.
	if p.idx > 0 && p.idx < len(segs)-1 {
		pinned := segs[p.idx]
		if p.idx == len(segs)-2 {
			candidates = [][]*segment{
				{head, marker, pinned, file},
				{marker, pinned, file},
				{pinned, file},
			}
		} else {
			candidates = [][]*segment{
				{head, marker, pinned, marker, full, file},
				{head, marker, pinned, marker, parent, file},
				{head, marker, pinned, marker, file},
				{marker, pinned, marker, file},
				{pinned, marker, file},
			}
		}
	}
	for _, candidate := range candidates {
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
func keepFilename(segs []*segment, width int, pinned bool) (string, []Span, []*segment, bool) {
	file := segs[len(segs)-1]

	head, headKeep := markAbbrev(&segment{src: segs[0].src, pinKeep: segs[0].pinKeep})
	budget := width - runewidth.StringWidth(head) - 1 // the separator
	fileWidth := runewidth.StringWidth(file.src)
	if head == "" || budget < 1 {
		return "", nil, nil, false
	}
	if fileWidth > budget && budget < minFilenameCells && !pinned {
		// The name would have to be cut down to nothing to make room for the
		// directory. At that point the row says more as the end of the path —
		// unless the directory is a pinned one, in which case the rows share
		// this name and the directory is the only thing that is not shared.
		return "", nil, nil, false
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
	return out, spans, kept, true
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
	if head, tail, ok := trimSegment(&segment{src: base, text: base}, width); ok {
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
