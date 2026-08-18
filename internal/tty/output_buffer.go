package tty

import (
	"hash/maphash"
	"regexp"
	"strings"
	"sync"

	"github.com/charmbracelet/x/ansi"
)

// Regexes for cleaning terminal output
var (
	// mouseEscapeRegex matches SGR mouse escape sequences like \x1b[<35;192;47M or \x1b[<0;50;20m
	// These can appear in captured tmux output when applications have mouse mode enabled.
	mouseEscapeRegex = regexp.MustCompile(`\x1b\[<\d+;\d+;\d+[Mm]`)

	// terminalModeRegex matches terminal mode escape sequences
	terminalModeRegex = regexp.MustCompile(`\x1b\[\?(?:1000|1002|1003|1005|1006|1015|2004)[hl]`)

	// partialMouseEscapeRegex matches SGR mouse sequences that lost their ESC prefix.
	// This happens when the ESC byte is consumed by readline/ZLE but the rest of the sequence
	// is printed as literal text in the terminal. Also handles truncated sequences missing
	// the trailing M/m (e.g., "[<65;103;31" captured mid-transmission).
	partialMouseEscapeRegex = regexp.MustCompile(`\[<\d+;\d+;\d+[Mm]?`)

	// partialMouseSeqRegex matches SGR mouse sequences that lost their ESC prefix
	// due to split-read timing in terminal input.
	PartialMouseSeqRegex = regexp.MustCompile(`^(\[<\d+;\d+;\d+[Mm])+$`)

	// mouseSequenceDetector is a lenient regex that catches any mouse-like content,
	// including truncated/split sequences. Used by ContainsMouseSequence() to filter
	// spurious key events during fast scrolling (td-e2ce50).
	mouseSequenceDetector = regexp.MustCompile(`\[<\d+[;\d]*`)
)

// PaneSnapshot is one atomically observed screen: history rows followed by the
// live pane grid, plus the split between them. The producer states the split
// rather than leaving it to be inferred from the content, because every way of
// re-deriving it downstream mixes observations taken at different instants and
// drifts (td-d29821).
//
// PaneRows is also what disambiguates the serialization. Capture-shaped output
// is row *separated*, so a final blank row and a trailing terminator look
// identical once split — and the difference is exactly one grid row, the row a
// cursor sits on at the bottom of a screen.
type PaneSnapshot struct {
	// Output is history rows followed by PaneRows live grid rows.
	Output string
	// BaseLine is the absolute line number of Output's first row. It is
	// meaningful only when Absolute is set.
	BaseLine int
	// Absolute says the snapshot carries trustworthy absolute coordinates, so
	// lazily loaded older history can be merged with it.
	Absolute bool
	// HistoryRows is how many of Output's leading rows sit above pane row 0, and
	// PaneRows how many trailing rows are the live grid. PaneRows of 0 means the
	// producer does not know the split; the buffer then reports no pane top and
	// consumers fall back to their own geometry.
	HistoryRows int
	PaneRows    int
}

// CaptureInput is one capture-pane rendering plus the geometry observed with
// it, as handed to CaptureSnapshot.
type CaptureInput struct {
	// Output is the capture text and BaseLine its first row's absolute line
	// number, meaningful only when Absolute is set.
	Output   string
	BaseLine int
	Absolute bool

	// PaneHeight is the pane's grid height at the instant of the capture. Zero
	// means no geometry was observed, and no split is published.
	PaneHeight int

	// RowsJoined says the capture was taken with tmux's -J, which collapses each
	// wrapped line into a single row. Such a capture has no row-for-row
	// correspondence with the grid, so no split can be stated for it.
	RowsJoined bool
}

