package contentlink

import (
	"fmt"
	"net/url"
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"
)

func TestRefParityAcrossSpanKinds(t *testing.T) {
	refs := []Ref{
		{Kind: KindURL, Value: "https://example.test/x"},
		{Kind: KindFile, Value: "internal/app/model.go", Line: 388},
		{Kind: KindIssue, Value: "td-7be1ec"},
		{Kind: KindDiff, Value: "abc1234"},
		{Kind: KindResource, Value: "CASH-42", Provider: "jira", Matcher: "issue"},
		{Kind: KindInternal, Value: "nt-4jdj4e", Namespace: "note"},
	}
	for _, want := range refs {
		if got := SpanForRef(want).Ref(); got != want {
			t.Errorf("SpanForRef(%+v).Ref() = %+v", want, got)
		}
		if !Activatable(want.Kind) {
			t.Errorf("kind %q is not activatable", want.Kind)
		}
	}
}

func TestParseInternalURIStrictCanonicalForm(t *testing.T) {
	got, err := ParseInternalURI("sidecar://note/nt-%34jdj4e")
	if err != nil {
		t.Fatal(err)
	}
	if got.Ref != (Ref{Kind: KindInternal, Namespace: "note", Value: "nt-4jdj4e"}) || len(got.Query) != 0 {
		t.Fatalf("parsed = %+v", got)
	}

	for _, raw := range []string{
		"SIDECAR://note/nt-1", "sidecar://Note/nt-1", "sidecar://note/", "sidecar:///nt-1",
		"sidecar://note/a/b", "sidecar://note/%2Fetc", "sidecar://note/a%2Fb", "sidecar://note/a%5Cb",
		"sidecar://note/nt-1#fragment", "sidecar://user@note/nt-1", "sidecar://note:7/nt-1",
		"sidecar://note/nt-%ZZ", "sidecar://note/nt-%00", "sidecar://note/nt-1?view=full",
	} {
		if parsed, err := ParseInternalURI(raw); err == nil {
			t.Errorf("ParseInternalURI(%q) accepted %+v", raw, parsed)
		}
	}
}

func TestParseInternalURIQueryAllowlistIsBoundedAndCopied(t *testing.T) {
	opts := URIOptions{AllowedQuery: map[string]struct{}{"view": {}}}
	got, err := ParseInternalURIWith("sidecar://note/nt-1?view=full", opts)
	if err != nil || got.Query.Get("view") != "full" {
		t.Fatalf("parsed=%+v err=%v", got, err)
	}
	got.Query.Set("view", "changed")
	if opts.AllowedQuery == nil {
		t.Fatal("parser mutated options")
	}
	for _, raw := range []string{
		"sidecar://note/nt-1?unknown=x",
		"sidecar://note/nt-1?view=a&view=b",
		"sidecar://note/nt-1?view=" + url.QueryEscape(strings.Repeat("x", MaxInternalQueryValueRunes+1)),
	} {
		if _, err := ParseInternalURIWith(raw, opts); err == nil {
			t.Errorf("query %q accepted", raw)
		}
	}
}

func TestInternalNamespaceValidatorAppliesToPlainAndExplicitLinks(t *testing.T) {
	opts := map[string]URIOptions{"note": {ValidateID: func(id string) bool {
		return strings.HasPrefix(id, "nt-") && len(id) <= 12
	}}}
	plain := ScanFrame("open sidecar://note/nt-4jdj4e", FrameOptions{InternalNamespaces: opts})
	osc := "\x1b]8;;sidecar://note/nt-4jdj4e\x1b\\Release checklist\x1b]8;;\x1b\\"
	explicit := ScanFrame(osc, FrameOptions{InternalNamespaces: opts})
	want := Ref{Kind: KindInternal, Namespace: "note", Value: "nt-4jdj4e"}
	if len(plain.Spans) != 1 || plain.Spans[0].Ref() != want || plain.Spans[0].Explicit {
		t.Fatalf("plain internal span = %+v", plain.Spans)
	}
	if len(explicit.Spans) != 1 || explicit.Spans[0].Ref() != want || !explicit.Spans[0].Explicit {
		t.Fatalf("explicit internal span = %+v", explicit.Spans)
	}
	if explicit.Output != "Release checklist" || strings.Contains(explicit.Output, "sidecar://") {
		t.Fatalf("explicit output leaked destination: %q", explicit.Output)
	}

	for _, raw := range []string{
		"sidecar://note/wrong-4jdj4e",
		"sidecar://note/nt-4jdj4e#part",
		"sidecar://note/nt-%00",
		"sidecar://unknown/nt-4jdj4e",
		"sidecar://note/nt-" + strings.Repeat("x", 20),
	} {
		got := ScanFrame(raw, FrameOptions{InternalNamespaces: opts})
		if len(got.Spans) != 0 {
			t.Errorf("%q became active: %+v", raw, got.Spans)
		}
	}
}

