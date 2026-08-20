package contentlink

import (
	"net/url"
	"strings"
	"unicode"
)

// SafeHTTPURL trims prose punctuation and accepts only http(s), with a host
// and no controls.
func SafeHTTPURL(raw string) (string, bool) {
	raw = strings.TrimRight(raw, ".,;!?) ]}")
	if strings.ContainsFunc(raw, unicode.IsControl) {
		return "", false
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Host == "" ||
		(parsed.Scheme != "http" && parsed.Scheme != "https") {
		return "", false
	}
	return raw, true
}
