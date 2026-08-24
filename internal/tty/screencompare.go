package tty

import (
	"encoding/json"
	"fmt"
	"os"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/marcus/sidecar/internal/tty/screenmodel"
)

// Diagnostic comparison retained from the byte-screen evaluation.
//
// With SIDECAR_TMUX_SCREEN_COMPARE=1 the control client additionally feeds a
// byte-fed pane model for every subscription that has an OnSnapshot consumer,
// and compares that model against tmux's own capture at the exact instant the
// capture response is processed. This explicit diagnostic uses capture as an
// independent oracle; normal presentation remains model-backed.
//
// The comparison point is not incidental. Both the capture response and the
// %output notifications reach the client's single ordered actor on one stream,
// so at the moment the capture response is handled the model has consumed
// exactly the bytes that precede that capture and none that follow it. The two
// sides therefore describe the same moment by construction, which is what makes
// a mismatch attributable to the model rather than to skew.
//
// PRIVACY IS A HARD REQUIREMENT HERE. Everything recorded is a count, a
// dimension, a coordinate, or a fixed class name. No terminal text, no OSC
// payload, and no captured line ever enters the counters or the report. The
// gap classifier below inspects cell values to decide a class, but only the
// class name survives the call.

// screenCompareEnv is the environment variable that turns shadow comparison on.
// It is deliberately an environment variable and not a feature flag: this is a
// diagnostic, not a product capability, and it must not appear in config.
const screenCompareEnv = "SIDECAR_TMUX_SCREEN_COMPARE"

// screenCompareReportEnv optionally names a file the JSON report is written to
// when the control manager stops.
const screenCompareReportEnv = "SIDECAR_TMUX_SCREEN_COMPARE_REPORT"

var screenCompareOn = sync.OnceValue(func() bool {
	switch strings.ToLower(strings.TrimSpace(os.Getenv(screenCompareEnv))) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
})

// screenCompareForced lets a test drive the shadow path without re-execing the
// process. It is nil in production; the environment is the only production
// switch.
var screenCompareForced atomic.Pointer[bool]

// ScreenCompareEnabled reports whether shadow comparison is on for this process.
func ScreenCompareEnabled() bool {
	if forced := screenCompareForced.Load(); forced != nil {
		return *forced
	}
	return screenCompareOn()
}

// ---------------------------------------------------------------------------
// Counters
// ---------------------------------------------------------------------------

// ScreenCompareSample locates one mismatch without describing it. Coordinates
// and class only: a grapheme, a colour value, or a link URL would be terminal
// content and is never recorded.
type ScreenCompareSample struct {
	Signature string `json:"signature"`
	Class     string `json:"class"`
	Row       int    `json:"row"`
	Col       int    `json:"col"`
}

// ScreenCompareStats is the privacy-safe diagnostic counter set the plan's
// "Rollback and observability" section requires. A zero value is usable.
type ScreenCompareStats struct {
	mu sync.Mutex
	ScreenCompareSnapshot
}

