package filefind

import (
	"sort"
	"strings"
	"unicode"
)

// MatchRange represents a contiguous range of matched characters, as byte
// offsets into the matched path.
type MatchRange struct {
	Start int
	End   int
}

// Match represents a file matching the fuzzy query.
type Match struct {
	Path        string       // Relative path from root
	Name        string       // Base filename
	Score       int          // Match score (higher = better)
	MatchRanges []MatchRange // Ranges for highlighting matched chars
}

// The scorer. A query is one or more whitespace-separated terms, and a path
// matches when every term matches it somewhere. Each term is aligned to the
// path by dynamic programming — the best-scoring alignment the pass finds
// wins, not the leftmost one — so "cases" against "recall/packs/firstuse/cases.jsonl"
// lights up the word "cases" rather than a c, an a and an s scattered across
// three directories. The greedy matcher this replaced did exactly that, and
// its score for the scattered alignment was what the row was ranked by.
//
// Scoring is in the style of fzf's v2 algorithm: every matched character is
// worth scoreMatch, a gap between matched characters costs a start penalty plus
// a per-character extension, and a character earns a bonus for where it sits.
// The bonuses are ordered so that the start of a path segment beats the start
// of a word inside one, which beats a camelCase hump, which beats merely
// continuing a run. A run that starts on a boundary keeps that boundary's bonus
// for its whole length, which is what makes a contiguous word at the head of a
// filename decisively better than the same letters found one at a time. (Each
// cell keeps one predecessor, so the carried bonus can in principle lose to a
// predecessor that scored a point or two higher without it; like fzf's, the
// pass is a very good approximation of the optimum rather than a proof of it,
// and no realistic path has been found where the two differ.)
//
// Two things fzf does not do, because it ranks arbitrary lines and this ranks
// paths: a character matched inside the basename is worth more than one
// matched in a directory, and a deeper path pays a small per-segment penalty,
// so "src/test.go" outranks "test/something.go" for "test" and "test.go"
// outranks "a/b/c/test.go". Both are tie-breakers in size: a clearly better
// alignment still wins from any depth.
const (
	scoreMatch     = 16
	scoreGapStart  = -3
	scoreGapExtend = -1

	// bonusSegment is for the first character after a '/' (or of the path).
	bonusSegment = 10
	// bonusWord is for the first character after a separator inside a segment.
	bonusWord = 8
	// bonusCamel is for an upper-case letter after a lower-case one, or a digit
	// after a letter.
	bonusCamel = 7
	// bonusConsecutive is the floor for a character that continues a run.
	bonusConsecutive = 4
	// bonusFirstCharMultiplier doubles the positional bonus of a term's first
	// character: where a term starts matters more than where it continues.
	bonusFirstCharMultiplier = 2
	// bonusBasename is added to every character matched inside the basename.
	bonusBasename = 5
	// penaltyDepth is charged per path segment above the first, capped at
	// penaltyDepthMax so a deep tree is not unsearchable.
	penaltyDepth    = 1
	penaltyDepthMax = 8

	// unreachable marks a DP cell no alignment reaches.
	unreachable = -1 << 30
)

// FuzzyMatch scores how well query matches target. It returns 0 and nil ranges
// when the query does not match; otherwise a positive score and the byte
// ranges of target that matched, in order, for highlighting.
//
// The query is split on whitespace into terms, every one of which must match.
// Matching ignores case, and treats '-', '_' and ' ' as one another, so
// "use_cases" and "use cases" both find "USE-CASES.md".
func FuzzyMatch(query, target string) (int, []MatchRange) {
	var m matcher
	m.setQuery(query)
	return m.match(target)
}

// isWordSeparator returns true for characters that start word boundaries.
func isWordSeparator(r rune) bool {
	return r == '/' || r == '_' || r == '-' || r == '.' || r == ' '
}

// isSeparatorClass reports whether r is one of the characters the matcher
// treats as interchangeable in a query.
func isSeparatorClass(r rune) bool {
	return r == '-' || r == '_' || r == ' '
}

// runesEqual is the matcher's notion of "the same character": lower-cased
// already, and any separator-class character stands for any other.
func runesEqual(q, t rune) bool {
	return q == t || (isSeparatorClass(q) && isSeparatorClass(t))
}

