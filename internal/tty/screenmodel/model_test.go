package screenmodel

import (
	"errors"
	"fmt"
	"io"
	"slices"
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
	// Five scrolled-off rows above the four live ones: history_size 12 with 5
	// loaded rows puts the capture's first row at absolute 7.
	err := m.Seed(Seed{
		Output:        "h0\nh1\nh2\nh3\nh4\nhello\nworld\n\n",
		Width:         8,
		Height:        4,
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
	// history_size 5 with three loaded rows: the capture starts at absolute 2.
	if err := m.Seed(Seed{Output: "x\ny\nz\na\nb\nc", Width: 10, Height: 3, HistorySize: 5, CursorRow: 2, CursorCol: 1}); err != nil {
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
	// Output is what becomes ControlSnapshot.Output, so its shape is a contract,
	// not a smoke test: the loaded scrolled-off lines in order, then exactly
	// Height live rows, newline separated and nothing else.
	got := strings.Split(f.CombinedOutput(), "\n")
	want := []string{"x", "y", "z", "a", "b", "c", "d", "e"}
	if !slices.Equal(got, want) {
		t.Errorf("output lines = %#v, want %#v (5 scrolled-off then the 3 live rows)", got, want)
	}
}

func TestAbsoluteHistoryOutgrowsRetainedScrollback(t *testing.T) {
	m := New(8, 2)
	defer m.Close()
	if err := m.Seed(Seed{
		Output: "seed-a\nseed-b", Width: 8, Height: 2,
		HistorySize: 100, HistoryLimit: 3, CursorRow: 1,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Write([]byte("\r\n01\r\n02\r\n03\r\n04\r\n05\r\n06\r\n07\r\n08\r\n09\r\n10")); err != nil {
		t.Fatal(err)
	}
	f, err := m.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if f.HistorySize != 110 {
		t.Fatalf("absolute history = %d, want 110 after ten rows scroll off", f.HistorySize)
	}
	if got := m.emu.ScrollbackLen(); got != 3 {
		t.Fatalf("retained scrollback = %d, want configured window 3", got)
	}
	if f.CaptureBase != 107 {
		t.Fatalf("capture base = %d, want oldest retained absolute row 107", f.CaptureBase)
	}
	if lines := strings.Split(f.CombinedOutput(), "\n"); len(lines) != 5 {
		t.Fatalf("output rows = %d, want 3 retained + 2 live: %q", len(lines), f.CombinedOutput())
	}
}

func TestED3ResetsAbsoluteAndLoadedHistoryCoordinates(t *testing.T) {
	m := New(8, 2)
	defer m.Close()
	if err := m.Seed(Seed{
		Output: "old-a\nold-b\nlive-a\nlive-b", Width: 8, Height: 2,
		HistorySize: 100, HistoryLimit: 3, CursorRow: 1,
	}); err != nil {
		t.Fatal(err)
	}
	before, err := m.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if before.LoadedHistory.Rows() != 2 || before.HistorySize != 100 {
		t.Fatalf("precondition loaded/absolute = %d/%d, want 2/100",
			before.LoadedHistory.Rows(), before.HistorySize)
	}

	if err := m.Write([]byte("\x1b[3J")); err != nil {
		t.Fatal(err)
	}
	cleared, err := m.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if cleared.HistorySize != 0 || cleared.CaptureBase != 0 || cleared.LoadedHistory.Rows() != 0 {
		t.Fatalf("after ED3 history/base/loaded = %d/%d/%d, want 0/0/0",
			cleared.HistorySize, cleared.CaptureBase, cleared.LoadedHistory.Rows())
	}

	if err := m.Write([]byte("\x1b[2;1Hafter\r\nnext")); err != nil {
		t.Fatal(err)
	}
	after, err := m.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if after.HistorySize != 1 || after.CaptureBase != 0 || after.LoadedHistory.Rows() != 1 {
		t.Fatalf("post-clear push history/base/loaded = %d/%d/%d, want 1/0/1",
			after.HistorySize, after.CaptureBase, after.LoadedHistory.Rows())
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

func TestAltScreenFrameKeepsLoadedMainHistory(t *testing.T) {
	m := New(10, 2)
	defer m.Close()
	if err := m.Seed(Seed{Output: "one\ntwo", Width: 10, Height: 2, CursorRow: 1, HistoryLimit: 4}); err != nil {
		t.Fatal(err)
	}
	if err := m.Write([]byte("\r\nthree\x1b[?1049h\x1b[Halt")); err != nil {
		t.Fatal(err)
	}
	f, err := m.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if !f.AltScreen || f.LoadedHistory.Rows() != 1 {
		t.Fatalf("alt/history = %v/%d, want true/1", f.AltScreen, f.LoadedHistory.Rows())
	}
	if got := f.CombinedOutput(); !strings.HasPrefix(got, "one\n") || !strings.Contains(got, "alt") {
		t.Fatalf("alt frame omitted loaded history or grid: %q", got)
	}
}

func TestLoadedHistorySnapshotRemainsImmutable(t *testing.T) {
	m := New(8, 2)
	defer m.Close()
	_ = m.Seed(Seed{Output: "a\nb", Width: 8, Height: 2, CursorRow: 1, HistoryLimit: 2})
	_ = m.Write([]byte("\r\nc"))
	before, _ := m.Frame()
	want := before.LoadedHistory.Output()
	_ = m.Write([]byte("\r\nd\r\ne"))
	if got := before.LoadedHistory.Output(); got != want {
		t.Fatalf("published history mutated after later writes: got %q want %q", got, want)
	}
}

func TestLoadedHistorySnapshotOutputIsSafeWhileHistoryAppends(t *testing.T) {
	m := New(8, 2)
	defer m.Close()
	if err := m.Seed(Seed{Output: "a\nb", Width: 8, Height: 2, CursorRow: 1, HistoryLimit: 2000}); err != nil {
		t.Fatal(err)
	}
	if err := m.Write([]byte("\r\nc")); err != nil {
		t.Fatal(err)
	}
	snapshot, err := m.Frame()
	if err != nil {
		t.Fatal(err)
	}
	want := snapshot.LoadedHistory.Output()

	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 1000 {
				if got := snapshot.LoadedHistory.Output(); got != want {
					t.Errorf("published history changed during append: got %q want %q", got, want)
					return
				}
			}
		}()
	}
	for range 1000 {
		if err := m.Write([]byte("\r\nx")); err != nil {
			t.Fatal(err)
		}
	}
	wg.Wait()
}

func TestAltScreenSeedRestoresSavedMainGrid(t *testing.T) {
	m := New(10, 3)
	defer m.Close()
	if err := m.Seed(Seed{
		MainOutput: "main-a\nmain-b\nmain-c", Output: "alt-a\nalt-b\nalt-c",
		Width: 10, Height: 3, AltScreen: true, CursorVisible: true,
	}); err != nil {
		t.Fatal(err)
	}
	if err := m.Write([]byte("\x1b[?1049l")); err != nil {
		t.Fatal(err)
	}
	f, err := m.Frame()
	if err != nil {
		t.Fatal(err)
	}
	if f.AltScreen || !strings.Contains(f.Output, "main-a") || strings.Contains(f.Output, "alt-a") {
		t.Fatalf("leaving alt did not restore saved main grid: alt=%v output=%q", f.AltScreen, f.Output)
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
	if _, err := m.emu.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("emulator still open after Close: %v", err)
	}
}

// TestCloseReleasesAFaultedModel is the regression for the leak: a faulted
// model is exactly the model a consumer closes, because taking ErrModelFault is
// what sends it back to the capture fallback. Close must still release the
// emulator's pipe and buffers instead of refusing on the sticky error.
func TestCloseReleasesAFaultedModel(t *testing.T) {
	m := New(10, 3)
	if err := m.do(func() error { panic("boom") }); !errors.Is(err, ErrModelFault) {
		t.Fatalf("err = %v, want ErrModelFault", err)
	}

	m.Close()

	if !m.closed {
		t.Error("Close on a faulted model did not mark it closed")
	}
	if _, err := m.emu.Write([]byte("x")); !errors.Is(err, io.ErrClosedPipe) {
		t.Fatalf("faulted model leaked its emulator: write after Close = %v, want io.ErrClosedPipe", err)
	}
	// Terminal and idempotent: closing again is a no-op, and the model reports
	// closed rather than faulted from here on.
	m.Close()
	if err := m.Write([]byte("x")); !errors.Is(err, ErrClosed) {
		t.Fatalf("err = %v, want ErrClosed after closing a faulted model", err)
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

// Seed.Output is row separated, not row terminated. tmux reports a trailing
// blank row whenever the cursor sits on an empty line below the content, and
// dropping it shifts the whole screen up by one against the cursor position
// reported in the same transaction — which silently overwrote the last line of
// the first real mid-stream seed.
func TestSeedHonoursATrailingBlankRow(t *testing.T) {
	m := New(6, 4)
	defer m.Close()
	// Four rows: two of content and two blank, with the cursor on row 2.
	if err := m.Seed(Seed{Output: "aa\nbb\n\n", Width: 6, Height: 4, CursorRow: 2}); err != nil {
		t.Fatal(err)
	}
	if err := m.Write([]byte("cc")); err != nil {
		t.Fatal(err)
	}
	f, err := m.Frame()
	if err != nil {
		t.Fatal(err)
	}
	rows := strings.Split(f.Output, "\n")
	if len(rows) != 4 {
		t.Fatalf("frame rows = %d: %q", len(rows), f.Output)
	}
	if strings.TrimSpace(rows[0]) != "aa" || strings.TrimSpace(rows[1]) != "bb" ||
		strings.TrimSpace(rows[2]) != "cc" {
		t.Fatalf("seed lost a row: %#v", rows)
	}
}

// assertHistoryAccounting checks the one invariant the viewport's cursor and
// scrollback arithmetic both stand on: the absolute window a frame describes,
// HistorySize-CaptureBase, is exactly the history it actually carries.
func assertHistoryAccounting(t *testing.T, m *Model, stage string) Frame {
	t.Helper()
	f, err := m.Frame()
	if err != nil {
		t.Fatalf("%s: frame: %v", stage, err)
	}
	if got, want := f.HistorySize-f.CaptureBase, f.LoadedHistory.Rows(); got != want {
		t.Fatalf("%s: HistorySize-CaptureBase = %d-%d = %d, want the %d loaded rows",
			stage, f.HistorySize, f.CaptureBase, got, want)
	}
	return f
}

// seedWithHistory builds a capture of historyRows scrolled-off rows followed by
// height live rows — the shape tmux delivers, trailing blanks included — and
// pairs it with the history_size the metadata reported. The two disagree when
// the capture and the display-message that accompanied it were observed at
// different instants, which is the race behind td-d29821.
func seedWithHistory(historyRows, height, reportedHistorySize int) Seed {
	rows := make([]string, 0, historyRows+height)
	for i := range historyRows {
		rows = append(rows, fmt.Sprintf("hist%02d", i))
	}
	for i := range height {
		rows = append(rows, fmt.Sprintf("screen%02d", i))
	}
	return Seed{
		Output:      strings.Join(rows, "\n"),
		Width:       12,
		Height:      height,
		HistorySize: reportedHistorySize,
		CursorRow:   height - 1,
	}
}

// td-d29821: the seed freezes the absolute coordinate system, so a seed whose
// capture and metadata disagree stays wrong until the pane is re-seeded. The
// accounting therefore has to come out consistent at the seed itself, and stay
// consistent through writes, ED 2, and a full clear.
func TestHistoryAccountingInvariantHolds(t *testing.T) {
	const height = 4

	for _, tc := range []struct {
		name        string
		historyRows int
		reported    int
		wantHistory int
		wantBase    int
	}{
		// The ordinary case: the metadata describes the capture it arrived with.
		{"consistent", 12, 12, 12, 0},
		// Two lines scrolled off between the display-message and the capture, so
		// the capture carries history the metadata never counted. The capture is
		// the fresher observation and wins.
		{"capture ahead of metadata", 14, 12, 14, 0},
		// The mirror case: history_size names rows the capture did not deliver,
		// so those rows sit below the loaded window rather than in it.
		{"capture behind metadata", 10, 12, 12, 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m := New(12, height)
			defer m.Close()
			if err := m.Seed(seedWithHistory(tc.historyRows, height, tc.reported)); err != nil {
				t.Fatalf("seed: %v", err)
			}
			f := assertHistoryAccounting(t, m, "seed")
			if f.HistorySize != tc.wantHistory || f.CaptureBase != tc.wantBase {
				t.Fatalf("seed history/base = %d/%d, want %d/%d",
					f.HistorySize, f.CaptureBase, tc.wantHistory, tc.wantBase)
			}
			if f.LoadedHistory.Rows() != tc.historyRows {
				t.Fatalf("loaded rows = %d, want the %d the capture carried",
					f.LoadedHistory.Rows(), tc.historyRows)
			}

			// Ordinary output that scrolls rows off the bottom.
			if err := m.Write([]byte("\r\nnew-a\r\nnew-b\r\nnew-c")); err != nil {
				t.Fatalf("write: %v", err)
			}
			scrolled := assertHistoryAccounting(t, m, "after scrolling writes")
			if scrolled.HistorySize != f.HistorySize+3 {
				t.Fatalf("history = %d after three rows scrolled off, want %d",
					scrolled.HistorySize, f.HistorySize+3)
			}

			// ED 2 erases the screen and, in this emulator as in tmux, pushes the
			// erased rows into history rather than dropping them. Either way the
			// accounting has to follow whatever it did.
			if err := m.Write([]byte("\x1b[H\x1b[2J")); err != nil {
				t.Fatalf("ED 2: %v", err)
			}
			cleared := assertHistoryAccounting(t, m, "after ED 2")
			if cleared.HistorySize < scrolled.HistorySize {
				t.Fatalf("ED 2 shrank history to %d from %d without clearing it",
					cleared.HistorySize, scrolled.HistorySize)
			}

			// A full `clear` drops history entirely, which resets the coordinate
			// system rather than merely shrinking the loaded window.
			if err := m.Write([]byte("\x1b[3J\x1b[H\x1b[2J")); err != nil {
				t.Fatalf("clear: %v", err)
			}
			wiped := assertHistoryAccounting(t, m, "after clear")
			if wiped.HistorySize != 0 || wiped.CaptureBase != 0 || wiped.LoadedHistory.Rows() != 0 {
				t.Fatalf("clear left history/base/loaded = %d/%d/%d, want 0/0/0",
					wiped.HistorySize, wiped.CaptureBase, wiped.LoadedHistory.Rows())
			}
		})
	}
}
