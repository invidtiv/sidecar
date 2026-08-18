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
	ref, refusal := ReferenceForLocator(matchers, "jira-work", "CASH-1245")
	if refusal != "" || ref.Instance != "jira-work" || ref.Matcher != "issue-key" || ref.Locator != "CASH-1245" {
		t.Fatalf("reference = %#v refusal=%q", ref, refusal)
	}
}

func TestReferenceForLocatorDeclinesUnclaimedLocator(t *testing.T) {
	matchers := []terminallink.ResourceMatcher{{Provider: "jira-work", ID: "issue-key", Re: regexp.MustCompile(`CASH-[0-9]+`)}}
	if _, refusal := ReferenceForLocator(matchers, "jira-work", "prefix CASH-1245"); refusal == "" {
		t.Fatal("a partial match claimed the locator")
	}
	if _, refusal := ReferenceForLocator(matchers, "missing", "CASH-1245"); refusal != "provider missing has no live matchers" {
		t.Fatalf("missing-provider refusal = %q", refusal)
	}
}
