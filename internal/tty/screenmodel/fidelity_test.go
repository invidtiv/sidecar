package screenmodel

import (
	"fmt"
	"reflect"
	"strings"
	"testing"
)

// replayResult is everything the harness asserts about one replay.
type replayResult struct {
	grid    Grid
	cursor  CursorState
	modes   ModeState
	width   int
	height  int
	history int
}

// replay drives the model through a corpus entry. splitEvery > 0 replays every
// write chunk in pieces of that many bytes; splitStep/splitAt splits exactly
// one chunk at one offset. The three modes together cover whole-write replay,
// every single split boundary, and the pathological byte-at-a-time stream.
type replayPlan struct {
	splitEvery int
	splitStep  int
	splitAt    int
}

func replayEntry(t *testing.T, entry corpusEntry, plan replayPlan) replayResult {
	t.Helper()
	m := New(entry.Width, entry.Height)
	defer m.Close()

	for i, step := range entry.Steps {
		if step.isResize() {
			if err := m.Resize(step.ResizeW, step.ResizeH); err != nil {
				t.Fatalf("resize: %v", err)
			}
			continue
		}
		for _, chunk := range chunksFor(step.Write, i, plan) {
			if err := m.Write([]byte(chunk)); err != nil {
				t.Fatalf("write: %v", err)
			}
		}
	}

	f, err := m.DiagnosticFrame()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	return replayResult{
		grid:    f.Cells,
		cursor:  CursorState{Row: f.CursorRow, Col: f.CursorCol, Visible: f.CursorVisible},
		modes:   ModeState{AltScreen: f.AltScreen, MouseAny: f.Mouse.Any(), MouseSGR: f.Mouse.SGR, BracketedPast: f.BracketedPaste},
		width:   f.Width,
		height:  f.Height,
		history: f.HistorySize,
	}
}

func chunksFor(s string, stepIndex int, plan replayPlan) []string {
	switch {
	case plan.splitEvery > 0:
		var out []string
		for i := 0; i < len(s); i += plan.splitEvery {
			end := min(i+plan.splitEvery, len(s))
			out = append(out, s[i:end])
		}
		return out
	case plan.splitStep == stepIndex && plan.splitAt > 0 && plan.splitAt < len(s):
		return []string{s[:plan.splitAt], s[plan.splitAt:]}
	default:
		return []string{s}
	}
}

// TestCorpusMatchesTmuxOracle is the slice 0 exit criterion: for every
// deterministic fixture, the model's canonical cells, cursor, and modes must
// equal what tmux produced from the same bytes.
func TestCorpusMatchesTmuxOracle(t *testing.T) {
	for _, entry := range corpus {
		t.Run(entry.Name, func(t *testing.T) {
			f := loadFixture(t, entry.Name)
			if f.Fingerprint != entry.fingerprint() {
				t.Fatalf("fixture %s is stale (input changed since it was recorded); re-record with: go test ./internal/tty/screenmodel -run TestRecordCorpus -record", entry.Name)
			}
			got := replayEntry(t, entry, replayPlan{})

			if got.width != f.PaneWidth || got.height != f.PaneHeight {
				t.Fatalf("geometry: tmux %dx%d, model %dx%d", f.PaneWidth, f.PaneHeight, got.width, got.height)
			}

			var all []Mismatch
			oracle := DecodeCapture(f.Capture, f.PaneWidth, f.PaneHeight)
			all = append(all, CompareGrids(oracle, got.grid, f.PaneWidth, f.PaneHeight)...)
			if !entry.SkipCursorAssert {
				all = append(all, CompareCursor(
					CursorState{Row: f.CursorY, Col: f.CursorX, Visible: f.CursorFlag},
					got.cursor)...)
			}
			// tmux exposes alternate_on and mouse_any_flag as formats. It has
			// no format for bracketed paste or the SGR mouse encoding, so those
			// two are model-only state and are not asserted against tmux here.
			if !entry.SkipHistoryAssert && f.HistorySize != got.history {
				all = append(all, Mismatch{Kind: "history", Field: "size", Row: -1, Col: -1,
					Want: fmt.Sprint(f.HistorySize), Got: fmt.Sprint(got.history)})
			}
			all = append(all, CompareModes(
				ModeState{AltScreen: f.AlternateOn, MouseAny: f.MouseAnyFlag,
					MouseSGR: got.modes.MouseSGR, BracketedPast: got.modes.BracketedPast},
				got.modes)...)

			gaps := Signatures(all)
			want := entry.KnownGaps
			if want == nil {
				want = []string{}
			}
			if gaps == nil {
				gaps = []string{}
			}
			if !reflect.DeepEqual(gaps, want) {
				t.Fatalf("mismatch signatures = %v, documented known gaps = %v\n%s",
					gaps, want, FormatMismatches(all, 40))
			}
		})
	}
}