// matcher holds a prepared query and the scratch space the alignment needs, so
// filtering a large file list allocates once rather than once per path.
type matcher struct {
	terms [][]rune

	// Per-target scratch.
	tLower []rune // target, lower-cased, one rune per position
	tOff   []int  // byte offset of each rune position, plus one past the end
	bonus  []int  // positional bonus of each rune position
	base   int    // rune index where the basename starts

	// DP tables, sized n*m for the current term and target.
	score    []int32
	from     []int32
	runBonus []int16
	// positions is the alignment read back from the tables, in rune indices.
	positions []int
}

// setQuery prepares the terms of query.
func (m *matcher) setQuery(query string) {
	m.terms = m.terms[:0]
	for _, field := range strings.Fields(query) {
		runes := []rune(field)
		for i, r := range runes {
			runes[i] = unicode.ToLower(r)
		}
		m.terms = append(m.terms, runes)
	}
}

// prepareTarget lower-cases target into the scratch buffers and computes the
// positional bonus of every character.
func (m *matcher) prepareTarget(target string) {
	m.tLower = m.tLower[:0]
	m.tOff = m.tOff[:0]
	m.bonus = m.bonus[:0]
	m.base = 0

	prev := '/'
	i := 0
	for off, r := range target {
		m.tOff = append(m.tOff, off)
		m.tLower = append(m.tLower, unicode.ToLower(r))
		m.bonus = append(m.bonus, positionBonus(prev, r))
		if r == '/' {
			m.base = i + 1
		}
		prev = r
		i++
	}
	m.tOff = append(m.tOff, len(target))
}

// positionBonus is what a character at a position is worth for where it sits,
// given the character before it.
func positionBonus(prev, cur rune) int {
	switch {
	case prev == '/':
		return bonusSegment
	case isWordSeparator(prev):
		return bonusWord
	case unicode.IsUpper(cur) && unicode.IsLower(prev):
		return bonusCamel
	case unicode.IsDigit(cur) && unicode.IsLetter(prev):
		return bonusCamel
	}
	return 0
}

// match scores target against the prepared query and reports the ranges that
// matched.
func (m *matcher) match(target string) (int, []MatchRange) {
	return m.run(target, true)
}

// scoreOnly is match without the ranges, for the pass over a whole file list: the
// ranges are only ever drawn for the handful of rows that make the cut, and
// allocating them for every path that matched was most of a keystroke's cost.
func (m *matcher) scoreOnly(target string) int {
	total, _ := m.run(target, false)
	return total
}

func (m *matcher) run(target string, withRanges bool) (int, []MatchRange) {
	if len(m.terms) == 0 || target == "" {
		return 0, nil
	}
	// Most of a file list does not match most queries. Rejecting those paths
	// on their bytes, before any rune table is built for them, is what keeps
	// the finder's cost per keystroke close to the greedy matcher's.
	if isASCII(target) {
		for _, term := range m.terms {
			if !asciiSubsequence(term, target) {
				return 0, nil
			}
		}
	}
	m.prepareTarget(target)

	total := 0
	var ranges []MatchRange
	for _, term := range m.terms {
		score, ok := m.alignTerm(term)
		if !ok {
			return 0, nil
		}
		total += score
		if withRanges {
			ranges = appendRuneRanges(ranges, m.positions, m.tOff)
		}
	}

	depth := strings.Count(target, "/")
	if depth > penaltyDepthMax {
		depth = penaltyDepthMax
	}
	total -= depth * penaltyDepth
	if total < 1 {
		// A match is a match: the score is only ever compared to other
		// matches, and callers read 0 as "did not match".
		total = 1
	}

	if !withRanges {
		return total, nil
	}
	return total, mergeRanges(ranges)
}

