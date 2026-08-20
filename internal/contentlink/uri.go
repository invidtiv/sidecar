package contentlink

import (
	"fmt"
	"net/url"
	"strings"
	"unicode/utf8"
)

// InternalURI carries the stable intent reference and handler-owned options.
// Query is copied and may be safely retained by a handler.
type InternalURI struct {
	Ref   Ref
	Query url.Values
}

// URIOptions is the generic parser's query allowlist. The zero value accepts
// no query parameters. Namespace handlers choose their own bounded keys.
type URIOptions struct {
	AllowedQuery map[string]struct{}
	// ValidateID applies namespace-owned identity rules after generic URI
	// decoding and bounds checks. A nil validator accepts any generic ID.
	ValidateID func(string) bool
}

func ParseInternalURI(raw string) (InternalURI, error) {
	return ParseInternalURIWith(raw, URIOptions{})
}

func ParseInternalURIWith(raw string, opts URIOptions) (InternalURI, error) {
	if raw == "" || len(raw) > MaxInternalURIBytes || !utf8.ValidString(raw) || containsControl(raw) ||
		!strings.HasPrefix(raw, "sidecar://") || strings.Contains(raw, "#") {
		return InternalURI{}, fmt.Errorf("invalid sidecar URI")
	}
	parsed, err := url.ParseRequestURI(raw)
	if err != nil || parsed.Scheme != "sidecar" || parsed.User != nil || parsed.Fragment != "" || parsed.Opaque != "" {
		return InternalURI{}, fmt.Errorf("invalid sidecar URI")
	}
	namespace := parsed.Host
	if !validNamespace(namespace) || strings.Contains(parsed.Host, ":") {
		return InternalURI{}, fmt.Errorf("invalid sidecar namespace")
	}
	escapedPath := parsed.EscapedPath()
	if !strings.HasPrefix(escapedPath, "/") || len(escapedPath) == 1 || strings.Contains(escapedPath[1:], "/") {
		return InternalURI{}, fmt.Errorf("sidecar URI needs one id")
	}
	id, err := url.PathUnescape(escapedPath[1:])
	if err != nil || id == "" || !utf8.ValidString(id) || utf8.RuneCountInString(id) > MaxInternalIDRunes ||
		containsControl(id) || strings.HasPrefix(id, "/") || strings.Contains(id, "\\") ||
		(opts.ValidateID != nil && !opts.ValidateID(id)) {
		return InternalURI{}, fmt.Errorf("invalid sidecar id")
	}
	query, err := url.ParseQuery(parsed.RawQuery)
	if err != nil || len(query) > MaxInternalQueryParameters {
		return InternalURI{}, fmt.Errorf("invalid sidecar query")
	}
	copyQuery := make(url.Values, len(query))
	for key, values := range query {
		if _, ok := opts.AllowedQuery[key]; !ok || key == "" || len(key) > MaxInternalQueryKeyBytes || containsControl(key) {
			return InternalURI{}, fmt.Errorf("unsupported sidecar query option")
		}
		if len(values) != 1 || utf8.RuneCountInString(values[0]) > MaxInternalQueryValueRunes || containsControl(values[0]) {
			return InternalURI{}, fmt.Errorf("invalid sidecar query value")
		}
		copyQuery[key] = append([]string(nil), values...)
	}
	return InternalURI{
		Ref:   Ref{Kind: KindInternal, Namespace: namespace, Value: id},
		Query: copyQuery,
	}, nil
}

func validNamespace(namespace string) bool {
	if namespace == "" || len(namespace) > MaxInternalNamespaceBytes {
		return false
	}
	for i := range len(namespace) {
		c := namespace[i]
		if i == 0 && (c < 'a' || c > 'z') || i > 0 && !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return namespace[len(namespace)-1] != '-'
}