// ScreenCompareSnapshot is the counter payload, separated from its mutex so a
// reader can copy and marshal it.
type ScreenCompareSnapshot struct {
	// Model lifecycle.
	Seeds        int            `json:"seeds"`
	SeedRaces    int            `json:"seed_races"`
	Resyncs      map[string]int `json:"resyncs"`
	Faults       int            `json:"faults"`
	Fallbacks    int            `json:"fallbacks"`
	ModelsOpened int            `json:"models_opened"`
	ModelsClosed int            `json:"models_closed"`

	// Byte flow.
	RawEvents      int   `json:"raw_events"`
	RawBytes       int64 `json:"raw_bytes"`
	DiscardedBytes int64 `json:"discarded_bytes"`

	// Frames and captures.
	ModelFrames     int `json:"model_frames"`
	Captures        int `json:"captures"`
	MetadataQueries int `json:"metadata_queries"`
	// CapturesWhileModelLive is the number of capture transactions that ran at
	// a moment a live pane model already held the same screen. Under byte-fed
	// authority these are exactly the captures that would not be issued, which
	// is what the decision gate's "zero capture-pane per output burst" asks
	// about. SeedCaptures is the expected remainder.
	CapturesWhileModelLive int `json:"captures_while_model_live"`
	SeedCaptures           int `json:"seed_captures"`
	DiscardProbes          int `json:"discard_probes"`

	// Comparison outcome.
	Comparisons          int `json:"comparisons"`
	ComparisonsClean     int `json:"comparisons_clean"`
	ComparisonsAttrib    int `json:"comparisons_attributable"`
	ComparisonsOpenWin   int `json:"comparisons_in_open_discard_window"`
	ComparisonsMetaRaced int `json:"comparisons_with_capture_metadata_race"`
	// UncomparableCells counts cell differences dropped because capture-pane
	// cannot express the answer (trailing trimmed blanks). They are neither
	// agreements nor mismatches; recorded so "clean" is never overstated.
	UncomparableCells int `json:"uncomparable_cells"`
	// ComparedCells is the total visible surface offered to the comparator
	// (width × height per comparison). It is the denominator UncomparableCells
	// has to be read against; without it an absolute count of declined cells
	// says nothing about how much of the screen was actually covered.
	ComparedCells int64 `json:"compared_cells"`
	// ComparisonsSkipped counts comparisons abandoned because the capture side
	// described no surface. They are not clean and not mismatched.
	ComparisonsSkipped int `json:"comparisons_skipped"`

	// Mismatches, split the way the decision gate is judged.
	MismatchesBySignature map[string]int `json:"mismatches_by_signature"`
	MismatchesByClass     map[string]int `json:"mismatches_by_class"`
	// FramesWithMismatch counts comparisons (not cells) that had at least one
	// mismatch, so one corrupted row does not read as hundreds of failures.
	FramesWithMismatch            int                   `json:"frames_with_mismatch"`
	FramesWithUnexplainedMismatch int                   `json:"frames_with_unexplained_mismatch"`
	Samples                       []ScreenCompareSample `json:"samples"`

	// Timing, in microseconds, and memory, in bytes.
	OutputToFrameUS   latencyStat `json:"output_to_frame_us"`
	OutputToCaptureUS latencyStat `json:"output_to_capture_us"`
	ModelWriteUS      latencyStat `json:"model_write_us"`
	ModelRenderUS     latencyStat `json:"model_render_us"`
	CompareUS         latencyStat `json:"compare_us"`
	ModelBytesPeak    int64       `json:"model_bytes_peak"`
	ModelBytesLast    int64       `json:"model_bytes_last"`

	Started time.Time `json:"started"`
}

// latencyStat is a count/sum/max summary. A histogram would be more precise;
// count, mean and max are what the decision gate actually asks for.
type latencyStat struct {
	N   int   `json:"n"`
	Sum int64 `json:"sum"`
	Max int64 `json:"max"`
}

func (s *latencyStat) add(d time.Duration) {
	us := d.Microseconds()
	s.N++
	s.Sum += us
	if us > s.Max {
		s.Max = us
	}
}

// Mean returns the mean in microseconds, or 0 with no samples.
func (s latencyStat) Mean() float64 {
	if s.N == 0 {
		return 0
	}
	return float64(s.Sum) / float64(s.N)
}

// maxSamples bounds the retained mismatch coordinate list.
const maxSamples = 200

var screenCompareStats = &ScreenCompareStats{
	ScreenCompareSnapshot: ScreenCompareSnapshot{Started: time.Now()},
}

// ScreenCompare returns the process-wide shadow-comparison counters.
func ScreenCompare() *ScreenCompareStats { return screenCompareStats }

// ResetScreenCompare clears the counters. Tests use it; production never does.
func ResetScreenCompare() {
	screenCompareStats.mu.Lock()
	defer screenCompareStats.mu.Unlock()
	screenCompareStats.ScreenCompareSnapshot = ScreenCompareSnapshot{Started: time.Now()}
}

func (s *ScreenCompareStats) bump(field *int, n int) {
	s.mu.Lock()
	*field += n
	s.mu.Unlock()
}

func (s *ScreenCompareStats) recordSeed(reason ResyncReason) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.Seeds++
	if s.Resyncs == nil {
		s.Resyncs = map[string]int{}
	}
	s.Resyncs[reason.String()]++
	if reason == ResyncSeedRace {
		s.SeedRaces++
	}
}

