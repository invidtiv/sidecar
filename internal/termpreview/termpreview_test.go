package termpreview

import (
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/x/ansi"
)

func rows(view string) []string { return strings.Split(view, "\n") }

func TestSplitFloorsAndHiddenSidebar(t *testing.T) {
	cfg := SplitConfig{SidebarVisible: true, SidebarPercent: 40, DividerWidth: 1, PanelOverhead: 4, ContentInset: 2, SidebarMin: 15, PreviewMin: 40}

	wide := SplitFor(200, cfg)
	if wide.SidebarWidth != (199*40)/100 {
		t.Fatalf("wide sidebar = %d, want the requested share", wide.SidebarWidth)
	}
	if wide.PreviewX != wide.SidebarWidth+1 || wide.SidebarWidth+1+wide.PreviewWidth != 200 {
		t.Fatalf("split does not tile the width: %+v", wide)
	}
	if wide.ContentX != wide.PreviewX+2 || wide.ContentWidth != wide.PreviewWidth-4 {
		t.Fatalf("panel chrome not accounted for: %+v", wide)
	}

	// A tiny requested share still leaves a usable list…
	if got := SplitFor(200, withPercent(cfg, 1)).SidebarWidth; got != 15 {
		t.Fatalf("sidebar floor = %d, want 15", got)
	}
	// …and a greedy one still leaves a usable preview.
	greedy := SplitFor(120, withPercent(cfg, 95))
	if greedy.PreviewWidth != 40 {
		t.Fatalf("preview floor = %d, want 40", greedy.PreviewWidth)
	}

	hidden := SplitFor(200, SplitConfig{SidebarPercent: 40, DividerWidth: 1, PanelOverhead: 4, ContentInset: 2, SidebarMin: 15, PreviewMin: 40})
	if hidden.SidebarWidth != 0 || hidden.PreviewX != 0 || hidden.PreviewWidth != 200 {
		t.Fatalf("hidden sidebar split = %+v, want the preview to own the width", hidden)
	}
	if hidden.ContentX != 2 || hidden.ContentWidth != 196 {
		t.Fatalf("hidden sidebar content = %+v", hidden)
	}
}

func withPercent(cfg SplitConfig, percent int) SplitConfig {
	cfg.SidebarPercent = percent
	return cfg
}

func TestSurfaceInIsTheBoxMinusItsHeaderRow(t *testing.T) {
	surface := SurfaceIn(Box{X: 10, Y: 3, W: 60, H: 20})
	if !surface.OK || surface.X != 10 || surface.HeaderY != 3 || surface.Y != 4 {
		t.Fatalf("surface = %+v, want the header on the box's first row", surface)
	}
	if surface.Width != 60 || surface.Height != 19 {
		t.Fatalf("surface size = %dx%d, want 60x19", surface.Width, surface.Height)
	}
	if placed := SurfaceIn(Box{}); placed.OK {
		t.Fatalf("empty box placed a surface: %+v", placed)
	}
}

func TestHeaderRowDropsWholeChipsAndClipsHints(t *testing.T) {
	chips := []string{"[one]", "[two]", "[three]"}

	full := HeaderRow(chips, "hint", 40, 0, nil)
	if ansi.StringWidth(full) > 40 || strings.Contains(full, "\n") {
		t.Fatalf("header row = %q, want exactly one row inside 40 columns", full)
	}
	if !strings.HasSuffix(full, "hint") {
		t.Fatalf("hints are not right-aligned: %q", full)
	}

	// Narrow: chips are kept whole or dropped, never clipped in half.
	narrow := HeaderRow(chips, "", 12, 0, nil)
	if strings.Contains(narrow, "[thr") && !strings.Contains(narrow, "[three]") {
		t.Fatalf("chip was clipped mid-chip: %q", narrow)
	}
	if ansi.StringWidth(narrow) > 12 {
		t.Fatalf("narrow header overflowed: %q", narrow)
	}

	// A hint floor inverts the priority: the right region keeps its columns and
	// the chips give way.
	floored := HeaderRow(chips, "EXIT", 14, 4, nil)
	if !strings.HasSuffix(floored, "EXIT") {
		t.Fatalf("hint floor did not protect the hints: %q", floored)
	}

	if HeaderRow(chips, "hint", 0, 0, nil) != "" {
		t.Fatal("zero width drew a header row")
	}
}

