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
	Decorate           bool
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
		clean, explicit := extractExplicit(line, opts.InternalNamespaces)
		if containsSourceOSCIntroducer(clean) {
			clean, explicit = "", nil
		}
		if ansi.StringWidth(clean) > MaxRenderedColumns {
			clean = ansi.Truncate(clean, MaxRenderedColumns, "")
			kept := explicit[:0]
			for _, span := range explicit {
				if span.EndCol < MaxRenderedColumns {
					kept = append(kept, span)
				}
			}
			explicit = kept
		}
		plain := ansi.Strip(clean)
		pending := []Pending(nil)
		resolve := func(kind Kind, raw string) (string, Extra, bool) {
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
		spans := scanPlain(plain, explicit, autoOpts, &pending)
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

type explicitTarget struct {
	ref Ref
	ok  bool
}

func extractExplicit(line string, namespaces map[string]URIOptions) (string, []Span) {
	var out strings.Builder
	var spans []Span
	var active explicitTarget
	startByte := 0
	closeActive := func(commit bool) {
		if !active.ok {
			return
		}
		if commit {
			text := out.String()
			startCol, endCol := ansi.StringWidth(text[:startByte]), ansi.StringWidth(text)-1
			if endCol >= startCol && endCol-startCol+1 <= MaxExplicitLabelColumns {
				span := SpanForRef(active.ref)
				span.StartCol, span.EndCol, span.Explicit = startCol, endCol, true
				spans = append(spans, span)
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
		return explicitTarget{}, true, false
	}
	uri := rest[semi+1:]
	if uri == "" {
		return explicitTarget{}, true, true
	}
	if parsedURL, err := url.ParseRequestURI(uri); err == nil && parsedURL.Scheme == "sidecar" {
		if policy, registered := namespaces[parsedURL.Host]; registered {
			if parsed, err := ParseInternalURIWith(uri, policy); err == nil {
				return explicitTarget{ref: parsed.Ref, ok: true}, true, false
			}
		}
	}
	if safe, ok := SafeHTTPURL(uri); ok && safe == uri {
		return explicitTarget{ref: Ref{Kind: KindURL, Value: safe}, ok: true}, true, false
	}
	return explicitTarget{}, true, false
}
