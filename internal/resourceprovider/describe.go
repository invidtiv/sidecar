package resourceprovider

import (
	"fmt"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/marcus/sidecar/internal/resource"
)

// ValidateDescription sanitizes and checks a describe response. It is
// all-or-nothing on purpose: a provider that declares one uncompilable pattern
// has a bug the user needs to see, and silently publishing its other matchers
// would hide it while changing what the scanner recognizes.
//
// The returned Description is safe to publish; the returned error, when
// non-nil, is a *TransportError with ReasonInvalidDescribe.
func ValidateDescription(instance string, info *Info, matchers []Matcher) (Description, error) {
	fail := func(format string, args ...any) (Description, error) {
		return Description{}, &TransportError{
			Instance: instance,
			Method:   MethodDescribe,
			Reason:   ReasonInvalidDescribe,
			Detail:   fmt.Sprintf(format, args...),
		}
	}

	var out Description
	if info != nil {
		out.Info = Info{
			Kind:    resource.SanitizeLine(info.Kind, resource.MaxProviderKindChars),
			Name:    resource.SanitizeLine(info.Name, resource.MaxProviderNameChars),
			Version: resource.SanitizeLine(info.Version, resource.MaxProviderVersionChars),
			DocsURL: resource.SanitizeURL(info.DocsURL),
		}
	}

	if len(matchers) > resource.MaxMatchersPerProvider {
		return fail("declared %d matchers, the limit is %d", len(matchers), resource.MaxMatchersPerProvider)
	}

	seen := make(map[string]bool, len(matchers))
	out.Matchers = make([]Matcher, 0, len(matchers))
	for i, m := range matchers {
		id := resource.SanitizeLine(m.ID, resource.MaxMatcherIDChars)
		if id == "" {
			return fail("matcher %d has no id", i)
		}
		if id != strings.TrimSpace(m.ID) {
			// A matcher ID is persisted in resource references. Accepting a
			// sanitized rewrite would orphan saved tabs the next time the
			// provider sends the original, so refuse instead.
			return fail("matcher %q has an id Sidecar cannot store verbatim", id)
		}
		if seen[id] {
			return fail("matcher id %q is declared more than once", id)
		}
		seen[id] = true

		if m.Pattern == "" {
			return fail("matcher %q has no pattern", id)
		}
		if utf8.RuneCountInString(m.Pattern) > resource.MaxPatternChars {
			return fail("matcher %q pattern is longer than %d characters", id, resource.MaxPatternChars)
		}
		if !utf8.ValidString(m.Pattern) {
			return fail("matcher %q pattern is not valid UTF-8", id)
		}
		if _, err := regexp.Compile(m.Pattern); err != nil {
			// RE2 only: regexp.Compile is the guarantee of linear-time
			// matching, and its rejection is the guarantee we want.
			return fail("matcher %q pattern is not a valid RE2 expression", id)
		}

		out.Matchers = append(out.Matchers, Matcher{ID: id, Pattern: m.Pattern, Priority: m.Priority})
	}
	return out, nil
}
