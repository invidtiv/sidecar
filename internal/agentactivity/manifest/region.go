package manifest

import (
	"fmt"
	"strconv"
	"strings"
)

// RegionKind names one of Herdr's fifteen detection regions. The evaluator
// switches on this rather than re-parsing the spec string.
type RegionKind int

// The fifteen regions Herdr's region() and validate_region_name() accept.
const (
	// RegionWholeRecent is the whole captured screen.
	RegionWholeRecent RegionKind = iota
	// RegionWholeRecentWithoutCurrentPromptMarker is the whole screen, but
	// empty when a current Codex prompt marker is on screen.
	RegionWholeRecentWithoutCurrentPromptMarker
	// RegionAfterLastPromptMarker is everything below the last Codex prompt line.
	RegionAfterLastPromptMarker
	// RegionBeforeCurrentPromptMarker is everything above the current Codex prompt line.
	RegionBeforeCurrentPromptMarker
	// RegionCurrentPromptBlockMarker is the block immediately above the current Codex prompt.
	RegionCurrentPromptBlockMarker
	// RegionAfterCurrentPromptBlockMarker is everything after that block.
	RegionAfterCurrentPromptBlockMarker
	// RegionPromptBoxBody is the body of the Claude-shaped prompt box.
	RegionPromptBoxBody
	// RegionAbovePromptBox is everything above that box.
	RegionAbovePromptBox
	// RegionLastNonEmptyAbovePromptBox is the last non-empty line above that box.
	RegionLastNonEmptyAbovePromptBox
	// RegionAfterLastHorizontalRule is everything below the last horizontal rule.
	RegionAfterLastHorizontalRule
	// RegionOSCTitle is the terminal title (tmux #{pane_title}), not the screen.
	RegionOSCTitle
	// RegionOSCProgress is the OSC progress string, not the screen.
	RegionOSCProgress
	// RegionBottomLines is the last Count lines, empty ones included.
	RegionBottomLines
	// RegionBottomNonEmptyLines spans from the Count-th non-empty line from the
	// bottom to the end of the screen.
	RegionBottomNonEmptyLines
	// RegionTopNonEmptyLines spans from the start of the screen through the
	// Count-th non-empty line. Requires engine version 3.
	RegionTopNonEmptyLines
)

// TopNonEmptyLinesEngineVersion is the engine version top_non_empty_lines(N)
// requires (Herdr's TOP_NON_EMPTY_LINES_ENGINE_VERSION).
const TopNonEmptyLinesEngineVersion = 3

// MaxTopRegionLineCount caps top_non_empty_lines(N) at Herdr's
// MAX_TOP_REGION_LINE_COUNT, which is u16::MAX.
const MaxTopRegionLineCount = 65535

var regionNames = map[string]RegionKind{
	"whole_recent": RegionWholeRecent,
	"whole_recent_without_current_prompt_marker": RegionWholeRecentWithoutCurrentPromptMarker,
	"after_last_prompt_marker":                   RegionAfterLastPromptMarker,
	"before_current_prompt_marker":               RegionBeforeCurrentPromptMarker,
	"current_prompt_block_marker":                RegionCurrentPromptBlockMarker,
	"after_current_prompt_block_marker":          RegionAfterCurrentPromptBlockMarker,
	"prompt_box_body":                            RegionPromptBoxBody,
	"above_prompt_box":                           RegionAbovePromptBox,
	"last_non_empty_above_prompt_box":            RegionLastNonEmptyAbovePromptBox,
	"after_last_horizontal_rule":                 RegionAfterLastHorizontalRule,
	"osc_title":                                  RegionOSCTitle,
	"osc_progress":                               RegionOSCProgress,
}

var regionKindSpecs = map[RegionKind]string{
	RegionWholeRecent: "whole_recent",
	RegionWholeRecentWithoutCurrentPromptMarker: "whole_recent_without_current_prompt_marker",
	RegionAfterLastPromptMarker:                 "after_last_prompt_marker",
	RegionBeforeCurrentPromptMarker:             "before_current_prompt_marker",
	RegionCurrentPromptBlockMarker:              "current_prompt_block_marker",
	RegionAfterCurrentPromptBlockMarker:         "after_current_prompt_block_marker",
	RegionPromptBoxBody:                         "prompt_box_body",
	RegionAbovePromptBox:                        "above_prompt_box",
	RegionLastNonEmptyAbovePromptBox:            "last_non_empty_above_prompt_box",
	RegionAfterLastHorizontalRule:               "after_last_horizontal_rule",
	RegionOSCTitle:                              "osc_title",
	RegionOSCProgress:                           "osc_progress",
	RegionBottomLines:                           "bottom_lines",
	RegionBottomNonEmptyLines:                   "bottom_non_empty_lines",
	RegionTopNonEmptyLines:                      "top_non_empty_lines",
}

