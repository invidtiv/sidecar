package contentlink

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Decorate underlines local references and synthesizes OSC-8 for safe HTTP(S)
// destinations: built-in URL spans, and resource spans whose locator is itself
// a browser URL — a claimed GitHub URL must keep its emulator hyperlink so
// cmd-click remains the escape hatch even after Sidecar reclassifies the span.
// A resource span claimed by its rendered label carries the destination it was
// claimed away from in Extra.Destination and keeps the hyperlink through that,
// for the same reason. Locators that are keys or refs with no destination stay
// underline-only.
//
// Source OSC must first be removed by StripOSC8 or ScanFrame.
func Decorate(line string, spans []Span) string {
	active := make([]Span, 0, len(spans))
	for _, span := range spans {
		if Activatable(span.Kind) {
			active = append(active, span)
		}
	}
	sort.SliceStable(active, func(i, j int) bool { return active[i].StartCol > active[j].StartCol })
	for _, span := range active {
		open, close := "\x1b[4m", "\x1b[24m"
		if span.Kind == KindURL || span.Kind == KindResource {
			if span.Explicit && len(span.Value) > MaxExplicitDestinationBytes {
				continue
			}
			if safe, ok := hyperlinkFor(span); ok {
				open = "\x1b]8;;" + safe + "\x1b\\\x1b[4m"
				close = "\x1b[24m\x1b]8;;\x1b\\"
			} else if span.Kind == KindURL {
				continue
			}
		}
		line = WrapVisualRange(line, span.StartCol, span.EndCol, open, close)
	}
	return line
}

// hyperlinkFor is the browser destination a span should carry, preferring its
// own locator and falling back to the destination a label claim took it from.
func hyperlinkFor(span Span) (string, bool) {
	if safe, ok := SafeHTTPURL(span.Value); ok {
		return safe, true
	}
	if len(span.Extra.Destination) > MaxExplicitDestinationBytes {
		return "", false
	}
	return SafeHTTPURL(span.Extra.Destination)
}

func WrapVisualRange(line string, startCol, endCol int, open, close string) string {
	var out strings.Builder
	state, col, wrapping := ansi.NormalState, 0, false
	for len(line) > 0 {
		seq, width, n, newState := ansi.GraphemeWidth.DecodeSequenceInString(line, state, nil)
		if n <= 0 {
			out.WriteString(line)
			break
		}
		inRange := width > 0 && col >= startCol && col <= endCol
		if inRange && !wrapping {
			out.WriteString(open)
			wrapping = true
		} else if !inRange && wrapping && width > 0 {
			out.WriteString(close)
			wrapping = false
		}
		out.WriteString(seq)
		col += width
		state, line = newState, line[n:]
	}
	if wrapping {
		out.WriteString(close)
	}
	return out.String()
}

// StripOSC8 strips every source OSC sequence. It fails closed if removal could
// leave a fresh introducer behind.
func StripOSC8(line string) string {
	cleaned, _ := extractExplicit(line, nil)
	if containsSourceOSCIntroducer(cleaned) {
		return ""
	}
	return cleaned
}

func oscIntroducerLen(value string, pos int) int {
	switch {
	case pos+1 < len(value) && value[pos] == '\x1b' && value[pos+1] == ']':
		return 2
	case value[pos] == '\x9d':
		return 1
	case pos+1 < len(value) && value[pos] == '\xc2' && value[pos+1] == '\x9d':
		return 2
	default:
		return 0
	}
}
func oscTerminatorLen(value string, pos int) int {
	switch {
	case value[pos] == '\x07' || value[pos] == '\x9c':
		return 1
	case pos+1 < len(value) && value[pos] == '\x1b' && value[pos+1] == '\\':
		return 2
	case pos+1 < len(value) && value[pos] == '\xc2' && value[pos+1] == '\x9c':
		return 2
	default:
		return 0
	}
}
func containsSourceOSCIntroducer(value string) bool {
	for pos := 0; pos < len(value); {
		if oscIntroducerLen(value, pos) > 0 {
			return true
		}
		_, size := utf8.DecodeRuneInString(value[pos:])
		pos += size
	}
	return false
}