func (s *ScreenCompareStats) recordRaw(n int) {
	s.mu.Lock()
	s.RawEvents++
	s.RawBytes += int64(n)
	s.mu.Unlock()
}

func (s *ScreenCompareStats) recordModelBytes(n int64) {
	s.mu.Lock()
	s.ModelBytesLast = n
	if n > s.ModelBytesPeak {
		s.ModelBytesPeak = n
	}
	s.mu.Unlock()
}

// Snapshot returns a copy safe to read and marshal.
func (s *ScreenCompareStats) Snapshot() ScreenCompareSnapshot {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := s.ScreenCompareSnapshot
	out.Resyncs = copyCounts(s.Resyncs)
	out.MismatchesBySignature = copyCounts(s.MismatchesBySignature)
	out.MismatchesByClass = copyCounts(s.MismatchesByClass)
	out.Samples = append([]ScreenCompareSample(nil), s.Samples...)
	return out
}

func copyCounts(in map[string]int) map[string]int {
	if in == nil {
		return map[string]int{}
	}
	out := make(map[string]int, len(in))
	for k, v := range in {
		out[k] = v
	}
	return out
}

// JSON renders the counters. The output is safe to attach to a report: by
// construction it contains no terminal content.
func (s *ScreenCompareStats) JSON() []byte {
	snap := s.Snapshot()
	data, err := json.MarshalIndent(snap, "", "  ")
	if err != nil {
		return []byte("{}")
	}
	return data
}

// Report renders the decision-gate evidence as markdown.
func (s *ScreenCompareStats) Report() string {
	snap := s.Snapshot()
	var b strings.Builder
	fmt.Fprintf(&b, "## Shadow comparison counters\n\n")
	fmt.Fprintf(&b, "| Counter | Value |\n| --- | --- |\n")
	rows := [][2]any{
		{"model seeds", snap.Seeds},
		{"seed races detected", snap.SeedRaces},
		{"model faults", snap.Faults},
		{"control fallbacks", snap.Fallbacks},
		{"models opened / closed", fmt.Sprintf("%d / %d", snap.ModelsOpened, snap.ModelsClosed)},
		{"raw %output events", snap.RawEvents},
		{"raw bytes fed", snap.RawBytes},
		{"discarded bytes (client_discarded growth)", snap.DiscardedBytes},
		{"model frames rendered", snap.ModelFrames},
		{"capture-pane transactions", snap.Captures},
		{"display-message metadata queries", snap.MetadataQueries},
		{"captures that a byte-fed authority would avoid", snap.CapturesWhileModelLive},
		{"seed capture transactions (expected exception)", snap.SeedCaptures},
		{"client_discarded cadence probes", snap.DiscardProbes},
		{"comparisons", snap.Comparisons},
		{"comparisons clean", snap.ComparisonsClean},
		{"comparisons attributable (discard window closed)", snap.ComparisonsAttrib},
		{"comparisons inside an open discard window", snap.ComparisonsOpenWin},
		{"comparisons with a capture-metadata race", snap.ComparisonsMetaRaced},
		{"comparisons with any mismatch", snap.FramesWithMismatch},
		{"comparisons with an unexplained mismatch", snap.FramesWithUnexplainedMismatch},
		{"comparisons skipped (degenerate capture geometry)", snap.ComparisonsSkipped},
		{"cells capture-pane could not describe (trailing blanks)",
			fmt.Sprintf("%d of %d compared cells (%.1f%%)", snap.UncomparableCells, snap.ComparedCells,
				percent(int64(snap.UncomparableCells), snap.ComparedCells))},
		{"model memory, peak / last (bytes)", fmt.Sprintf("%d / %d", snap.ModelBytesPeak, snap.ModelBytesLast)},
	}
	for _, row := range rows {
		fmt.Fprintf(&b, "| %s | %v |\n", row[0], row[1])
	}

	fmt.Fprintf(&b, "\n### Latency and cost (microseconds)\n\n")
	fmt.Fprintf(&b, "| Path | n | mean | max |\n| --- | --- | --- | --- |\n")
	for _, l := range []struct {
		name string
		stat latencyStat
	}{
		{"output -> model frame", snap.OutputToFrameUS},
		{"output -> capture snapshot (baseline)", snap.OutputToCaptureUS},
		{"model write", snap.ModelWriteUS},
		{"model render", snap.ModelRenderUS},
		{"shadow compare (diagnostic only)", snap.CompareUS},
	} {
		fmt.Fprintf(&b, "| %s | %d | %.1f | %d |\n", l.name, l.stat.N, l.stat.Mean(), l.stat.Max)
	}

	fmt.Fprintf(&b, "\n### Mismatches by class\n\n")
	if len(snap.MismatchesByClass) == 0 {
		b.WriteString("none\n")
	} else {
		fmt.Fprintf(&b, "| Class | Cells |\n| --- | --- |\n")
		for _, k := range sortedKeys(snap.MismatchesByClass) {
			fmt.Fprintf(&b, "| %s | %d |\n", k, snap.MismatchesByClass[k])
		}
	}

	fmt.Fprintf(&b, "\n### Mismatches by signature\n\n")
	if len(snap.MismatchesBySignature) == 0 {
		b.WriteString("none\n")
	} else {
		fmt.Fprintf(&b, "| Signature | Count |\n| --- | --- |\n")
		for _, k := range sortedKeys(snap.MismatchesBySignature) {
			fmt.Fprintf(&b, "| %s | %d |\n", k, snap.MismatchesBySignature[k])
		}
	}

	fmt.Fprintf(&b, "\n### Resync reasons\n\n")
	if len(snap.Resyncs) == 0 {
		b.WriteString("none\n")
	} else {
		fmt.Fprintf(&b, "| Reason | Count |\n| --- | --- |\n")
		for _, k := range sortedKeys(snap.Resyncs) {
			fmt.Fprintf(&b, "| %s | %d |\n", k, snap.Resyncs[k])
		}
	}
	return b.String()
}

