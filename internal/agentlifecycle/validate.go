package agentlifecycle

import (
	"errors"
	"fmt"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/agentactivity"
)

// Bounds on every string and number a report may carry.
//
// These exist because provider hook input is untrusted local data and the
// report command is the seam it enters through. A bound that lives only in the
// CLI's flag parsing is a bound that the next caller — a test helper, a future
// socket transport, a custom reporter — silently does not have. Putting them
// here makes the limit a property of the record rather than of one entry point.
//
// The values are chosen to be comfortably above anything a legitimate adapter
// needs and far below anything that could carry a prompt, a stack trace, or a
// tool result.
const (
	// MaxDetailBytes bounds [Report.Detail], the only free-form field in the
	// record. It is deliberately small: Detail exists to disambiguate a reason
	// code, not to describe anything. A provider with more to say than this has
	// something to say that this store must not be keeping.
	MaxDetailBytes = 200

	// MaxIdentityFieldBytes bounds each identity component. Real values are
	// hostnames, "%7"-shaped pane IDs, and digests.
	MaxIdentityFieldBytes = 256

	// MaxSourceBytes bounds Source and SourceVersion.
	MaxSourceBytes = 128

	// MaxIDBytes bounds Report.ID.
	MaxIDBytes = 128

	// MaxSequence bounds Report.Sequence. Sequences are assigned per run by a
	// hook that fires once per provider event, so anything approaching this is
	// a bug or an attempt to poison ordering, not a long session.
	MaxSequence = 1 << 40

	// MaxClockSkew is how far a report's ObservedAt may sit from the receiver's
	// clock in either direction. Reports are written by a hook on the same host
	// microseconds before they are read, so any real skew is small; a large one
	// means a wrong clock or a replayed record, and either way it must not be
	// allowed to sit in the future and stay "fresh" indefinitely.
	MaxClockSkew = 5 * time.Minute
)

// ErrValidation is the sentinel for every validation failure, so a caller can
// distinguish "this record is not acceptable" from an I/O error without
// matching on message text.
//
// It is deliberately not named ErrInvalidReport: that identifier is already the
// frozen [ErrorCode] a CLI reports for exactly this condition, and the two are
// different things — one is a Go error to match with errors.Is, the other is a
// wire value. The report command maps this sentinel onto that code.
var ErrValidation = errors.New("agentlifecycle: invalid report")

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: %s", ErrValidation, fmt.Sprintf(format, args...))
}

// SanitizeDetail reduces a diagnostic string to something safe to persist and
// safe to print.
//
// It removes control characters — which is what stops a report from smuggling
// ANSI escapes, carriage returns, or newlines into a log line, a terminal, or
// the JSONL file's line framing — collapses runs of whitespace, and truncates
// to [MaxDetailBytes] on a rune boundary.
//
// It is deliberately not a filter for secrets. Nothing here can tell a password
// from a word, so the rule that matters is upstream and absolute: adapters do
// not put provider content in Detail. This function bounds the damage of a
// mistake; it does not license one.
func SanitizeDetail(s string) string {
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	lastSpace := false
	for _, r := range s {
		switch {
		case r == utf8.RuneError:
			// Invalid UTF-8 decodes to RuneError; drop it rather than persist a
			// byte sequence that renders unpredictably.
			continue
		case unicode.IsSpace(r):
			// Checked before the control test on purpose: newline, tab, and
			// carriage return are control characters, but they are *separating*
			// ones. Dropping them outright would weld the words on either side
			// together and change what the diagnostic says.
			if !lastSpace && b.Len() > 0 {
				b.WriteByte(' ')
				lastSpace = true
			}
		case unicode.IsControl(r), unicode.Is(unicode.Cf, r):
			// Everything else in those categories — escape, the C1 range, bidi
			// overrides — makes a string display as something other than itself,
			// so it is removed rather than replaced.
			continue
		default:
			b.WriteRune(r)
			lastSpace = false
		}
	}
	out := strings.TrimSpace(b.String())
	if len(out) <= MaxDetailBytes {
		return out
	}
	// Truncate on a rune boundary so the result stays valid UTF-8.
	out = out[:MaxDetailBytes]
	for len(out) > 0 && !utf8.ValidString(out) {
		out = out[:len(out)-1]
	}
	return strings.TrimSpace(out)
}

