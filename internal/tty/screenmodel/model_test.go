package screenmodel

import (
	"errors"
	"strings"
	"sync"
	"testing"
)

func TestWriteRendersFrame(t *testing.T) {
	m := New(10, 3)
	defer m.Close()
	if err := m.Write([]byte("hi\r\nthere")); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := m.Frame()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	if f.Width != 10 || f.Height != 3 {
		t.Fatalf("geometry = %dx%d, want 10x3", f.Width, f.Height)
	}
	if f.Cells[0][0].Grapheme != "h" || f.Cells[1][0].Grapheme != "t" {
		t.Fatalf("unexpected grid: %q %q", f.Cells[0][0].Grapheme, f.Cells[1][0].Grapheme)
	}
	if f.CursorRow != 1 || f.CursorCol != 5 {
		t.Fatalf("cursor = (%d,%d), want (1,5)", f.CursorRow, f.CursorCol)
	}
	if !f.CursorVisible || f.AltScreen || f.Mouse.Any() || f.BracketedPaste {
		t.Fatalf("unexpected initial state: %+v", f)
	}
	if !strings.Contains(f.Output, "there") {
		t.Fatalf("output missing content: %q", f.Output)
	}
}

func TestModeAndCursorStateTracked(t *testing.T) {
	m := New(10, 3)
	defer m.Close()
	if err := m.Write([]byte("\x1b[?25l\x1b[?1002h\x1b[?1006h\x1b[?2004h\x1b[?1049h")); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, _ := m.Frame()
	if f.CursorVisible {
		t.Error("cursor should be hidden")
	}
	if !f.AltScreen {
		t.Error("alt screen should be on")
	}
	if !f.Mouse.ButtonEvent || !f.Mouse.SGR || !f.Mouse.Any() {
		t.Errorf("mouse state = %+v", f.Mouse)
	}
	if !f.BracketedPaste {
		t.Error("bracketed paste should be on")
	}

	// The SGR encoding mode alone must not read as "the app is mouse aware".
	m2 := New(10, 3)
	defer m2.Close()
	_ = m2.Write([]byte("\x1b[?1006h"))
	f2, _ := m2.Frame()
	if f2.Mouse.Any() {
		t.Error("SGR encoding alone must not set Any()")
	}
}

func TestSeedRestoresScreenCursorAndModes(t *testing.T) {
	m := New(8, 4)
	defer m.Close()
	err := m.Seed(Seed{
		Output:        "hello\nworld",
		Width:         8,
		Height:        4,
		CaptureBase:   7,
		HistorySize:   12,
		CursorRow:     2,
		CursorCol:     3,
		CursorVisible: false,
		Mouse:         MouseState{Normal: true},
	})
	if err != nil {
		t.Fatalf("seed: %v", err)
	}
	f, _ := m.Frame()
	if f.Cells[0][0].Grapheme != "h" || f.Cells[1][0].Grapheme != "w" {
		t.Fatalf("seeded grid wrong: %q %q", f.Cells[0][0].Grapheme, f.Cells[1][0].Grapheme)
	}
	if f.CursorRow != 2 || f.CursorCol != 3 {
		t.Fatalf("cursor = (%d,%d), want (2,3)", f.CursorRow, f.CursorCol)
	}
	if f.CursorVisible {
		t.Error("cursor should be hidden after seeding")
	}
	if !f.Mouse.Normal || !f.Mouse.Any() {
		t.Errorf("mouse = %+v", f.Mouse)
	}
	if f.CaptureBase != 7 || f.HistorySize != 12 {
		t.Fatalf("history coords = base %d size %d, want 7/12", f.CaptureBase, f.HistorySize)
	}
}

