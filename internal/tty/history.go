package tty

// How far back a surface can read is a property of this layer, not of the
// surface. A host names the buffer, the target, and what to do with the rows
// that come back; the order of the read is written here, once, so a pane
// reads the same distance back wherever it is drawn and in either state.
//
// The order is: one request per bound-hit, a scroll arriving while a request is
// in flight coalesced onto it rather than lost, a result carrying a superseded
// generation ignored, and a hard stop once the buffer starts at tmux's oldest
// line. Only the request state lives here. Rebasing what a prepend renumbered —
// a pinned window, a selection anchor, a search match — stays with the host that
// owns those coordinates.

const (
	// HistoryChunkLines is how much older history one bound-hit reads. It bounds
	// a single capture, not the reach: repeated hits walk back a chunk at a time
	// until tmux's history is exhausted.
	HistoryChunkLines = 600

	// HistoryBufferLines is how many lines of a pane a surface's buffer holds.
	// It is sized to tmux's retained history rather than to the seed capture,
	// because a buffer sized to the seed would trim every line the reach loaded
	// and leave the surface dead-ended at its initial window.
	HistoryBufferLines = HistoryLimit + 512

	// HistoryExhaustedNotice is what a surface says when a reach lands on tmux's
	// oldest line. Every host says it in these words: the reader is being told
	// about the pane, not about the surface they happen to be reading it in.
	HistoryExhaustedNotice = "This is the beginning of this pane's history"
)

// HistoryOutcome is what came of asking for older history, in the terms a host
// answers a reader with: a read was opened, one is already running, tmux has
// nothing older, or nothing here can say yet.
type HistoryOutcome uint8

const (
	HistoryRequested HistoryOutcome = iota
	HistoryInFlight
	HistoryEnded
	HistoryUnavailable
)

// HistoryRange is one read, in the relative coordinates capture-pane addresses
// history by, tagged with the generation that admits its result.
type HistoryRange struct {
	Start, End int
	Generation uint64
}

// HistoryReach is one surface's standing reach into a pane's older history.
type HistoryReach struct {
	// HistorySize is tmux's own count of the lines above the pane, the origin
	// the relative coordinates of a capture are measured from.
	HistorySize int

	// Loading says a read is in flight; PendingScroll is the movement waiting on
	// it, replayed once the rows land.
	Loading       bool
	PendingScroll int

	// Exhausted records that the buffer already starts at tmux's oldest line.
	Exhausted bool

	// RequestGen retires superseded reads: a result carrying any other
	// generation belongs to a request that has since been cancelled or replaced.
	RequestGen uint64

	// NoticeShown records that the reader has already been told this pane has no
	// more history, so a wheel held at the bound says it once.
	NoticeShown bool
}

// Record adopts a pane's own report of how much history it holds.
func (r *HistoryReach) Record(historySize int) {
	if historySize < 0 {
		return
	}
	r.HistorySize = historySize
	r.Exhausted = historySize == 0
}

// Request opens a read of the chunk immediately older than base, the buffer's
// first loaded line. scrollLines is what the reader is owed once those rows
// land; a scroll arriving while a read is in flight is added to it rather than
// starting a second read of the same range.
func (r *HistoryReach) Request(base int, absolute bool, scrollLines int) (HistoryRange, HistoryOutcome) {
	if scrollLines <= 0 {
		return HistoryRange{}, HistoryUnavailable
	}
	if r.Loading {
		r.PendingScroll += scrollLines
		return HistoryRange{}, HistoryInFlight
	}
	// A pane that reports no history has nothing above its grid, and a buffer
	// that starts at line 0 already holds all of it.
	if r.Exhausted || (absolute && base <= 0) {
		r.PendingScroll = 0
		return HistoryRange{}, HistoryEnded
	}
	// Nothing older can be addressed for a buffer whose lines are not tmux's
	// coordinates, or for a pane whose history size nothing has reported yet.
	if !absolute || r.HistorySize <= 0 {
		r.PendingScroll = 0
		return HistoryRange{}, HistoryUnavailable
	}
	r.PendingScroll += scrollLines
	r.Loading = true
	r.RequestGen++
	return r.rangeBefore(base, HistoryChunkLines), HistoryRequested
}

// RequestAll opens a read of everything above base, superseding any bounded read
// in flight. Searching is user-initiated and covers the whole of a pane's
// history, not only the ranges scrolling happened to visit.
func (r *HistoryReach) RequestAll(base int, absolute bool) (HistoryRange, bool) {
	if !absolute || base <= 0 || r.HistorySize <= 0 {
		return HistoryRange{}, false
	}
	r.PendingScroll = 0
	r.Loading = true
	r.RequestGen++
	return r.rangeBefore(base, HistoryLimit), true
}

// rangeBefore is the range immediately older than base, capped at chunk rows.
// capture-pane counts history back from the live grid, so an absolute line is
// addressed as its distance from the pane's history size.
func (r *HistoryReach) rangeBefore(base, chunk int) HistoryRange {
	start := max(base-chunk, 0)
	return HistoryRange{
		Start:      start - r.HistorySize,
		End:        base - 1 - r.HistorySize,
		Generation: r.RequestGen,
	}
}

// Accept admits a completed read and reports the scroll waiting on it. A result
// tagged with any other generation belongs to a request this reach superseded,
// and admitting it would move a window the reader has since placed elsewhere.
func (r *HistoryReach) Accept(generation uint64) (scrollLines int, ok bool) {
	if generation != r.RequestGen {
		return 0, false
	}
	r.Loading = false
	pending := r.PendingScroll
	r.PendingScroll = 0
	return pending, true
}

// Settle records what a completed read left behind: tmux's history size as of
// the read, and whether the buffer now starts at its oldest line.
func (r *HistoryReach) Settle(newBase, historySize int) {
	r.HistorySize = historySize
	r.Exhausted = newBase == 0
	if !r.Exhausted {
		r.NoticeShown = false
	}
}

// Remainder is what the reader is still owed after a prepend of added rows: a
// scroll larger than the chunk that arrived walks back another chunk.
func (r *HistoryReach) Remainder(scrollLines, added int) (int, bool) {
	if scrollLines > added && !r.Exhausted {
		return scrollLines - added, true
	}
	return 0, false
}

// Cancel drops a pending read and the scroll waiting on it, and retires the
// generation so a result already in flight cannot land on a window the reader
// has moved.
func (r *HistoryReach) Cancel() {
	r.PendingScroll = 0
	r.Loading = false
	r.RequestGen++
}

// NoteEnd reports whether the reader still has to be told that tmux has no more
// history for this pane. It answers true once, so a wheel held at the bound
// does not repeat itself, and again after a read proves there was more.
func (r *HistoryReach) NoteEnd() bool {
	if r.NoticeShown {
		return false
	}
	r.NoticeShown = true
	return true
}
