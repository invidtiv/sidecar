package terminallink

import (
	"regexp"
	"unicode/utf8"
)

// Bounds on what an external matcher may produce. They are stated here rather
// than imported because internal/resource depends on this package for OSC
// stripping, so the dependency cannot run the other way. A test in
// internal/resource asserts the two copies agree.
const (
	// MaxResourceLocatorChars bounds one matched locator in runes. A locator
	// reaches persisted state and becomes an argv element of a provider call.
	MaxResourceLocatorChars = 200
	// MaxResourceMatchesPerLine bounds how many resource spans one line may
	// contribute, across every matcher. It is the scanner's protection against
	// a configured pattern that matches everywhere.
	MaxResourceMatchesPerLine = 32
)

// ResourceMatcher is one compiled provider-declared pattern the scanner may
// run. The whole match is the locator: there are no replacement templates and
// no provider code runs during a scan.
//
// Hosts build these from an immutable resourceprovider snapshot, already in
// precedence order. This package deliberately knows nothing about providers,
// processes, or documents — only that a pattern maps a span to a reference.
type ResourceMatcher struct {
	// Provider is the configured provider instance ID.
	Provider string
	// ID is the provider-stable matcher ID.
	ID string
	// Re is the compiled RE2. A nil expression is skipped rather than
	// panicking: an unready provider must degrade to plain text.
	Re *regexp.Regexp
}

// scanResources runs external matchers after every built-in has claimed its
// spans, so built-in precedence is structural rather than a rule matchers are
// trusted to respect. Overlaps go through the same visual-column authority as
// every other kind, first-wins.
func scanResources(plain string, existing []Span, matchers []ResourceMatcher) []Span {
	if len(matchers) == 0 {
		return nil
	}
	var spans []Span
	matched := 0
	for _, m := range matchers {
		if m.Re == nil || m.Provider == "" || m.ID == "" {
			continue
		}
		for _, loc := range m.Re.FindAllStringIndex(plain, -1) {
			if matched >= MaxResourceMatchesPerLine {
				return spans
			}
			start, end := loc[0], loc[1]
			if start >= end {
				// A zero-width match is not a locator anything can resolve.
				continue
			}
			locator := plain[start:end]
			if containsControl(locator) || utf8.RuneCountInString(locator) > MaxResourceLocatorChars {
				continue
			}
			if overlaps(plain, existing, spans, start, end) {
				continue
			}
			spans = append(spans, Span{
				Kind:     KindResource,
				StartCol: colAt(plain, start),
				EndCol:   colAt(plain, end) - 1,
				Value:    locator,
				Extra:    Extra{Provider: m.Provider, Matcher: m.ID},
			})
			matched++
		}
	}
	return spans
}
