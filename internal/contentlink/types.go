// Package contentlink recognizes activatable references in rendered text.
//
// Recognition is presentation-neutral and side-effect free. Callers provide
// ready resolver snapshots; unresolved candidates are returned as pending
// work for a later command rather than resolved during rendering.
package contentlink

import (
	"regexp"

	"github.com/marcus/sidecar/internal/mouse"
)

// Kind classifies an activatable reference.
type Kind string

const (
	KindURL      Kind = "url"
	KindFile     Kind = "file"
	KindIssue    Kind = "issue"
	KindDiff     Kind = "diff"
	KindResource Kind = "resource"
	KindInternal Kind = "internal"
	// KindSession identifies a Sidecar-owned tmux session that a host can
	// attach. Only names Sidecar itself mints are recognized from text.
	KindSession Kind = "session"
)

// Ref is the presentation-neutral identity produced by every recognition
// path. Line is 1-based and zero when absent. Provider and Matcher identify an
// external resource matcher. Namespace is set only for internal intents.
type Ref struct {
	Kind      Kind
	Value     string
	Line      int
	Provider  string
	Matcher   string
	Namespace string
}

// KindSet bounds the reference kinds a rendered surface allows the host to
// recognize. The zero value allows none.
type KindSet map[Kind]struct{}

// NewKindSet constructs a set containing kinds.
func NewKindSet(kinds ...Kind) KindSet {
	set := make(KindSet, len(kinds))
	for _, kind := range kinds {
		set[kind] = struct{}{}
	}
	return set
}

// Allows reports whether kind may be activated from the surface.
func (s KindSet) Allows(kind Kind) bool {
	_, ok := s[kind]
	return ok
}

// Surface describes one exact, already-rendered text rectangle that a host may
// scan for content links. Rect uses plugin-local coordinates.
type Surface struct {
	ID          string
	Rect        mouse.Rect
	WorkDir     string
	ProjectRoot string
	Kinds       KindSet
	ReadOnly    bool
	// RendererOwned marks a rectangle Sidecar's own Markdown renderer drew, as
	// opposed to bytes a foreign program wrote. It licenses exactly one thing —
	// see FrameOptions.RendererOwned — and a surface showing anything a PTY, a
	// subprocess, or a file's own escape sequences can reach must leave it false.
	RendererOwned bool
}

// Extra preserves the terminal-link scanner's original public shape while
// callers migrate to Ref. Raw is the token as rendered before ready resolution.
type Extra struct {
	Line      int
	Raw       string
	Provider  string
	Matcher   string
	Namespace string
}

// Span is one non-overlapping reference in inclusive visual columns. Row is
// zero for a line scan and the zero-based rendered row for a frame scan.
type Span struct {
	Kind     Kind
	Row      int
	StartCol int
	EndCol   int
	Value    string
	Extra    Extra
	Explicit bool
}

// Ref returns the shared identity for this rendered span.
func (s Span) Ref() Ref {
	return Ref{
		Kind: s.Kind, Value: s.Value, Line: s.Extra.Line,
		Provider: s.Extra.Provider, Matcher: s.Extra.Matcher,
		Namespace: s.Extra.Namespace,
	}
}

// SpanForRef constructs a coordinate-free span. It is useful at adapters that
// still consume the compatibility Span vocabulary.
func SpanForRef(ref Ref) Span {
	extra := Extra{Line: ref.Line, Provider: ref.Provider, Matcher: ref.Matcher, Namespace: ref.Namespace}
	return Span{Kind: ref.Kind, Value: ref.Value, Extra: extra}
}

// Activatable reports whether hosts may bind and decorate the kind.
func Activatable(k Kind) bool {
	switch k {
	case KindURL, KindFile, KindIssue, KindDiff, KindResource, KindInternal, KindSession:
		return true
	default:
		return false
	}
}

// Resolver and DiffResolver are the legacy synchronous scanner hooks. New
// rendered surfaces should use ScanFrame with a ResolutionSnapshot instead.
type Resolver func(raw string) (value string, extra Extra, ok bool)
type DiffResolver func(raw string) (value string, extra Extra, ok bool)

// ResourceMatcher is one live external matcher. The whole match is the
// locator.
//
// ClaimHosts carries the matcher's instance configuration: the lowercased
// hostnames whose built-in URL spans that instance may reclassify into
// resource spans. It is empty for an instance that claims nothing, and it
// never widens what Re itself matches — a URL is yielded only when its host is
// listed here AND Re matches the entire URL string. Built-in precedence is not
// inverted anywhere else.
type ResourceMatcher struct {
	Provider   string
	ID         string
	Re         *regexp.Regexp
	ClaimHosts []string
}

type Options struct {
	Resolve     Resolver
	ResolveDiff DiffResolver
	Matchers    []ResourceMatcher
	// RendererOwned is FrameOptions.RendererOwned, threaded down so
	// yieldClaimedURLs can tell a renderer-owned frame from a terminal one. Line
	// scanning (ScanWith) never sets it.
	RendererOwned bool
}

const (
	MaxNewDiffResolves         = 16
	MaxResourceLocatorChars    = 200
	MaxResourceMatchesPerLine  = 32
	MaxAutomaticMatchesPerLine = 64
	MaxRenderedRows            = 1000
	MaxRenderedColumns         = 4096
	MaxPendingResolutions      = 128
	MaxInternalURIBytes        = 2048
	MaxInternalNamespaceBytes  = 32
	MaxInternalIDRunes         = 512
	MaxInternalQueryParameters = 16
	MaxInternalQueryKeyBytes   = 32
	MaxInternalQueryValueRunes = 256
	MaxExplicitLabelColumns    = 4096
	// MaxExplicitDestinationBytes bounds a source-supplied OSC-8 URI before
	// contentlink retains it in a Span or resynthesizes it into rendered output.
	// The destination is zero-width terminal control data, so the rendered
	// column limit cannot bound it.
	MaxExplicitDestinationBytes = 2048
)
