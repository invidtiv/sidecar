package shellstate

import (
	"errors"
	"fmt"
)

// CurrentVersion is the shells.json schema version this binary writes.
//
// Version 2 is version 1 plus the `tombstones` array. The bump exists so the
// version field stops being decoration: a writer that finds a version it does
// not understand now refuses the write instead of marshalling its own narrower
// struct over the file and silently dropping every key it could not parse.
// That is the same failure mode the whole shell-record durability plan is
// about — one writer destroying information it does not understand — one level
// up, in the format itself.
//
// A v1 file is upgraded in place on the first write: v1 is a subset of v2, so
// there is nothing to migrate beyond the number.
//
// Version 3 adds the per-shell `agent` and `restore` objects: the structured
// provider binding, the exact native session reference an official integration
// reported, and the cold-restore policy and eligibility. Like v1→v2 the change
// is purely additive, so v3 needs no migration step either — a v2 file read by
// this build simply has no agent or restore object on any record, which is
// exactly what "this shell has never run a reported agent" already means.
//
// The bump is what buys the refusal in the other direction, and here it matters
// more than it did for tombstones. A v2 binary that rewrote a v3 file would drop
// the session reference, and the visible symptom would not be an error: it would
// be a cold restore that quietly declines to resume a conversation it was
// holding a valid reference for. CheckWritableVersion is what prevents that.
const CurrentVersion = 3

// ErrUnknownVersion reports a manifest written by a newer Sidecar than this
// one. Callers that want to distinguish "the file is from the future" from an
// ordinary IO failure test for it with errors.Is.
var ErrUnknownVersion = errors.New("written by a newer Sidecar; upgrade Sidecar to write it")

// CheckWritableVersion reports whether a manifest carrying this version may be
// rewritten. Reads are always allowed — a newer file still parses into the
// fields this build knows, and refusing to read it would break `sidecar shell
// name` against a manifest a newer binary touched. Only writes are refused,
// because a write is what loses the fields.
func CheckWritableVersion(version int) error {
	if version <= CurrentVersion {
		return nil
	}
	return &Error{
		Kind: KindState,
		Msg: fmt.Sprintf("refusing to rewrite this project's shell manifest (file version %d, this build understands %d)",
			version, CurrentVersion),
		Err: ErrUnknownVersion,
	}
}

// IsUnknownVersion reports whether err is a refusal to rewrite a
// newer-than-understood manifest.
func IsUnknownVersion(err error) bool { return errors.Is(err, ErrUnknownVersion) }
