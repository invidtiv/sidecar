package ui

import (
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
// the leading directories are the least informative thing on the line and are
// spent first:
//
//	.claude/skills/create-modal/SKILL.md  ->  .c/s/create-modal/SKILL.md
//
// What survives longest is the filename and the directory immediately above it
// — the pair that actually differs between rows. When even that will not fit
// the path is truncated from the front, which keeps the tail rather than
// eliding the middle: a middle elision keeps the shared prefix and throws away
// the discriminating segment, which is how a narrow list turns into a dozen
// identical-looking rows.
func ElidePath(path string, width int) (string, []Span) {
	if width <= 0 {
		return "", nil
	}
	if runewidth.StringWidth(path) <= width {
		return path, []Span{{SrcStart: 0, SrcEnd: len(path), Dst: 0}}
	}

	if slash := strings.LastIndex(path, "/"); slash >= 0 {
		if out, spans, ok := abbreviateLeadingDirs(path, slash, width); ok {
			return out, spans
		}
	}
	return truncateStartSpans(path, width)
}

// abbreviateLeadingDirs shortens every directory above the file's own parent to
// its first character (two for a dotted name like ".claude", so the dot does not
// become the whole segment), leaving the parent directory and the filename
// intact. It reports false when the result still does not fit.
func abbreviateLeadingDirs(path string, lastSlash, width int) (string, []Span, bool) {
	dir := path[:lastSlash]
	parentStart := strings.LastIndex(dir, "/")
	if parentStart < 0 {
		// Only one directory; there is nothing above it to abbreviate.
		return "", nil, false
	}

	var out strings.Builder
	var spans []Span
	pos := 0
	segStart := 0
	for i := 0; i <= parentStart; i++ {
		if path[i] != '/' {
			continue
		}
		seg := path[segStart:i]
		short := leadingRunes(seg, 1)
		if strings.HasPrefix(seg, ".") {
			short = leadingRunes(seg, 2)
		}
		spans = append(spans, Span{SrcStart: segStart, SrcEnd: segStart + len(short), Dst: pos})
		out.WriteString(short)
		out.WriteString("/")
		pos += len(short) + 1
		segStart = i + 1
	}

	tail := path[segStart:] // parent/file.go
	spans = append(spans, Span{SrcStart: segStart, SrcEnd: len(path), Dst: pos})
	out.WriteString(tail)

	result := out.String()
	if runewidth.StringWidth(result) > width {
		return "", nil, false
	}
	return result, spans, true
}

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

// TruncateAnchored fits s into width cells with the highlighted range anchored
// near the left edge: leading context is given up before trailing context, so
// what follows the match — the part a reader needs to recognise the line — stays
// on screen.
//
// The window it replaces was centred on the match, which is why a narrow pane
// rendered every row as four characters of the query and nothing else.
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

	// How much of the line before the match we can afford: a few cells of
	// context help, but never at the cost of the match itself.
	matchWidth := runewidth.StringWidth(string(runes[hlStart:hlEnd]))
	lead := 0
	if spare := width - matchWidth; spare > 0 {
		lead = minInt(6, spare/4)
	}

	start := hlStart
	used := 0
	for start > 0 {
		w := runewidth.RuneWidth(runes[start-1])
		if used+w > lead {
			break
		}
		used += w
		start--
	}

	budget := width
	var out []rune
	if start > 0 {
		out = append(out, '…')
		budget--
	}

	newStart, newEnd := -1, -1
	i := start
	for ; i < len(runes); i++ {
		w := runewidth.RuneWidth(runes[i])
		if used := runewidth.StringWidth(string(out)); used+w > budget {
			break
		}
		if i == hlStart {
			newStart = len(out)
		}
		out = append(out, runes[i])
		if i == hlEnd-1 {
			newEnd = len(out)
		}
	}
	if i < len(runes) {
		// Trailing ellipsis replaces the last cell rather than overflowing.
		if len(out) > 0 && runewidth.StringWidth(string(out)) >= budget {
			out = out[:len(out)-1]
		}
		out = append(out, '…')
	}

	if newStart < 0 {
		newStart = 0
	}
	if newEnd < 0 {
		// The match itself ran off the end; highlight what of it survived,
		// never the trailing ellipsis.
		newEnd = len(out)
		if i < len(runes) && newEnd > 0 {
			newEnd--
		}
	}
	if newEnd > len(out) {
		newEnd = len(out)
	}
	if newEnd < newStart {
		newEnd = newStart
	}
	return string(out), newStart, newEnd
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
