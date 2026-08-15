package ui

import (
	"strings"
	"testing"

	"github.com/mattn/go-runewidth"
)

func TestTruncateStartKeepsFilenameEnd(t *testing.T) {
	const path = "internal/plugins/workspace/plugin.go"
	if got := TruncateStart(path, 100); got != path {
		t.Fatalf("wide = %q", got)
	}
	if got := TruncateStart(path, 11); got != "…/plugin.go" {
		t.Fatalf("filename budget = %q, want …/plugin.go", got)
	}
	if got := TruncateStart("ab", 1); got != "…" {
		t.Fatalf("width 1 = %q", got)
	}
	if got := TruncateStart("ab", 0); got != "" {
		t.Fatalf("width 0 = %q", got)
	}
}

func TestElidePathSpansMapRangesOntoTheElidedText(t *testing.T) {
	path := ".claude/skills/create-modal/SKILL.md"
	out, spans := ElidePath(path, 30)
	if out != ".c/s/create-modal/SKILL.md" {
		t.Fatalf("ElidePath = %q", out)
	}
	// "modal" in the original must land on "modal" in the elided text.
	src := strings.Index(path, "modal")
	start, end, ok := MapSpans(spans, src, src+len("modal"))
	if !ok {
		t.Fatal("the range did not survive an elision that kept the text it names")
	}
	if got := out[start:end]; got != "modal" {
		t.Errorf("mapped range spells %q, want %q", got, "modal")
	}

	// A range in a segment that was abbreviated away is reported as gone rather
	// than mapped onto the wrong characters.
	src = strings.Index(path, "skills")
	if _, _, ok := MapSpans(spans, src+2, src+5); ok {
		t.Error("a range whose characters were dropped was mapped anyway")
	}
}

func TestTruncateAnchoredKeepsTheMatchAndTrailingContext(t *testing.T) {
	line := "if p.displayMode == modeSplit && other != nil {"
	start := strings.Index(line, "displayMode")
	runeStart := len([]rune(line[:start]))
	runeEnd := runeStart + len([]rune("displayMode"))

	for _, width := range []int{20, 30, 45} {
		got, hlStart, hlEnd := TruncateAnchored(line, width, runeStart, runeEnd)
		if w := runewidth.StringWidth(got); w > width {
			t.Errorf("width %d: got %d cells: %q", width, w, got)
		}
		if !strings.Contains(got, "display") {
			t.Errorf("width %d: the match was clipped away: %q", width, got)
		}
		runes := []rune(got)
		if hlStart < 0 || hlEnd > len(runes) || hlStart > hlEnd {
			t.Errorf("width %d: highlight %d..%d is outside %q", width, hlStart, hlEnd, got)
		}
	}

	// A line that fits is returned untouched.
	if got, s, e := TruncateAnchored("short", 40, 0, 5); got != "short" || s != 0 || e != 5 {
		t.Errorf("a fitting line was rewritten: %q %d %d", got, s, e)
	}
}