func TestSeedDiscardsPreviousState(t *testing.T) {
	m := New(8, 4)
	defer m.Close()
	// Leave hidden VT state behind: margins, saved cursor, hidden cursor, alt.
	_ = m.Write([]byte("\x1b[2;3r\x1b7\x1b[?25l\x1b[?1049hstale"))
	if err := m.Seed(Seed{Output: "fresh", Width: 8, Height: 4, CursorVisible: true}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	f, _ := m.Frame()
	if f.AltScreen {
		t.Error("alt screen survived reseeding")
	}
	if !f.CursorVisible {
		t.Error("hidden cursor survived reseeding")
	}
	if strings.Contains(f.Output, "stale") {
		t.Errorf("previous content survived reseeding: %q", f.Output)
	}
	// A scroll past the old bottom margin proves the margins are gone too.
	_ = m.Write([]byte("\x1b[4;1H\n\n"))
	f2, _ := m.Frame()
	if f2.HistorySize == 0 {
		t.Error("scrolling did not push a line off the screen; old margins survived")
	}
}

func TestSeedRejectsImpossibleGeometry(t *testing.T) {
	m := New(4, 4)
	defer m.Close()
	if err := m.Seed(Seed{Width: 0, Height: 4}); !errors.Is(err, ErrInvalidGeometry) {
		t.Fatalf("err = %v, want ErrInvalidGeometry", err)
	}
	// A rejected input is not a fault: the caller can retry the seed with real
	// geometry rather than being forced back to polling.
	if err := m.Seed(Seed{Output: "ok", Width: 4, Height: 4}); err != nil {
		t.Fatalf("retry after rejection: %v", err)
	}
}

func TestResizeChangesReportedGeometry(t *testing.T) {
	m := New(10, 4)
	defer m.Close()
	if err := m.Resize(20, 6); err != nil {
		t.Fatalf("resize: %v", err)
	}
	f, _ := m.Frame()
	if f.Width != 20 || f.Height != 6 {
		t.Fatalf("geometry = %dx%d, want 20x6", f.Width, f.Height)
	}
	if len(f.Cells) != 6 || len(f.Cells[0]) != 20 {
		t.Fatalf("grid = %dx%d", len(f.Cells[0]), len(f.Cells))
	}
	if err := m.Resize(0, 6); !errors.Is(err, ErrInvalidGeometry) {
		t.Fatalf("err = %v, want ErrInvalidGeometry", err)
	}
}

func TestHistoryGrowsAsLinesScrollOff(t *testing.T) {
	m := New(10, 3)
	defer m.Close()
	if err := m.Seed(Seed{Output: "a\nb\nc", Width: 10, Height: 3, HistorySize: 5, CaptureBase: 2, CursorRow: 2, CursorCol: 1}); err != nil {
		t.Fatalf("seed: %v", err)
	}
	if err := m.Write([]byte("\r\nd\r\ne")); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, _ := m.Frame()
	if f.HistorySize != 7 {
		t.Fatalf("history size = %d, want 7 (5 seeded + 2 scrolled off)", f.HistorySize)
	}
	if !f.HasHistory {
		t.Error("HasHistory should be true")
	}
	if f.CaptureBase != 2 {
		t.Fatalf("capture base = %d, want the seeded 2", f.CaptureBase)
	}
	if !strings.Contains(f.Output, "a") || !strings.Contains(f.Output, "e") {
		t.Errorf("output should carry loaded history and live rows: %q", f.Output)
	}
}

func TestAltScreenFreezesHistory(t *testing.T) {
	m := New(10, 3)
	defer m.Close()
	_ = m.Seed(Seed{Output: "a\nb\nc", Width: 10, Height: 3, HistorySize: 4})
	_ = m.Write([]byte("\x1b[?1049h\r\nx\r\ny\r\nz"))
	f, _ := m.Frame()
	if !f.AltScreen {
		t.Fatal("expected alt screen")
	}
	if f.HistorySize != 4 {
		t.Fatalf("history size = %d, want the frozen 4", f.HistorySize)
	}
	if strings.Contains(f.Output, "a") {
		t.Errorf("alt-screen output must not include main-screen history: %q", f.Output)
	}
}

func TestSingleActorGuardRefusesConcurrentUse(t *testing.T) {
	m := New(10, 3)
	defer m.Close()

	entered := make(chan struct{})
	release := make(chan struct{})
	var inner error
	var wg sync.WaitGroup
	wg.Add(1)
	// Frame() runs a callback under the guard; block inside it by writing from
	// another goroutine while the first is still in flight.
	go func() {
		defer wg.Done()
		_ = m.do(func() error {
			close(entered)
			<-release
			return nil
		})
	}()
	<-entered
	inner = m.Write([]byte("x"))
	close(release)
	wg.Wait()

	if !errors.Is(inner, ErrConcurrentUse) {
		t.Fatalf("err = %v, want ErrConcurrentUse", inner)
	}
	// The guard must release cleanly afterwards.
	if err := m.Write([]byte("y")); err != nil {
		t.Fatalf("write after contention: %v", err)
	}
}

func TestModelFaultIsContainedNotPanicked(t *testing.T) {
	m := New(10, 3)
	err := m.do(func() error { panic("boom") })
	if !errors.Is(err, ErrModelFault) {
		t.Fatalf("err = %v, want ErrModelFault", err)
	}
	if _, err := m.Frame(); !errors.Is(err, ErrModelFault) {
		t.Fatalf("frame err = %v, want a sticky fault", err)
	}
	m.Close()
}

func TestCloseIsIdempotentAndRefusesFurtherUse(t *testing.T) {
	m := New(10, 3)
	m.Close()
	m.Close()
	if err := m.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed", err)
	}
}