// percent renders a/b as a percentage, or 0 when b is zero.
func percent(a, b int64) float64 {
	if b == 0 {
		return 0
	}
	return 100 * float64(a) / float64(b)
}

func sortedKeys(m map[string]int) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// writeScreenCompareReport dumps the JSON report if the environment asks for
// one. Called on manager stop, and periodically while comparing.
//
// The periodic write matters for the real-application proof: a Sidecar driven
// headlessly is usually killed rather than shut down cleanly, so a
// stop-only report would be empty exactly when it is needed.
func writeScreenCompareReport() {
	path := strings.TrimSpace(os.Getenv(screenCompareReportEnv))
	if path == "" || !ScreenCompareEnabled() {
		return
	}
	_ = os.WriteFile(path, screenCompareStats.JSON(), 0o600)
}

// reportEvery is how many comparisons pass between periodic report writes.
const reportEvery = 25

// ---------------------------------------------------------------------------
// Comparison
// ---------------------------------------------------------------------------

// Gap classes. The named ones are the slice-0 emulator defects; anything the
// classifier cannot place is "unexplained", which is the number the decision
// gate is judged on.
const (
	gapClassUnexplained = "unexplained"
	gapClassOSC8Swap    = "gap-3-osc8-url-params-swapped"
	gapClassOSC8Semi    = "gap-4-osc8-semicolon-dropped"
	gapClassSGR21       = "gap-2-sgr21-double-underline"
	gapClassCluster     = "gap-6/9-grapheme-cluster"
	gapClassClusterCur  = "gap-9-cursor-after-cluster"
	gapClassRISHistory  = "gap-8-ris-history"
	// Adapter-level divergences found by this slice. They are named rather than
	// "unexplained" because their cause is understood and written down in the
	// slice-2 evidence — but unlike the gap-* classes they are Sidecar's own
	// defects, not upstream ones, and they are gate-blocking.
	adapterAltHistoryRows = "adapter-alt-screen-history-not-rendered"
	adapterHistoryDrift   = "adapter-absolute-history-drift"
)

// screenCompareInput is everything one comparison needs from the capture side.
type screenCompareInput struct {
	CaptureOutput string
	Width, Height int
	CursorRow     int
	CursorCol     int
	CursorVisible bool
	HistorySize   int
	AltScreen     bool
	MouseAny      bool
	MouseSGR      bool
	// CursorTrustworthy is false when pane bytes arrived between the capture
	// path's own metadata response and its capture response. The capture path
	// (unlike the seed transaction) writes those two commands separately, so its
	// metadata can describe an older moment than its capture. A cursor
	// difference in that window says nothing about the model.
	CursorTrustworthy bool
}

