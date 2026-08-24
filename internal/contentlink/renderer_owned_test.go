package contentlink

import (
	"regexp"
	"strings"
	"testing"
)

// jiraMatchers mirrors an issue-key-shaped provider instance: its pattern can
// never match a whole browse URL, so yielding on the destination is impossible
// and the label is the only thing that can carry the key.
func jiraMatchers(claimHosts ...string) []ResourceMatcher {
	return []ResourceMatcher{{
		Provider:   "jira-work",
		ID:         "issue-key",
		Re:         regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]+\b`),
		ClaimHosts: claimHosts,
	}}
}

// markdownLink is the shape internal/markdown emits for `[label](dest)`: an
// OSC-8 open carrying Glamour's session-scoped id= parameter, the label, and a
// close. Real Glamour terminates with BEL and uses a numeric id; the ST
// terminator is used here so both terminators stay covered, with the BEL form
// exercised end-to-end against the live renderer in
// docview.TestScanContentLinksYieldsMarkdownLinkLabelsOnlyWhenRendered.
func markdownLink(label, dest string) string {
	return "\x1b]8;id=13-1;" + dest + "\x1b\\" + label + "\x1b]8;;\x1b\\"
}

const jiraBrowseURL = "https://avalara.atlassian.net/browse/ZMS-37161"

// The plan's motivating case: a Jira key that only ever appears as the label of
// its own browse URL becomes a resource span on a renderer-owned frame.
func TestRendererOwnedFrameYieldsOnTheRenderedLabel(t *testing.T) {
	result := ScanFrame(markdownLink("ZMS-37161", jiraBrowseURL), FrameOptions{
		Matchers:      jiraMatchers("avalara.atlassian.net"),
		RendererOwned: true,
	})
	got := resourceSpans(result.Spans)
	if len(got) != 1 {
		t.Fatalf("want 1 resource span, got %+v", result.Spans)
	}
	if got[0].Value != "ZMS-37161" {
		t.Errorf("locator = %q, want the label the provider is invoked with", got[0].Value)
	}
	if got[0].Extra.Provider != "jira-work" || got[0].Extra.Matcher != "issue-key" {
		t.Errorf("reference = {%q,%q}, want {jira-work,issue-key}", got[0].Extra.Provider, got[0].Extra.Matcher)
	}
	if got[0].StartCol != 0 || got[0].EndCol != len("ZMS-37161")-1 {
		t.Errorf("cols = %d..%d, want the label's own cells 0..%d",
			got[0].StartCol, got[0].EndCol, len("ZMS-37161")-1)
	}
}

// The same bytes from a PTY keep today's rule: a terminal can lie about where
// its label points, so its explicit destination is never reclassified.
func TestTerminalFrameNeverYieldsAnExplicitSpan(t *testing.T) {
	result := ScanFrame(markdownLink("ZMS-37161", jiraBrowseURL), FrameOptions{
		Matchers: jiraMatchers("avalara.atlassian.net"),
	})
	if len(result.Spans) != 1 || result.Spans[0].Kind != KindURL || !result.Spans[0].Explicit {
		t.Fatalf("terminal frame yielded an explicit span: %+v", result.Spans)
	}
	if result.Spans[0].Value != jiraBrowseURL {
		t.Errorf("destination = %q, want the browser link untouched", result.Spans[0].Value)
	}
}

// The destination branch is unchanged in spirit: sidecar-github's whole-URL
// match behaves in Markdown exactly as it does in a terminal, and the locator
// stays the URL rather than becoming the prose label.
func TestRendererOwnedFrameStillYieldsOnTheWholeDestination(t *testing.T) {
	const dest = "https://github.com/marcus/sidecar/pull/88"
	result := ScanFrame(markdownLink("the sidecar PR", dest), FrameOptions{
		Matchers:      claimingMatchers(),
		RendererOwned: true,
	})
	got := resourceSpans(result.Spans)
	if len(got) != 1 {
		t.Fatalf("want 1 resource span, got %+v", result.Spans)
	}
	if got[0].Value != dest {
		t.Errorf("locator = %q, want the destination URL", got[0].Value)
	}
	if got[0].Extra.Provider != "github" || got[0].Extra.Matcher != "github-url" {
		t.Errorf("reference = {%q,%q}, want {github,github-url}", got[0].Extra.Provider, got[0].Extra.Matcher)
	}
}

// Condition 2 still bites on both sides: a claimed host whose label and whose
// destination both fail to match wholly keeps the browser link.
func TestRendererOwnedFrameKeepsBrowserLinkWhenNeitherSideMatches(t *testing.T) {
	const dest = "https://avalara.atlassian.net/wiki/spaces/ENG/overview"
	result := ScanFrame(markdownLink("the engineering wiki", dest), FrameOptions{
		Matchers:      jiraMatchers("avalara.atlassian.net"),
		RendererOwned: true,
	})
	if len(result.Spans) != 1 || result.Spans[0].Kind != KindURL {
		t.Fatalf("claimed host with no whole match was rewritten: %+v", result.Spans)
	}
}

// Condition 1 is untouched: the host gate reads the destination, so a label a
// provider would love cannot pull a URL off a host that instance never listed.
func TestRendererOwnedFrameNeverYieldsOnAnUnclaimedHost(t *testing.T) {
	result := ScanFrame(markdownLink("ZMS-37161", jiraBrowseURL), FrameOptions{
		Matchers:      jiraMatchers("other.example.test"),
		RendererOwned: true,
	})
	if len(result.Spans) != 1 || result.Spans[0].Kind != KindURL {
		t.Fatalf("unclaimed host yielded on its label: %+v", result.Spans)
	}
}

// A destination that is not a safe http(s) URL never becomes a target. The
// label's cells stay claimed and inert so automatic matching cannot promote
// them either.
func TestRendererOwnedFrameLeavesInvalidDestinationsInert(t *testing.T) {
	for name, frame := range map[string]string{
		"unsafe scheme": markdownLink("ZMS-37161", "javascript:alert(1)"),
		"unterminated":  "\x1b]8;id=13-1;" + jiraBrowseURL + "\x1b\\ZMS-37161",
	} {
		t.Run(name, func(t *testing.T) {
			result := ScanFrame(frame, FrameOptions{
				Matchers:      jiraMatchers("avalara.atlassian.net"),
				RendererOwned: true,
			})
			if len(result.Spans) != 0 {
				t.Fatalf("inert claim became activatable: %+v", result.Spans)
			}
		})
	}
}

// A label is a locator candidate only within the same bound every other
// locator obeys.
func TestRendererOwnedFrameBoundsTheLabelLocator(t *testing.T) {
	label := strings.Repeat("A", MaxResourceLocatorChars+1)
	result := ScanFrame(markdownLink(label, "https://claimed.example.test/x"), FrameOptions{
		Matchers: []ResourceMatcher{{
			Provider: "greedy", ID: "all", Re: regexp.MustCompile(`A+`),
			ClaimHosts: []string{"claimed.example.test"},
		}},
		RendererOwned: true,
	})
	if len(result.Spans) != 1 || result.Spans[0].Kind != KindURL {
		t.Fatalf("oversized label became a locator: %+v", result.Spans)
	}
}

// Glamour prints the destination as visible text on the row after the label,
// wrapped in its own OSC-8 pair. That row is a claimed explicit span too, so it
// has to reach the provider by the destination branch.
func TestRendererOwnedFrameYieldsGlamoursPrintedDestinationRow(t *testing.T) {
	const dest = "https://github.com/marcus/sidecar/pull/88"
	result := ScanFrame(markdownLink("the sidecar PR", dest)+"\n"+markdownLink(dest, dest), FrameOptions{
		Matchers:      claimingMatchers(),
		RendererOwned: true,
	})
	got := resourceSpans(result.Spans)
	if len(got) != 2 {
		t.Fatalf("want the label and the printed URL to both claim, got %+v", result.Spans)
	}
	if got[0].Row != 0 || got[1].Row != 1 {
		t.Errorf("rows = %d,%d, want 0,1", got[0].Row, got[1].Row)
	}
}

// Taking a link over must not remove the browser escape hatch it had. A label
// claim keeps the destination so decoration still synthesizes the emulator
// hyperlink over the label's cells — cmd-click reaches the ticket in a browser
// exactly as it did before the provider claimed it.
func TestRendererOwnedLabelClaimKeepsTheEmulatorHyperlink(t *testing.T) {
	result := ScanFrame(markdownLink("ZMS-37161", jiraBrowseURL), FrameOptions{
		Matchers:      jiraMatchers("avalara.atlassian.net"),
		RendererOwned: true,
		Decorate:      true,
	})
	got := resourceSpans(result.Spans)
	if len(got) != 1 {
		t.Fatalf("want 1 resource span, got %+v", result.Spans)
	}
	if got[0].Extra.Destination != jiraBrowseURL {
		t.Errorf("destination = %q, want the URL the label was claimed away from", got[0].Extra.Destination)
	}
	if !strings.Contains(result.Output, "\x1b]8;;"+jiraBrowseURL+"\x1b\\") {
		t.Errorf("claimed label lost its OSC-8 hyperlink: %q", result.Output)
	}
	if n := strings.Count(result.Output, "]8;;"); n != 2 {
		t.Errorf("OSC-8 pair count = %d, want exactly one synthesized pair: %q", n, result.Output)
	}
}

// A destination-branch claim keeps the URL as its locator, so it has no
// separate destination to remember.
func TestRendererOwnedDestinationClaimCarriesNoSeparateDestination(t *testing.T) {
	const dest = "https://github.com/marcus/sidecar/pull/88"
	result := ScanFrame(markdownLink("the sidecar PR", dest), FrameOptions{
		Matchers:      claimingMatchers(),
		RendererOwned: true,
	})
	got := resourceSpans(result.Spans)
	if len(got) != 1 || got[0].Extra.Destination != "" {
		t.Fatalf("destination-branch span = %+v, want an empty Extra.Destination", result.Spans)
	}
}

// A label clipped by the rendered-column bound must not whole-match on the tail
// nobody can see.
func TestRendererOwnedFrameDropsTheLabelOfAColumnClippedSpan(t *testing.T) {
	pad := strings.Repeat("x", MaxRenderedColumns-4)
	frame := pad + markdownLink("ZMS-37161", jiraBrowseURL)
	result := ScanFrame(frame, FrameOptions{
		Matchers:      jiraMatchers("avalara.atlassian.net"),
		RendererOwned: true,
	})
	if got := resourceSpans(result.Spans); len(got) != 0 {
		t.Fatalf("clipped label still claimed: %+v", got)
	}
}
