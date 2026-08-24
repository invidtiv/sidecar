package termpreview

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/terminalperf"
	"github.com/marcus/sidecar/internal/tty"
)

type recordingLinkResolver struct {
	calls int
	fresh []FreshLinkRequest
}

func (r *recordingLinkResolver) Resolve(root string, candidate contentlink.Pending) (contentlink.Ref, bool) {
	r.calls++
	if candidate.Kind == contentlink.KindFile && candidate.Raw == "README.md" {
		return contentlink.Ref{Kind: contentlink.KindFile, Value: root + "/README.md"}, true
	}
	return contentlink.Ref{}, false
}

func (r *recordingLinkResolver) ResolveFresh(request FreshLinkRequest) (contentlink.Ref, bool) {
	r.fresh = append(r.fresh, request)
	return contentlink.Ref{Kind: request.Candidate.Kind, Value: request.Root + "/fresh"}, true
}

func runLinkCoordinatorCmd(t *testing.T, coordinator LinkCoordinator) {
	t.Helper()
	cmd := coordinator.TakeCmd()
	if cmd == nil {
		return
	}
	msg := cmd()
	batch, ok := msg.(tea.BatchMsg)
	if !ok {
		result, ok := msg.(LinkResultMsg)
		if !ok {
			t.Fatalf("link command returned %T", msg)
		}
		coordinator.Apply(result)
		return
	}
	for _, child := range batch {
		result, ok := child().(LinkResultMsg)
		if !ok {
			t.Fatalf("link batch child returned %T", child())
		}
		coordinator.Apply(result)
	}
}

func TestLinkCoordinatorPreparesOnceAndPublishesImmutableReadyState(t *testing.T) {
	resolver := &recordingLinkResolver{}
	coordinator := NewLinkCoordinator(resolver)
	buffer := tty.NewOutputBuffer(20)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: "open README.md"})
	scope := LinkScope{Host: "test", Surface: "primary", Target: "pane", Root: "/repo", Buffer: buffer, AllowedKinds: "*"}
	prepare := LinkPrepare{Scope: scope, Rows: []LinkRow{{AbsoluteLine: 4, Text: "open README.md"}}}

	first := coordinator.Prepare(prepare)
	prepare.Previous = first
	_ = coordinator.Prepare(prepare) // The in-flight candidate must deduplicate.
	runLinkCoordinatorCmd(t, coordinator)
	if resolver.calls != 1 {
		t.Fatalf("resolver calls = %d, want one in-flight request", resolver.calls)
	}

	ready := coordinator.Prepare(prepare)
	styled := "\x1b[41mopen README.md"
	span, ok := ready.SpanAt(styled, 4, len("open "))
	if !ok || span.Kind != contentlink.KindFile || span.Value != "/repo/README.md" {
		t.Fatalf("ready hit = (%+v, %v)", span, ok)
	}
	decorated := ready.Decorate(styled, 4)
	if !strings.Contains(decorated, "\x1b[4mREADME.md\x1b[24m") {
		t.Fatalf("decoration did not use the prepared span: %q", decorated)
	}
	spans := ready.Spans(styled, 4)
	spans[0].Value = "mutated"
	if got, _ := ready.SpanAt(styled, 4, len("open ")); got.Value != "/repo/README.md" {
		t.Fatalf("caller mutated published state: %+v", got)
	}
}

func TestLinkCoordinatorReusesUnchangedRowsAcrossBufferRevisions(t *testing.T) {
	coordinator := NewLinkCoordinator(nil)
	buffer := tty.NewOutputBuffer(20)
	buffer.ApplySnapshot(tty.PaneSnapshot{Output: "visit https://example.test"})
	scope := LinkScope{Host: "test", Surface: "panel:2", Target: "pane", Root: "/repo", Buffer: buffer, AllowedKinds: "*"}
	input := LinkPrepare{Scope: scope, Rows: []LinkRow{{AbsoluteLine: 0, Text: "visit https://example.test"}}}
	first := coordinator.Prepare(input)
	buffer.Update("visit https://example.test\nnew revision")
	input.Previous = first

	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)
	second := coordinator.Prepare(input)
	snapshot := counters.Snapshot()
	if snapshot.RowCacheHits != 1 || snapshot.RowCacheMisses != 0 {
		t.Fatalf("unchanged-row counters = %+v", snapshot)
	}
	if _, ok := second.SpanAt("visit https://example.test", 0, len("visit ")); !ok {
		t.Fatal("reused row lost its prepared URL span")
	}
}

