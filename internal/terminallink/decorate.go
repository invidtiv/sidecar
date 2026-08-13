package terminallink

import (
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/x/ansi"
)

// Decorate underlines file spans and synthesizes OSC-8 for validated URLs.
// Issue spans are ignored: hosts bind that kind later, and decorating them
// would look like a dead link. Callers must StripOSC8 first.
func Decorate(line string, spans []Span) string {
	active := make([]Span, 0, len(spans))
	for _, span := range spans {
		if span.Kind == KindURL || span.Kind == KindFile {
			active = append(active, span)
		}
	}
	sort.SliceStable(active, func(i, j int) bool {
		return active[i].StartCol > active[j].StartCol
	})
	for _, span := range active {
		open, close := "\x1b[4m", "\x1b[24m"
		if span.Kind == KindURL {
			open = "\x1b]8;;" + span.Value + "\x1b\\\x1b[4m"
			close = "\x1b[24m\x1b]8;;\x1b\\"
		}
		line = WrapVisualRange(line, span.StartCol, span.EndCol, open, close)
	}
	return line
}

// WrapVisualRange wraps the inclusive visual-column range in open/close.
func WrapVisualRange(line string, startCol, endCol int, open, close string) string {
	var out strings.Builder
	state := ansi.NormalState
	col := 0
	wrapping := false
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
		state = newState
		line = line[n:]
	}
	if wrapping {
		out.WriteString(close)
	}
	return out.String()
}

// StripOSC8 removes source-supplied OSC sequences so only synthesized
// hyperlinks remain. Fail closed if a removal could concatenate a new
// introducer.
func StripOSC8(line string) string {
	out := make([]byte, 0, len(line))
	inOSC := false
	for pos := 0; pos < len(line); {
		if inOSC {
			if terminatorLen := oscTerminatorLen(line, pos); terminatorLen > 0 {
				pos += terminatorLen
				inOSC = false
				continue
			}
			if introLen := oscIntroducerLen(line, pos); introLen > 0 {
				pos += introLen
				continue
			}
			_, size := utf8.DecodeRuneInString(line[pos:])
			pos += size
			continue
		}

		if introLen := oscIntroducerLen(line, pos); introLen > 0 {
			pos += introLen
			inOSC = true
			continue
		}
		_, size := utf8.DecodeRuneInString(line[pos:])
		segment := line[pos : pos+size]
		if segment[0] == ']' {
			for len(out) > 0 && out[len(out)-1] == '\x1b' {
				out = out[:len(out)-1]
			}
		}
		out = append(out, segment...)
		pos += size
	}
	cleaned := string(out)
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