// TestCorpusSplitBoundaries replays every fixture split at every byte offset
// and asserts the result is identical to the single-write replay. This is what
// catches partial CSI, OSC, UTF-8, and grapheme state that does not survive a
// Write boundary.
func TestCorpusSplitBoundaries(t *testing.T) {
	for _, entry := range corpus {
		t.Run(entry.Name, func(t *testing.T) {
			base := replayEntry(t, entry, replayPlan{})
			seen := map[string]bool{}
			for i, step := range entry.Steps {
				if step.isResize() {
					continue
				}
				for at := 1; at < len(step.Write); at++ {
					got := replayEntry(t, entry, replayPlan{splitStep: i, splitAt: at})
					diffs := diffReplays(base, got)
					for _, sig := range Signatures(diffs) {
						seen[sig] = true
					}
					if unexpected := undocumented(diffs, entry.KnownSplitGaps); len(unexpected) > 0 {
						t.Fatalf("splitting step %d at byte %d changed the result in an undocumented way\n%s",
							i, at, FormatMismatches(unexpected, 20))
					}
				}
			}
			assertGapsStillReal(t, seen, entry.KnownSplitGaps, "split")
		})
	}
}

// TestCorpusByteAtATime is the strongest split case: one Write per byte.
func TestCorpusByteAtATime(t *testing.T) {
	for _, entry := range corpus {
		t.Run(entry.Name, func(t *testing.T) {
			base := replayEntry(t, entry, replayPlan{})
			got := replayEntry(t, entry, replayPlan{splitEvery: 1})
			diffs := diffReplays(base, got)
			if unexpected := undocumented(diffs, entry.KnownSplitGaps); len(unexpected) > 0 {
				t.Fatalf("byte-at-a-time replay differs from single-write replay in an undocumented way\n%s",
					FormatMismatches(unexpected, 20))
			}
		})
	}
}

// undocumented filters out mismatches whose signature is a declared known gap.
func undocumented(ms []Mismatch, allowed []string) []Mismatch {
	if len(ms) == 0 {
		return nil
	}
	ok := map[string]bool{}
	for _, a := range allowed {
		ok[a] = true
	}
	var out []Mismatch
	for _, m := range ms {
		if !ok[m.Signature()] {
			out = append(out, m)
		}
	}
	return out
}

// assertGapsStillReal fails when a declared gap no longer reproduces. An
// upstream fix must delete the declaration and the evidence entry, not sit
// there quietly excusing a mismatch that no longer happens.
func assertGapsStillReal(t *testing.T, seen map[string]bool, declared []string, kind string) {
	t.Helper()
	for _, d := range declared {
		if !seen[d] {
			t.Errorf("declared known %s gap %q no longer reproduces; remove it and update the slice 0 evidence", kind, d)
		}
	}
}

func diffReplays(want, got replayResult) []Mismatch {
	var out []Mismatch
	if want.width != got.width || want.height != got.height {
		out = append(out, Mismatch{Kind: "geometry", Field: "size", Row: -1, Col: -1,
			Want: fmt.Sprintf("%dx%d", want.width, want.height),
			Got:  fmt.Sprintf("%dx%d", got.width, got.height)})
		return out
	}
	out = append(out, CompareGrids(want.grid, got.grid, want.width, want.height)...)
	out = append(out, CompareCursor(want.cursor, got.cursor)...)
	out = append(out, CompareModes(want.modes, got.modes)...)
	return out
}

