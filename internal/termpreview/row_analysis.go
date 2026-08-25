package termpreview

import (
	"hash/maphash"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/terminalperf"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

var rowAnalysisSeed = maphash.MakeSeed()

// RowAnalyzer owns the bounded ANSI facts for one terminal surface. A host may
// keep it across draws: changing buffers rotates the whole context, while a
// changed revision retains only byte-identical rows that are still required by
// the current visible/live windows and their predecessor lookbacks.
type RowAnalyzer struct {
	mu sync.Mutex

	buffer   *tty.OutputBuffer
	revision uint64
	context  analysisContext
	rows     map[int]*analyzedRow
}

type analysisContext struct {
	visibleStart, visibleEnd int
	paneTop, paneHeight      int
	backgrounds              tty.BackgroundMode
	spanMax                  int
}

type analyzedRow struct {
	fingerprint uint64
	byteLen     int
	raw         string // collision guard; bounded by the retained row windows

	backgroundFree string
	visibleText    string
	visibleWidth   int
	blank          bool

	hasTransition bool
	trailing      string
	prefixTouches bool
	prefixBG      string
	hasPrintable  bool
	explicitBGs   []string

	resolvedValid    bool
	resolvedIncoming string
	resolved         resolvedRow
	resolveCount     uint64 // package-test evidence for carry convergence
}

type resolvedRow struct {
	wire           string
	backgroundFree string
	trailing       string
	touched        bool
	backgrounds    []string
	first          string
	blank          bool
}

type analysisWindow struct {
	revision               uint64
	visible                []resolvedRow
	visiblePredecessorBand int
	live                   []resolvedRow
}

type indexedRows struct {
	from int
	rows []string
}

// analyze acquires revision-consistent visible/live bands, reuses collision-
// safe raw facts, and resolves carried background state without re-decoding
// ANSI. The two bands stay disjoint when a user is far back in history; the
// intervening scrollback is neither copied nor retained.
func (a *RowAnalyzer) analyze(in RowsInput, backgrounds tty.BackgroundMode, spanMax int) analysisWindow {
	if a == nil {
		a = &RowAnalyzer{}
	}
	buffer := in.Buffer
	layout := in.Layout
	visibleFrom := max(layout.Start-rowBackgroundLookback, 0)
	visibleTo := layout.End
	liveFrom, liveTo := 0, 0
	if backgrounds == tty.BackgroundAuto && layout.PaneTop >= 0 && in.PaneHeight > 0 {
		liveFrom = max(layout.PaneTop-rowBackgroundLookback, 0)
		liveTo = layout.PaneTop + in.PaneHeight
	}

	revision, bands := consistentBands(buffer, [][2]int{{visibleFrom, visibleTo}, {liveFrom, liveTo}})
	visibleBand, liveBand := bands[0], bands[1]

	a.mu.Lock()
	defer a.mu.Unlock()

	context := analysisContext{
		visibleStart: layout.Start,
		visibleEnd:   layout.End,
		paneTop:      layout.PaneTop,
		paneHeight:   in.PaneHeight,
		backgrounds:  backgrounds,
		spanMax:      spanMax,
	}
	if a.buffer != buffer {
		a.buffer = buffer
		a.rows = make(map[int]*analyzedRow)
	} else if a.context != context {
		// Raw ANSI facts remain valid, but a new window/mode owns a new carried
		// derivation. This makes mode/span and predecessor-band rotations explicit.
		for _, row := range a.rows {
			row.resolvedValid = false
		}
	}
	a.context = context
	a.revision = revision
	if a.rows == nil {
		a.rows = make(map[int]*analyzedRow)
	}

	required := make(map[int]struct{}, len(visibleBand.rows)+len(liveBand.rows))
	for _, band := range []indexedRows{visibleBand, liveBand} {
		for i, raw := range band.rows {
			index := band.from + i
			required[index] = struct{}{}
			fingerprint := maphash.String(rowAnalysisSeed, raw)
			cached, ok := a.rows[index]
			if ok && cached.fingerprint == fingerprint && cached.byteLen == len(raw) && cached.raw == raw {
				terminalperf.Record(terminalperf.RowCacheHit)
				continue
			}
			a.rows[index] = analyzeRawRow(raw, fingerprint)
			terminalperf.Record(terminalperf.RowCacheMiss)
		}
	}
	for index := range a.rows {
		if _, ok := required[index]; !ok {
			delete(a.rows, index)
		}
	}

	visible, visiblePredecessorBand := a.resolveBand(visibleBand, layout.Start)
	live, _ := a.resolveBand(liveBand, layout.PaneTop)
	return analysisWindow{
		revision:               revision,
		visible:                visible,
		visiblePredecessorBand: visiblePredecessorBand,
		live:                   live,
	}
}

func consistentBands(buffer *tty.OutputBuffer, ranges [][2]int) (uint64, []indexedRows) {
	bands := make([]indexedRows, len(ranges))
	if buffer == nil {
		return 0, bands
	}
	requested := make([]tty.RowRange, len(ranges))
	for i, bounds := range ranges {
		requested[i] = tty.RowRange{Start: bounds[0], End: bounds[1]}
		bands[i].from = bounds[0]
	}
	revision, snapshots := buffer.SnapshotRanges(requested...)
	for i := range bands {
		bands[i].rows = snapshots[i]
	}
	return revision, bands
}

func analyzeRawRow(raw string, fingerprint uint64) *analyzedRow {
	// OutputBuffer rows are split substrings and may still point at the full
	// capture allocation. The cache outlives that snapshot, so its collision
	// guard must own only this bounded row's bytes.
	row := &analyzedRow{fingerprint: fingerprint, byteLen: len(raw), raw: strings.Clone(raw)}
	var backgroundFree, visible strings.Builder
	backgroundFree.Grow(len(raw))
	visible.Grow(len(raw))
	explicit := make(map[string]struct{})
	state := ansi.NormalState
	remaining := raw
	for len(remaining) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(remaining, state, nil)
		if n <= 0 {
			backgroundFree.WriteString(remaining)
			visible.WriteString(remaining)
			break
		}
		backgroundFree.WriteString(ui.StripSequenceBackgrounds(seq))
		if next, touches := ui.SGRBackground(seq); touches {
			row.hasTransition = true
			if next == ui.RowBackgroundDefault {
				row.trailing = ""
			} else {
				row.trailing = next
				explicit[next] = struct{}{}
			}
			if !row.hasPrintable {
				row.prefixTouches = true
				row.prefixBG = row.trailing
			}
		}
		if width > 0 {
			row.hasPrintable = true
			row.visibleWidth += width
			visible.WriteString(seq)
		}
		state = newState
		remaining = remaining[n:]
	}
	row.backgroundFree = backgroundFree.String()
	row.visibleText = visible.String()
	row.blank = strings.TrimSpace(row.visibleText) == ""
	row.explicitBGs = make([]string, 0, len(explicit))
	for bg := range explicit {
		row.explicitBGs = append(row.explicitBGs, bg)
	}
	return row
}