// CaptureSnapshot describes a capture-pane rendering: PaneHeight grid rows
// preceded by whatever history the capture carried. It is a plain function over
// the capture and the geometry observed with it, so every capture-shaped
// producer — control snapshots, poll fallbacks, the workspace's own pollers —
// states the split the same way.
//
// The invariant it rests on is that the capture's rows and the pane's rows
// correspond one for one, which is what makes "the last PaneHeight rows are the
// grid" true. A -J capture breaks it — wrapped lines arrive collapsed, so the
// capture has fewer rows than the grid and both halves of the split would be
// wrong — and RowsJoined is how a producer says so. It publishes no split
// rather than an authoritative wrong one; consumers then fall back to their own
// geometry, which is where they were before the split was carried at all.
//
// A degenerate capture that carries fewer rows than the pane is tall has no
// usable split either; it is reported as all-pane, which puts pane row 0 at the
// top of the buffer, matching what such a capture actually shows.
func CaptureSnapshot(in CaptureInput) PaneSnapshot {
	snapshot := PaneSnapshot{Output: in.Output, BaseLine: in.BaseLine, Absolute: in.Absolute}
	if in.PaneHeight <= 0 || in.RowsJoined {
		return snapshot
	}
	rows := len(splitOutputLines(in.Output))
	snapshot.PaneRows = min(in.PaneHeight, rows)
	snapshot.HistoryRows = rows - snapshot.PaneRows
	return snapshot
}

// OutputBuffer is a thread-safe bounded buffer for terminal output.
// Uses maphash for efficient content change detection to avoid duplicate processing.
type OutputBuffer struct {
	mu          sync.Mutex
	lines       []string
	cap         int
	baseLine    int
	absolute    bool
	lastHash    uint64       // Hash of cleaned content (after mouse sequence stripping)
	lastRawHash uint64       // Hash of raw content before processing
	lastLen     int          // Length of last content (collision guard)
	lastBase    int          // Absolute base of the last live snapshot
	hashSeed    maphash.Seed // Seed for stable hashing

	// paneBase is the line holding pane row 0, in the same coordinate space as
	// baseLine, and paneKnown says a producer has supplied it. It is stored
	// alongside the content it describes and updated in the same critical
	// section, so a reader can never pair a row count from one publication with
	// a split from another.
	paneBase  int
	paneKnown bool
	revision  uint64
}

// NewOutputBuffer creates a new output buffer with the given capacity.
func NewOutputBuffer(capacity int) *OutputBuffer {
	return &OutputBuffer{
		lines:    make([]string, 0, capacity),
		cap:      capacity,
		hashSeed: maphash.MakeSeed(),
	}
}

// Update replaces buffer content if it has changed (detected via hash).
// Returns true if content was updated, false if content was unchanged.
//
// The content carries no pane split, so the buffer reports none. Producers that
// know where the live grid starts should call ApplySnapshot instead.
func (b *OutputBuffer) Update(content string) bool {
	return b.ApplySnapshot(PaneSnapshot{Output: content})
}

// UpdateSnapshot merges a live capture whose first line has the supplied
// absolute pane coordinate. Older lines loaded with PrependSnapshot are
// retained while the overlapping live tail is replaced.
func (b *OutputBuffer) UpdateSnapshot(content string, baseLine int) bool {
	return b.ApplySnapshot(PaneSnapshot{Output: content, BaseLine: baseLine, Absolute: true})
}

// ApplySnapshot replaces the buffer's live tail with one observed snapshot,
// recording its pane split in the same critical section as its content.
func (b *OutputBuffer) ApplySnapshot(s PaneSnapshot) bool {
	if !s.Absolute {
		return b.applyRelative(s)
	}
	return b.applyAbsolute(s)
}

func (b *OutputBuffer) applyRelative(s PaneSnapshot) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	// Check hash BEFORE expensive regex processing
	// Compute hash of raw content first
	rawHash := maphash.String(b.hashSeed, s.Output)
	rawLen := len(s.Output)
	if !b.absolute && rawHash == b.lastRawHash && rawLen == b.lastLen {
		return false // Content unchanged - skip ALL processing
	}

	content := cleanOutput(s.Output)

	// Store cleaned content hash for future comparisons
	cleanHash := maphash.String(b.hashSeed, content)
	b.lastHash = cleanHash
	b.lastRawHash = rawHash
	b.lastLen = rawLen
	b.baseLine = 0
	b.absolute = false
	b.lastBase = 0
	b.lines = splitSnapshotRows(content, s)
	b.setPaneSplitLocked(0, s)
	b.trimLocked()
	b.revision++

	return true
}

