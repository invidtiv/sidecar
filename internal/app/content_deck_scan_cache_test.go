package app

import (
	"testing"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/terminalperf"
)

// scanCounter installs a fresh probe and reports the primary-surface scan
// counters as deltas, which is how the M0 harness reads them too.
type scanCounter struct {
	counters *terminalperf.Counters
	scans    uint64
	hits     uint64
	requests uint64
}

func newScanCounter(t *testing.T) *scanCounter {
	t.Helper()
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)
	return &scanCounter{counters: counters}
}

// take returns the scans and cache hits recorded since the previous take.
func (c *scanCounter) take() (scans, hits uint64) {
	snapshot := c.counters.Snapshot()
	scans, hits = snapshot.FilesLinkScans-c.scans, snapshot.FilesLinkScanCacheHits-c.hits
	c.scans, c.hits = snapshot.FilesLinkScans, snapshot.FilesLinkScanCacheHits
	return scans, hits
}

func (c *scanCounter) want(t *testing.T, what string, scans, hits uint64) {
	t.Helper()
	gotScans, gotHits := c.take()
	if gotScans != scans || gotHits != hits {
		t.Fatalf("%s: got %d scans / %d cache hits, want %d / %d", what, gotScans, gotHits, scans, hits)
	}
}

// takeRequests returns the resolution requests actually scheduled since the
// previous call. h.queued cannot answer this: the deck appends a live-pane
// reconcile command to it on every render, so its length grows whether or not a
// resolution was scheduled.
func (c *scanCounter) takeRequests() uint64 {
	requests := c.counters.Snapshot().DocumentResolutionRequests
	delta := requests - c.requests
	c.requests = requests
	return delta
}

func (c *scanCounter) wantRequests(t *testing.T, what string, requests uint64) {
	t.Helper()
	if got := c.takeRequests(); got != requests {
		t.Fatalf("%s: scheduled %d resolution requests, want %d", what, got, requests)
	}
}

// scanCacheTestDeck renders once so the deck exists, then hands back the deck
// and a counter zeroed after that first render.
func scanCacheTestDeck(t *testing.T, p *deckHostTestPlugin) (*Model, *appContentDeck, *scanCounter) {
	t.Helper()
	m := appDeckTestModel(t, t.TempDir(), p)
	counter := newScanCounter(t)
	m.renderContent(160, 40)
	h := m.currentContentDeck()
	if h == nil {
		t.Fatal("content deck was not created")
	}
	counter.take()
	counter.takeRequests()
	return m, h, counter
}

func TestAppContentDeckScanCacheHitsOnAnIdenticalFrame(t *testing.T) {
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "td-abcd and https://example.test"}
	m, h, counter := scanCacheTestDeck(t, p)

	first := m.renderContent(160, 40)
	counter.want(t, "first render after warmup", 0, 1)
	firstLinks := append([]appContentLinkHit(nil), h.links...)

	second := m.renderContent(160, 40)
	counter.want(t, "second identical render", 0, 1)

	if first != second {
		t.Fatalf("a cached scan changed the frame:\nfirst  %q\nsecond %q", first, second)
	}
	if len(h.links) != len(firstLinks) || len(h.links) == 0 {
		t.Fatalf("cached scan registered %d links, want the same %d", len(h.links), len(firstLinks))
	}
	for i := range h.links {
		if h.links[i].Ref != firstLinks[i].Ref || h.links[i].Rect != firstLinks[i].Rect {
			t.Fatalf("cached link %d = %+v, want %+v", i, h.links[i], firstLinks[i])
		}
	}
}

func TestAppContentDeckScanCacheMissesWhenARowChanges(t *testing.T) {
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview",
		linkRect: mouse.Rect{W: 40, H: 2}, frame: "td-abcd\nnothing here"}
	m, _, counter := scanCacheTestDeck(t, p)

	m.renderContent(160, 40)
	counter.want(t, "unchanged frame", 0, 1)

	p.frame = "td-abcd\nnow td-beef is here"
	m.renderContent(160, 40)
	counter.want(t, "one row changed", 1, 0)

	m.renderContent(160, 40)
	counter.want(t, "changed frame settles", 0, 1)
}

func TestAppContentDeckScanCacheMissesWhenAResolutionLands(t *testing.T) {
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "missing.go"}
	m, h, counter := scanCacheTestDeck(t, p)

	m.renderContent(160, 40)
	counter.want(t, "unchanged frame", 0, 1)

	// A landing resolution changes what the row draws, so the next render has to
	// scan it again rather than replay an undecorated one.
	if !h.resolution.PutForRoot("/tmp", contentlink.Pending{Kind: contentlink.KindFile, Raw: "missing.go"},
		contentlink.Ref{Kind: contentlink.KindFile, Value: "missing.go"}, true) {
		t.Fatal("the test resolution did not change the ready answer")
	}
	m.renderContent(160, 40)
	counter.want(t, "resolution landed", 1, 0)

	m.renderContent(160, 40)
	counter.want(t, "resolution settles", 0, 1)
}

