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

	hasTransition bool
	trailing      string
	hasPrintable  bool
	hasTab        bool
	described     bool

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

	// visibleWidth is the row's printable width before tab expansion, and
	// hasTab says whether expansion can change it. Together they let a drawn
	// row rebuild the cells tmux trimmed without a second ANSI walk.
	visibleWidth int
	hasTab       bool

	// described says the capture emitted bytes for this row. A row it emitted
	// nothing for is the one shape the trimmed capture cannot spell: a wholly
	// blank row of the carried colour and a wholly blank default row are both
	// the empty string.
	described bool
}

type analysisWindow struct {
	revision               uint64
	visible                []resolvedRow
	visiblePredecessorBand int
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

	revision, bands := consistentBands(buffer, [][2]int{{visibleFrom, visibleTo}})
	visibleBand := bands[0]

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

	required := make(map[int]struct{}, len(visibleBand.rows))
	for i, raw := range visibleBand.rows {
		index := visibleBand.from + i
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
	for index := range a.rows {
		if _, ok := required[index]; !ok {
			delete(a.rows, index)
		}
	}

	visible, visiblePredecessorBand := a.resolveBand(visibleBand, layout.Start)
	return analysisWindow{
		revision:               revision,
		visible:                visible,
		visiblePredecessorBand: visiblePredecessorBand,
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
	row := &analyzedRow{
		fingerprint: fingerprint,
		byteLen:     len(raw),
		raw:         strings.Clone(raw),
		hasTab:      strings.IndexByte(raw, '\t') >= 0,
		described:   len(raw) > 0,
	}
	var backgroundFree, visible strings.Builder
	backgroundFree.Grow(len(raw))
	visible.Grow(len(raw))
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
	resolved := resolvedRow{
		wire:           wire,
		backgroundFree: backgroundFree,
		trailing:       trailing,
		touched:        incoming != "" || r.hasTransition,
		visibleWidth:   r.visibleWidth,
		hasTab:         r.hasTab,
		described:      r.described,
	}
	r.resolvedIncoming = incoming
	r.resolved = resolved
	r.resolvedValid = true
	r.resolveCount++
	return resolved
}
