package termpreview

import (
	"hash/maphash"
	"sort"
	"strings"
	"sync"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/terminalperf"
	"github.com/marcus/sidecar/internal/tty"
	"github.com/marcus/sidecar/internal/ui"
)

var linkRowSeed = maphash.MakeSeed()

// LinkScope is every identity that can change what a terminal row means.
// Revision is deliberately absent: unchanged visible rows survive buffer
// revisions, while Buffer distinguishes a replaced model with reused text.
type LinkScope struct {
	Host              string
	Surface           string
	Target            string
	Root              string
	Buffer            *tty.OutputBuffer
	AllowedKinds      string
	MatcherGeneration uint64
}

type LinkRow struct {
	AbsoluteLine int
	Text         string
}

type LinkPrepare struct {
	Scope    LinkScope
	Rows     []LinkRow
	Allowed  contentlink.KindSet
	Matchers []contentlink.ResourceMatcher
	Previous LinkState
}

type preparedLinkRow struct {
	sourceFingerprint  uint64
	visibleFingerprint uint64
	spans              []contentlink.Span
}

// LinkState is an immutable ready-only set of visible row spans. Decoration
// and hit testing both read this exact value.
type LinkState struct {
	data *linkStateData
}

type linkStateData struct {
	scope      LinkScope
	resolution uint64
	rows       map[int]preparedLinkRow
}

func (s LinkState) Scope() LinkScope {
	if s.data == nil {
		return LinkScope{}
	}
	return s.data.scope
}

func (s LinkState) Decorate(line string, absoluteLine int) string {
	clean := contentlink.StripOSC8(line)
	if s.data == nil {
		return clean
	}
	row, ok := s.data.rows[absoluteLine]
	if !ok || row.visibleFingerprint != visibleLinkFingerprint(line) {
		return clean
	}
	return contentlink.Decorate(clean, row.spans)
}

func (s LinkState) Spans(line string, absoluteLine int) []contentlink.Span {
	if s.data == nil {
		return nil
	}
	row, ok := s.data.rows[absoluteLine]
	if !ok || row.visibleFingerprint != visibleLinkFingerprint(line) {
		return nil
	}
	return append([]contentlink.Span(nil), row.spans...)
}

func (s LinkState) SpanAt(line string, absoluteLine, col int) (contentlink.Span, bool) {
	for _, span := range s.Spans(line, absoluteLine) {
		if col >= span.StartCol && col <= span.EndCol {
			return span, true
		}
	}
	return contentlink.Span{}, false
}

func sourceLinkFingerprint(line string) uint64 {
	return maphash.String(linkRowSeed, ui.ExpandTabs(line, tty.DefaultTabWidth))
}

// visibleLinkFingerprint deliberately ignores presentation-only ANSI state.
// Hosts may carry a background SGR into a row between Prepare and View, but
// that must not invalidate spans whose columns are based on the same visible
// text. The source fingerprint remains exact so OSC claim changes still force
// a rescan on the next accepted update.
func visibleLinkFingerprint(line string) uint64 {
	visible := ansi.Strip(contentlink.StripOSC8(ui.ExpandTabs(line, tty.DefaultTabWidth)))
	return maphash.String(linkRowSeed, visible)
}

type LinkResolver interface {
	Resolve(root string, candidate contentlink.Pending) (contentlink.Ref, bool)
}

type LinkResolverFunc func(root string, candidate contentlink.Pending) (contentlink.Ref, bool)

func (f LinkResolverFunc) Resolve(root string, candidate contentlink.Pending) (contentlink.Ref, bool) {
	return f(root, candidate)
}

type LinkResultMsg struct{ Result contentlink.ResolutionResult }

type FreshLinkRequest struct {
	Root      string // canonical root used when the span was prepared
	RawRoot   string // host root re-canonicalized at activation
	Candidate contentlink.Pending
}

type FreshLinkResult struct {
	Request FreshLinkRequest
	Ref     contentlink.Ref
	Found   bool
}

// LinkCoordinator is the presentation-neutral contract terminal hosts receive.
// The app owns one implementation for the process lifetime.
type LinkCoordinator interface {
	Prepare(LinkPrepare) LinkState
	Apply(LinkResultMsg) (changed, accepted bool)
	TakeCmd() tea.Cmd
	ResolveFresh(FreshLinkRequest, func(FreshLinkResult) tea.Msg) tea.Cmd
}

type linkCoordinator struct {
	mu       sync.Mutex
	index    *contentlink.ResolutionIndex
	resolver LinkResolver
	pending  []contentlink.ResolutionRequest
}

func NewLinkCoordinator(resolver LinkResolver) LinkCoordinator {
	return NewLinkCoordinatorWithIndex(resolver, contentlink.NewResolutionIndex(contentlink.MaxResolutionEntries))
}