// String renders the region kind's base name, without any line count.
func (k RegionKind) String() string {
	if name, ok := regionKindSpecs[k]; ok {
		return name
	}
	return fmt.Sprintf("RegionKind(%d)", int(k))
}

// Parameterised reports whether the kind takes a line count.
func (k RegionKind) Parameterised() bool {
	switch k {
	case RegionBottomLines, RegionBottomNonEmptyLines, RegionTopNonEmptyLines:
		return true
	default:
		return false
	}
}

// Region is a parsed region specification.
type Region struct {
	Kind RegionKind
	// Count is the N of a parameterised region and zero otherwise.
	Count int
	// Spec is the trimmed spec string as it appeared in the manifest.
	Spec string
}

// String renders the region back to its spec form.
func (r Region) String() string {
	if r.Kind.Parameterised() {
		return fmt.Sprintf("%s(%d)", r.Kind, r.Count)
	}
	return r.Kind.String()
}

// UsesScreen reports whether the region reads the screen snapshot. The two OSC
// regions read their own captured strings instead.
func (r Region) UsesScreen() bool {
	return r.Kind != RegionOSCTitle && r.Kind != RegionOSCProgress
}

// ParseRegion parses a region specification exactly as Herdr's engine does
// (validate_region_name, region_count and top_region_count in manifest.rs).
//
// Two asymmetries are Herdr's, and are reproduced rather than tidied:
// bottom_lines(N) and bottom_non_empty_lines(N) accept any usize, including 0
// and forms with leading zeros, while top_non_empty_lines(N) rejects a leading
// zero, rejects any non-ASCII-digit, and caps N at 65535. The distribution
// checker is stricter still and requires [1-9][0-9]* for all three; that
// stricter form is enforced by ValidateDistribution, not here, so the engine
// accepts every file the engine upstream accepts.
func ParseRegion(spec string) (Region, error) {
	trimmed := strings.TrimSpace(spec)
	if kind, ok := regionNames[trimmed]; ok {
		return Region{Kind: kind, Spec: trimmed}, nil
	}
	if count, ok := regionCount(trimmed, "bottom_lines"); ok {
		return Region{Kind: RegionBottomLines, Count: count, Spec: trimmed}, nil
	}
	if count, ok := regionCount(trimmed, "bottom_non_empty_lines"); ok {
		return Region{Kind: RegionBottomNonEmptyLines, Count: count, Spec: trimmed}, nil
	}
	if count, ok := topRegionCount(trimmed); ok {
		return Region{Kind: RegionTopNonEmptyLines, Count: count, Spec: trimmed}, nil
	}
	return Region{}, fmt.Errorf("%s", trimmed)
}

// regionCount ports Herdr's region_count: strip the name, strip "(", strip
// ")", parse the remainder as an unsigned integer.
func regionCount(spec, name string) (int, bool) {
	rest, ok := strings.CutPrefix(spec, name)
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutPrefix(rest, "(")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ")")
	if !ok {
		return 0, false
	}
	// Rust parses the count with usize::from_str, which accepts one optional
	// leading '+'. ParseUint does not, so bottom_lines(+5) would be rejected
	// here and accepted upstream — a file Herdr loads and Sidecar refuses,
	// which is the one direction a port must never diverge in.
	// top_non_empty_lines is deliberately not given the same latitude: its own
	// parser (topRegionCount) requires every byte to be an ASCII digit, and
	// Herdr's own test asserts that "+1" is rejected there.
	rest = strings.TrimPrefix(rest, "+")
	count, err := strconv.ParseUint(rest, 10, 64)
	if err != nil || count > uint64(maxInt) {
		return 0, false
	}
	return int(count), true
}

const maxInt = int(^uint(0) >> 1)

// topRegionCount ports Herdr's top_region_count, which is deliberately
// stricter than region_count.
func topRegionCount(spec string) (int, bool) {
	rest, ok := strings.CutPrefix(spec, "top_non_empty_lines")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutPrefix(rest, "(")
	if !ok {
		return 0, false
	}
	rest, ok = strings.CutSuffix(rest, ")")
	if !ok {
		return 0, false
	}
	if rest == "" || strings.HasPrefix(rest, "0") {
		return 0, false
	}
	for i := 0; i < len(rest); i++ {
		if rest[i] < '0' || rest[i] > '9' {
			return 0, false
		}
	}
	count, err := strconv.Atoi(rest)
	if err != nil || count > MaxTopRegionLineCount {
		return 0, false
	}
	return count, true
}
