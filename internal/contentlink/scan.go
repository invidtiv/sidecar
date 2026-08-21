package contentlink

import (
	"regexp"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

var (
	urlPattern      = regexp.MustCompile(`https?://[^\s<>"']+`)
	internalPattern = regexp.MustCompile(`sidecar://[^\s<>"']+`)
	pathLinePattern = regexp.MustCompile(
		`(?:^|[\s(\[])((?:~/|\.{0,2}/|/)?[A-Za-z0-9_][A-Za-z0-9_./-]*\.[A-Za-z0-9_+-]+):([1-9][0-9]*)`,
	)
	bareFilePattern = regexp.MustCompile(
		`(?:^|[\s(\x5b` + "`" + `])((?:~/|\.{0,2}/|/)?[^\s()\x5b\x5d` + "`" + `<>:"']+\.[A-Za-z0-9_+-]+[.,;!?)}\x5d` + "`" + `]*)`,
	)
	issuePattern   = regexp.MustCompile(`\btd-[0-9a-fA-F]{4,}\b`)
	issueIDPattern = regexp.MustCompile(`^td-[0-9a-fA-F]{4,}$`)
	noteIDPattern  = regexp.MustCompile(`^nt-[a-z0-9]{1,64}$`)
	// Only Sidecar-owned attachable sessions are recognized. Internal terminal
	// and editor pane sessions deliberately remain ordinary text.
	sessionPattern     = regexp.MustCompile(`\bsidecar-(?:sh|ws)-[A-Za-z0-9][A-Za-z0-9_-]{0,63}`)
	sessionNamePattern = regexp.MustCompile(`^sidecar-(?:sh|ws)-[A-Za-z0-9][A-Za-z0-9_-]{0,63}$`)
	gitRevPattern      = regexp.MustCompile(`[0-9a-f]{7,64}`)
	// gitDottedPattern recognizes A..B / A...B where each side is a short hex
	// rev or a symbolic name (main, HEAD, feature-x). Symbolic sides are
	// verified by git before a span is decorated — a prose "..." costs one
	// cached, negative resolution and never renders as a link. Terminal
	// scanning (internal/terminallink) deliberately stays hex-only.
	gitDottedPattern     = regexp.MustCompile(`(?:[0-9a-f]{7,64}|[A-Za-z][0-9A-Za-z_-]*)(?:\.\.\.|\.\.)(?:[0-9a-f]{7,64}|[A-Za-z][0-9A-Za-z_-]*)`)
	gitCommitWordPattern = regexp.MustCompile(`\bcommit[ \t]+[0-9a-f]{7,64}`)
)

func IssueID(value string) bool { return issueIDPattern.MatchString(value) }

// NoteID reports whether value is a td note identity (nt-…).
func NoteID(value string) bool { return noteIDPattern.MatchString(value) }

// SessionName reports whether value is a Sidecar-owned attachable tmux
// session name. Stored notification targets use the same validation as scans.
func SessionName(value string) bool { return sessionNamePattern.MatchString(value) }

func Scan(line string, resolve Resolver, resolveDiff DiffResolver) []Span {
	return ScanWith(line, Options{Resolve: resolve, ResolveDiff: resolveDiff})
}

// ScanWith preserves the existing synchronous terminal scanner contract.
// Built-ins win in URL, path:line, file, issue, diff, resource order.
func ScanWith(line string, opts Options) []Span {
	plain := ansi.Strip(line)
	return scanPlain(plain, nil, opts, nil)
}

func scanPlain(plain string, claimed []Span, opts Options, pending *[]Pending) []Span {
	spans := append([]Span(nil), claimed...)
	appendBounded := func(found []Span) {
		for _, span := range found {
			if len(spans)-len(claimed) >= MaxAutomaticMatchesPerLine {
				return
			}
			spans = append(spans, span)
		}
	}
	appendBounded(scanURLs(plain, spans))
	appendBounded(scanPathLines(plain, spans, opts.Resolve, pending))
	if opts.Resolve != nil || pending != nil {
		appendBounded(scanBareFiles(plain, spans, opts.Resolve, pending))
	}
	// A worktree session can contain an issue id or end in hex. Claim the whole
	// session before issue and diff recognition can claim a substring.
	appendBounded(scanSessions(plain, spans))
	appendBounded(scanIssues(plain, spans))
	if opts.ResolveDiff != nil || pending != nil {
		appendBounded(scanGitSpecs(plain, spans, opts.ResolveDiff, pending))
	}
	appendBounded(scanResources(plain, spans, opts.Matchers))
	return spans
}

// scanSessions finds only whole Sidecar-owned session tokens. A session name
// embedded in a path or filename is not an attach target.
func scanSessions(plain string, existing []Span) []Span {
	var out []Span
	for _, loc := range sessionPattern.FindAllStringIndex(plain, -1) {
		start, end := loc[0], loc[1]
		if overlapsBytes(plain, append(existing, out...), start, end) || !issueTokenWhole(plain, start, end) {
			continue
		}
		out = append(out, makeSpan(KindSession, plain, start, end, plain[start:end], Extra{}))
	}
	return out
}

func scanInternalURIs(plain string, existing []Span, namespaces map[string]URIOptions) []Span {
	if len(namespaces) == 0 {
		return nil
	}
	var out []Span
	for _, loc := range internalPattern.FindAllStringIndex(plain, -1) {
		raw := strings.TrimRight(plain[loc[0]:loc[1]], ".,;!?)]}")
		end := loc[0] + len(raw)
		if raw == "" || overlapsBytes(plain, append(existing, out...), loc[0], end) {
			continue
		}
		var parsed InternalURI
		matched := false
		for namespace, opts := range namespaces {
			candidate, err := ParseInternalURIWith(raw, opts)
			if err == nil && candidate.Ref.Namespace == namespace {
				parsed, matched = candidate, true
				break
			}
		}
		if !matched {
			continue
		}
		span := SpanForRef(parsed.Ref)
		span.StartCol = colAt(plain, loc[0])
		span.EndCol = colAt(plain, end) - 1
		out = append(out, span)
	}
	return out
}

func scanURLs(plain string, existing []Span) []Span {
	var out []Span
	for _, loc := range urlPattern.FindAllStringIndex(plain, -1) {
		value, ok := SafeHTTPURL(plain[loc[0]:loc[1]])
		end := loc[0] + len(value)
		if !ok || overlapsBytes(plain, existing, loc[0], end) {
			continue
		}
		out = append(out, makeSpan(KindURL, plain, loc[0], end, value, Extra{}))
	}
	return out
}

func scanPathLines(plain string, existing []Span, resolve Resolver, pending *[]Pending) []Span {
	var out []Span
	for _, loc := range pathLinePattern.FindAllStringSubmatchIndex(plain, -1) {
		if len(loc) < 6 || loc[2] < 0 || loc[4] < 0 {
			continue
		}
		start, end := loc[2], loc[3]
		raw := plain[start:end]
		line, err := strconv.Atoi(plain[loc[4]:loc[5]])
		if err != nil || containsControl(raw) || overlapsBytes(plain, append(existing, out...), start, end) {
			continue
		}
		value, extra := raw, Extra{Line: line}
		if resolve != nil {
			resolved, got, ok := resolve(raw)
			if !ok {
				continue
			}
			value, extra = resolved, got
			extra.Line = line
			if extra.Raw == "" {
				extra.Raw = raw
			}
		} else if pending != nil {
			*pending = appendPending(*pending, Pending{Kind: KindFile, Raw: raw})
			continue
		}
		out = append(out, makeSpan(KindFile, plain, start, end, value, extra))
	}
	return out
}

func scanBareFiles(plain string, existing []Span, resolve Resolver, pending *[]Pending) []Span {
	var out []Span
	for _, loc := range bareFilePattern.FindAllStringSubmatchIndex(plain, -1) {
		if len(loc) < 4 || loc[2] < 0 {
			continue
		}
		start := loc[2]
		raw := strings.TrimRight(plain[start:loc[3]], ".,;!?)]}`")
		end := start + len(raw)
		if loc[3] < len(plain) && !isBareFileRightBoundary(plain[loc[3]]) || raw == "" ||
			containsControl(raw) || overlapsBytes(plain, append(existing, out...), start, end) {
			continue
		}
		if resolve == nil {
			*pending = appendPending(*pending, Pending{Kind: KindFile, Raw: raw})
			continue
		}
		value, extra, ok := resolve(raw)
		if !ok {
			continue
		}
		if extra.Raw == "" {
			extra.Raw = raw
		}
		out = append(out, makeSpan(KindFile, plain, start, end, value, extra))
	}
	return out
}

func scanIssues(plain string, existing []Span) []Span {
	var out []Span
	for _, loc := range issuePattern.FindAllStringIndex(plain, -1) {
		if overlapsBytes(plain, append(existing, out...), loc[0], loc[1]) || !issueTokenWhole(plain, loc[0], loc[1]) {
			continue
		}
		out = append(out, makeSpan(KindIssue, plain, loc[0], loc[1], plain[loc[0]:loc[1]], Extra{}))
	}
	return out
}

func scanGitSpecs(plain string, existing []Span, resolve DiffResolver, pending *[]Pending) []Span {
	var out []Span
	for _, re := range []*regexp.Regexp{gitDottedPattern, gitCommitWordPattern, gitRevPattern} {
		for _, loc := range re.FindAllStringIndex(plain, -1) {
			start, end := loc[0], loc[1]
			if overlapsBytes(plain, append(append(existing, out...), out...), start, end) || !issueTokenWhole(plain, start, end) {
				continue
			}
			raw := plain[start:end]
			if containsControl(raw) {
				continue
			}
			if resolve == nil {
				*pending = appendPending(*pending, Pending{Kind: KindDiff, Raw: raw})
				continue
			}
			value, extra, ok := resolve(raw)
			if !ok {
				continue
			}
			if value == "" {
				value = raw
			}
			if extra.Raw == "" {
				extra.Raw = raw
			}
			out = append(out, makeSpan(KindDiff, plain, start, end, value, extra))
		}
	}
	return out
}

func scanResources(plain string, existing []Span, matchers []ResourceMatcher) []Span {
	var out []Span
	for _, m := range matchers {
		if m.Re == nil || m.Provider == "" || m.ID == "" {
			continue
		}
		for _, loc := range m.Re.FindAllStringIndex(plain, -1) {
			if len(out) >= MaxResourceMatchesPerLine {
				return out
			}
			start, end := loc[0], loc[1]
			locator := plain[start:end]
			if start >= end || containsControl(locator) || utf8.RuneCountInString(locator) > MaxResourceLocatorChars ||
				overlapsBytes(plain, append(existing, out...), start, end) {
				continue
			}
			out = append(out, makeSpan(KindResource, plain, start, end, locator, Extra{Provider: m.Provider, Matcher: m.ID}))
		}
	}
	return out
}

func makeSpan(kind Kind, plain string, start, end int, value string, extra Extra) Span {
	return Span{Kind: kind, StartCol: colAt(plain, start), EndCol: colAt(plain, end) - 1, Value: value, Extra: extra}
}

func overlapsBytes(plain string, spans []Span, start, end int) bool {
	startCol, endCol := colAt(plain, start), colAt(plain, end)-1
	for _, span := range spans {
		if startCol <= span.EndCol && endCol >= span.StartCol {
			return true
		}
	}
	return false
}

func colAt(plain string, byteIndex int) int {
	byteIndex = max(0, min(byteIndex, len(plain)))
	return ansi.StringWidth(plain[:byteIndex])
}

func containsControl(raw string) bool { return strings.ContainsFunc(raw, unicode.IsControl) }

func issueTokenWhole(plain string, start, end int) bool {
	if start > 0 && isIssueTokenByte(plain[start-1]) {
		return false
	}
	if end >= len(plain) {
		return true
	}
	next := plain[end]
	if !isIssueTokenByte(next) {
		return true
	}
	if next != '.' {
		return false
	}
	return end+1 >= len(plain) || !isAlphanumeric(plain[end+1])
}

func isIssueTokenByte(b byte) bool {
	return b == '.' || b == '/' || b == '-' || b == '_' || b == '~' || isAlphanumeric(b)
}
func isAlphanumeric(b byte) bool {
	return b >= '0' && b <= '9' || b >= 'a' && b <= 'z' || b >= 'A' && b <= 'Z'
}
func isBareFileRightBoundary(b byte) bool {
	return b == ' ' || b == '\t' || b == '\r' || b == '\n' || b == ')' || b == ']' || b == '}' || b == '`'
}
