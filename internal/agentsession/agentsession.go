// Package agentsession owns the exact binding between a Sidecar-managed shell
// and the native agent conversation running inside it.
//
// Everything here works on structured values: a reference is a kind plus a
// value, and a resume is an argument vector. Nothing in this package builds,
// stores, or replays a shell string, because a session identifier that reaches
// a shell is an injection waiting for a value with a quote in it.
//
// The package is deliberately free of tmux, CLI, TUI, and provider-config
// dependencies so the rules can be tested as values and reused by the restore
// coordinator without a terminal.
package agentsession

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

// RefKind is how a provider names one of its own conversations.
type RefKind string

const (
	// RefID is an opaque provider-assigned identifier, such as a UUID.
	RefID RefKind = "id"
	// RefPath is an absolute path to the provider's own transcript file.
	RefPath RefKind = "path"
)

// RefKinds lists the kinds in the order the CLI documents them.
func RefKinds() []RefKind { return []RefKind{RefID, RefPath} }

func (k RefKind) valid() bool {
	for _, known := range RefKinds() {
		if k == known {
			return true
		}
	}
	return false
}

// ParseRefKind accepts a kind name from a caller.
func ParseRefKind(value string) (RefKind, error) {
	kind := RefKind(strings.ToLower(strings.TrimSpace(value)))
	if !kind.valid() {
		return "", fmt.Errorf("unknown session reference kind %q; use one of: id, path", value)
	}
	return kind, nil
}

// Bounds on a reported reference.
//
// These are refusal thresholds, not buffer sizes. A provider that legitimately
// needs more than this has changed its contract, and the honest answer is to
// requalify the adapter rather than to widen the bound and hope.
const (
	// MaxIDBytes bounds an opaque identifier. A UUID is 36 bytes; the headroom
	// covers prefixed and compound identifiers without admitting a payload.
	MaxIDBytes = 256
	// MaxPathBytes bounds a transcript path, comfortably above PATH_MAX on the
	// platforms Sidecar targets.
	MaxPathBytes = 1024
	// MaxSourceBytes bounds an integration source identifier.
	MaxSourceBytes = 128
)

// Errors returned by validation. They are values so callers can map them onto
// their own refusal codes without matching on message text.
var (
	// ErrInvalidRef means the reference is not a well-formed reference of its
	// kind, independent of any provider.
	ErrInvalidRef = errors.New("invalid session reference")
	// ErrUntrustedSource means the reporting source is not an official Sidecar
	// integration and so may not set an auto-resumable reference.
	ErrUntrustedSource = errors.New("untrusted session reference source")
	// ErrOutsideStoreRoot means a path reference points outside every store root
	// the provider is approved to keep conversations in.
	ErrOutsideStoreRoot = errors.New("session path is outside the provider's approved store roots")
	// ErrStaleGeneration means the report came from a provider process that is
	// no longer the one running in the pane.
	ErrStaleGeneration = errors.New("session report is from a stale provider generation")
	// ErrUnsupportedKind means the provider does not name sessions that way.
	ErrUnsupportedKind = errors.New("provider does not support that session reference kind")
	// ErrKindMismatch means the provider a report names is not the provider
	// running in the pane it would bind.
	//
	// This is a different failure from a stale generation, and the distinction
	// matters. A stale generation is the right provider talking too late; a kind
	// mismatch is the wrong provider talking on time. The second is reachable
	// because a hook entry is a file on disk, not a channel: grok reads
	// ~/.claude/settings.json deliberately, for Claude Code compatibility, so an
	// installed Claude session-identity hook fires inside grok sessions too and
	// arrives with a --kind flag that describes the file it was installed in
	// rather than the process that ran it. Binding on that flag alone recorded a
	// grok conversation as kind=claude, which a cold restore would then offer to
	// resume with `claude --resume <grok-id>` — wrong rather than refused.
	//
	// So the flag is treated as a claim to be checked, never as evidence.
	ErrKindMismatch = errors.New("the reported provider is not the provider running in this pane")
)

// Ref is an exact native session reference as reported by an integration.
//
// A Ref is only ever produced by validation, so an existing Ref value has
// already passed its bounds, character, and shape rules.
type Ref struct {
	// Kind is how Value names the conversation.
	Kind RefKind `json:"kind"`
	// Value is the identifier or absolute path itself.
	Value string `json:"value"`
	// Source is the integration that reported it, e.g. "sidecar.codex.hooks".
	Source string `json:"source"`
	// Reported is true when Source is an official Sidecar integration. Only a
	// reported reference is ever eligible for automatic resume; a same-cwd
	// discovery may propose a candidate but never sets this.
	Reported bool `json:"reported"`
	// Generation pins the provider process generation that reported the value,
	// in the same "pid=N,start=..." form the lifecycle store uses.
	Generation string `json:"generation,omitempty"`
	// ReportedAt is when the report was accepted.
	ReportedAt time.Time `json:"reportedAt,omitzero"`
}