// The plan proposed keying the cache on the root's whole resolution generation.
// That counter advances for tokens this frame never mentioned, and — because a
// negative file answer expires after two seconds and is then requested again —
// it advances several times a second under a completely still preview. This is
// the deliberate difference: the cache holds while the answers the frame drew
// from say the same thing.
func TestAppContentDeckScanCacheHoldsThroughNegativeChurn(t *testing.T) {
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "missing.go"}
	m, h, counter := scanCacheTestDeck(t, p)
	candidate := contentlink.Pending{Kind: contentlink.KindFile, Raw: "missing.go"}

	// The candidate resolves negatively. Nothing about the row changes: an
	// unresolved token and a known-missing one are both drawn plain.
	h.resolution.PutForRoot("/tmp", candidate, contentlink.Ref{}, false)
	m.renderContent(160, 40)
	counter.want(t, "negative answer landed", 0, 1)
	counter.wantRequests(t, "negative answer landed", 0)

	// The negative expires. Still nothing to redraw, but the candidate has to be
	// chased again or a file created later would never light up.
	h.resolution.Reset()
	m.renderContent(160, 40)
	counter.want(t, "negative expired", 0, 1)
	counter.wantRequests(t, "negative expired", 1)

	// An unrelated token resolving under the same root advances that root's
	// generation and must not cost this frame a scan.
	h.resolution.PutForRoot("/tmp", contentlink.Pending{Kind: contentlink.KindFile, Raw: "elsewhere.go"},
		contentlink.Ref{Kind: contentlink.KindFile, Value: "/tmp/elsewhere.go"}, true)
	m.renderContent(160, 40)
	counter.want(t, "an unrelated token resolved", 0, 1)
}

func TestAppContentDeckScanCacheMissesWhenTheSurfaceRectChanges(t *testing.T) {
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview",
		linkRect: mouse.Rect{W: 40, H: 1}, frame: "td-abcd"}
	m, _, counter := scanCacheTestDeck(t, p)

	m.renderContent(160, 40)
	counter.want(t, "unchanged surface", 0, 1)

	// Same bytes, different rectangle: the tree pane was dragged, so the
	// preview now starts in a different column and the scan is not reusable.
	p.linkRect = mouse.Rect{X: 2, W: 38, H: 1}
	m.renderContent(160, 40)
	counter.want(t, "surface rect moved", 1, 0)

	m.renderContent(160, 40)
	counter.want(t, "moved surface settles", 0, 1)
}

func TestAppContentDeckScanCacheReplaysSpansAtANewOrigin(t *testing.T) {
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview",
		linkRect: mouse.Rect{W: 40, H: 1}, frame: "warm up the deck"}
	_, h, counter := scanCacheTestDeck(t, p)

	// scanPrimary is called directly here because the origin is the one input
	// the cache key deliberately excludes, and driving it through renderContent
	// would move the surface rect at the same time.
	const frame = "see td-abcd here"
	h.links = nil
	fresh := h.scanPrimary(frame, mouse.Rect{X: 4, Y: 2})
	counter.want(t, "fresh scan at the first origin", 1, 0)
	at42 := append([]appContentLinkHit(nil), h.links...)
	if len(at42) == 0 {
		t.Fatal("the fresh scan registered no links")
	}

	h.links = nil
	cached := h.scanPrimary(frame, mouse.Rect{X: 11, Y: 7})
	counter.want(t, "cached scan at a new origin", 0, 1)
	at117 := h.links

	if cached != fresh {
		t.Fatalf("a replayed scan changed the frame:\nfresh  %q\ncached %q", fresh, cached)
	}
	if len(at117) != len(at42) {
		t.Fatalf("replayed %d links, want %d", len(at117), len(at42))
	}
	for i := range at42 {
		want := at42[i]
		want.Rect.X += 11 - 4
		want.Rect.Y += 7 - 2
		if at117[i].Ref != want.Ref || at117[i].Rect != want.Rect {
			t.Fatalf("replayed link %d = %+v, want %+v", i, at117[i], want)
		}
	}
}

func TestAppContentDeckScanCacheQueuesAPendingResolutionExactlyOnce(t *testing.T) {
	p := &deckHostTestPlugin{id: "file-browser", focus: "preview", frame: "missing.go"}
	m, h, counter := scanCacheTestDeck(t, p)

	// The warmup render already scanned this frame and scheduled the one
	// resolution "missing.go" needs.
	if len(h.pending) != 1 {
		t.Fatalf("the scan recorded %d pending candidates, want 1", len(h.pending))
	}

	for i := 0; i < 4; i++ {
		m.renderContent(160, 40)
	}
	counter.want(t, "four cached renders", 0, 4)
	counter.wantRequests(t, "four cached renders", 0)
	if len(h.pending) != 1 {
		t.Fatalf("cached renders grew pending to %d, want 1", len(h.pending))
	}

	// A cached frame still re-offers its candidates, so a resolution that has
	// finished and expired out of the index is picked up again rather than lost.
	h.resolution.Reset()
	m.renderContent(160, 40)
	counter.want(t, "render after the index was reset", 0, 1)
	counter.wantRequests(t, "render after the index was reset", 1)
}