func TestScanFrameExplicitInternalWinsAndPreservesRenderedCoordinates(t *testing.T) {
	open := "\x1b]8;;sidecar://note/nt-4jdj4e\x1b\\"
	close := "\x1b]8;;\x1b\\"
	frame := "\x1b[31m世e\u0301\t\x1b[0m " + open + "td-7be1ec" + close + "\nnext td-abcd"
	got := ScanFrame(frame, FrameOptions{Decorate: true, InternalNamespaces: map[string]URIOptions{"note": {}}})
	if strings.Contains(got.Output, "sidecar://") || strings.Contains(got.Output, open) {
		t.Fatalf("internal OSC escaped into output: %q", got.Output)
	}
	if len(got.Spans) != 2 {
		t.Fatalf("spans = %+v, want explicit internal and automatic issue", got.Spans)
	}
	first := got.Spans[0]
	if !first.Explicit || first.Ref() != (Ref{Kind: KindInternal, Namespace: "note", Value: "nt-4jdj4e"}) {
		t.Fatalf("explicit span = %+v ref=%+v", first, first.Ref())
	}
	wantStart := ansi.StringWidth("世e\u0301\t ")
	if first.Row != 0 || first.StartCol != wantStart || first.EndCol != wantStart+ansi.StringWidth("td-7be1ec")-1 {
		t.Fatalf("explicit coordinates = row %d cols %d..%d", first.Row, first.StartCol, first.EndCol)
	}
	if got.Spans[1].Row != 1 || got.Spans[1].Kind != KindIssue {
		t.Fatalf("second-row span = %+v", got.Spans[1])
	}
	if strings.Count(got.Output, "\x1b[4m") != 2 {
		t.Fatalf("decorated output = %q", got.Output)
	}
}

func TestScanFrameExplicitHTTPIsResynthesizedButForeignAndMalformedAreInert(t *testing.T) {
	osc := func(uri, label, close string) string {
		return "\x1b]8;;" + uri + "\x1b\\" + label + close
	}
	close := "\x1b]8;;\x1b\\"
	frame := osc("https://example.test/x#part", "web", close) + " " +
		osc("javascript:alert(1)", "bad", close) + " " +
		osc("sidecar://note/nt-1", "unterminated", "")
	got := ScanFrame(frame, FrameOptions{Decorate: true, InternalNamespaces: map[string]URIOptions{"note": {}}})
	if len(got.Spans) != 1 || got.Spans[0].Kind != KindURL || !got.Spans[0].Explicit {
		t.Fatalf("spans = %+v", got.Spans)
	}
	if strings.Count(got.Output, "\x1b]8;;") != 2 { // synthesized open and close
		t.Fatalf("safe URL was not uniquely resynthesized: %q", got.Output)
	}
	if strings.Contains(got.Output, "javascript") || strings.Contains(got.Output, "sidecar://") {
		t.Fatalf("unsafe source OSC survived: %q", got.Output)
	}
	if plain := ansi.Strip(got.Output); plain != "web bad unterminated" {
		t.Fatalf("visible labels changed: %q", plain)
	}
}

func TestInvalidExplicitOSCClaimsValidLookingLabelInert(t *testing.T) {
	const httpPrefix = "https://example.test/"
	wrap := func(uri, label string) string {
		return "\x1b]8;;" + uri + "\x1b\\" + label + "\x1b]8;;\x1b\\"
	}
	validLooking := "sidecar://note/nt-4jdj4e https://example.test td-abcd"
	registered := map[string]URIOptions{"note": {ValidateID: func(id string) bool { return id == "nt-4jdj4e" }}}
	for _, tc := range []struct {
		name string
		uri  string
	}{
		{name: "unknown namespace", uri: "sidecar://unknown/nt-4jdj4e"},
		{name: "malformed destination", uri: "https:///missing-host"},
		{name: "one byte over destination boundary", uri: httpPrefix + strings.Repeat("x", MaxExplicitDestinationBytes-len(httpPrefix)+1)},
		{name: "javascript destination", uri: "javascript:alert(1)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := ScanFrame(wrap(tc.uri, validLooking), FrameOptions{Decorate: true, InternalNamespaces: registered})
			if got.Output != validLooking {
				t.Fatalf("visible label changed: %q", got.Output)
			}
			if len(got.Spans) != 0 || len(got.Pending) != 0 {
				t.Fatalf("invalid wrapper label became active: spans=%+v pending=%+v", got.Spans, got.Pending)
			}
		})
	}
}