// screenCompareResult is one comparison outcome.
type screenCompareResult struct {
	Mismatches  []screenmodel.Mismatch
	Classes     []string // parallel to Mismatches
	Unexplained int
	HistoryRows int
	// Uncomparable counts differences that were dropped because
	// `capture-pane -e` cannot express the answer, not because the two sides
	// agreed. Reported so the clean-comparison number is never read as broader
	// than it is.
	Uncomparable int
	// VisibleCells is the size of the compared surface (width × height). It is
	// the denominator Uncomparable has to be read against.
	VisibleCells int
	// Invalid marks a comparison that could not be performed at all — today
	// only degenerate capture geometry. It must never be folded in as a clean
	// comparison; recordComparison counts it separately.
	Invalid bool
}

// beyondCaptureExtent reports whether a cell difference falls in the region
// `capture-pane -e` cannot describe: at or past the column where tmux trimmed
// the row's trailing blanks, with the model also holding a plain blank there.
// A real character the capture does not have is still a mismatch; only the
// *styling* of trailing blanks is unknowable.
func beyondCaptureExtent(extents []screenExtent, m screenmodel.Mismatch, got screenmodel.Cell) bool {
	if m.Row < 0 || m.Col < 0 || m.Row >= len(extents) {
		return false
	}
	if m.Col < extents[m.Row] {
		return false
	}
	if m.Field == "grapheme" || m.Field == "width" {
		return false
	}
	return got.Grapheme == " " && got.Width == 1
}

// screenExtent is a per-row content end column.
type screenExtent = int