// TestSeedFromCaptureReproducesOracle exercises the mid-stream attach path
// with real tmux data: feed a recorded capture back in as a seed and require
// the model to reconstruct the same canonical cells and cursor the capture
// came from. Everything Sidecar does on pane switch and restart depends on
// this round trip.
func TestSeedFromCaptureReproducesOracle(t *testing.T) {
	for _, entry := range corpus {
		t.Run(entry.Name, func(t *testing.T) {
			f := loadFixture(t, entry.Name)
			if f.Fingerprint != entry.fingerprint() {
				t.Skip("fixture is stale; re-record")
			}
			m := New(f.PaneWidth, f.PaneHeight)
			defer m.Close()
			err := m.Seed(seedInput{
				Output:        f.Capture,
				Width:         f.PaneWidth,
				Height:        f.PaneHeight,
				HistorySize:   f.HistorySize,
				CursorRow:     f.CursorY,
				CursorCol:     f.CursorX,
				CursorVisible: f.CursorFlag,
				AlternateOn:   f.AlternateOn,
				MouseAny:      f.MouseAnyFlag,
			}.toSeed())
			if err != nil {
				t.Fatalf("seed: %v", err)
			}
			got, gerr := m.DiagnosticFrame()
			if gerr != nil {
				t.Fatalf("frame: %v", gerr)
			}
			oracle := DecodeCapture(f.Capture, f.PaneWidth, f.PaneHeight)
			var all []Mismatch
			all = append(all, CompareGrids(oracle, got.Cells, f.PaneWidth, f.PaneHeight)...)
			all = append(all, CompareCursor(
				CursorState{Row: f.CursorY, Col: f.CursorX, Visible: f.CursorFlag},
				CursorState{Row: got.CursorRow, Col: got.CursorCol, Visible: got.CursorVisible})...)
			if unexpected := undocumented(all, entry.KnownSeedGaps); len(unexpected) > 0 {
				t.Fatalf("seeding from the recorded capture did not reproduce it\n%s",
					FormatMismatches(unexpected, 30))
			}
		})
	}
}

// TestFrameOutputRendersTheFrame is the fidelity assertion for [Frame.Output],
// the field that becomes tty.ControlSnapshot.Output and is therefore the only
// part of a frame today's viewport, search, and selection journey actually
// reads. Cells are compared against tmux elsewhere; Output has to be held to
// the claim its doc comment makes — "the ANSI-rendered loaded history followed
// by the live pane rows, in the same shape capture-pane -p -e produces" — and
// that claim has three testable parts:
//
//  1. Shape. Exactly one line per loaded scrolled-off row followed by exactly
//     Height live rows, newline separated. On the alternate screen there is no
//     history, so it is Height rows and nothing else.
//  2. Spelling. Decoding the visible rows with the harness's independent
//     capture decoder — the same hand-written one used against tmux, which
//     shares no code with x/vt — must reproduce the frame's own canonical
//     cells. This is what makes "Output means the same thing as Cells" a
//     tested property rather than an assumption.
//  3. Fixed point. Feeding those rows back in through [Model.Seed] must give
//     the same cells again, because that is exactly what Sidecar does on
//     reattach. This is where GAP-3 surfaces: ultraviolet renders the link
//     correctly from the swapped cell x/vt parsed, so the swap is spelled into
//     Output and re-reading applies it twice.
func TestFrameOutputRendersTheFrame(t *testing.T) {
	for _, entry := range corpus {
		t.Run(entry.Name, func(t *testing.T) {
			f := replayFrame(t, entry)

			lines := strings.Split(f.CombinedOutput(), "\n")
			wantLines := f.Height
			if !f.AltScreen {
				wantLines += f.HistorySize
			}
			if len(lines) != wantLines {
				t.Fatalf("Output has %d lines, want %d (%d loaded history + %d live rows)",
					len(lines), wantLines, wantLines-f.Height, f.Height)
			}
			visible := strings.Join(lines[len(lines)-f.Height:], "\n")

			if ms := CompareGrids(DecodeCapture(visible, f.Width, f.Height), f.Cells, f.Width, f.Height); len(ms) > 0 {
				t.Fatalf("Output does not spell out the frame's own cells\n%s", FormatMismatches(ms, 30))
			}

			m := New(f.Width, f.Height)
			defer m.Close()
			if err := m.Seed(Seed{
				Output: visible, Width: f.Width, Height: f.Height,
				CursorRow: f.CursorRow, CursorCol: f.CursorCol,
				CursorVisible: f.CursorVisible, AltScreen: f.AltScreen, Mouse: f.Mouse,
			}); err != nil {
				t.Fatalf("reseed from Output: %v", err)
			}
			again, err := m.DiagnosticFrame()
			if err != nil {
				t.Fatalf("frame: %v", err)
			}
			reseed := CompareGrids(f.Cells, again.Cells, f.Width, f.Height)
			gaps := Signatures(reseed)
			want := entry.KnownOutputGaps
			if want == nil {
				want = []string{}
			}
			if gaps == nil {
				gaps = []string{}
			}
			if !reflect.DeepEqual(gaps, want) {
				t.Fatalf("reseeding from Output: mismatch signatures = %v, documented known gaps = %v\n%s",
					gaps, want, FormatMismatches(reseed, 30))
			}
		})
	}
}

