package resourceview

import "github.com/marcus/sidecar/internal/terminallink"

// ReferenceForLocator chooses the first live matcher from provider whose whole
// match is locator. UI requests intentionally carry no matcher ID: the running
// app is the authority for the provider snapshot and its declared precedence.
func ReferenceForLocator(matchers []terminallink.ResourceMatcher, provider, locator string) (Ref, bool) {
	for _, matcher := range matchers {
		if matcher.Provider != provider || matcher.ID == "" || matcher.Re == nil {
			continue
		}
		match := matcher.Re.FindStringIndex(locator)
		if len(match) == 2 && match[0] == 0 && match[1] == len(locator) {
			ref := Ref{Instance: provider, Matcher: matcher.ID, Locator: locator}
			return ref, ref.Valid()
		}
	}
	return Ref{}, false
}

// ProviderHasMatchers reports whether provider contributes any usable matcher
// to the live snapshot. It lets request hosts explain unavailable providers
// separately from locators that a ready provider declines to recognize.
func ProviderHasMatchers(matchers []terminallink.ResourceMatcher, provider string) bool {
	for _, matcher := range matchers {
		if matcher.Provider == provider && matcher.ID != "" && matcher.Re != nil {
			return true
		}
	}
	return false
}