// compareCaptureWithModel is the canonical comparison: cells, style and link
// attributes, cursor, dimensions, alternate-screen state, mouse modes, and
// loaded history. It never compares rendered string spelling — the model side
// explicitly requests the model's canonical diagnostic grid, and the capture
// side is decoded by screenmodel's independent hand-written decoder.
func compareCaptureWithModel(in screenCompareInput, frame screenmodel.DiagnosticFrame) screenCompareResult {
	var res screenCompareResult
	add := func(m screenmodel.Mismatch, class string) {
		res.Mismatches = append(res.Mismatches, m)
		res.Classes = append(res.Classes, class)
		if class == gapClassUnexplained {
			res.Unexplained++
		}
	}

	w, h := in.Width, in.Height
	if w < 1 || h < 1 {
		// A degenerate capture describes no surface, so nothing can be
		// concluded from it. Returning an empty result here used to score as a
		// clean comparison, which would have inflated the fidelity numbers with
		// comparisons that never happened.
		res.Invalid = true
		return res
	}

	// Dimensions.
	if frame.Width != w {
		add(screenmodel.Mismatch{Kind: "geometry", Field: "width", Row: -1, Col: -1}, gapClassUnexplained)
	}
	if frame.Height != h {
		add(screenmodel.Mismatch{Kind: "geometry", Field: "height", Row: -1, Col: -1}, gapClassUnexplained)
	}

	captureLines := splitCaptureLines(in.CaptureOutput)
	visible, history := splitVisibleAndHistory(captureLines, h)

	// Visible cells.
	want, extents := screenmodel.DecodeCaptureExtent(strings.Join(visible, "\n"), w, h)
	got := frame.Cells.Normalize(w, h)
	res.VisibleCells = w * h
	clusterSeen := false
	for _, m := range screenmodel.CompareGrids(want, got, w, h) {
		wantCell, gotCell := cellAt(want, m.Row, m.Col), cellAt(got, m.Row, m.Col)
		if beyondCaptureExtent(extents, m, gotCell) {
			res.Uncomparable++
			continue
		}
		class := classifyCellMismatch(m, wantCell, gotCell)
		if class == gapClassCluster {
			clusterSeen = true
		}
		add(m, class)
	}

	// Cursor.
	if in.CursorTrustworthy {
		cursor := screenmodel.CompareCursor(
			screenmodel.CursorState{Row: in.CursorRow, Col: in.CursorCol, Visible: in.CursorVisible},
			screenmodel.CursorState{Row: frame.CursorRow, Col: frame.CursorCol, Visible: frame.CursorVisible},
		)
		for _, m := range cursor {
			class := gapClassUnexplained
			if m.Field == "position" && clusterSeen {
				// A wrongly split cluster advances the cursor differently from
				// the single cell it should have formed (slice-0 GAP-9).
				class = gapClassClusterCur
			}
			add(m, class)
		}
	}

	// Modes. Bracketed paste is excluded on purpose: tmux exposes no format for
	// it, and per plan §4 tmux — not this model — owns paste correctness.
	for _, m := range screenmodel.CompareModes(
		screenmodel.ModeState{AltScreen: in.AltScreen, MouseAny: in.MouseAny, MouseSGR: in.MouseSGR},
		screenmodel.ModeState{AltScreen: frame.AltScreen, MouseAny: frame.Mouse.Any(), MouseSGR: frame.Mouse.SGR},
	) {
		add(m, gapClassUnexplained)
	}

	// Loaded history.
	res.HistoryRows = len(history)
	if frame.HistorySize != in.HistorySize {
		class := gapClassUnexplained
		switch {
		case frame.HistorySize == 0 && in.HistorySize > 0 && frame.HardResets > 0:
			// GAP-8: on RIS the emulator discards the screen where tmux pushes
			// it into history. Requires positive evidence that a RIS actually
			// reached the model since the last seed — without that, any cause
			// that zeroes the model's history would be amnestied by this class.
			class = gapClassRISHistory
		case frame.ScrollbackAtCap:
			// The model counts scrolled-off lines against the emulator's own
			// bounded scrollback, so once that cap is reached the absolute
			// count stops tracking tmux's. Slice 0 recorded the limitation;
			// this is it being reached in a real session. The precondition is
			// the emulator being *at* its cap, not the reported number being
			// large: a big history that the model is still tracking correctly
			// has no excuse here.
			class = adapterHistoryDrift
		}
		add(screenmodel.Mismatch{Kind: "history", Field: "size", Row: -1, Col: -1}, class)
	}
	if len(history) > 0 {
		modelLines := splitCaptureLines(frame.CombinedOutput())
		_, modelHistory := splitVisibleAndHistory(modelLines, frame.Height)
		if len(modelHistory) < len(history) {
			class := gapClassUnexplained
			if in.AltScreen && len(modelHistory) == 0 {
				// On the alternate screen the model frame renders the alternate
				// grid only, while tmux's capture still returns the main
				// screen's loaded history above it. Understood and recorded;
				// see the slice-2 evidence.
				class = adapterAltHistoryRows
			}
			add(screenmodel.Mismatch{Kind: "history", Field: "rows", Row: -1, Col: -1}, class)
		} else {
			// Align from the bottom: the model retains more history than the
			// bounded capture window, and only the overlap is comparable.
			modelTail := modelHistory[len(modelHistory)-len(history):]
			wantH, histExtents := screenmodel.DecodeCaptureExtent(strings.Join(history, "\n"), w, len(history))
			gotH := screenmodel.DecodeCapture(strings.Join(modelTail, "\n"), w, len(history))
			for _, m := range screenmodel.CompareGrids(wantH, gotH, w, len(history)) {
				wantCell, gotCell := cellAt(wantH, m.Row, m.Col), cellAt(gotH, m.Row, m.Col)
				if beyondCaptureExtent(histExtents, m, gotCell) {
					res.Uncomparable++
					continue
				}
				m.Kind = "history"
				add(m, classifyCellMismatch(m, wantCell, gotCell))
			}
		}
	}
	return res
}

func cellAt(g screenmodel.Grid, row, col int) screenmodel.Cell {
	if row < 0 || col < 0 || row >= len(g) || col >= len(g[row]) {
		return screenmodel.BlankCell
	}
	return g[row][col]
}