func TestLinkCoordinatorRescansChangedOSCClaimsWithSameVisibleText(t *testing.T) {
	coordinator := NewLinkCoordinator(nil)
	scope := LinkScope{Host: "test", Surface: "primary", Target: "pane", Root: "/repo", AllowedKinds: "*"}
	osc := func(destination string) string {
		return "\x1b]8;;" + destination + "\x1b\\label\x1b]8;;\x1b\\"
	}
	first := coordinator.Prepare(LinkPrepare{Scope: scope, Rows: []LinkRow{{AbsoluteLine: 0, Text: osc("https://one.test")}}})
	second := coordinator.Prepare(LinkPrepare{Scope: scope, Rows: []LinkRow{{AbsoluteLine: 0, Text: osc("https://two.test")}}, Previous: first})
	span, ok := second.SpanAt("label", 0, 0)
	if !ok || span.Value != "https://two.test" {
		t.Fatalf("changed explicit claim reused stale state: (%+v, %v)", span, ok)
	}
}

func TestLinkCoordinatorFreshResolutionUsesActivationRequest(t *testing.T) {
	resolver := &recordingLinkResolver{}
	coordinator := NewLinkCoordinator(resolver)
	request := FreshLinkRequest{Root: "/canonical", RawRoot: "/raw", Candidate: contentlink.Pending{Kind: contentlink.KindFile, Raw: "README.md"}}
	cmd := coordinator.ResolveFresh(request, func(result FreshLinkResult) tea.Msg { return result })
	if cmd == nil {
		t.Fatal("fresh resolution command is nil")
	}
	result, ok := cmd().(FreshLinkResult)
	if !ok || !result.Found || result.Ref.Value != "/canonical/fresh" {
		t.Fatalf("fresh result = (%+v, %v)", result, ok)
	}
	if len(resolver.fresh) != 1 || resolver.fresh[0] != request {
		t.Fatalf("fresh requests = %+v", resolver.fresh)
	}
}

func TestLinkCoordinatorNegativeExpiryMakesNewFileLinkable(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	exists := false
	resolver := LinkResolverFunc(func(root string, candidate contentlink.Pending) (contentlink.Ref, bool) {
		if !exists {
			return contentlink.Ref{}, false
		}
		return contentlink.Ref{Kind: candidate.Kind, Value: root + "/" + candidate.Raw}, true
	})
	index := contentlink.NewResolutionIndexWithClock(8, func() time.Time { return now })
	coordinator := NewLinkCoordinatorWithIndex(resolver, index)
	input := LinkPrepare{
		Scope: LinkScope{Host: "test", Surface: "primary", Target: "pane", Root: "/repo", AllowedKinds: "*"},
		Rows:  []LinkRow{{AbsoluteLine: 0, Text: "README.md"}},
	}
	input.Previous = coordinator.Prepare(input)
	runLinkCoordinatorCmd(t, coordinator)
	negative := coordinator.Prepare(input)
	if _, ok := negative.SpanAt("README.md", 0, 0); ok {
		t.Fatal("negative file result decorated a span")
	}
	exists = true
	input.Previous = negative
	_ = coordinator.Prepare(input)
	if cmd := coordinator.TakeCmd(); cmd != nil {
		t.Fatal("negative result was retried before expiry")
	}
	now = now.Add(2 * time.Second)
	input.Previous = negative
	_ = coordinator.Prepare(input)
	runLinkCoordinatorCmd(t, coordinator)
	ready := coordinator.Prepare(input)
	if span, ok := ready.SpanAt("README.md", 0, 0); !ok || span.Value != "/repo/README.md" {
		t.Fatalf("new file after negative expiry = (%+v, %v)", span, ok)
	}
}

func TestLinkCoordinatorDoesNotCountInFlightDedupeAsCacheHit(t *testing.T) {
	resolver := &recordingLinkResolver{}
	coordinator := NewLinkCoordinator(resolver)
	input := LinkPrepare{
		Scope: LinkScope{Host: "test", Surface: "primary", Target: "pane", Root: "/repo", AllowedKinds: "*"},
		Rows:  []LinkRow{{AbsoluteLine: 0, Text: "README.md"}},
	}
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)
	first := coordinator.Prepare(input)
	input.Previous = first
	_ = coordinator.Prepare(input)
	before := counters.Snapshot()
	if before.ContentLinkResolutionRequests != 1 || before.ContentLinkResolutionCacheHits != 0 {
		t.Fatalf("before result counters = %+v, want 1 request and 0 hits", before)
	}
	runLinkCoordinatorCmd(t, coordinator)
	_ = coordinator.Prepare(input)
	after := counters.Snapshot()
	if after.ContentLinkResolutionRequests != 1 || after.ContentLinkResolutionCacheHits != 1 {
		t.Fatalf("after result counters = %+v, want 1 request and 1 hit", after)
	}
}
