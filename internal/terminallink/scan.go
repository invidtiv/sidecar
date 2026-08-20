package terminallink

import "github.com/marcus/sidecar/internal/contentlink"

// Compatibility aliases keep terminal hosts source-compatible while the
// presentation-neutral contentlink package remains recognition authority.
type Kind = contentlink.Kind

const (
	KindURL      = contentlink.KindURL
	KindFile     = contentlink.KindFile
	KindIssue    = contentlink.KindIssue
	KindDiff     = contentlink.KindDiff
	KindResource = contentlink.KindResource
	KindInternal = contentlink.KindInternal
	KindSession  = contentlink.KindSession
)

type Extra = contentlink.Extra
type Span = contentlink.Span
type Resolver = contentlink.Resolver
type DiffResolver = contentlink.DiffResolver
type Options = contentlink.Options

const MaxNewDiffResolves = contentlink.MaxNewDiffResolves

func Activatable(kind Kind) bool { return contentlink.Activatable(kind) }
func IssueID(value string) bool  { return contentlink.IssueID(value) }
func SessionName(value string) bool {
	return contentlink.SessionName(value)
}
func Scan(line string, resolve Resolver, resolveDiff DiffResolver) []Span {
	return contentlink.Scan(line, resolve, resolveDiff)
}
func ScanWith(line string, opts Options) []Span { return contentlink.ScanWith(line, opts) }
