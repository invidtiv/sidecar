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

// urlHost extracts the lowercased hostname from an already-validated http(s)
// URL value, without its port. Claimed-host matching is by exact hostname:
// listing github.com claims github.com, not a subdomain of it.
func urlHost(value string) (string, bool) {
	parsed, err := url.Parse(value)
	if err != nil || parsed.Host == "" {
		return "", false
	}
	host := strings.ToLower(parsed.Hostname())
	return host, host != ""
}