// alignTerm finds the best-scoring alignment of term against the prepared
// target, leaving the matched positions in m.positions. It reports false when
// the term is not a subsequence of the target.
func (m *matcher) alignTerm(term []rune) (int, bool) {
	n, w := len(term), len(m.tLower)
	if n == 0 || n > w {
		return 0, false
	}

	// A cheap forward pass answers the common question — does this term
	// match at all? — before the tables are touched, and bounds the columns
	// the tables need: nothing before the first possible position of term[0]
	// can be part of any alignment.
	first := -1
	qi := 0
	for j := 0; j < w && qi < n; j++ {
		if runesEqual(term[qi], m.tLower[j]) {
			if qi == 0 {
				first = j
			}
			qi++
		}
	}
	if qi < n {
		return 0, false
	}

	// Columns run over [first, w); cell (i, j) is the best score of an
	// alignment of term[:i+1] that puts term[i] at position first+j.
	cols := w - first
	size := n * cols
	m.score = growInt32(m.score, size)
	m.from = growInt32(m.from, size)
	m.runBonus = growInt16(m.runBonus, size)

	for i := 0; i < n; i++ {
		row := i * cols
		prevRow := row - cols
		q := term[i]

		// best is the highest score reachable for term[:i] ending with a gap
		// before the current column, with the gap's cost already charged;
		// bestK is the column it ends at.
		best, bestK := int32(unreachable), int32(-1)

		for j := 0; j < cols; j++ {
			cell := row + j
			m.score[cell] = unreachable
			m.from[cell] = -1
			m.runBonus[cell] = 0

			if i > 0 {
				// Every open gap grows by one character. The column two
				// back is newly eligible to start a gap of length one.
				if best != unreachable {
					best += scoreGapExtend
				}
				if j >= 2 {
					if prev := m.score[prevRow+j-2]; prev != unreachable && prev+scoreGapStart > best {
						best, bestK = prev+scoreGapStart, int32(j-2)
					}
				}
			}

			t := m.tLower[first+j]
			if !runesEqual(q, t) {
				continue
			}

			pos := first + j
			positional := m.bonus[pos]
			inBase := 0
			if pos >= m.base {
				inBase = bonusBasename
			}

			if i == 0 {
				m.score[cell] = int32(scoreMatch + positional*bonusFirstCharMultiplier + inBase)
				m.runBonus[cell] = int16(positional)
				continue
			}

			// Start a run here, after a gap.
			if best != unreachable {
				m.score[cell] = best + int32(scoreMatch+positional+inBase)
				m.from[cell] = bestK
				m.runBonus[cell] = int16(positional)
			}

			// Or continue the run from the previous column. A run that began
			// on a boundary keeps the boundary's bonus for its whole length;
			// any run is worth at least bonusConsecutive per character.
			if j >= 1 {
				if prev := m.score[prevRow+j-1]; prev != unreachable {
					carried := int(m.runBonus[prevRow+j-1])
					b := positional
					if carried >= bonusWord && b < carried {
						b = carried
					} else if b < bonusConsecutive {
						b = bonusConsecutive
					}
					if s := prev + int32(scoreMatch+b+inBase); s > m.score[cell] {
						m.score[cell] = s
						m.from[cell] = int32(j - 1)
						m.runBonus[cell] = int16(b)
					}
				}
			}
		}
	}

	// The best alignment ends wherever the last row peaks; ties go to the
	// earliest column, which favours a match that starts sooner.
	lastRow := (n - 1) * cols
	bestEnd, bestScore := -1, int32(unreachable)
	for j := 0; j < cols; j++ {
		if s := m.score[lastRow+j]; s > bestScore {
			bestScore, bestEnd = s, j
		}
	}
	if bestEnd < 0 {
		return 0, false
	}

	m.positions = m.positions[:0]
	for i, j := n-1, bestEnd; i >= 0; i-- {
		m.positions = append(m.positions, first+j)
		j = int(m.from[i*cols+j])
	}
	// Reverse into ascending order.
	for a, b := 0, len(m.positions)-1; a < b; a, b = a+1, b-1 {
		m.positions[a], m.positions[b] = m.positions[b], m.positions[a]
	}
	return int(bestScore), true
}

func growInt32(buf []int32, size int) []int32 {
	if cap(buf) < size {
		return make([]int32, size)
	}
	return buf[:size]
}

func growInt16(buf []int16, size int) []int16 {
	if cap(buf) < size {
		return make([]int16, size)
	}
	return buf[:size]
}

// appendRuneRanges appends the byte ranges covering the given ascending rune
// positions, coalescing adjacent positions into one range.
func appendRuneRanges(ranges []MatchRange, positions []int, offsets []int) []MatchRange {
	for i := 0; i < len(positions); {
		j := i
		for j+1 < len(positions) && positions[j+1] == positions[j]+1 {
			j++
		}
		ranges = append(ranges, MatchRange{Start: offsets[positions[i]], End: offsets[positions[j]+1]})
		i = j + 1
	}
	return ranges
}

