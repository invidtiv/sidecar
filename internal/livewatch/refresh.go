package livewatch

import (
	"encoding/json"
	"hash/fnv"
	"strconv"
	"sync/atomic"
)

// Refresher sequences re-reads of externally-owned state for one pane.
//
// It is deliberately tiny and deliberately not concurrent: a host owns one per
// pane and drives it from its own Update loop, on the goroutine that owns the
// view. It holds no timers, no descriptors and no view state, so it can be
// embedded in a pane struct that outlives any particular watcher.
//
// The zero value is ready: nothing owed, nothing running, nothing known.
type Refresher struct {
	dirty    bool
	inFlight bool

	fingerprint string
	known       bool
}

// Observe records that the underlying state may have moved.
//
// It is the only input from the watcher side, and it is intentionally cheap and
// idempotent: ten signals arriving before the first re-read starts owe exactly
// one re-read.
func (r *Refresher) Observe() { r.dirty = true }

// Pending reports whether a re-read is owed but has not started.
func (r *Refresher) Pending() bool { return r.dirty }

// InFlight reports whether a re-read is running.
func (r *Refresher) InFlight() bool { return r.inFlight }

// Begin reports whether the host should issue a re-read now, claiming the
// single in-flight slot if so.
//
// suppressed is the host's veto — an editor holding an unsaved buffer, a modal
// that owns the pane, a search the user is halfway through typing. A suppressed
// change is not dropped: it stays owed, so the refresh lands as soon as the
// veto lifts and the user never has to know a signal was deferred.
//
// The single in-flight slot is what keeps a busy repository from stacking
// re-reads. Re-reading the diff costs half a dozen git subprocesses; without
// this, a rebase would queue one set per ref that moved.
func (r *Refresher) Begin(suppressed bool) bool {
	if !r.dirty || r.inFlight || suppressed {
		return false
	}
	r.dirty = false
	r.inFlight = true
	return true
}

// Done releases the in-flight slot and reports whether a further change arrived
// while the last re-read was running, so the host can immediately Begin again.
func (r *Refresher) Done() bool {
	r.inFlight = false
	return r.dirty
}

// Changed reports whether fingerprint differs from the last one applied, and
// records it either way.
//
// This is the no-repaint gate. The signals these panes watch are coarser than
// the state they render: touching any issue moves the td store's mtime, and any
// git command at all moves the index. Without this gate a pane repaints on
// every unrelated write, which is visible as a flash and is exactly what the
// tickets asked to avoid. With it, a re-read that produced identical state
// costs a comparison and stops.
func (r *Refresher) Changed(fingerprint string) bool {
	if r.known && r.fingerprint == fingerprint {
		return false
	}
	r.fingerprint = fingerprint
	r.known = true
	return true
}

// Adopt records a fingerprint as already on screen without reporting it as a
// change. Hosts call this after the pane's initial load, so the first watcher
// signal is measured against what the user is actually looking at.
func (r *Refresher) Adopt(fingerprint string) {
	r.fingerprint = fingerprint
	r.known = true
}

// Reset returns the Refresher to its zero state.
//
// Hosts call this when the pane retargets — a different issue, a different
// file, a different diff. The next result must always be applied then, because
// "unchanged since last time" is meaningless once the subject changed, and a
// suppressed re-read owed for the previous subject must not fire against the
// new one.
func (r *Refresher) Reset() {
	r.dirty = false
	r.inFlight = false
	r.fingerprint = ""
	r.known = false
}

// Fingerprint reduces a value to a short string that changes when the value
// does.
//
// It is a change detector, not a checksum: collisions would show as a missed
// refresh, never as corruption, and 64 bits of FNV over the JSON encoding makes
// that vanishingly unlikely for the sizes involved. JSON is the encoding
// because it is stable for the plain data structs these panes carry and because
// it ignores unexported render caches, which change constantly and mean nothing
// to the user.
//
// A value that cannot be encoded yields a fingerprint that never repeats, so a
// caller that cannot be compared repaints rather than showing stale content.
func Fingerprint(v any) string {
	h := fnv.New64a()
	if err := json.NewEncoder(h).Encode(v); err != nil {
		return "!" + strconv.FormatUint(uncomparableSeq.Add(1), 36)
	}
	return strconv.FormatUint(h.Sum64(), 36)
}

// FingerprintString reduces raw text to a change-detecting fingerprint. Use it
// for content already in hand — file bodies, diff output — where re-encoding
// through JSON would only add work.
func FingerprintString(s string) string {
	h := fnv.New64a()
	_, _ = h.Write([]byte(s))
	return strconv.FormatUint(h.Sum64(), 36)
}

// uncomparableSeq makes each unencodable Fingerprint distinct from every other.
var uncomparableSeq atomic.Uint64