func (b *OutputBuffer) applyAbsolute(s PaneSnapshot) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	content, baseLine := s.Output, s.BaseLine
	rawHash := maphash.String(b.hashSeed, content)
	rawLen := len(content)
	if b.absolute && rawHash == b.lastRawHash && rawLen == b.lastLen && baseLine == b.lastBase {
		return false
	}

	cleaned := cleanOutput(content)
	incoming := splitSnapshotRows(cleaned, s)
	if baseLine < 0 {
		baseLine = 0
	}

	if b.absolute && baseLine >= b.baseLine && baseLine <= b.baseLine+len(b.lines) {
		prefixLen := baseLine - b.baseLine
		merged := make([]string, 0, prefixLen+len(incoming))
		merged = append(merged, b.lines[:prefixLen]...)
		merged = append(merged, incoming...)
		b.lines = merged
	} else {
		b.lines = incoming
		b.baseLine = baseLine
	}
	b.absolute = true
	b.setPaneSplitLocked(baseLine, s)
	b.trimLocked()

	b.lastHash = maphash.String(b.hashSeed, cleaned)
	b.lastRawHash = rawHash
	b.lastLen = rawLen
	b.lastBase = baseLine
	b.revision++
	return true
}

// setPaneSplitLocked records where pane row 0 sits in the buffer's own
// coordinates. base is the coordinate of the snapshot's first row: its absolute
// base for an absolute buffer, and 0 for a relative one.
func (b *OutputBuffer) setPaneSplitLocked(base int, s PaneSnapshot) {
	if s.PaneRows <= 0 {
		b.paneKnown = false
		return
	}
	b.paneBase = base + s.HistoryRows
	b.paneKnown = true
}

// splitSnapshotRows splits capture-shaped output into rows. Without a stated row
// count every trailing newline is treated as a terminator, which is what tmux's
// capture-pane emits. With one, only the newlines past that count are, so a
// blank final grid row survives instead of being eaten by the terminator rule —
// it is the row a cursor sits on at the bottom of a screen, and losing it left
// the buffer one row shorter than the pane (td-d29821).
//
// A count larger than the content is not padded out: a snapshot can legitimately
// carry fewer rows than the pane is tall, and inventing blank rows would put
// content at coordinates tmux never used.
func splitSnapshotRows(content string, s PaneSnapshot) []string {
	want := s.HistoryRows + s.PaneRows
	if s.PaneRows <= 0 || want <= 0 {
		return splitOutputLines(content)
	}
	lines := strings.Split(content, "\n")
	for len(lines) > want && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines
}

// PrependSnapshot merges an older bounded capture into an absolute buffer.
// The ranges must overlap or touch; a gap is rejected because inventing blank
// terminal rows would make selection and search coordinates incorrect.
func (b *OutputBuffer) PrependSnapshot(content string, baseLine int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()

	if baseLine < 0 {
		baseLine = 0
	}
	incoming := splitOutputLines(cleanOutput(content))
	if len(incoming) == 0 {
		return false
	}
	if !b.absolute {
		b.lines = incoming
		b.baseLine = baseLine
		b.absolute = true
		// The older capture replaced the buffer wholesale, so whatever split the
		// previous relative content had no longer describes these rows.
		b.paneKnown = false
		b.trimLocked()
		b.revision++
		return true
	}

	currentEnd := b.baseLine + len(b.lines)
	incomingEnd := baseLine + len(incoming)
	if incomingEnd < b.baseLine || baseLine > currentEnd {
		return false
	}

	combinedBase := min(baseLine, b.baseLine)
	combinedEnd := max(incomingEnd, currentEnd)
	combined := make([]string, combinedEnd-combinedBase)
	copy(combined[baseLine-combinedBase:], incoming)
	// A range capture may finish after a newer live snapshot has arrived.
	// Preserve the current buffer on overlap so delayed history can only add
	// older rows, never roll the live tail backward.
	copy(combined[b.baseLine-combinedBase:], b.lines)

	changed := combinedBase != b.baseLine || len(combined) != len(b.lines)
	if !changed {
		for i := range combined {
			if combined[i] != b.lines[i] {
				changed = true
				break
			}
		}
	}
	if !changed {
		return false
	}

	b.lines = combined
	b.baseLine = combinedBase
	b.trimLocked()
	b.revision++
	return true
}

// WouldChange reports whether content differs from the last raw update without
// mutating the buffer. Async capture commands use this to do status work off
// the UI goroutine while deferring the actual update until ownership is checked.
func (b *OutputBuffer) WouldChange(content string) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	rawHash := maphash.String(b.hashSeed, content)
	return rawHash != b.lastRawHash || len(content) != b.lastLen
}

