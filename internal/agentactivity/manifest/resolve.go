package manifest

import (
	"strings"
	"unicode"
)

// Region resolution, ported line by line from Herdr's src/detect/manifest.rs
// (region() at :1286 and the helpers at :1350-1546, commit e2b85c7). Every
// helper below cites the Rust function it reproduces. Where upstream makes a
// choice that reads as arbitrary — which glyph counts as a horizontal rule,
// what a Codex prompt line is, whether a whitespace-only line counts as
// non-empty — the choice is reproduced, not improved. An "improvement" here is
// a silent divergence from the oracle the differential harness checks against.

// resolver carves regions out of one read window and caches what it produces,
// because a manifest's rules overwhelmingly share a handful of regions and the
// workspace evaluates every pane a few times a second.
type resolver struct {
	window   string
	lines    []string
	title    string
	progress string

	cache map[string]regionText
}

// regionText is a resolved region plus its lazily folded lower-case form. The
// fold is the only per-rule allocation Herdr's compiled_rule_matches makes, so
// caching it per region is where the allocation budget is won.
type regionText struct {
	text  string
	lower string
	// folded records that lower has been computed, so an empty region is not
	// folded on every rule that reads it.
	folded bool
}

func newResolver(in Input) *resolver {
	window := ReadWindow(in.Screen, in.rows())
	return &resolver{
		window:   window,
		lines:    splitLines(window),
		title:    in.Title,
		progress: in.Progress,
		cache:    make(map[string]regionText, 8),
	}
}

// region returns the text a rule's region selects. Herdr's region() takes the
// spec string; this takes the parsed form, which Parse and Validate have
// already produced, and falls back to the same "" an unknown spec yields
// upstream.
func (r *resolver) region(spec Region) string {
	if cached, ok := r.cache[spec.Spec]; ok {
		return cached.text
	}
	text := r.resolve(spec)
	r.cache[spec.Spec] = regionText{text: text}
	return text
}

// lowerRegion returns the region text folded to lower case, computing the fold
// at most once per region per observation.
func (r *resolver) lowerRegion(spec Region) string {
	cached, ok := r.cache[spec.Spec]
	if !ok {
		cached = regionText{text: r.resolve(spec)}
	}
	if !cached.folded {
		cached.lower = strings.ToLower(cached.text)
		cached.folded = true
	}
	r.cache[spec.Spec] = cached
	return cached.lower
}

func (r *resolver) resolve(spec Region) string {
	// The OSC regions source from their own captured strings, not the screen
	// (manifest.rs:1288-1293).
	switch spec.Kind {
	case RegionOSCTitle:
		return r.title
	case RegionOSCProgress:
		return r.progress
	}
	content := r.window
	lines := r.lines
	switch spec.Kind {
	case RegionWholeRecent:
		return content
	case RegionAfterLastPromptMarker:
		return afterLastPromptMarker(content, lines)
	case RegionBeforeCurrentPromptMarker:
		return beforeCurrentPromptMarker(content, lines)
	case RegionWholeRecentWithoutCurrentPromptMarker:
		return wholeRecentWithoutCurrentPromptMarker(content, lines)
	case RegionCurrentPromptBlockMarker:
		return currentPromptBlockMarker(lines)
	case RegionAfterCurrentPromptBlockMarker:
		return afterCurrentPromptBlockMarker(content, lines)
	case RegionPromptBoxBody:
		return promptBoxBody(content, lines)
	case RegionAbovePromptBox:
		return abovePromptBox(content, lines)
	case RegionLastNonEmptyAbovePromptBox:
		return lastNonEmptyLine(abovePromptBox(content, lines))
	case RegionAfterLastHorizontalRule:
		return afterLastHorizontalRule(content)
	case RegionBottomLines:
		return bottomLines(content, lines, spec.Count)
	case RegionBottomNonEmptyLines:
		return bottomNonEmptyLines(content, lines, spec.Count)
	case RegionTopNonEmptyLines:
		return topNonEmptyLines(content, lines, spec.Count)
	default:
		return ""
	}
}

// lineStartOffset ports manifest.rs:1537 line_start_offset.
func lineStartOffset(content string, lines []string, index int) int {
	if index > len(lines) {
		index = len(lines)
	}
	offset := 0
	for i := 0; i < index; i++ {
		offset += len(lines[i]) + 1
	}
	return min(offset, len(content))
}