func TestRenderReadOnlyFillsAnExactBoxWithNoCursor(t *testing.T) {
	snap := Snapshot{PaneID: "%1", Lines: []string{"first", "second", strings.Repeat("wide", 40)}, CapturedAt: time.Now()}
	result := RenderReadOnly(snap, ReadOnlyOptions{Width: 30, Height: 6, Chips: []string{"shell"}, Hints: "read-only"})

	lines := rows(result.View)
	if len(lines) != 6 {
		t.Fatalf("rendered %d rows, want exactly 6", len(lines))
	}
	for i, line := range lines {
		if width := ansi.StringWidth(line); width != 30 {
			t.Fatalf("row %d is %d columns, want exactly 30: %q", i, width, line)
		}
	}
	if result.Rows != 5 {
		t.Fatalf("body rows = %d, want height minus the header row", result.Rows)
	}
	// A read-only capture has no live cursor to place, and drawing one would
	// invite typing into a surface that forwards nothing.
	if strings.Contains(result.View, "\x1b[?25h") || strings.Contains(result.View, "\x1b[6n") {
		t.Fatal("read-only preview emitted cursor control sequences")
	}
}

func TestRenderReadOnlyScrollsBackFromTheLiveBottom(t *testing.T) {
	lines := make([]string, 0, 20)
	for i := range 20 {
		lines = append(lines, string(rune('a'+i)))
	}
	snap := Snapshot{PaneID: "%1", Lines: lines}
	opts := ReadOnlyOptions{Width: 10, Height: 6}

	live := RenderReadOnly(snap, opts)
	if live.MaxOffset != 15 || live.Start != 15 {
		t.Fatalf("live view start=%d max=%d, want the last 5 lines", live.Start, live.MaxOffset)
	}
	if !strings.Contains(live.View, "t") {
		t.Fatalf("live view is not following output: %q", live.View)
	}

	opts.Offset = 4
	back := RenderReadOnly(snap, opts)
	if back.Start != 11 {
		t.Fatalf("scrolled start = %d, want 11", back.Start)
	}

	opts.Offset = 999
	clamped := RenderReadOnly(snap, opts)
	if clamped.Start != 0 {
		t.Fatalf("over-scroll start = %d, want clamped to the top", clamped.Start)
	}
}

func TestRenderReadOnlyStatesWithNothingToDraw(t *testing.T) {
	// An unavailable preview is a message, not a blank box: the reason and the
	// item's metadata are what the pane is for when there is no capture.
	result := RenderReadOnly(Snapshot{}, ReadOnlyOptions{
		Width: 24, Height: 4, Chips: []string{"api"},
		Message: "No live session\nbranch feature/x",
	})
	if !strings.Contains(result.View, "No live session") || !strings.Contains(result.View, "branch feature/x") {
		t.Fatalf("unavailable state lost its reason or metadata: %q", result.View)
	}
	for _, line := range rows(result.View) {
		if ansi.StringWidth(line) != 24 {
			t.Fatalf("unavailable row is not padded to width: %q", line)
		}
	}

	// A failed capture is not silently drawn as empty output.
	failed := RenderReadOnly(Snapshot{PaneID: "%1", Err: errFake{}}, ReadOnlyOptions{Width: 20, Height: 3, Message: "capture failed"})
	if !strings.Contains(failed.View, "capture failed") {
		t.Fatalf("capture error state = %q", failed.View)
	}

	if view := RenderReadOnly(Snapshot{}, ReadOnlyOptions{Width: 0, Height: 5}).View; view != "" {
		t.Fatalf("zero width rendered %q", view)
	}
	// Too short for a body: the header still fits, and nothing wraps.
	short := RenderReadOnly(Snapshot{Lines: []string{"x"}}, ReadOnlyOptions{Width: 10, Height: 1, Chips: []string{"api"}})
	if strings.Contains(short.View, "\n") {
		t.Fatalf("single-row box wrapped: %q", short.View)
	}
}

type errFake struct{}

func (errFake) Error() string { return "boom" }

func TestSnapshotLinesDropsTrailingBlankRows(t *testing.T) {
	lines := SnapshotLines("one\r\ntwo\n\n\n")
	if len(lines) != 2 || lines[0] != "one" || lines[1] != "two" {
		t.Fatalf("SnapshotLines = %q", lines)
	}
	if SnapshotLines("") != nil || SnapshotLines("\n\n") != nil {
		t.Fatal("blank capture produced lines")
	}
	// A line that is only ANSI styling is still blank.
	if got := SnapshotLines("real\n\x1b[0m   \x1b[0m\n"); len(got) != 1 {
		t.Fatalf("styled blank row was kept: %q", got)
	}
}

func TestSnapshotIsAValueWithNoWriteBack(t *testing.T) {
	source := []string{"one", "two"}
	snap := Snapshot{PaneID: "%1", Lines: source}
	RenderReadOnly(snap, ReadOnlyOptions{Width: 8, Height: 4})
	if source[0] != "one" || source[1] != "two" {
		t.Fatalf("rendering mutated the capture: %q", source)
	}
	if snap.Empty() {
		t.Fatal("snapshot with lines reported empty")
	}
	if !(Snapshot{}).Empty() {
		t.Fatal("zero snapshot reported non-empty")
	}
}