func TestExplicitHTTPDestinationByteBound(t *testing.T) {
	const prefix = "https://example.test/"
	osc := func(uri string) string {
		return "\x1b]8;;" + uri + "\x1b\\x\x1b]8;;\x1b\\"
	}

	boundary := prefix + strings.Repeat("x", MaxExplicitDestinationBytes-len(prefix))
	got := ScanFrame(osc(boundary), FrameOptions{Decorate: true})
	if len(got.Spans) != 1 || !got.Spans[0].Explicit || got.Spans[0].Value != boundary {
		t.Fatalf("boundary destination was not retained: spans=%+v", got.Spans)
	}
	if !strings.Contains(got.Output, boundary) {
		t.Fatal("boundary destination was not resynthesized")
	}

	for name, uri := range map[string]string{
		"one byte over":                prefix + strings.Repeat("x", MaxExplicitDestinationBytes-len(prefix)+1),
		"4KiB tiny label reproduction": prefix + strings.Repeat("x", 4096),
		"malformed":                    "https:///missing-host",
	} {
		t.Run(name, func(t *testing.T) {
			got := ScanFrame(osc(uri), FrameOptions{Decorate: true})
			if len(got.Spans) != 0 {
				t.Fatalf("destination became active: %+v", got.Spans)
			}
			if got.Output != "x" {
				t.Fatalf("output retained destination/control bytes: len=%d output=%q", len(got.Output), got.Output)
			}
		})
	}
}

func TestDecorateRefusesOversizeExplicitHTTPSpan(t *testing.T) {
	uri := "https://example.test/" + strings.Repeat("x", MaxExplicitDestinationBytes)
	got := Decorate("x", []Span{{Kind: KindURL, Value: uri, StartCol: 0, EndCol: 0, Explicit: true}})
	if got != "x" {
		t.Fatalf("Decorate resynthesized oversize explicit destination: len=%d", len(got))
	}
}

func TestScanFrameReadyOnlyResolutionReturnsBoundedPendingWork(t *testing.T) {
	frame := "open README.md at main.go:12 from abc1234; issue td-abcd"
	first := ScanFrame(frame, FrameOptions{})
	if len(first.Pending) != 3 {
		t.Fatalf("pending = %+v, want two files and one diff", first.Pending)
	}
	if len(first.Spans) != 1 || first.Spans[0].Kind != KindIssue {
		t.Fatalf("unready candidates became links: %+v", first.Spans)
	}

	index := NewResolutionIndex(8)
	index.Put(Pending{Kind: KindFile, Raw: "README.md"}, Ref{Kind: KindFile, Value: "README.md"}, true)
	index.Put(Pending{Kind: KindFile, Raw: "main.go"}, Ref{}, false)
	index.Put(Pending{Kind: KindDiff, Raw: "abc1234"}, Ref{Kind: KindDiff, Value: "abc1234"}, true)
	second := ScanFrame(frame, FrameOptions{Ready: index.Snapshot()})
	if len(second.Pending) != 0 {
		t.Fatalf("ready positive/negative results were requeued: %+v", second.Pending)
	}
	if kinds := spanKinds(second.Spans); strings.Join(kinds, ",") != "file,issue,diff" {
		t.Fatalf("resolved kinds = %v spans=%+v", kinds, second.Spans)
	}
	if second.Spans[0].Ref() != (Ref{Kind: KindFile, Value: "README.md"}) {
		t.Fatalf("resolved file ref = %+v", second.Spans[0].Ref())
	}
}

func TestResolutionIndexEvictsOldestAndRejectsMismatchedResults(t *testing.T) {
	index := NewResolutionIndex(2)
	a := Pending{Kind: KindFile, Raw: "a.go"}
	b := Pending{Kind: KindFile, Raw: "b.go"}
	c := Pending{Kind: KindDiff, Raw: "abc1234"}
	if !index.Put(a, Ref{Kind: KindFile, Value: "a.go"}, true) ||
		!index.Put(b, Ref{Kind: KindFile, Value: "b.go"}, true) ||
		!index.Put(c, Ref{Kind: KindDiff, Value: "abc1234"}, true) {
		t.Fatal("valid result refused")
	}
	if _, _, ready := index.Snapshot().Lookup(a.Kind, a.Raw); ready {
		t.Fatal("oldest resolution was not evicted")
	}
	if index.Put(a, Ref{Kind: KindIssue, Value: "td-abcd"}, true) {
		t.Fatal("mismatched resolution kind accepted")
	}
}