// isASCII reports whether s has no multi-byte characters, so bytes are runes.
func isASCII(s string) bool {
	for i := 0; i < len(s); i++ {
		if s[i] >= 0x80 {
			return false
		}
	}
	return true
}

// asciiSubsequence is the matcher's subsequence test on bytes, for an ASCII
// target. A term with a non-ASCII character cannot be answered this way and
// is left to the rune path.
func asciiSubsequence(term []rune, target string) bool {
	qi := 0
	for i := 0; i < len(target) && qi < len(term); i++ {
		q := term[qi]
		if q >= 0x80 {
			return true
		}
		c := rune(target[i])
		if c >= 'A' && c <= 'Z' {
			c += 'a' - 'A'
		}
		if runesEqual(q, c) {
			qi++
		}
	}
	return qi == len(term)
}

// mergeRanges sorts ranges and merges the ones that overlap or touch, which
// two terms matching the same stretch of a path produce.
func mergeRanges(ranges []MatchRange) []MatchRange {
	if len(ranges) == 0 {
		return nil
	}
	sorted := true
	for i := 1; i < len(ranges); i++ {
		if ranges[i].Start < ranges[i-1].Start {
			sorted = false
			break
		}
	}
	if !sorted {
		sort.Slice(ranges, func(i, j int) bool { return ranges[i].Start < ranges[j].Start })
	}
	out := ranges[:1]
	for _, r := range ranges[1:] {
		last := &out[len(out)-1]
		if r.Start <= last.End {
			if r.End > last.End {
				last.End = r.End
			}
			continue
		}
		out = append(out, r)
	}
	return out
}

// FuzzySort orders matches best first: by score descending, then shorter path
// first, then by path, so equal candidates come out in the same order every
// time regardless of how the input was ordered.
func FuzzySort(matches []Match) {
	sort.SliceStable(matches, func(i, j int) bool {
		a, b := matches[i], matches[j]
		if a.Score != b.Score {
			return a.Score > b.Score
		}
		if len(a.Path) != len(b.Path) {
			return len(a.Path) < len(b.Path)
		}
		return a.Path < b.Path
	})
}

// FuzzyFilter filters and scores files against a query, returning the best
// maxResults matches in order. See FilterOptions for what an empty query shows.
func FuzzyFilter(files []string, query string, maxResults int) []Match {
	return Filter(files, query, maxResults, FilterOptions{})
}

// FilterOptions tunes Filter beyond the query.
type FilterOptions struct {
	// Recent lists paths the user has been working with, most recent first.
	// With an empty query they lead the list, ahead of the shallowest paths in
	// the project; with a query each earns a small bonus, enough to break a
	// near-tie in favour of the file the user was just looking at but never
	// enough to outrank a clearly better match.
	Recent []string
}

// recentBonus is what the most recent path earns; each older one earns
// recentBonusStep less, down to nothing.
const (
	recentBonus     = 12
	recentBonusStep = 2
)

// Filter is FuzzyFilter with options.
func Filter(files []string, query string, maxResults int, opts FilterOptions) []Match {
	if maxResults <= 0 {
		return nil
	}

	recent := make(map[string]int, len(opts.Recent))
	for i, path := range opts.Recent {
		if _, seen := recent[path]; !seen {
			recent[path] = i
		}
	}

	if query == "" {
		return emptyQueryMatches(files, maxResults, opts.Recent, recent)
	}

	var m matcher
	m.setQuery(query)
	if len(m.terms) == 0 {
		// Whitespace is not a query: it names no term, and a caller that
		// treats "some matches" as a reason to move a cursor must not be
		// handed the shallowest files for a stray space.
		return nil
	}

	// Score every path, but keep only the best maxResults as they go by: a
	// broad query matches most of a big list, and sorting fifty thousand rows
	// to show fifty was the other half of a keystroke's cost.
	best := topK[Match]{limit: maxResults, worse: worseMatch}
	for _, f := range files {
		score := m.scoreOnly(f)
		if score <= 0 {
			continue
		}
		if rank, ok := recent[f]; ok {
			if bonus := recentBonus - rank*recentBonusStep; bonus > 0 {
				score += bonus
			}
		}
		best.offer(Match{Path: f, Score: score})
	}

	matches := best.sorted()
	for i := range matches {
		_, matches[i].MatchRanges = m.match(matches[i].Path)
		matches[i].Name = baseName(matches[i].Path)
	}
	return matches
}