// WouldChangeSnapshot reports whether an absolute live capture differs from
// the last one without mutating the buffer.
func (b *OutputBuffer) WouldChangeSnapshot(content string, baseLine int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	rawHash := maphash.String(b.hashSeed, content)
	return !b.absolute || rawHash != b.lastRawHash || len(content) != b.lastLen || baseLine != b.lastBase
}

// Write replaces content in the buffer (for backward compatibility).
// Prefer Update() for change detection.
func (b *OutputBuffer) Write(content string) {
	b.mu.Lock()
	defer b.mu.Unlock()

	content = cleanOutput(content)

	// Replace instead of append to avoid duplication
	// Trim trailing newline before split (same as Update method)
	b.lines = splitOutputLines(content)
	b.baseLine = 0
	b.absolute = false
	b.lastBase = 0
	b.paneKnown = false

	// Trim to capacity (keep most recent lines)
	b.trimLocked()
	b.revision++
}

// Lines returns a copy of all lines in the buffer.
func (b *OutputBuffer) Lines() []string {
	b.mu.Lock()
	defer b.mu.Unlock()
	result := make([]string, len(b.lines))
	copy(result, b.lines)
	return result
}

// LinesRange returns a copy of lines in the specified range [start, end).
// This is more efficient than Lines() when only a portion is needed.
//
// It answers for a nil buffer, as do the other three methods of
// [textselect.Buffer]: a host with no terminal open hands its buffer to the
// selection engine anyway, and a nil *OutputBuffer inside that interface is not
// a nil interface for the engine to test.
func (b *OutputBuffer) LinesRange(start, end int) []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if start < 0 {
		start = 0
	}
	if end > len(b.lines) {
		end = len(b.lines)
	}
	if start >= end {
		return nil
	}
	result := make([]string, end-start)
	copy(result, b.lines[start:end])
	return result
}

// LinesAbsoluteRange returns a copy of absolute lines in [start, end).
func (b *OutputBuffer) LinesAbsoluteRange(start, end int) []string {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.absolute {
		return nil
	}
	start -= b.baseLine
	end -= b.baseLine
	if start < 0 {
		start = 0
	}
	if end > len(b.lines) {
		end = len(b.lines)
	}
	if start >= end {
		return nil
	}
	result := make([]string, end-start)
	copy(result, b.lines[start:end])
	return result
}

