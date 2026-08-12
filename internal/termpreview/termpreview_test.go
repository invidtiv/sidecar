package termpreview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
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

func drawBuffer(t *testing.T, in RenderBufferInput, lines []string) string {
	t.Helper()
	buffer := tty.NewOutputBuffer(600)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: strings.Join(lines, "\n")})
	if len(lines) == 0 {
		buffer = nil
	}
	in.Buffer = buffer
	in.Layout = tty.FitViewport(tty.ViewportInput{
		Buffer: buffer, Width: in.Width, Height: in.Height - HeaderRows,
		Offset: in.Layout.Start, Follow: in.Layout.Start == 0,
	})
	return RenderBuffer(in)
}

func TestRenderBufferFillsAnExactBoxWithNoCursor(t *testing.T) {
	view := drawBuffer(t, RenderBufferInput{
		Width: 30, Height: 6, Chips: []string{"shell"}, Hints: "read-only",
	}, []string{"first", "second", strings.Repeat("wide", 40)})

	lines := rows(view)
	if len(lines) != 6 {
		t.Fatalf("rendered %d rows, want exactly 6", len(lines))
	}
	for i, line := range lines {
		if width := ansi.StringWidth(line); width != 30 {
			t.Fatalf("row %d is %d columns, want exactly 30: %q", i, width, line)
		}
	}
	// A captured pane has no live cursor to place, and drawing one would invite
	// typing into a surface that forwards nothing.
	if strings.Contains(view, "\x1b[?25h") || strings.Contains(view, "\x1b[6n") {
		t.Fatal("preview emitted cursor control sequences")
	}
}

// The window is the caller's — the same value it hit-tests against — so the box
// draws exactly the lines it was handed and never re-derives them.
func TestRenderBufferDrawsTheWindowItIsGiven(t *testing.T) {
	content := make([]string, 0, 20)
	for i := range 20 {
		content = append(content, string(rune('a'+i)))
	}
	buffer := tty.NewOutputBuffer(600)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: strings.Join(content, "\n")})

	draw := func(offset int, follow bool) string {
		in := RenderBufferInput{Width: 10, Height: 6, Buffer: buffer}
		in.Layout = tty.FitViewport(tty.ViewportInput{
			Buffer: buffer, Width: in.Width, Height: in.Height - HeaderRows,
			Offset: offset, Follow: follow,
		})
		return RenderBuffer(in)
	}

	if live := draw(0, true); !strings.Contains(live, "t") || strings.Contains(live, "a\n") {
		t.Fatalf("following the buffer did not draw its last rows: %q", live)
	}
	if back := draw(11, false); !strings.Contains(back, "l") || strings.Contains(back, "t") {
		t.Fatalf("a scrolled window drew the wrong rows: %q", back)
	}
}

// A selection is highlighted where the buffer is drawn, in the coordinates the
// selection was recorded in.
func TestRenderBufferHighlightsTheSelection(t *testing.T) {
	selection := &ui.SelectionState{}
	selection.SelectRange(ui.SelectionPoint{Line: 1, Col: 0}, ui.SelectionPoint{Line: 1, Col: 3}, false)

	lines := []string{"alpha", "bravo", "delta"}
	plain := drawBuffer(t, RenderBufferInput{Width: 20, Height: 5}, lines)
	highlighted := drawBuffer(t, RenderBufferInput{Width: 20, Height: 5, Selection: selection}, lines)

	if plain == highlighted {
		t.Fatal("a selection changed nothing about what was drawn")
	}
	if ansi.Strip(plain) != ansi.Strip(highlighted) {
		t.Fatalf("highlighting changed the text:\n%q\n%q", ansi.Strip(plain), ansi.Strip(highlighted))
	}
}

func TestRenderBufferStatesWithNothingToDraw(t *testing.T) {
	// An unavailable preview is a message, not a blank box: the reason and the
	// item's metadata are what the pane is for when there is no capture.
	view := drawBuffer(t, RenderBufferInput{
		Width: 24, Height: 4, Chips: []string{"api"},
		Message: "No live session\nbranch feature/x",
	}, nil)
	if !strings.Contains(view, "No live session") || !strings.Contains(view, "branch feature/x") {
		t.Fatalf("unavailable state lost its reason or metadata: %q", view)
	}
	for _, line := range rows(view) {
		if ansi.StringWidth(line) != 24 {
			t.Fatalf("unavailable row is not padded to width: %q", line)
		}
	}

	// A buffer with nothing in it is not silently drawn as blank output.
	if empty := drawBuffer(t, RenderBufferInput{Width: 20, Height: 3}, nil); !strings.Contains(empty, "No output captured") {
		t.Fatalf("empty state = %q", empty)
	}
	if view := drawBuffer(t, RenderBufferInput{Width: 0, Height: 5}, nil); view != "" {
		t.Fatalf("zero width rendered %q", view)
	}
	// Too short for a body: the header still fits, and nothing wraps.
	short := drawBuffer(t, RenderBufferInput{Width: 10, Height: 1, Chips: []string{"api"}}, []string{"x"})
	if strings.Contains(short, "\n") {
		t.Fatalf("single-row box wrapped: %q", short)
	}
}

type errFake struct{}

func (errFake) Error() string { return "boom" }