func TestCanonicalColorNormalizesSpelling(t *testing.T) {
	// SGR 33, SGR 38;5;3, and the bright form must canonicalize consistently.
	m := New(20, 1)
	defer m.Close()
	_ = m.Write([]byte("\x1b[33ma\x1b[38;5;3mb\x1b[93mc\x1b[38;2;1;2;3md"))
	f, _ := m.Frame()
	if got := f.Cells[0][0].Fg; got != IndexedColor(3) {
		t.Errorf("SGR 33 fg = %q, want i3", got)
	}
	if got := f.Cells[0][1].Fg; got != IndexedColor(3) {
		t.Errorf("SGR 38;5;3 fg = %q, want i3", got)
	}
	if got := f.Cells[0][2].Fg; got != IndexedColor(11) {
		t.Errorf("SGR 93 fg = %q, want i11", got)
	}
	if got := f.Cells[0][3].Fg; got != RGBColor(1, 2, 3) {
		t.Errorf("truecolor fg = %q, want #010203", got)
	}
}

func TestWideCellsReportContinuation(t *testing.T) {
	m := New(6, 1)
	defer m.Close()
	_ = m.Write([]byte("日x"))
	f, _ := m.Frame()
	if f.Cells[0][0].Width != 2 || f.Cells[0][0].Grapheme != "日" {
		t.Fatalf("wide cell = %s", f.Cells[0][0].Describe())
	}
	if f.Cells[0][1].Width != 0 || f.Cells[0][1].Grapheme != "" {
		t.Fatalf("continuation cell = %s", f.Cells[0][1].Describe())
	}
	if f.Cells[0][2].Grapheme != "x" {
		t.Fatalf("cell after wide = %s", f.Cells[0][2].Describe())
	}
}

func TestCompareGridsIgnoresTrailingBlankPadding(t *testing.T) {
	short := Grid{{{Grapheme: "a", Width: 1}}}
	full := Grid{{{Grapheme: "a", Width: 1}, BlankCell, BlankCell}}
	if ms := CompareGrids(short, full, 3, 1); len(ms) != 0 {
		t.Fatalf("padding should compare equal: %s", FormatMismatches(ms, 5))
	}
}

func TestCompareGridsReportsEveryCanonicalProperty(t *testing.T) {
	a := Grid{{{Grapheme: "a", Width: 1}}}
	b := Grid{{{
		Grapheme: "b", Width: 2, Fg: IndexedColor(1), Bg: RGBColor(1, 2, 3),
		UnderlineColor: IndexedColor(4), Underline: UnderlineCurly, Attrs: AttrBold,
		LinkURL: "u", LinkParams: "p",
	}}}
	got := map[string]bool{}
	for _, m := range CompareGrids(a, b, 1, 1) {
		got[m.Field] = true
	}
	for _, want := range []string{"grapheme", "width", "fg", "bg", "underline_color", "underline", "attrs", "link_url", "link_params"} {
		if !got[want] {
			t.Errorf("comparator did not report %q", want)
		}
	}
}

func TestPaneModelInterfaceIsSatisfied(t *testing.T) {
	var _ PaneModel = New(1, 1)
}

// TestSplitUTF8AndCSISurviveWriteBoundaries pins down which split classes are
// safe, separately from the corpus fixture whose known-gap allowance is
// fixture-wide. Splitting a multi-byte rune or an escape sequence must be
// invisible; only multi-rune grapheme clusters are not (GAP-9 in the slice 0
// evidence).
func TestSplitUTF8AndCSISurviveWriteBoundaries(t *testing.T) {
	cases := map[string]string{
		"wide-cjk":    "日本語",
		"csi-sgr":     "\x1b[1;31mred\x1b[0m",
		"csi-cup":     "\x1b[3;4Hxy",
		"osc8":        "\x1b]8;;https://example.com/a\x1b\\link\x1b]8;;\x1b\\",
		"mixed":       "a\x1b[32m日\x1b[0mb",
		"mode-toggle": "\x1b[?25l\x1b[?1002hz",
	}
	for name, input := range cases {
		t.Run(name, func(t *testing.T) {
			whole := renderOnce(t, input, nil)
			for at := 1; at < len(input); at++ {
				got := renderOnce(t, input, []int{at})
				if ms := CompareGrids(whole.Cells, got.Cells, 20, 3); len(ms) > 0 {
					t.Fatalf("split at byte %d changed the grid\n%s", at, FormatMismatches(ms, 10))
				}
				if ms := CompareCursor(
					CursorState{Row: whole.CursorRow, Col: whole.CursorCol, Visible: whole.CursorVisible},
					CursorState{Row: got.CursorRow, Col: got.CursorCol, Visible: got.CursorVisible}); len(ms) > 0 {
					t.Fatalf("split at byte %d changed the cursor\n%s", at, FormatMismatches(ms, 10))
				}
			}
		})
	}
}

func renderOnce(t *testing.T, input string, splits []int) Frame {
	t.Helper()
	m := New(20, 3)
	defer m.Close()
	prev := 0
	for _, at := range splits {
		if err := m.Write([]byte(input[prev:at])); err != nil {
			t.Fatalf("write: %v", err)
		}
		prev = at
	}
	if err := m.Write([]byte(input[prev:])); err != nil {
		t.Fatalf("write: %v", err)
	}
	f, err := m.Frame()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	return f
}