func TestScanFrameBoundsRowsResourcesAndPending(t *testing.T) {
	matcher := ResourceMatcher{Provider: "p", ID: "m", Re: regexp.MustCompile(`K-[0-9]+`)}
	line := strings.TrimSpace(strings.Repeat("K-1 ", MaxResourceMatchesPerLine+10))
	got := ScanFrame(line, FrameOptions{Matchers: []ResourceMatcher{matcher}})
	if len(got.Spans) != MaxResourceMatchesPerLine {
		t.Fatalf("resource spans=%d", len(got.Spans))
	}

	rows := strings.Repeat("td-abcd\n", MaxRenderedRows+10)
	got = ScanFrame(rows, FrameOptions{})
	if len(got.Spans) != MaxRenderedRows {
		t.Fatalf("rows produced %d spans, want cap %d", len(got.Spans), MaxRenderedRows)
	}

	files := make([]string, MaxPendingResolutions+10)
	for i := range files {
		files[i] = fmt.Sprintf("file%03d.go", i)
	}
	got = ScanFrame(strings.Join(files, "\n"), FrameOptions{})
	if len(got.Pending) != MaxPendingResolutions {
		t.Fatalf("pending=%d want cap %d", len(got.Pending), MaxPendingResolutions)
	}
}

func TestUnknownInternalNamespaceAndUnterminatedOSCAreVisibleButInert(t *testing.T) {
	close := "\x1b]8;;\x1b\\"
	frame := "\x1b]8;;sidecar://unknown/id\x1b\\label" + close + " tail\x1b]8;;sidecar://note/id"
	got := ScanFrame(frame, FrameOptions{InternalNamespaces: map[string]URIOptions{"note": {}}})
	if len(got.Spans) != 0 {
		t.Fatalf("unknown or unterminated OSC activated: %+v", got.Spans)
	}
	if got.Output != "label tail" {
		t.Fatalf("sanitized output = %q", got.Output)
	}
}

func TestAutomaticOverlapAndTerminalFacadeShape(t *testing.T) {
	resolve := func(raw string) (string, Extra, bool) {
		return raw, Extra{Raw: raw}, raw == "td-abcd.go"
	}
	spans := ScanWith("https://example.test/td-abcd then td-abcd.go and td-abcd", Options{Resolve: resolve})
	if kinds := strings.Join(spanKinds(spans), ","); kinds != "url,file,issue" {
		t.Fatalf("precedence = %s spans=%+v", kinds, spans)
	}
	for i, span := range spans {
		for j, other := range spans {
			if i != j && span.StartCol <= other.EndCol && span.EndCol >= other.StartCol {
				t.Fatalf("overlap: %+v %+v", span, other)
			}
		}
	}
}

func TestSessionRecognitionLivesInSharedScanner(t *testing.T) {
	for _, line := range []string{
		"agent idle in sidecar-sh-repo-1",
		"sidecar-ws-notification-center finished",
		"(sidecar-ws-td-331dbf19) needs review",
	} {
		spans := Scan(line, nil, nil)
		if len(spans) != 1 || spans[0].Kind != KindSession {
			t.Fatalf("%q: spans = %+v", line, spans)
		}
		if !SessionName(spans[0].Value) {
			t.Fatalf("%q: scanner emitted invalid session %q", line, spans[0].Value)
		}
	}

	for _, line := range []string{
		"attach to main",
		"see /tmp/sidecar-sh-repo-1.log",
		"sidecar-tp-repo is internal",
		"my-sidecar-sh-repo-1 is not ours",
	} {
		for _, span := range Scan(line, nil, nil) {
			if span.Kind == KindSession {
				t.Fatalf("%q: invented session %+v", line, span)
			}
		}
	}
}

func TestSessionRecognitionPrecedesContainedIssue(t *testing.T) {
	spans := Scan("sidecar-ws-td-331dbf19 is stuck", nil, nil)
	if len(spans) != 1 || spans[0].Kind != KindSession {
		t.Fatalf("spans = %+v", spans)
	}
}

func spanKinds(spans []Span) []string {
	out := make([]string, len(spans))
	for i, span := range spans {
		out[i] = string(span.Kind)
	}
	return out
}
