package contentlink

import (
	"net/url"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

type FrameOptions struct {
	Ready              ResolutionSnapshot
	Matchers           []ResourceMatcher
	InternalNamespaces map[string]URIOptions
	// AllowedKinds bounds the kinds returned, decorated, and queued for
	// resolution. Nil preserves the scanner's all-kinds default; a non-nil
	// empty set allows none. Explicit OSC labels still claim their cells when
	// their destination kind is excluded, so automatic matching cannot turn a
	// disallowed explicit destination into a different action.
	AllowedKinds KindSet
	Decorate     bool
}

type FrameResult struct {
	Output  string
	Spans   []Span
	Pending []Pending
}

// ScanFrame recognizes a bounded, already-rendered ANSI frame. Explicit OSC-8
// spans are extracted first and beat automatic matches. File and diff matching
// consults only Ready; misses become bounded, deduplicated Pending work.
func ScanFrame(frame string, opts FrameOptions) FrameResult {
	lines := strings.Split(frame, "\n")
	if len(lines) > MaxRenderedRows {
		lines = lines[:MaxRenderedRows]
	}
	var result FrameResult
	output := make([]string, len(lines))
	for row, line := range lines {
		clean, claimed := extractExplicit(line, opts.InternalNamespaces)
		if containsSourceOSCIntroducer(clean) {
			clean, claimed = "", nil
		}
		if ansi.StringWidth(clean) > MaxRenderedColumns {
			clean = ansi.Truncate(clean, MaxRenderedColumns, "")
			kept := claimed[:0]
			for _, span := range claimed {
				if span.StartCol >= MaxRenderedColumns {
					continue
				}
				if span.EndCol >= MaxRenderedColumns {
					span.EndCol = MaxRenderedColumns - 1
				}
				kept = append(kept, span)
			}
			claimed = kept
		}
		plain := ansi.Strip(clean)
		pending := []Pending(nil)
		resolve := func(kind Kind, raw string) (string, Extra, bool) {
			if !frameKindAllowed(opts.AllowedKinds, kind) {
				return "", Extra{}, false
			}
			ref, found, ready := opts.Ready.Lookup(kind, raw)
			if !ready {
				pending = appendPending(pending, Pending{Kind: kind, Raw: raw})
				return "", Extra{}, false
			}
			if !found {
				return "", Extra{}, false
			}
			extra := Extra{Line: ref.Line, Provider: ref.Provider, Matcher: ref.Matcher, Namespace: ref.Namespace, Raw: raw}
			return ref.Value, extra, true
		}
		autoOpts := Options{
			Matchers: opts.Matchers,
			Resolve: func(raw string) (string, Extra, bool) {
				return resolve(KindFile, raw)
			},
			ResolveDiff: func(raw string) (string, Extra, bool) {
				return resolve(KindDiff, raw)
			},
		}
		internal := scanInternalURIs(plain, claimed, opts.InternalNamespaces)
		spans := allowedActivatableSpans(
			scanPlain(plain, append(claimed, internal...), autoOpts, &pending),
			opts.AllowedKinds,
		)
		for i := range spans {
			spans[i].Row = row
		}
		result.Spans = append(result.Spans, spans...)
		for _, candidate := range pending {
			result.Pending = appendPending(result.Pending, candidate)
		}
		if opts.Decorate {
			output[row] = Decorate(clean, spans)
		} else {
			output[row] = clean
		}
	}
	result.Output = strings.Join(output, "\n")
	return result
}

func allowedActivatableSpans(spans []Span, allowed KindSet) []Span {
	out := spans[:0]
	for _, span := range spans {
		if Activatable(span.Kind) && frameKindAllowed(allowed, span.Kind) {
			out = append(out, span)
		}
	}
	return out
}

func frameKindAllowed(allowed KindSet, kind Kind) bool {
	return allowed == nil || allowed.Allows(kind)
}

type explicitTarget struct {
	ref   Ref
	ok    bool
	claim bool
}

func extractExplicit(line string, namespaces map[string]URIOptions) (string, []Span) {
	var out strings.Builder
	var spans []Span
	var active explicitTarget
	startByte := 0
	closeActive := func(validClose bool) {
		if !active.claim {
			return
		}
		text := out.String()
		startCol, endCol := ansi.StringWidth(text[:startByte]), ansi.StringWidth(text)-1
		if endCol >= startCol {
			if validClose && active.ok && endCol-startCol+1 <= MaxExplicitLabelColumns {
				span := SpanForRef(active.ref)
				span.StartCol, span.EndCol, span.Explicit = startCol, endCol, true
				spans = append(spans, span)
			} else {
				// Invalid and unterminated explicit destinations still own their
				// visible label cells. This inert claim prevents automatic scanners
				// from turning a valid-looking label into a different action.
				spans = append(spans, Span{StartCol: startCol, EndCol: endCol})
			}
		}
		active = explicitTarget{}
	}
	for pos := 0; pos < len(line); {
		intro := oscIntroducerLen(line, pos)
		if intro == 0 {
			_, size := utf8.DecodeRuneInString(line[pos:])
			if size <= 0 {
				break
			}
			out.WriteString(line[pos : pos+size])
			pos += size
			continue
		}
		end, term := findOSCEnd(line, pos+intro)
		if end < 0 {
			break
		}
		payload := line[pos+intro : end]
		if target, isOSC8, closing := parseOSC8(payload, namespaces); isOSC8 {
			if closing {
				closeActive(true)
			} else {
				closeActive(false)
				active = target
				startByte = out.Len()
			}
		}
		pos = end + term
	}
	closeActive(false)
	return out.String(), spans
}

func findOSCEnd(line string, pos int) (int, int) {
	for pos < len(line) {
		if n := oscTerminatorLen(line, pos); n > 0 {
			return pos, n
		}
		_, size := utf8.DecodeRuneInString(line[pos:])
		if size <= 0 {
			return -1, 0
		}
		pos += size
	}
	return -1, 0
}

func parseOSC8(payload string, namespaces map[string]URIOptions) (explicitTarget, bool, bool) {
	if !strings.HasPrefix(payload, "8;") {
		return explicitTarget{}, false, false
	}
	rest := payload[2:]
	semi := strings.IndexByte(rest, ';')
	if semi < 0 || semi > 256 {
		return explicitTarget{claim: true}, true, false
	}
	uri := rest[semi+1:]
	if uri == "" {
		return explicitTarget{}, true, true
	}
	if len(uri) > MaxExplicitDestinationBytes {
		return explicitTarget{claim: true}, true, false
	}
	if parsedURL, err := url.ParseRequestURI(uri); err == nil && parsedURL.Scheme == "sidecar" {
		if policy, registered := namespaces[parsedURL.Host]; registered {
			if parsed, err := ParseInternalURIWith(uri, policy); err == nil {
				return explicitTarget{ref: parsed.Ref, ok: true, claim: true}, true, false
			}
		}
	}
	if safe, ok := SafeHTTPURL(uri); ok && safe == uri {
		return explicitTarget{ref: Ref{Kind: KindURL, Value: safe}, ok: true, claim: true}, true, false
	}
	return explicitTarget{claim: true}, true, false
}
