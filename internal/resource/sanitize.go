package resource

import (
	"net/url"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/terminallink"
)

// replacementRune is what an invalid UTF-8 sequence becomes. The protocol says
// invalid sequences are replaced, not rejected: a provider with one bad byte in
// a description should still produce a usable card.
const replacementRune = "�"

func runeLen(s string) int { return utf8.RuneCountInString(s) }

// truncateRunes cuts s to at most max runes. It does not append an ellipsis:
// the host owns display truncation, this is only the security bound.
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	if utf8.RuneCountInString(s) <= max {
		return s
	}
	count := 0
	for i := range s {
		if count == max {
			return s[:i]
		}
		count++
	}
	return s
}

// StripOSC removes every OSC sequence from a provider-supplied string, using
// the same routine that protects terminal text. It fails closed: a string that
// still contains an OSC introducer after removal becomes empty rather than
// reaching a renderer.
func StripOSC(s string) string { return terminallink.StripOSC8(s) }

// SanitizeLine prepares a single-line provider string: OSC removed, invalid
// UTF-8 replaced, every control character stripped (including newlines and
// tabs, which have no meaning in a title or a field value), surrounding space
// trimmed, and the result bounded to maxChars runes.
func SanitizeLine(s string, maxChars int) string {
	if s == "" {
		return ""
	}
	s = StripOSC(s)
	s = strings.ToValidUTF8(s, replacementRune)
	s = stripControls(s, false)
	s = strings.TrimSpace(s)
	return truncateRunes(s, maxChars)
}

// SanitizeBody prepares multi-line provider text and discards the truncation
// flag. Use SanitizeBodyText when the caller needs to tell the user the body
// was cut.
func SanitizeBody(s string, maxBytes int) string {
	out, _ := SanitizeBodyText(s, maxBytes)
	return out
}

// SanitizeBodyText prepares multi-line provider text: OSC removed, invalid
// UTF-8 replaced, CRLF normalized, every control character except newline and
// tab stripped, and the result bounded to maxBytes bytes on a rune boundary.
// It reports whether anything was cut.
func SanitizeBodyText(s string, maxBytes int) (string, bool) {
	if s == "" {
		return "", false
	}
	s = StripOSC(s)
	s = strings.ToValidUTF8(s, replacementRune)
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	s = stripControls(s, true)
	out := truncateBytes(s, maxBytes)
	return out, len(out) < len(s)
}

// stripControls removes C0 controls, DEL, and C1 controls. When keepWhitespace
// is true, newline and tab survive — they are the only two control characters
// with a legitimate meaning in body text.
func stripControls(s string, keepWhitespace bool) string {
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		switch {
		case r == '\n' || r == '\t':
			if keepWhitespace {
				b.WriteRune(r)
			} else {
				b.WriteRune(' ')
			}
		case r < 0x20, r == 0x7f, r >= 0x80 && r <= 0x9f:
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	return b.String()
}

// truncateBytes cuts s to at most maxBytes, never splitting a rune.
func truncateBytes(s string, maxBytes int) string {
	if maxBytes <= 0 {
		return ""
	}
	if len(s) <= maxBytes {
		return s
	}
	cut := maxBytes
	for cut > 0 && !utf8.RuneStart(s[cut]) {
		cut--
	}
	return s[:cut]
}

// SanitizeURL validates a provider-supplied URL. Only absolute http and https
// URLs with a host survive; everything else — file:, ssh:, data:, javascript:,
// scheme-relative, malformed, oversize, or control-bearing — becomes the empty
// string. An invalid URL is never an error: the resource simply has no source
// action.
func SanitizeURL(raw string) string {
	if raw == "" {
		return ""
	}
	// Bound before parsing so a pathological input cannot cost parse time.
	if len(raw) > MaxURLChars*4 {
		return ""
	}
	cleaned := StripOSC(raw)
	cleaned = strings.ToValidUTF8(cleaned, replacementRune)
	if cleaned != raw {
		// A URL that changed under sanitization was carrying something it
		// should not have been. Refuse it rather than opening a repaired guess.
		return ""
	}
	if strings.TrimSpace(cleaned) != cleaned || cleaned == "" {
		return ""
	}
	for _, r := range cleaned {
		if r < 0x20 || r == 0x7f || (r >= 0x80 && r <= 0x9f) || unicode.IsSpace(r) {
			return ""
		}
	}
	if runeLen(cleaned) > MaxURLChars {
		return ""
	}
	u, err := url.Parse(cleaned)
	if err != nil {
		return ""
	}
	switch strings.ToLower(u.Scheme) {
	case "http", "https":
	default:
		return ""
	}
	if u.Host == "" {
		return ""
	}
	return cleaned
}
