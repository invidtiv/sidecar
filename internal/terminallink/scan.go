package terminallink

import (
	"regexp"
	"strconv"
	"strings"

	"github.com/charmbracelet/x/ansi"
)

// Kind classifies one detected span.
type Kind string

const (
	KindURL   Kind = "url"
	KindFile  Kind = "file"
	KindIssue Kind = "issue"
)

// Extra holds kind-specific fields. Line is 1-based and zero when the token
// has no :line suffix. Raw is the original file token when Value is rewritten
// by a Resolver.
type Extra struct {
	Line int
	Raw  string
}

// Span is one non-overlapping match in visual columns of the stripped line.
// EndCol is inclusive.
type Span struct {
	Kind     Kind
	StartCol int
	EndCol   int
	Value    string
	Extra    Extra
}

// Resolver reports whether a file token exists. value is what the host should
// store (typically a path relative to the selected root). ok=false drops the
// span. A nil Resolver skips existence-gated file spans (bare markdown).
type Resolver func(raw string) (value string, extra Extra, ok bool)

var (
	urlPattern = regexp.MustCompile(`https?://[^\s<>"']+`)
	// path:line — same token set the project plugin shipped before the extract.
	pathLinePattern = regexp.MustCompile(
		`(?:^|[\s(\[])((?:\.{0,2}/|/)?[A-Za-z0-9_][A-Za-z0-9_./-]*\.[A-Za-z0-9_+-]+):([1-9][0-9]*)`,
	)
	bareMarkdownPattern = regexp.MustCompile(
		`(?:^|[\s(\x5b` + "`" + `])((?:\.{0,2}/|/)?[^\s()\x5b\x5d` + "`" + `<>:"']+\.(?i:md|markdown)[.,;!?)}\x5d` + "`" + `]*)`,
	)
	// Current td id shape. Title matching is out of scope until a split binds
	// this kind to a td pane — not to the issue-preview modal.
	issuePattern = regexp.MustCompile(`\btd-[0-9a-fA-F]{4,}\b`)
)

// Scan finds URL, file, and issue spans in a terminal line.
//
// line may still contain ANSI; it is stripped before matching. Overlaps are
// resolved first-kind-wins in this order: url, path:line, resolved bare
// markdown, issue.
func Scan(line string, resolve Resolver) []Span {
	plain := ansi.Strip(line)
	var spans []Span
	spans = append(spans, scanURLs(plain)...)
	spans = append(spans, scanPathLines(plain, spans)...)
	if resolve != nil {
		spans = append(spans, scanBareMarkdown(plain, spans, resolve)...)
	}
	spans = append(spans, scanIssues(plain, spans)...)
	return spans
}

func scanURLs(plain string) []Span {
	var spans []Span
	for _, loc := range urlPattern.FindAllStringIndex(plain, -1) {
		value, ok := SafeHTTPURL(plain[loc[0]:loc[1]])
		if !ok {
			continue
		}
		endByte := loc[0] + len(value)
		spans = append(spans, Span{
			Kind:     KindURL,
			StartCol: colAt(plain, loc[0]),
			EndCol:   colAt(plain, endByte) - 1,
			Value:    value,
		})
	}
	return spans
}

func scanPathLines(plain string, existing []Span) []Span {
	var spans []Span
	for _, loc := range pathLinePattern.FindAllStringSubmatchIndex(plain, -1) {
		if len(loc) < 6 || loc[2] < 0 || loc[4] < 0 {
			continue
		}
		start, end := loc[2], loc[3]
		if overlaps(plain, existing, spans, start, end) {
			continue
		}
		lineNo, err := strconv.Atoi(plain[loc[4]:loc[5]])
		if err != nil {
			continue
		}
		spans = append(spans, Span{
			Kind:     KindFile,
			StartCol: colAt(plain, start),
			EndCol:   colAt(plain, end) - 1,
			Value:    plain[start:end],
			Extra:    Extra{Line: lineNo},
		})
	}
	return spans
}

func scanBareMarkdown(plain string, existing []Span, resolve Resolver) []Span {
	var spans []Span
	for _, loc := range bareMarkdownPattern.FindAllStringSubmatchIndex(plain, -1) {
		if len(loc) < 4 || loc[2] < 0 {
			continue
		}
		start, end := loc[2], loc[3]
		value := strings.TrimRight(plain[start:end], ".,;!?)]}`")
		end = start + len(value)
		// The regexp deliberately stops at the markdown extension so it can
		// retain punctuation for trimming. Require the next byte to be an
		// actual token boundary; otherwise README.md5 and markdowned prose
		// would borrow a valid prefix and become surprising links.
		matchEnd := loc[3]
		if matchEnd < len(plain) && !isBareMarkdownRightBoundary(plain[matchEnd]) {
			continue
		}
		if value == "" || overlaps(plain, existing, spans, start, end) {
			continue
		}
		resolved, extra, ok := resolve(value)
		if !ok {
			continue
		}
		if extra.Raw == "" {
			extra.Raw = value
		}
		spans = append(spans, Span{
			Kind:     KindFile,
			StartCol: colAt(plain, start),
			EndCol:   colAt(plain, end) - 1,
			Value:    resolved,
			Extra:    extra,
		})
	}
	return spans
}

func scanIssues(plain string, existing []Span) []Span {
	var spans []Span
	for _, loc := range issuePattern.FindAllStringIndex(plain, -1) {
		start, end := loc[0], loc[1]
		if overlaps(plain, existing, spans, start, end) {
			continue
		}
		spans = append(spans, Span{
			Kind:     KindIssue,
			StartCol: colAt(plain, start),
			EndCol:   colAt(plain, end) - 1,
			Value:    plain[start:end],
		})
	}
	return spans
}

func isBareMarkdownRightBoundary(next byte) bool {
	return next == ' ' || next == '\t' || next == '\r' || next == '\n' ||
		next == ')' || next == ']' || next == '}' || next == '`'
}

func overlapsBytes(plain string, existing []Span, start, end int) bool {
	startCol := colAt(plain, start)
	endCol := colAt(plain, end) - 1
	for _, span := range existing {
		if startCol <= span.EndCol && endCol >= span.StartCol {
			return true
		}
	}
	return false
}

func overlaps(plain string, existing, pending []Span, start, end int) bool {
	return overlapsBytes(plain, existing, start, end) || overlapsBytes(plain, pending, start, end)
}

func colAt(plain string, byteIndex int) int {
	if byteIndex < 0 {
		return 0
	}
	if byteIndex > len(plain) {
		byteIndex = len(plain)
	}
	return ansi.StringWidth(plain[:byteIndex])
}