// replayFrame replays a whole entry and returns the resulting frame, including
// the fields replayResult drops.
func replayFrame(t *testing.T, entry corpusEntry) DiagnosticFrame {
	t.Helper()
	m := New(entry.Width, entry.Height)
	defer m.Close()
	for _, step := range entry.Steps {
		if step.isResize() {
			if err := m.Resize(step.ResizeW, step.ResizeH); err != nil {
				t.Fatalf("resize: %v", err)
			}
			continue
		}
		if err := m.Write([]byte(step.Write)); err != nil {
			t.Fatalf("write: %v", err)
		}
	}
	f, err := m.DiagnosticFrame()
	if err != nil {
		t.Fatalf("frame: %v", err)
	}
	return f
}

// seedInput is the fixture-shaped seed the round-trip test builds.
type seedInput struct {
	Output        string
	Width, Height int
	HistorySize   int
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	AlternateOn   bool
	MouseAny      bool
}

func (s seedInput) toSeed() Seed {
	return Seed{
		// Fixtures record `capture-pane -p` exactly as the shell printed it, so
		// the final row carries a trailing terminator. Seed.Output is row
		// separated rather than row terminated, so the terminator is stripped
		// here rather than inside the model, where it is indistinguishable from
		// a real trailing blank row.
		Output:        strings.TrimSuffix(s.Output, "\n"),
		Width:         s.Width,
		Height:        s.Height,
		HistorySize:   s.HistorySize,
		CursorRow:     s.CursorRow,
		CursorCol:     s.CursorCol,
		CursorVisible: s.CursorVisible,
		AltScreen:     s.AlternateOn,
		Mouse:         MouseState{Normal: s.MouseAny},
	}
}

// TestCorpusCoversPlanCategories keeps the corpus honest: every category the
// plan lists must either have a fixture or a written reason why it cannot be
// covered by a byte fixture.
func TestCorpusCoversPlanCategories(t *testing.T) {
	covered := map[string]bool{}
	for _, entry := range corpus {
		if len(entry.Categories) == 0 {
			t.Errorf("fixture %s declares no plan category", entry.Name)
		}
		for _, c := range entry.Categories {
			if _, ok := planCategories[c]; !ok {
				t.Errorf("fixture %s declares unknown category %q", entry.Name, c)
			}
			covered[c] = true
		}
	}
	for c, reason := range planCategories {
		if covered[c] {
			continue
		}
		if reason == "" {
			t.Errorf("plan category %q has no fixture and no recorded reason", c)
		}
	}
}

// TestCorpusFixturesAreGenerated guards the "generated, non-personal fixtures"
// requirement: nothing in a recorded oracle may contain a home directory, a
// user name, or a host name.
func TestCorpusFixturesAreGenerated(t *testing.T) {
	banned := []string{"/Users/", "/home/", "marcus"}
	for _, entry := range corpus {
		f := loadFixture(t, entry.Name)
		for _, b := range banned {
			if strings.Contains(f.Capture, b) {
				t.Errorf("fixture %s capture contains %q", entry.Name, b)
			}
		}
	}
}

// TestCorpusFixturesDeclareSkipReasons makes a skipped assertion impossible to
// add silently.
func TestCorpusFixturesDeclareSkipReasons(t *testing.T) {
	for _, entry := range corpus {
		if entry.SkipCursorAssert && entry.SkipCursorReason == "" {
			t.Errorf("fixture %s skips the cursor assertion with no reason", entry.Name)
		}
		if entry.SkipHistoryAssert && entry.SkipHistoryReason == "" {
			t.Errorf("fixture %s skips the history assertion with no reason", entry.Name)
		}
		if len(entry.KnownSeedGaps) > 0 && entry.KnownSeedGapReason == "" {
			t.Errorf("fixture %s declares known seed gaps with no recorded reason", entry.Name)
		}
		if len(entry.KnownSplitGaps) > 0 && entry.KnownSplitGapReason == "" {
			t.Errorf("fixture %s declares known split gaps with no recorded reason", entry.Name)
		}
		if len(entry.KnownOutputGaps) > 0 && entry.KnownOutputGapReason == "" {
			t.Errorf("fixture %s declares known Output gaps with no recorded reason", entry.Name)
		}
		if len(entry.KnownGaps) > 0 && entry.KnownGapReason == "" {
			t.Errorf("fixture %s declares known gaps with no recorded reason", entry.Name)
		}
	}
}