func (a *RowAnalyzer) resolveBand(band indexedRows, keepFrom int) ([]resolvedRow, int) {
	if len(band.rows) == 0 {
		return nil, 0
	}
	incoming := ""
	out := make([]resolvedRow, 0, max(len(band.rows)-(keepFrom-band.from), 0))
	bandLen := 0
	predecessorBandLen := 0
	for i := range band.rows {
		index := band.from + i
		row := a.rows[index]
		resolved := row.resolve(incoming)
		incoming = resolved.trailing
		if index == keepFrom {
			predecessorBandLen = bandLen
		}
		if resolved.touched || resolved.trailing != "" {
			bandLen++
		} else {
			bandLen = 0
		}
		if index >= keepFrom {
			out = append(out, resolved)
		}
	}
	return out, predecessorBandLen
}

func (r *analyzedRow) resolve(incoming string) resolvedRow {
	if r.resolvedValid && r.resolvedIncoming == incoming {
		return r.resolved
	}
	trailing := incoming
	if r.hasTransition {
		trailing = r.trailing
	}
	wire := r.raw
	backgroundFree := r.backgroundFree
	if incoming != "" {
		wire = incoming + wire
		backgroundFree = ui.RowBackgroundDefault + backgroundFree
	}
	backgrounds := append([]string(nil), r.explicitBGs...)
	if incoming != "" {
		backgrounds = appendUnique(backgrounds, incoming)
	}
	first := incoming
	if r.hasPrintable {
		if r.prefixTouches {
			first = r.prefixBG
		}
	} else {
		first = trailing
	}
	resolved := resolvedRow{
		wire:           wire,
		backgroundFree: backgroundFree,
		trailing:       trailing,
		touched:        incoming != "" || r.hasTransition,
		backgrounds:    backgrounds,
		first:          first,
		blank:          r.blank,
	}
	r.resolvedIncoming = incoming
	r.resolved = resolved
	r.resolvedValid = true
	r.resolveCount++
	return resolved
}

