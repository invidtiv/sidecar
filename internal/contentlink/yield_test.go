package contentlink

import (
	"regexp"
	"strings"
	"testing"
)

// githubURLPattern mirrors the shape sidecar-github generates from its
// allowlist: unanchored, but only covering allowlisted owner/repo paths under
// github.com. Whole-string matching against it is what keeps everyone else's
// URLs in the browser.
func githubURLPattern(allow string) *regexp.Regexp {
	return regexp.MustCompile(`(?i)(?:https?://)?\b(?:www\.)?github\.com/(?:` + allow + `)/(?:issues|pull)/[1-9][0-9]*\b`)
}

func claimingMatchers() []ResourceMatcher {
	return []ResourceMatcher{{
		Provider:   "github",
		ID:         "github-url",
		Re:         githubURLPattern(`marcus/sidecar`),
		ClaimHosts: []string{"github.com"},
	}}
}

func urlSpans(spans []Span) []Span {
	var out []Span
	for _, s := range spans {
		if s.Kind == KindURL {
			out = append(out, s)
		}
	}
	return out
}

func resourceSpans(spans []Span) []Span {
	var out []Span
	for _, s := range spans {
		if s.Kind == KindResource {
			out = append(out, s)
		}
	}
	return out
}

// A PR URL on a claimed host whose entire string matches the instance's own
// matcher becomes a resource span carrying that instance and matcher ID.
func TestYieldClaimsAListedHostURLThatWhollyMatches(t *testing.T) {
	line := "review https://github.com/marcus/sidecar/pull/88 before landing"
	spans := ScanWith(line, Options{Matchers: claimingMatchers()})
	got := resourceSpans(spans)
	if len(got) != 1 {
		t.Fatalf("want 1 resource span, got %d (%+v)", len(got), spans)
	}
	if got[0].Value != "https://github.com/marcus/sidecar/pull/88" {
		t.Errorf("locator = %q", got[0].Value)
	}
	if got[0].Extra.Provider != "github" || got[0].Extra.Matcher != "github-url" {
		t.Errorf("reference = {%q,%q}, want {github,github-url}", got[0].Extra.Provider, got[0].Extra.Matcher)
	}
	wantStart := strings.Index(line, "https://")
	if got[0].StartCol != wantStart || got[0].EndCol != wantStart+len(got[0].Value)-1 {
		t.Errorf("cols = %d..%d, want %d..%d (reclassification must not move cells)",
			got[0].StartCol, got[0].EndCol, wantStart, wantStart+len(got[0].Value)-1)
	}
}

// A URL on a host nobody listed is untouched even when a greedy pattern would
// swallow it whole.
func TestYieldLeavesUnlistedHostsAlone(t *testing.T) {
	greedy := []ResourceMatcher{{
		Provider: "greedy", ID: "all", Re: regexp.MustCompile(`\S+`),
		ClaimHosts: []string{"example.test"},
	}}
	spans := ScanWith("see https://gitlab.test/x for details", Options{Matchers: greedy})
	got := urlSpans(spans)
	if len(got) != 1 || got[0].Kind != KindURL {
		t.Fatalf("want the URL to stay a URL, got %+v", spans)
	}
}

// The host is listed but the org is not in the instance's allowlist, so no
// matcher from that instance matches the URL at all.
func TestYieldLeavesUnlistedOrgsOnAListedHostAlone(t *testing.T) {
	spans := ScanWith("see https://github.com/random-org/foo/issues/1 for details", Options{Matchers: claimingMatchers()})
	if got := urlSpans(spans); len(got) != 1 {
		t.Fatalf("want the URL to stay a URL, got %+v", spans)
	}
	if got := resourceSpans(spans); len(got) != 0 {
		t.Fatalf("random org was claimed: %+v", got)
	}
}

// A partial match — here the allowlisted prefix of a deeper URL — is not a
// whole-string match, so the browser keeps it.
func TestYieldRequiresTheEntireURLToMatch(t *testing.T) {
	spans := ScanWith("diff at https://github.com/marcus/sidecar/pull/88/files", Options{Matchers: claimingMatchers()})
	if got := urlSpans(spans); len(got) != 1 {
		t.Fatalf("want the URL to stay a URL, got %+v", spans)
	}
	if got := resourceSpans(spans); len(got) != 0 {
		t.Fatalf("a substring match claimed the URL: %+v", got)
	}
}

// Host matching follows URLs, which are case-insensitive.
func TestYieldMatchesClaimedHostsCaseInsensitively(t *testing.T) {
	spans := ScanWith("see HTTPS://GitHub.com/marcus/sidecar/pull/88 for details", Options{Matchers: claimingMatchers()})
	got := resourceSpans(spans)
	if len(got) != 1 {
		t.Fatalf("want 1 resource span, got %+v", spans)
	}
}

// A disabled or removed instance contributes no matchers, so nothing carries
// its claim hosts and its URLs stay browser links. This is exactly what the
// manager publishes when an instance is off.
func TestYieldStopsWhenTheProviderIsAbsent(t *testing.T) {
	spans := ScanWith("review https://github.com/marcus/sidecar/pull/88", Options{})
	if got := urlSpans(spans); len(got) != 1 {
		t.Fatalf("want the URL to stay a URL, got %+v", spans)
	}
	// An instance that claims nothing cannot claim either.
	noClaims := []ResourceMatcher{{
		Provider: "github", ID: "github-url", Re: githubURLPattern(`marcus/sidecar`),
	}}
	if spans := ScanWith("review https://github.com/marcus/sidecar/pull/88", Options{Matchers: noClaims}); len(resourceSpans(spans)) != 0 {
		t.Fatalf("an instance with no claimHosts claimed a URL: %+v", spans)
	}
}

