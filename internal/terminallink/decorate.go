package terminallink

import "github.com/marcus/sidecar/internal/contentlink"

func Decorate(line string, spans []Span) string { return contentlink.Decorate(line, spans) }
func WrapVisualRange(line string, startCol, endCol int, open, close string) string {
	return contentlink.WrapVisualRange(line, startCol, endCol, open, close)
}
func StripOSC8(line string) string { return contentlink.StripOSC8(line) }
