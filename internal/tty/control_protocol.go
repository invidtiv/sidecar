package tty

import (
	"fmt"
	"strconv"
	"strings"
)

type controlEventKind uint8

const (
	controlEventResponse controlEventKind = iota + 1
	controlEventOutput
	controlEventLayout
	controlEventPause
	controlEventContinue
	controlEventExit
)

type controlResponse struct {
	Lines []string
	Err   error
}

type controlEvent struct {
	Kind controlEventKind
	Pane string
	// Payload carries the %output bytes still in tmux's octal escaping, as a
	// substring of the parsed line. Decoding is deliberately deferred: the only
	// consumer treats output notifications as a per-pane dirty flag and never
	// reads the bytes, so eager decoding would allocate on every notification
	// for a value nobody reads. Call DecodedPayload (decodeControlBytes) when a
	// real byte-fed screen model starts consuming these bytes — see
	// docs/research/active/lessons-from-herdr.md.
	Payload  string
	Response controlResponse
	// Callback is the FIFO-correlated command callback for a response event.
	// The transport pops it on the reader goroutine so command ordering is
	// preserved, but it is invoked by the single ordered actor that also
	// consumes notifications, so a response can never overtake pane bytes that
	// tmux emitted before it. See docs/plans/active/
	// td-64c916-byte-fed-tmux-screen-model-slice1-evidence.md.
	Callback func(controlResponse)
}

// DecodedPayload unescapes Payload on demand.
func (e controlEvent) DecodedPayload() []byte {
	return decodeControlBytes(e.Payload)
}

type controlFrame struct {
	token string
	lines []string
}

// controlParser parses tmux control-mode framing. Notifications may be
// interleaved with command responses, so they are emitted without disturbing
// the active response frame.
type controlParser struct {
	frame *controlFrame
}

func (p *controlParser) FeedLine(line string) []controlEvent {
	line = strings.TrimSuffix(line, "\r")

	if p.frame == nil {
		if token, ok := controlFrameToken(line, "%begin "); ok {
			p.frame = &controlFrame{token: token}
			return nil
		}
		if event, ok := parseControlNotification(line); ok {
			return []controlEvent{event}
		}
		return nil
	}

	if token, ok := controlFrameToken(line, "%end "); ok && token == p.frame.token {
		response := controlResponse{Lines: append([]string(nil), p.frame.lines...)}
		p.frame = nil
		return []controlEvent{{Kind: controlEventResponse, Response: response}}
	}
	if token, ok := controlFrameToken(line, "%error "); ok && token == p.frame.token {
		lines := append([]string(nil), p.frame.lines...)
		response := controlResponse{
			Lines: lines,
			Err:   fmt.Errorf("tmux control command: %s", strings.Join(lines, "\n")),
		}
		p.frame = nil
		return []controlEvent{{Kind: controlEventResponse, Response: response}}
	}

	// tmux guarantees notifications never occur inside an output block. A pane
	// can legitimately contain text resembling a notification or marker, so
	// only a matching terminator closes the current frame.
	p.frame.lines = append(p.frame.lines, line)
	return nil
}

func controlFrameToken(line, prefix string) (string, bool) {
	if !strings.HasPrefix(line, prefix) {
		return "", false
	}
	fields := strings.Fields(strings.TrimPrefix(line, prefix))
	if len(fields) < 2 {
		return "", false
	}
	// Timestamp + command number uniquely identify a response frame. Flags may
	// differ between begin/end on older tmux versions.
	return fields[0] + " " + fields[1], true
}

func parseControlNotification(line string) (controlEvent, bool) {
	switch {
	case strings.HasPrefix(line, "%output "):
		pane, encoded, ok := splitPanePayload(strings.TrimPrefix(line, "%output "))
		if !ok {
			return controlEvent{}, false
		}
		return controlEvent{Kind: controlEventOutput, Pane: pane, Payload: encoded}, true

	case strings.HasPrefix(line, "%extended-output "):
		rest := strings.TrimPrefix(line, "%extended-output ")
		pane, rest, ok := strings.Cut(rest, " ")
		if !ok {
			return controlEvent{}, false
		}
		if _, encoded, ok := strings.Cut(rest, " : "); ok {
			return controlEvent{Kind: controlEventOutput, Pane: pane, Payload: encoded}, true
		}
		return controlEvent{}, false

	case strings.HasPrefix(line, "%layout-change "):
		return controlEvent{Kind: controlEventLayout}, true

	case strings.HasPrefix(line, "%window-pane-changed "):
		fields := strings.Fields(line)
		pane := ""
		if len(fields) >= 3 {
			pane = fields[2]
		}
		return controlEvent{Kind: controlEventLayout, Pane: pane}, true

	case strings.HasPrefix(line, "%pause "):
		return controlEvent{Kind: controlEventPause, Pane: strings.TrimSpace(strings.TrimPrefix(line, "%pause "))}, true

	case strings.HasPrefix(line, "%continue "):
		return controlEvent{Kind: controlEventContinue, Pane: strings.TrimSpace(strings.TrimPrefix(line, "%continue "))}, true

	case line == "%exit" || strings.HasPrefix(line, "%exit "):
		return controlEvent{Kind: controlEventExit}, true
	}
	return controlEvent{}, false
}

func splitPanePayload(rest string) (pane, payload string, ok bool) {
	pane, payload, ok = strings.Cut(rest, " ")
	return pane, payload, ok && pane != ""
}

// decodeControlBytes decodes tmux's control-mode octal escaping. Unknown
// backslash forms are preserved verbatim so protocol extensions cannot corrupt
// terminal bytes.
func decodeControlBytes(encoded string) []byte {
	decoded := make([]byte, 0, len(encoded))
	for i := 0; i < len(encoded); i++ {
		if encoded[i] != '\\' {
			decoded = append(decoded, encoded[i])
			continue
		}
		if i+1 < len(encoded) && encoded[i+1] == '\\' {
			decoded = append(decoded, '\\')
			i++
			continue
		}
		if i+3 < len(encoded) && isOctal(encoded[i+1]) && isOctal(encoded[i+2]) && isOctal(encoded[i+3]) {
			value, _ := strconv.ParseUint(encoded[i+1:i+4], 8, 8)
			decoded = append(decoded, byte(value))
			i += 3
			continue
		}
		decoded = append(decoded, '\\')
	}
	return decoded
}

func isOctal(b byte) bool {
	return b >= '0' && b <= '7'
}