// worseMatch reports whether a sorts after b in FuzzySort's order.
func worseMatch(a, b Match) bool {
	if a.Score != b.Score {
		return a.Score < b.Score
	}
	if len(a.Path) != len(b.Path) {
		return len(a.Path) > len(b.Path)
	}
	return a.Path > b.Path
}

// emptyQueryMatches is what the list shows before the user types: the recent
// files that are still in the project, then the shallowest paths — the files
// at the top of the tree are the ones most worth a keystroke-free open.
func emptyQueryMatches(files []string, maxResults int, recentOrder []string, recent map[string]int) []Match {
	if len(files) == 0 {
		return nil
	}
	present := make(map[string]struct{}, len(recentOrder))
	if len(recent) > 0 {
		for _, f := range files {
			if _, ok := recent[f]; ok {
				present[f] = struct{}{}
			}
		}
	}

	matches := make([]Match, 0, maxResults)
	for _, path := range recentOrder {
		if len(matches) >= maxResults {
			return matches
		}
		if _, ok := present[path]; !ok {
			continue
		}
		delete(present, path)
		matches = append(matches, Match{Path: path, Name: baseName(path), Score: 1})
	}

	// The shallowest few of a list that may run to fifty thousand paths, found
	// without sorting the list: one pass keeps the best handful in a small
	// heap, so an empty query costs about what a keystroke does.
	want := maxResults - len(matches)
	if want <= 0 {
		return matches
	}
	top := topK[shallowItem]{limit: want, worse: deeper}
	for _, f := range files {
		if _, ok := recent[f]; ok {
			continue
		}
		top.offer(shallowItem{path: f, depth: strings.Count(f, "/")})
	}
	for _, item := range top.sorted() {
		matches = append(matches, Match{Path: item.path, Name: baseName(item.path), Score: 1})
	}
	return matches
}

type shallowItem struct {
	path  string
	depth int
}

// deeper reports whether a sorts after b: deeper, then longer, then later.
func deeper(a, b shallowItem) bool {
	if a.depth != b.depth {
		return a.depth > b.depth
	}
	if len(a.path) != len(b.path) {
		return len(a.path) > len(b.path)
	}
	return a.path > b.path
}

// topK keeps the limit best items offered to it, in the order worse defines.
// It is a heap with the worst kept item at the root, ready to be displaced,
// so a pass over n items costs n log limit rather than a sort of all n.
type topK[T any] struct {
	limit int
	worse func(a, b T) bool
	items []T
}

func (h *topK[T]) offer(item T) {
	if h.limit <= 0 {
		return
	}
	if len(h.items) < h.limit {
		h.items = append(h.items, item)
		h.up(len(h.items) - 1)
		return
	}
	if !h.worse(h.items[0], item) {
		return
	}
	h.items[0] = item
	h.down(0)
}

func (h *topK[T]) up(i int) {
	for i > 0 {
		parent := (i - 1) / 2
		if !h.worse(h.items[i], h.items[parent]) {
			return
		}
		h.items[i], h.items[parent] = h.items[parent], h.items[i]
		i = parent
	}
}

func (h *topK[T]) down(i int) {
	n := len(h.items)
	for {
		left, right := 2*i+1, 2*i+2
		worst := i
		if left < n && h.worse(h.items[left], h.items[worst]) {
			worst = left
		}
		if right < n && h.worse(h.items[right], h.items[worst]) {
			worst = right
		}
		if worst == i {
			return
		}
		h.items[i], h.items[worst] = h.items[worst], h.items[i]
		i = worst
	}
}

// sorted returns the kept items best first.
func (h *topK[T]) sorted() []T {
	sort.Slice(h.items, func(i, j int) bool { return h.worse(h.items[j], h.items[i]) })
	return h.items
}

func baseName(path string) string {
	if idx := strings.LastIndexByte(path, '/'); idx != -1 {
		return path[idx+1:]
	}
	return path
}
