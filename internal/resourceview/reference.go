package resourceview

import (
	"fmt"

	"github.com/marcus/sidecar/internal/terminallink"
)

// ReferenceForLocator chooses the first live matcher from provider whose whole
// match is locator. UI requests intentionally carry no matcher ID: the running
// app is the authority for the provider snapshot and its declared precedence.
func ReferenceForLocator(matchers []terminallink.ResourceMatcher, provider, locator string) (Ref, string) {
	hasProvider := false
	for _, matcher := range matchers {
		if matcher.Provider != provider || matcher.ID == "" || matcher.Re == nil {
			continue
		}
		hasProvider = true
		match := matcher.Re.FindStringIndex(locator)
		if len(match) == 2 && match[0] == 0 && match[1] == len(locator) {
			ref := Ref{Instance: provider, Matcher: matcher.ID, Locator: locator}
			if ref.Valid() {
				return ref, ""
			}
		}
	}
	if !hasProvider {
		return Ref{}, fmt.Sprintf("provider %s has no live matchers", provider)
	}
	return Ref{}, fmt.Sprintf("provider %s has no live matcher that recognizes %s", provider, locator)
}