// sliceFromLineIndex ports manifest.rs:1532 slice_from_line_index.
func sliceFromLineIndex(content string, lines []string, index int) string {
	return content[min(lineStartOffset(content, lines, index), len(content)):]
}

// bottomLines ports manifest.rs:1350. The count is a saturating subtraction, so
// bottom_lines(0) is the empty tail and a count above the line count is the
// whole window. Blank lines count.
func bottomLines(content string, lines []string, count int) string {
	start := len(lines) - count
	if start < 0 {
		start = 0
	}
	return sliceFromLineIndex(content, lines, start)
}

// bottomNonEmptyLines ports manifest.rs:1356: walk up from the bottom taking
// the last `count` lines whose trimmed form is non-empty, and slice from the
// highest of those. Interleaved blank lines are inside the region but do not
// count towards N. No non-empty line at all yields "".
func bottomNonEmptyLines(content string, lines []string, count int) string {
	if count == 0 {
		return ""
	}
	taken := 0
	start := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		taken++
		start = i
		if taken == count {
			break
		}
	}
	if start < 0 {
		return ""
	}
	return sliceFromLineIndex(content, lines, start)
}

// topNonEmptyLines ports manifest.rs:1372: read down from the top and return
// everything up to and including the count-th non-empty line, leading and
// interleaved blanks included. Requires engine 3.
func topNonEmptyLines(content string, lines []string, count int) string {
	if count == 0 {
		return ""
	}
	taken := 0
	end := -1
	for i := range lines {
		if strings.TrimSpace(lines[i]) == "" {
			continue
		}
		taken++
		end = i
		if taken == count {
			break
		}
	}
	if end < 0 {
		return ""
	}
	return content[:lineStartOffset(content, lines, end+1)]
}

// afterLastPromptMarker ports manifest.rs:1388. With no prompt line at all the
// region is the whole window, not the empty string.
func afterLastPromptMarker(content string, lines []string) string {
	index := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if codexPromptLine(lines[i]) {
			index = i
			break
		}
	}
	if index < 0 {
		return content
	}
	return sliceFromLineIndex(content, lines, index+1)
}

// beforeCurrentPromptMarker ports manifest.rs:1396.
func beforeCurrentPromptMarker(content string, lines []string) string {
	index, ok := currentCodexPromptIndex(lines)
	if !ok {
		return content
	}
	offset := 0
	for i := 0; i < index; i++ {
		offset += len(lines[i]) + 1
	}
	return content[:min(offset, len(content))]
}

// wholeRecentWithoutCurrentPromptMarker ports manifest.rs:1408: the whole
// window, or nothing at all when a live Codex composer is on screen. This is
// what stops a resolved historical prompt in the scrollback from producing a
// blocker.
func wholeRecentWithoutCurrentPromptMarker(content string, lines []string) string {
	if _, ok := currentCodexPromptIndex(lines); ok {
		return ""
	}
	return content
}

// currentPromptBlockMarker ports manifest.rs:1417: the nearest block-marker
// line above the current composer, as a single line with no trailing newline.
func currentPromptBlockMarker(lines []string) string {
	promptIndex, ok := currentCodexPromptIndex(lines)
	if !ok {
		return ""
	}
	for i := promptIndex - 1; i >= 0; i-- {
		if codexBlockMarkerLine(lines[i]) {
			return lines[i]
		}
	}
	return ""
}

// afterCurrentPromptBlockMarker ports manifest.rs:1427: everything from that
// block-marker line onwards, the marker line included.
func afterCurrentPromptBlockMarker(content string, lines []string) string {
	promptIndex, ok := currentCodexPromptIndex(lines)
	if !ok {
		return ""
	}
	for i := promptIndex - 1; i >= 0; i-- {
		if codexBlockMarkerLine(lines[i]) {
			return sliceFromLineIndex(content, lines, i)
		}
	}
	return ""
}