func appendUnique(values []string, value string) []string {
	for _, existing := range values {
		if existing == value {
			return values
		}
	}
	return append(values, value)
}

func inferCanvas(rows []resolvedRow) string {
	terminalperf.Record(terminalperf.CanvasInference)
	if len(rows) == 0 {
		return ""
	}
	last := len(rows) - 1
	for last >= 0 && rows[last].blank && len(rows[last].backgrounds) == 0 {
		last--
	}
	if last < 0 {
		return ""
	}
	rows = rows[:last+1]
	counts := make(map[string]int)
	blankRows := make(map[string]int)
	firstCell := make(map[string]int)
	overlap := make(map[string]int)
	paintedRowCount := 0
	for _, row := range rows {
		if len(row.backgrounds) == 0 {
			continue
		}
		paintedRowCount++
		if row.first != "" {
			firstCell[row.first]++
		}
		for _, bg := range row.backgrounds {
			counts[bg]++
			if row.blank {
				blankRows[bg]++
			}
			if len(row.backgrounds) > 1 {
				overlap[bg]++
			}
		}
	}
	canvas, best, tied := "", 0, false
	for bg, count := range counts {
		if count > best {
			canvas, best, tied = bg, count, false
		} else if count == best {
			tied = true
		}
	}
	if tied {
		canvas = ""
		bestFirst := 0
		for bg, count := range counts {
			if count != best {
				continue
			}
			if firstCell[bg] > bestFirst {
				canvas, bestFirst = bg, firstCell[bg]
			} else if firstCell[bg] == bestFirst {
				canvas = ""
			}
		}
	}
	if canvas == "" || paintedRowCount == 0 || best < CanvasRowShare(paintedRowCount) {
		return ""
	}
	// Row starts are always required; how many depends on whether blank rows
	// vouch for the candidate.
	//
	// A pane's canvas is the background its rows begin in. An inset block —
	// a chat bubble, a callout, a boxed banner — opens after the row's first
	// cell, and on a sparsely painted pane it can still cover nearly every
	// painted row and clear the share bar. Cursor's user-message bubble is
	// three such rows, two of them blank padding, in a grid where the only
	// other painted row is the input field: it took the canvas and flooded
	// the pane, then gave it back as the bubble scrolled out of the live
	// grid — a whole-pane repaint on streamed output (see CanvasRowShare).
	//
	// The bar is a strict majority, so a candidate that splits the row starts
	// evenly with another abstains rather than guessing, like the row-count
	// tie above. Row starts cannot separate a small inset block from a large
	// one, so a genuinely pane-wide canvas that is itself inset from column 0
	// is rejected too: that costs the seams this detection exists to hide,
	// which is the stable cosmetic failure it replaced flicker with. Painted
	// share of the live grid would separate them, but a TUI that repaints in
	// sections leaves most of its grid abstaining (see the interior-abstention
	// case in rows_test.go), so the grid is not a denominator we can use.
	if blankRows[canvas] == 0 {
		// Nothing blank vouches for it: demand near-total row starts and
		// same-row co-occurrence, which line-level highlighting never has.
		if firstCell[canvas] < CanvasRowShare(paintedRowCount) ||
			overlap[canvas] < max(2, counts[canvas]/4) {
			return ""
		}
	} else if firstCell[canvas]*2 <= paintedRowCount {
		// Blank rows tell a canvas from a highlight, but an inset block has
		// those too; row starts are what separate it from a canvas.
		return ""
	}
	return canvas
}