func NewLinkCoordinatorWithIndex(resolver LinkResolver, index *contentlink.ResolutionIndex) LinkCoordinator {
	if index == nil {
		index = contentlink.NewResolutionIndex(contentlink.MaxResolutionEntries)
	}
	return &linkCoordinator{index: index, resolver: resolver}
}

func (c *linkCoordinator) Prepare(in LinkPrepare) LinkState {
	ready := c.index.SnapshotForRoot(in.Scope.Root)
	state := LinkState{data: &linkStateData{scope: in.Scope, resolution: ready.Generation(), rows: make(map[int]preparedLinkRow, len(in.Rows))}}
	previousCompatible := in.Previous.data != nil && in.Previous.data.scope == in.Scope && in.Previous.data.resolution == ready.Generation()
	for _, row := range in.Rows {
		text := ui.ExpandTabs(row.Text, tty.DefaultTabWidth)
		sourceFingerprint := sourceLinkFingerprint(text)
		if previousCompatible {
			if old, ok := in.Previous.data.rows[row.AbsoluteLine]; ok && old.sourceFingerprint == sourceFingerprint {
				state.data.rows[row.AbsoluteLine] = old
				terminalperf.Record(terminalperf.RowCacheHit)
				continue
			}
		}
		result := contentlink.ScanFrame(text, contentlink.FrameOptions{
			Ready: ready, Matchers: in.Matchers, AllowedKinds: in.Allowed,
		})
		for range result.ReadyHits {
			terminalperf.Record(terminalperf.ContentLinkResolutionCacheHit)
		}
		spans := append([]contentlink.Span(nil), result.Spans...)
		state.data.rows[row.AbsoluteLine] = preparedLinkRow{
			sourceFingerprint:  sourceFingerprint,
			visibleFingerprint: visibleLinkFingerprint(text),
			spans:              spans,
		}
		terminalperf.Record(terminalperf.RowCacheMiss)
		for _, candidate := range result.Pending {
			request, outcome := c.index.BeginClassified(in.Scope.Root, candidate)
			switch outcome {
			case contentlink.BeginRequested:
				c.mu.Lock()
				c.pending = append(c.pending, request)
				c.mu.Unlock()
				terminalperf.Record(terminalperf.ContentLinkResolutionRequest)
			case contentlink.BeginReady:
				terminalperf.Record(terminalperf.ContentLinkResolutionCacheHit)
			}
		}
	}
	return state
}

func (c *linkCoordinator) Apply(msg LinkResultMsg) (changed, accepted bool) {
	return c.index.Apply(msg.Result)
}

func (c *linkCoordinator) TakeCmd() tea.Cmd {
	c.mu.Lock()
	requests := append([]contentlink.ResolutionRequest(nil), c.pending...)
	c.pending = nil
	c.mu.Unlock()
	if len(requests) == 0 || c.resolver == nil {
		return nil
	}
	// Stable ordering makes focused tests and traces reproducible even when two
	// surfaces discover the same candidates in different update orders.
	sort.SliceStable(requests, func(i, j int) bool {
		a, b := requests[i], requests[j]
		if a.Root != b.Root {
			return a.Root < b.Root
		}
		if a.Candidate.Kind != b.Candidate.Kind {
			return a.Candidate.Kind < b.Candidate.Kind
		}
		return a.Candidate.Raw < b.Candidate.Raw
	})
	cmds := make([]tea.Cmd, 0, len(requests))
	for _, request := range requests {
		request := request
		cmds = append(cmds, func() tea.Msg {
			ref, found := c.resolver.Resolve(request.Root, request.Candidate)
			return LinkResultMsg{Result: contentlink.ResolutionResult{Request: request, Ref: ref, Found: found}}
		})
	}
	return tea.Batch(cmds...)
}

func (c *linkCoordinator) ResolveFresh(request FreshLinkRequest, wrap func(FreshLinkResult) tea.Msg) tea.Cmd {
	if c == nil || c.resolver == nil || wrap == nil || strings.TrimSpace(request.Root) == "" {
		return nil
	}
	return func() tea.Msg {
		var ref contentlink.Ref
		var found bool
		if fresh, ok := c.resolver.(interface {
			ResolveFresh(FreshLinkRequest) (contentlink.Ref, bool)
		}); ok {
			ref, found = fresh.ResolveFresh(request)
		} else {
			ref, found = c.resolver.Resolve(request.Root, request.Candidate)
		}
		return wrap(FreshLinkResult{Request: request, Ref: ref, Found: found})
	}
}

func AllowedKindsKey(set contentlink.KindSet) string {
	if set == nil {
		return "*"
	}
	kinds := make([]string, 0, len(set))
	for kind := range set {
		kinds = append(kinds, string(kind))
	}
	sort.Strings(kinds)
	return strings.Join(kinds, ",")
}