// AbsoluteRange reports the half-open absolute line range represented by the
// buffer. ok is false until the buffer has received an absolute snapshot.
func (b *OutputBuffer) AbsoluteRange() (start, end int, ok bool) {
	if b == nil {
		return 0, 0, false
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.absolute {
		return 0, 0, false
	}
	return b.baseLine, b.baseLine + len(b.lines), true
}

// PaneWindow reports the buffer's line count together with the index of the
// line holding pane row 0, both read under one lock. ok is false until a
// producer has stated the split, which is what a consumer needs in order to
// fall back to its own geometry rather than trust a stale or invented value.
func (b *OutputBuffer) PaneWindow() (lineCount, paneTop int, ok bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if !b.paneKnown {
		return len(b.lines), 0, false
	}
	return len(b.lines), min(max(b.paneBase-b.baseLine, 0), len(b.lines)), true
}

// LineCount returns the number of lines without copying.
func (b *OutputBuffer) LineCount() int {
	if b == nil {
		return 0
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

// Revision advances only when accepted content changes. Consumers may use it
// to cache work derived from a captured terminal without tying invalidation to
// animation or other render-only state.
func (b *OutputBuffer) Revision() uint64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.revision
}

// LastNonEmptyLine returns the index of the last line containing printable
// content, or -1 when every line is empty. It scans under the buffer lock and
// avoids copying the full scrollback into the render path.
func (b *OutputBuffer) LastNonEmptyLine() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	for i := len(b.lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(ansi.Strip(b.lines[i])) != "" {
			return i
		}
	}
	return -1
}

// String returns the buffer contents as a single string.
func (b *OutputBuffer) String() string {
	return strings.Join(b.Lines(), "\n")
}

// Clear removes all lines from the buffer.
func (b *OutputBuffer) Clear() {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.lines = b.lines[:0]
	b.baseLine = 0
	b.absolute = false
	b.lastHash = 0
	b.lastRawHash = 0
	b.lastLen = 0
	b.lastBase = 0
	b.paneBase = 0
	b.paneKnown = false
	b.revision++
}

// Len returns the number of lines in the buffer.
func (b *OutputBuffer) Len() int {
	b.mu.Lock()
	defer b.mu.Unlock()
	return len(b.lines)
}

func (b *OutputBuffer) trimLocked() {
	if len(b.lines) <= b.cap {
		return
	}
	drop := len(b.lines) - b.cap
	b.lines = b.lines[drop:]
	if b.absolute {
		b.baseLine += drop
		return
	}
	// Relative buffers have no base to move, so the pane split — an index into
	// the lines that were just dropped from the front — moves instead.
	b.paneBase = max(b.paneBase-drop, 0)
}

func cleanOutput(content string) string {
	if strings.Contains(content, "\x1b[<") {
		content = mouseEscapeRegex.ReplaceAllString(content, "")
	}
	if strings.Contains(content, "\x1b[?") {
		content = terminalModeRegex.ReplaceAllString(content, "")
	}
	if strings.Contains(content, "[<") {
		content = partialMouseEscapeRegex.ReplaceAllString(content, "")
	}
	return content
}

func splitOutputLines(content string) []string {
	return strings.Split(strings.TrimSuffix(content, "\n"), "\n")
}

// ContainsMouseSequence checks if input looks like it contains SGR mouse data (td-e2ce50).
// More lenient than PartialMouseSeqRegex - catches truncated/split sequences.
// Used to filter spurious key events during fast scrolling.
func ContainsMouseSequence(s string) bool {
	return strings.Contains(s, "[<") && mouseSequenceDetector.MatchString(s)
}

// LooksLikeMouseFragment checks if input could be a fragment of an SGR mouse sequence (td-e2ce50).
// This is even more lenient than ContainsMouseSequence - catches very short fragments
// like "[<" or "M[<" that occur when terminal splits mouse events across reads.
// Used to suppress snap-back and key forwarding during fast scrolling.
func LooksLikeMouseFragment(s string) bool {
	if len(s) == 0 {
		return false
	}

	// Very short strings (1-4 chars): check for mouse sequence markers
	if len(s) <= 4 {
		// Repeated [ (2-3 chars) is likely CSI start from split sequence.
		// Single "[" is not filtered here — callers use time-gating after ESC instead.
		if len(s) >= 2 && len(s) <= 3 && strings.Count(s, "[") == len(s) {
			return true
		}
		return strings.Contains(s, "[<") || // Start of sequence
			strings.Contains(s, "[") && containsDigit(s) || // [ with digit (partial CSI)
			strings.Contains(s, ";") && containsDigit(s) || // Mid-sequence
			(strings.HasSuffix(s, "M") || strings.HasSuffix(s, "m")) && containsDigit(s) // End of sequence
	}

	// Repeated [ characters (split CSI sequences arriving together)
	if isRepeatedBrackets(s) {
		return true
	}

	// Check for mouse sequence markers anywhere in the string
	if strings.Contains(s, "[<") {
		return true // Any string containing [< is likely mouse garbage
	}

	// Check for [ followed by digit (partial CSI parameter)
	if strings.Contains(s, "[") && containsDigit(s) && !strings.ContainsAny(s, " \t\n") {
		return true
	}

	// Check for concatenated sequences like "M[<" or sequences ending with M/m
	if (strings.Contains(s, "M[") || strings.Contains(s, "m[")) && containsDigit(s) {
		return true
	}

	// Check for semicolon-heavy strings with digits (mouse coordinate data)
	// Pattern: multiple semicolons with digits suggests mouse sequence garbage
	semicolonCount := strings.Count(s, ";")
	if semicolonCount >= 2 && containsDigit(s) && !strings.ContainsAny(s, " \t\n") {
		return true
	}

	// Longer strings: use full check
	return ContainsMouseSequence(s)
}

// isRepeatedBrackets returns true if s is mostly repeated [ characters.
func isRepeatedBrackets(s string) bool {
	if len(s) < 2 {
		return false
	}
	bracketCount := strings.Count(s, "[")
	// If more than 60% brackets, it's likely split CSI garbage
	return bracketCount > 0 && bracketCount*100/len(s) > 60
}

// containsDigit returns true if s contains at least one ASCII digit.
func containsDigit(s string) bool {
	for _, r := range s {
		if r >= '0' && r <= '9' {
			return true
		}
	}
	return false
}
