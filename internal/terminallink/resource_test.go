package terminallink

import (
	"regexp"
	"strings"
	"testing"
)

func jiraMatchers() []ResourceMatcher {
	return []ResourceMatcher{{
		Provider: "jira-work",
		ID:       "issue-key",
		Re:       regexp.MustCompile(`\b(?:CASH|GRES|AVATAXUI)-[1-9][0-9]*\b`),
	}}
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

func TestScanWithNoMatchersLeavesResourceKeysPlain(t *testing.T) {
	spans := ScanWith("see CASH-1245 for details", Options{})
	if got := resourceSpans(spans); len(got) != 0 {
		t.Fatalf("an unready provider must contribute no span, got %+v", got)
	}
}

func TestScanWithMatchesLocatorAndCarriesTheReference(t *testing.T) {
	line := "agent says CASH-1245 is ready"
	spans := resourceSpans(ScanWith(line, Options{Matchers: jiraMatchers()}))
	if len(spans) != 1 {
		t.Fatalf("want 1 resource span, got %d (%+v)", len(spans), spans)
	}
	got := spans[0]
	if got.Value != "CASH-1245" {
		t.Errorf("locator = %q, want CASH-1245", got.Value)
	}
	if got.Extra.Provider != "jira-work" || got.Extra.Matcher != "issue-key" {
		t.Errorf("reference = {%q,%q}, want {jira-work,issue-key}", got.Extra.Provider, got.Extra.Matcher)
	}
	// The span must cover exactly the key, not the surrounding words.
	wantStart := strings.Index(line, "CASH-1245")
	if got.StartCol != wantStart || got.EndCol != wantStart+len("CASH-1245")-1 {
		t.Errorf("cols = %d..%d, want %d..%d", got.StartCol, got.EndCol, wantStart, wantStart+len("CASH-1245")-1)
	}
}

func TestScanWithBuiltinsKeepPrecedenceOverExternalMatchers(t *testing.T) {
	// A matcher greedy enough to cover a URL must not be able to claim it:
	// built-ins run first and the overlap is first-wins.
	greedy := []ResourceMatcher{{
		Provider: "greedy", ID: "all", Re: regexp.MustCompile(`\S+`),
	}}
	spans := ScanWith("https://example.test/x", Options{Matchers: greedy})
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %+v", spans)
	}
	if spans[0].Kind != KindURL {
		t.Errorf("kind = %q, want the built-in url to win", spans[0].Kind)
	}
}

func TestScanWithExternalMatchersRunInGivenOrderFirstWins(t *testing.T) {
	matchers := []ResourceMatcher{
		{Provider: "first", ID: "a", Re: regexp.MustCompile(`CASH-[0-9]+`)},
		{Provider: "second", ID: "b", Re: regexp.MustCompile(`CASH-[0-9]+`)},
	}
	spans := resourceSpans(ScanWith("CASH-7", Options{Matchers: matchers}))
	if len(spans) != 1 {
		t.Fatalf("an overlapping second matcher must not double-underline: %+v", spans)
	}
	if spans[0].Extra.Provider != "first" {
		t.Errorf("provider = %q, want first", spans[0].Extra.Provider)
	}
}

func TestScanWithBoundsMatchesPerLine(t *testing.T) {
	line := strings.TrimSpace(strings.Repeat("CASH-1 ", MaxResourceMatchesPerLine+10))
	spans := resourceSpans(ScanWith(line, Options{Matchers: jiraMatchers()}))
	if len(spans) != MaxResourceMatchesPerLine {
		t.Fatalf("got %d spans, want the per-line bound %d", len(spans), MaxResourceMatchesPerLine)
	}
}

func TestScanWithRejectsOversizeLocator(t *testing.T) {
	long := "CASH-" + strings.Repeat("1", MaxResourceLocatorChars)
	matchers := []ResourceMatcher{{Provider: "p", ID: "m", Re: regexp.MustCompile(`CASH-[0-9]+`)}}
	if spans := resourceSpans(ScanWith(long, Options{Matchers: matchers})); len(spans) != 0 {
		t.Fatalf("a locator past the bound must be dropped, got %+v", spans)
	}
}

func TestScanWithIgnoresDegenerateMatchers(t *testing.T) {
	matchers := []ResourceMatcher{
		{Provider: "p", ID: "nil-re", Re: nil},
		{Provider: "", ID: "no-provider", Re: regexp.MustCompile(`CASH-1`)},
		{Provider: "p", ID: "", Re: regexp.MustCompile(`CASH-1`)},
		{Provider: "p", ID: "empty", Re: regexp.MustCompile(`x*`)},
	}
	if spans := resourceSpans(ScanWith("CASH-1", Options{Matchers: matchers})); len(spans) != 0 {
		t.Fatalf("degenerate matchers must contribute nothing, got %+v", spans)
	}
}

func TestScanWithStripsANSIBeforeMatchingAndUsesVisualColumns(t *testing.T) {
	// The key is preceded by a wide grapheme, so byte offset and visual column
	// disagree. Hit testing works in columns, so the span must too.
	line := "\x1b[31m世\x1b[0m CASH-9"
	spans := resourceSpans(ScanWith(line, Options{Matchers: jiraMatchers()}))
	if len(spans) != 1 {
		t.Fatalf("want 1 span, got %+v", spans)
	}
	if spans[0].StartCol != 3 {
		t.Errorf("StartCol = %d, want 3 (wide rune is 2 columns plus a space)", spans[0].StartCol)
	}
	if spans[0].Value != "CASH-9" {
		t.Errorf("value = %q, want CASH-9", spans[0].Value)
	}
}

func TestScanWithRejectsLocatorWithControlBytes(t *testing.T) {
	matchers := []ResourceMatcher{{Provider: "p", ID: "m", Re: regexp.MustCompile(`CASH-\x07[0-9]+`)}}
	if spans := resourceSpans(ScanWith("CASH-\x071", Options{Matchers: matchers})); len(spans) != 0 {
		t.Fatalf("a locator carrying a control byte must be dropped, got %+v", spans)
	}
}

func TestDecorateUnderlinesResourceButNeverHyperlinksIt(t *testing.T) {
	line := "CASH-1245"
	spans := ScanWith(line, Options{Matchers: jiraMatchers()})
	out := Decorate(line, spans)
	if !strings.Contains(out, "\x1b[4m") {
		t.Errorf("resource span should be underlined, got %q", out)
	}
	if strings.Contains(out, "\x1b]8;;") {
		t.Errorf("a locator is not a URL and must not become OSC-8, got %q", out)
	}
}

func TestActivatableCoversEveryBoundKind(t *testing.T) {
	for _, k := range []Kind{KindURL, KindFile, KindIssue, KindDiff, KindResource} {
		if !Activatable(k) {
			t.Errorf("kind %q should be activatable", k)
		}
	}
	if Activatable(Kind("nonsense")) {
		t.Error("an unknown kind must not be activatable")
	}
}