// currentCodexPromptIndex ports manifest.rs:1436. The last composer line counts
// as "current" only when no block marker appears below it: a marker below means
// the agent has written output since, so the composer is scrollback.
func currentCodexPromptIndex(lines []string) (int, bool) {
	promptIndex := -1
	for i := len(lines) - 1; i >= 0; i-- {
		if codexPromptLine(lines[i]) {
			promptIndex = i
			break
		}
	}
	if promptIndex < 0 {
		return 0, false
	}
	for _, line := range lines[promptIndex+1:] {
		if codexBlockMarkerLine(line) {
			return 0, false
		}
	}
	return promptIndex, true
}

// codexPromptLine ports manifest.rs:1447. Note that it is column-anchored and
// takes no leading whitespace: "  › foo" is not a prompt line.
func codexPromptLine(line string) bool {
	return line == "›" || strings.HasPrefix(line, "› ")
}

// codexBlockMarkerLine ports manifest.rs:1451.
func codexBlockMarkerLine(line string) bool {
	return strings.HasPrefix(line, "•") || strings.HasPrefix(line, "■") ||
		strings.HasPrefix(line, "✗") || strings.HasPrefix(line, "✓")
}

// promptBoxBody ports manifest.rs:1455: the lines between the box's top border
// and the next horizontal rule below it (or the end of the window).
func promptBoxBody(content string, lines []string) string {
	top, ok := promptBoxTopBorderIndex(lines)
	if !ok {
		return ""
	}
	start := lineStartOffset(content, lines, top+1)
	endIndex := len(lines)
	for i := top + 1; i < len(lines); i++ {
		if isHorizontalRule(lines[i]) {
			endIndex = i
			break
		}
	}
	end := lineStartOffset(content, lines, endIndex)
	start = min(start, len(content))
	end = min(end, len(content))
	if end < start {
		return ""
	}
	return content[start:end]
}

// abovePromptBox ports manifest.rs:1468. With no box on screen the region is
// the whole window.
func abovePromptBox(content string, lines []string) string {
	top, ok := promptBoxTopBorderIndex(lines)
	if !ok {
		return content
	}
	return content[:min(lineStartOffset(content, lines, top), len(content))]
}

// afterLastHorizontalRule ports manifest.rs:1477. It walks the whole window
// forward rather than reusing the split lines, exactly as upstream does, so a
// window with no rule yields the whole window.
func afterLastHorizontalRule(content string) string {
	lastRuleEnd := 0
	offset := 0
	for _, line := range splitLines(content) {
		next := offset + len(line) + 1
		if isHorizontalRule(line) {
			lastRuleEnd = min(next, len(content))
		}
		offset = next
	}
	return content[lastRuleEnd:]
}

// lastNonEmptyLine ports manifest.rs:1490. The result carries no newline.
func lastNonEmptyLine(content string) string {
	lines := splitLines(content)
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) != "" {
			return lines[i]
		}
	}
	return ""
}

// promptBoxTopBorderIndex ports manifest.rs:1498: the *second* horizontal rule
// counting up from the bottom. The bottom rule closes the box; the one above it
// opens it.
func promptBoxTopBorderIndex(lines []string) (int, bool) {
	borders := 0
	for i := len(lines) - 1; i >= 0; i-- {
		if isHorizontalRule(lines[i]) {
			borders++
			if borders == 2 {
				return i, true
			}
		}
	}
	return 0, false
}

// isHorizontalRule ports manifest.rs:1511 and is the fussiest helper in the
// file, so it is spelled out:
//
//   - the line is trimmed on both sides; an empty line is never a rule;
//   - only U+2500 BOX DRAWINGS LIGHT HORIZONTAL counts as a rule glyph, and
//     only in an unbroken run at the start of the trimmed line;
//   - a line is a rule if that run is the whole trimmed line, or if the run is
//     at least three glyphs long and anything at all follows it.
//
// The last clause is what lets `───────── (bypass permissions on) ─` count as a
// box border while a single `─` used as a bullet does not.
func isHorizontalRule(line string) bool {
	trimmed := strings.TrimSpace(line)
	if trimmed == "" {
		return false
	}
	ruleChars := 0
	ruleBytes := 0
	for _, ch := range trimmed {
		if ch != '─' {
			break
		}
		ruleChars++
		ruleBytes += len(string(ch))
	}
	if ruleChars == 0 {
		return false
	}
	suffix := strings.TrimLeftFunc(trimmed[ruleBytes:], unicode.IsSpace)
	return suffix == "" || ruleChars >= 3
}