// Validate checks one report against the frozen vocabularies and the bounds
// above, using now as the receiver's clock reading.
//
// It is pure and total: it reads no clock, touches no file, and returns the
// first problem it finds wrapped in [ErrValidation]. The store calls it
// before appending, and the report command calls it before it gets that far, so
// a malformed record is refused at the edge rather than persisted and skipped
// forever afterwards by every reader.
func Validate(r Report, now time.Time) error {
	if r.SchemaVersion != SchemaVersion {
		return invalid("schemaVersion %d, want %d", r.SchemaVersion, SchemaVersion)
	}
	if err := boundedField("id", r.ID, MaxIDBytes, true); err != nil {
		return err
	}

	if !validKind(r.Kind) {
		return invalid("unknown kind %q", r.Kind)
	}

	// Kind decides which of State and Outcome may be present. Allowing both, or
	// neither, would let a record mean two things at once and make the resolver
	// the place that decides which — exactly the split-brain the single-resolver
	// rule exists to prevent.
	switch r.Kind {
	case KindState:
		if !isReportState(r.State) {
			return invalid("kind state requires one of %v, got %q", ReportStates(), r.State)
		}
		if r.Outcome != "" {
			return invalid("kind state must not carry an outcome")
		}
	case KindEnd:
		if !validOutcome(r.Outcome) {
			return invalid("kind end requires one of %v, got %q", Outcomes(), r.Outcome)
		}
		if r.State != "" {
			return invalid("kind end must not carry a state")
		}
	case KindSession, KindRelease:
		if r.State != "" {
			return invalid("kind %s must not carry a state", r.Kind)
		}
		if r.Outcome != "" {
			return invalid("kind %s must not carry an outcome", r.Kind)
		}
	}

	if r.Reason != "" && !validReason(r.Reason) {
		return invalid("reason %q is not in the frozen allowlist", r.Reason)
	}

	if err := boundedField("source", r.Source, MaxSourceBytes, true); err != nil {
		return err
	}
	if err := boundedField("sourceVersion", r.SourceVersion, MaxSourceBytes, false); err != nil {
		return err
	}

	if r.Sequence > MaxSequence {
		return invalid("sequence %d exceeds %d", r.Sequence, MaxSequence)
	}

	if err := validateIdentity(r.Identity); err != nil {
		return err
	}

	if r.ObservedAt.IsZero() {
		return invalid("observedAt is zero")
	}
	if skew := r.ObservedAt.Sub(now); skew > MaxClockSkew || skew < -MaxClockSkew {
		return invalid("observedAt skew %s exceeds %s", skew, MaxClockSkew)
	}

	// Detail is checked rather than silently sanitized here: a caller that
	// hands Validate an unsanitized string has a bug, and quietly fixing it
	// would hide the one field where content leaks are plausible. Callers run
	// SanitizeDetail first; the store does exactly that.
	if r.Detail != SanitizeDetail(r.Detail) {
		return invalid("detail is not sanitized")
	}
	return nil
}

func validateIdentity(id Identity) error {
	required := []struct {
		name  string
		value string
	}{
		{"identity.host", id.Host},
		{"identity.serverIncarnation", id.ServerIncarnation},
		{"identity.paneId", id.PaneID},
		{"identity.provider", id.Provider},
		{"identity.runId", id.RunID},
		{"identity.processGeneration", id.ProcessGeneration},
	}
	for _, f := range required {
		if err := boundedField(f.name, f.value, MaxIdentityFieldBytes, true); err != nil {
			return err
		}
	}
	return boundedField("identity.sessionFingerprint", id.SessionFingerprint, MaxIdentityFieldBytes, false)
}

// boundedField enforces presence, length, valid UTF-8, and the absence of
// control characters on one field.
func boundedField(name, value string, max int, required bool) error {
	if value == "" {
		if required {
			return invalid("%s is required", name)
		}
		return nil
	}
	if len(value) > max {
		return invalid("%s is %d bytes, limit %d", name, len(value), max)
	}
	if !utf8.ValidString(value) {
		return invalid("%s is not valid UTF-8", name)
	}
	for _, r := range value {
		if unicode.IsControl(r) || unicode.Is(unicode.Cf, r) {
			return invalid("%s contains a control character", name)
		}
	}
	return nil
}

func validKind(k Kind) bool {
	for _, v := range Kinds() {
		if v == k {
			return true
		}
	}
	return false
}

func validOutcome(o Outcome) bool {
	for _, v := range Outcomes() {
		if v == o {
			return true
		}
	}
	return false
}

func validReason(c ReasonCode) bool {
	for _, v := range Reasons() {
		if v == c {
			return true
		}
	}
	return false
}

// IsReportState reports whether a lane is one a report may assert. It is the
// exported form of the resolver's internal check, for callers building reports.
func IsReportState(s agentactivity.State) bool { return isReportState(s) }
