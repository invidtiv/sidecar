package resourceview

import (
	"regexp"
	"testing"

	"github.com/marcus/sidecar/internal/terminallink"
)

func TestReferenceForLocatorUsesFirstWholeLiveProviderMatch(t *testing.T) {
	matchers := []terminallink.ResourceMatcher{
		{Provider: "other", ID: "wrong-provider", Re: regexp.MustCompile(`CASH-[0-9]+`)},
		{Provider: "jira-work", ID: "partial", Re: regexp.MustCompile(`CASH`)},
		{Provider: "jira-work", ID: "issue-key", Re: regexp.MustCompile(`CASH-[0-9]+`)},
		{Provider: "jira-work", ID: "later", Re: regexp.MustCompile(`.*`)},
	}
	ref, ok := ReferenceForLocator(matchers, "jira-work", "CASH-1245")
	if !ok || ref.Instance != "jira-work" || ref.Matcher != "issue-key" || ref.Locator != "CASH-1245" {
		t.Fatalf("reference = %#v ok=%v", ref, ok)
	}
}

func TestReferenceForLocatorDeclinesUnclaimedLocator(t *testing.T) {
	matchers := []terminallink.ResourceMatcher{{Provider: "jira-work", ID: "issue-key", Re: regexp.MustCompile(`CASH-[0-9]+`)}}
	if _, ok := ReferenceForLocator(matchers, "jira-work", "prefix CASH-1245"); ok {
		t.Fatal("a partial match claimed the locator")
	}
	if !ProviderHasMatchers(matchers, "jira-work") || ProviderHasMatchers(matchers, "missing") {
		t.Fatal("provider readiness did not follow the usable live snapshot")
	}
}