// Empty reports whether the reference names nothing.
func (r Ref) Empty() bool { return r.Kind == "" || r.Value == "" }

// controlFree reports whether s contains no control characters.
//
// The check covers the C0 range, DEL, and the C1 range rather than only ASCII
// control bytes: a reference is written into JSON, rendered in a terminal, and
// compared for equality, and a C1 byte survives all three while changing what a
// human sees.
func controlFree(s string) bool {
	for _, r := range s {
		if r == utf8.RuneError {
			return false
		}
		if unicode.IsControl(r) {
			return false
		}
	}
	return true
}

// idAllowed reports whether r may appear in an opaque identifier.
//
// The set is deliberately narrow and does not include a path separator: an
// identifier that can contain a slash is an identifier that can be mistaken for
// a path by the next piece of code to handle it.
func idAllowed(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	case r == '-', r == '_', r == '.', r == ':':
		return true
	default:
		return false
	}
}

// ValidateID checks an opaque identifier's shape and bounds.
func ValidateID(value string) error {
	if value == "" {
		return fmt.Errorf("%w: the identifier is empty", ErrInvalidRef)
	}
	if len(value) > MaxIDBytes {
		return fmt.Errorf("%w: the identifier is %d bytes, over the %d byte cap", ErrInvalidRef, len(value), MaxIDBytes)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: the identifier is not valid UTF-8", ErrInvalidRef)
	}
	if !controlFree(value) {
		return fmt.Errorf("%w: the identifier contains a control character", ErrInvalidRef)
	}
	for _, r := range value {
		if !idAllowed(r) {
			return fmt.Errorf("%w: the identifier contains %q, which is not allowed in an identifier", ErrInvalidRef, r)
		}
	}
	// A leading dot would make the identifier usable as a relative path
	// component that escapes, and no provider names a session that way.
	if strings.HasPrefix(value, ".") {
		return fmt.Errorf("%w: the identifier starts with a dot", ErrInvalidRef)
	}
	// A leading dash would make the identifier a FLAG to the provider CLI the
	// moment a resume is built from it. Nothing quotes its way out of that:
	// the value is a correct, separate argv entry and the provider still reads
	// it as an option. No real provider names a session this way, so refusing
	// costs nothing and closes an argument-injection path into a command
	// Sidecar runs unattended at restore time.
	if strings.HasPrefix(value, "-") {
		return fmt.Errorf("%w: the identifier starts with a dash, which a provider would read as a flag", ErrInvalidRef)
	}
	return nil
}

// ValidatePath checks a transcript path's shape and bounds.
//
// It does not touch the filesystem: existence is a restore-time question, and a
// validator that stats is a validator whose answer changes under it.
func ValidatePath(value string) error {
	if value == "" {
		return fmt.Errorf("%w: the path is empty", ErrInvalidRef)
	}
	if len(value) > MaxPathBytes {
		return fmt.Errorf("%w: the path is %d bytes, over the %d byte cap", ErrInvalidRef, len(value), MaxPathBytes)
	}
	if !utf8.ValidString(value) {
		return fmt.Errorf("%w: the path is not valid UTF-8", ErrInvalidRef)
	}
	if !controlFree(value) {
		return fmt.Errorf("%w: the path contains a control character", ErrInvalidRef)
	}
	if !filepath.IsAbs(value) {
		return fmt.Errorf("%w: the path is not absolute", ErrInvalidRef)
	}
	if filepath.Clean(value) != value {
		return fmt.Errorf("%w: the path is not normalized (%q would normalize to %q)", ErrInvalidRef, value, filepath.Clean(value))
	}
	return nil
}

// ValidateSource checks an integration source identifier's shape.
func ValidateSource(source string) error {
	if source == "" {
		return fmt.Errorf("%w: the source is empty", ErrInvalidRef)
	}
	if len(source) > MaxSourceBytes {
		return fmt.Errorf("%w: the source is %d bytes, over the %d byte cap", ErrInvalidRef, len(source), MaxSourceBytes)
	}
	if !utf8.ValidString(source) || !controlFree(source) {
		return fmt.Errorf("%w: the source is not printable UTF-8", ErrInvalidRef)
	}
	for _, r := range source {
		if !idAllowed(r) {
			return fmt.Errorf("%w: the source contains %q, which is not allowed", ErrInvalidRef, r)
		}
	}
	return nil
}

// ValidateValue checks a value against its kind.
func ValidateValue(kind RefKind, value string) error {
	switch kind {
	case RefID:
		return ValidateID(value)
	case RefPath:
		return ValidatePath(value)
	default:
		return fmt.Errorf("%w: unknown kind %q", ErrInvalidRef, kind)
	}
}