// splitCaptureLines splits a capture-pane rendering into rows. It is row
// separated, not row terminated (see screenmodel.seedBody), so a trailing
// newline means a final blank row.
func splitCaptureLines(text string) []string {
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

// splitVisibleAndHistory returns the last h rows (the live grid) and everything
// before them (the loaded history). tmux's capture ends at the bottom of the
// visible pane, so the split is positional and exact.
func splitVisibleAndHistory(lines []string, h int) (visible, history []string) {
	if h < 1 || len(lines) <= h {
		return lines, nil
	}
	return lines[len(lines)-h:], lines[:len(lines)-h]
}

// classifyCellMismatch attributes a cell difference to a documented slice-0
// emulator gap where the difference has that gap's exact shape, and to
// "unexplained" otherwise. Only the returned class is ever recorded; the cell
// values it inspects do not leave this function.
func classifyCellMismatch(m screenmodel.Mismatch, want, got screenmodel.Cell) string {
	switch m.Field {
	case "link_url", "link_params":
		// GAP-3: x/vt assigns OSC 8 params to URL and URL to params.
		if want.LinkURL == got.LinkParams && want.LinkParams == got.LinkURL &&
			(want.LinkURL != "" || want.LinkParams != "") {
			return gapClassOSC8Swap
		}
		// GAP-4: a URI containing ';' loses its link entirely.
		if want.LinkURL != "" && got.LinkURL == "" && got.LinkParams == "" &&
			strings.Contains(want.LinkURL, ";") {
			return gapClassOSC8Semi
		}
		return gapClassUnexplained
	case "underline":
		// GAP-2: SGR 21 is not read as a double underline.
		if want.Underline == screenmodel.UnderlineDouble && got.Underline == screenmodel.UnderlineNone {
			return gapClassSGR21
		}
		return gapClassUnexplained
	case "grapheme", "width":
		// GAP-6 / GAP-9: a multi-rune cluster committed as its leading rune.
		if isClusterPrefixSplit(want, got) {
			return gapClassCluster
		}
		return gapClassUnexplained
	default:
		return gapClassUnexplained
	}
}

// isClusterPrefixSplit reports the shape both cluster gaps produce: tmux holds
// a multi-rune grapheme and the model holds a strict prefix of it (usually the
// base character alone), or the corresponding width difference.
func isClusterPrefixSplit(want, got screenmodel.Cell) bool {
	if len([]rune(want.Grapheme)) > 1 && got.Grapheme != "" &&
		strings.HasPrefix(want.Grapheme, got.Grapheme) && want.Grapheme != got.Grapheme {
		return true
	}
	// The row after a wrongly split cluster is shifted, so the neighbouring
	// continuation column reads as a real cell. Treat only the exact
	// wide-vs-narrow pairing as attributable.
	if want.Width == 2 && got.Width == 1 && len([]rune(want.Grapheme)) > 1 &&
		strings.HasPrefix(want.Grapheme, got.Grapheme) {
		return true
	}
	return false
}

// shadowCompare compares tmux's just-delivered capture against the byte-fed
// model for the same pane. It runs on the client's ordered actor, inside the
// capture response, which is the only instant at which the two sides are known
// to describe the same moment.
//
// It never mutates the snapshot and never touches delivery: capture-pane
// remains the frame the consumer receives, exactly as it does with shadow mode
// off.
func (c *sessionControlClient) shadowCompare(pane string, snapshot ControlSnapshot, extras captureExtras) {
	state := c.comparePane(pane)
	if state == nil {
		return
	}
	metaRaced := state.rawSinceMeta > 0
	state.metaSeen = false
	state.rawSinceMeta = 0
	if !state.pendingSince.IsZero() {
		screenCompareStats.mu.Lock()
		screenCompareStats.OutputToCaptureUS.add(time.Since(state.pendingSince))
		screenCompareStats.mu.Unlock()
		state.pendingSince = time.Time{}
	}
	screenCompareStats.bump(&screenCompareStats.Captures, 1)

	var feed *paneModelFeed
	for _, candidate := range c.models {
		if candidate.pane == pane && candidate.state == modelLive {
			feed = candidate
			break
		}
	}
	if feed == nil {
		return
	}
	// This capture ran while a live model already held the same screen, so it is
	// one of the captures a byte-fed authority would not have issued.
	screenCompareStats.bump(&screenCompareStats.CapturesWhileModelLive, 1)

	frame, err := feed.model.DiagnosticFrame()
	if err != nil {
		c.faultFeed(feed, ResyncModelFault, err)
		return
	}
	start := time.Now()
	res := compareCaptureWithModel(screenCompareInput{
		CaptureOutput:     snapshot.Output,
		Width:             snapshot.PaneWidth,
		Height:            snapshot.PaneHeight,
		CursorRow:         snapshot.CursorRow,
		CursorCol:         snapshot.CursorCol,
		CursorVisible:     snapshot.CursorVisible,
		HistorySize:       snapshot.HistorySize,
		AltScreen:         extras.AltScreen,
		MouseAny:          snapshot.MouseReporting,
		MouseSGR:          extras.MouseSGR,
		CursorTrustworthy: !metaRaced,
	}, frame)

	// The slice-1 precondition: a mismatch is attributable to the model only
	// once a discard check *later* than this observation confirms the counter
	// did not move. The extended capture metadata carries client_discarded from
	// the same transaction as the screen, so the check is exact here rather than
	// bounded by the 1 s cadence — the window closes at the comparison point
	// itself. It stays open only when the counter grew or was never read.
	//
	// STRUCTURAL HAZARD, disclosed rather than removed: this defaults to
	// *false*, and an unattributable mismatch is recorded under a
	// "discard-window/" prefix that the unexplained tally and the evidence
	// table both exclude. A break in client_discarded parsing would therefore
	// zero the headline mismatch number while every comparison still counted.
	// ComparisonsOpenWin is the tripwire: the matrix asserts it is zero, so the
	// silent-amnesty path cannot be entered without failing the evidence run.
	attributable := feed.discardSeen && extras.Valid && extras.Discarded == feed.discarded
	if extras.Valid && extras.Discarded > feed.discarded {
		screenCompareStats.mu.Lock()
		screenCompareStats.DiscardedBytes += extras.Discarded - feed.discarded
		screenCompareStats.mu.Unlock()
		feed.discarded = extras.Discarded
		c.invalidate(feed, ResyncDiscarded, nil, false)
		c.beginSeed(feed, ResyncDiscarded)
	}
	if extras.Valid {
		feed.discardCheckedAt = time.Now()
	}
	if screenCompareStats.recordComparison(res, attributable, metaRaced, time.Since(start)) {
		writeScreenCompareReport()
	}
}

// record folds one comparison into the counters.
// recordComparison folds one comparison into the counters and reports whether
// this was a periodic report checkpoint.
func (s *ScreenCompareStats) recordComparison(res screenCompareResult, attributable, metaRaced bool, took time.Duration) (checkpoint bool) {
	s.mu.Lock()
	defer func() {
		checkpoint = s.Comparisons%reportEvery == 0
		s.mu.Unlock()
	}()
	s.Comparisons++
	s.CompareUS.add(took)
	s.UncomparableCells += res.Uncomparable
	s.ComparedCells += int64(res.VisibleCells)
	if res.Invalid {
		s.ComparisonsSkipped++
		return false
	}
	if attributable {
		s.ComparisonsAttrib++
	} else {
		s.ComparisonsOpenWin++
	}
	if metaRaced {
		s.ComparisonsMetaRaced++
	}
	if len(res.Mismatches) == 0 {
		s.ComparisonsClean++
		return false
	}
	s.FramesWithMismatch++
	if res.Unexplained > 0 {
		s.FramesWithUnexplainedMismatch++
	}
	if s.MismatchesBySignature == nil {
		s.MismatchesBySignature = map[string]int{}
	}
	if s.MismatchesByClass == nil {
		s.MismatchesByClass = map[string]int{}
	}
	for i, m := range res.Mismatches {
		class := gapClassUnexplained
		if i < len(res.Classes) {
			class = res.Classes[i]
		}
		// An unattributable comparison still records its shape, but under a
		// separate class so it can never be counted as a model defect.
		if !attributable {
			class = "discard-window/" + class
		}
		s.MismatchesBySignature[m.Signature()]++
		s.MismatchesByClass[class]++
		if len(s.Samples) < maxSamples {
			s.Samples = append(s.Samples, ScreenCompareSample{
				Signature: m.Signature(), Class: class, Row: m.Row, Col: m.Col,
			})
		}
	}
	return false
}