// Two instances may claim the same host; configured-provider order decides,
// which is the outer key of the existing matcher ordering — the snapshot hands
// the scanner an already-sorted list, so iteration is first-wins.
func TestYieldFirstWinsAcrossClaimingInstances(t *testing.T) {
	matchers := []ResourceMatcher{
		{
			Provider:   "configured-first",
			ID:         "url",
			Re:         githubURLPattern(`marcus/sidecar`),
			ClaimHosts: []string{"github.com"},
		},
		{
			Provider:   "configured-second",
			ID:         "url",
			Re:         githubURLPattern(`marcus/sidecar`),
			ClaimHosts: []string{"github.com"},
		},
	}
	spans := ScanWith("see https://github.com/marcus/sidecar/pull/88", Options{Matchers: matchers})
	got := resourceSpans(spans)
	if len(got) != 1 {
		t.Fatalf("want 1 resource span, got %+v", spans)
	}
	if got[0].Extra.Provider != "configured-first" {
		t.Errorf("provider = %q, want configured-first", got[0].Extra.Provider)
	}

	// If the earlier instance cannot whole-match, the next claiming instance
	// gets its turn.
	matchers[0].Re = githubURLPattern(`other-org/repo`)
	spans = ScanWith("see https://github.com/marcus/sidecar/pull/88", Options{Matchers: matchers})
	got = resourceSpans(spans)
	if len(got) != 1 || got[0].Extra.Provider != "configured-second" {
		t.Fatalf("want configured-second to claim when the first cannot, got %+v", got)
	}
}

// An explicit OSC-8 destination means what it says: automatic matching must
// never turn it into a different action, so it stays a browser link even on a
// claimed host.
func TestYieldNeverRewritesExplicitOSCDestinations(t *testing.T) {
	frame := "\x1b]8;;https://github.com/marcus/sidecar/pull/88\x1b\\https://github.com/marcus/sidecar/pull/88\x1b]8;;\x1b\\"
	result := ScanFrame(frame, FrameOptions{Matchers: claimingMatchers(), Decorate: true})
	if len(result.Spans) != 1 {
		t.Fatalf("want 1 span, got %+v", result.Spans)
	}
	if result.Spans[0].Kind != KindURL || !result.Spans[0].Explicit {
		t.Fatalf("explicit URL span was rewritten: %+v", result.Spans[0])
	}
}

// A claimed URL keeps its emulator hyperlink after reclassification: the
// locator is still a browser URL, so cmd-click stays the escape hatch even
// though Sidecar's own click now follows the resource span. Decoration carries
// exactly one OSC-8 pair — the synthesized one, over source-stripped text.
func TestDecorateHyperlinksClaimedResourceURLs(t *testing.T) {
	spans := ScanWith("see https://github.com/marcus/sidecar/pull/88", Options{Matchers: claimingMatchers()})
	got := resourceSpans(spans)
	if len(got) != 1 {
		t.Fatalf("want 1 claimed resource span, got %+v", spans)
	}
	out := Decorate("see https://github.com/marcus/sidecar/pull/88", spans)
	if !strings.Contains(out, "\x1b]8;;https://github.com/marcus/sidecar/pull/88\x1b\\") {
		t.Errorf("claimed resource lost its OSC-8 hyperlink: %q", out)
	}
	if got := strings.Count(out, "]8;;"); got != 2 { // one open + one close
		t.Errorf("OSC-8 pair count = %d, want exactly one pair (%q)", got, out)
	}
}

// Resource locators that are not http(s) URLs stay underline-only; there is no
// browser destination to synthesize for a key or a ref.
func TestDecorateNeverHyperlinksNonHTTPResourceLocators(t *testing.T) {
	spans := ScanWith("CASH-1245", Options{Matchers: []ResourceMatcher{{
		Provider: "jira-work", ID: "issue-key",
		Re: regexp.MustCompile(`CASH-[0-9]+`),
	}}})
	out := Decorate("CASH-1245", spans)
	if !strings.Contains(out, "\x1b[4m") {
		t.Errorf("resource span should be underlined: %q", out)
	}
	if strings.Contains(out, "\x1b]8;;") {
		t.Errorf("a non-http resource locator must not become OSC-8: %q", out)
	}
}

// Explicit destinations are never double-wrapped: ScanFrame strips the source
// OSC first and decoration synthesizes exactly one pair back.
func TestDecorateSynthesizesOneOSCPairOverExplicitSpans(t *testing.T) {
	frame := "\x1b]8;;https://example.test/x\x1b\\https://example.test/x\x1b]8;;\x1b\\"
	result := ScanFrame(frame, FrameOptions{Decorate: true})
	if len(result.Spans) != 1 || result.Spans[0].Kind != KindURL {
		t.Fatalf("want 1 explicit URL span, got %+v", result.Spans)
	}
	if got := strings.Count(result.Output, "]8;;"); got != 2 {
		t.Errorf("OSC-8 pair count = %d, want exactly one synthesized pair: %q", got, result.Output)
	}
}
