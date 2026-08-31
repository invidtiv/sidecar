package docview

import (
	"encoding/binary"
	"reflect"
	"sort"
	"strings"

	"github.com/cespare/xxhash/v2"
	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/mouse"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/terminalperf"
)

// PrepareOptions names the host-owned inputs to a document frame. Root and
// Resolution are paired: the snapshot must have been obtained with
// ResolutionIndex.SnapshotForRoot for the same root.
type PrepareOptions struct {
	Root               string
	Resolution         contentlink.ResolutionSnapshot
	Matchers           []contentlink.ResourceMatcher
	MatcherGeneration  uint64
	AllowedKinds       contentlink.KindSet
	InternalNamespaces map[string]contentlink.URIOptions
	// NamespaceGeneration must advance when a validator's behavior changes
	// without changing its function identity (for example, a mutable closure).
	NamespaceGeneration uint64
	Decorate            bool
	Links               bool
}

// PreparedFrame is an immutable visible document frame. Its hit rectangles are
// relative to the document body so moving a pane does not invalidate the
// expensive body or link scan.
type PreparedFrame struct{ data *preparedFrameData }

type preparedFrameData struct {
	output  string
	hits    []ContentLinkHit
	pending []contentlink.Pending
}

type preparedFrameKey struct {
	visualRevision      uint64
	width, height       int
	styleKey            string
	presentationStyle   uint64
	root                string
	resolution          uint64
	matcherGeneration   uint64
	matchers            string
	allowedKinds        uint64
	internalNamespaces  uint64
	namespaceGeneration uint64
	decorate            bool
	links               bool
}

// Output returns the already-rendered and, when enabled, decorated body.
func (f PreparedFrame) Output() string {
	if f.data == nil {
		return ""
	}
	return f.data.output
}

// Valid reports whether this value came from PrepareFrame.
func (f PreparedFrame) Valid() bool { return f.data != nil }

// AppendHitsAt replays relative hit descriptions at the body origin. dst is
// reused so hosts can rebuild mutable hit maps without allocating a second
// prepared hit list on every application View.
func (f PreparedFrame) AppendHitsAt(dst []ContentLinkHit, originX, originY int) []ContentLinkHit {
	if f.data == nil {
		return dst
	}
	for _, hit := range f.data.hits {
		hit.Rect.X += originX
		hit.Rect.Y += originY
		dst = append(dst, hit)
	}
	return dst
}

// EachHitAt visits relative hit descriptions at the current body origin
// without allocating an intermediate replay slice.
func (f PreparedFrame) EachHitAt(originX, originY int, yield func(ContentLinkHit)) {
	if f.data == nil || yield == nil {
		return
	}
	for _, hit := range f.data.hits {
		hit.Rect.X += originX
		hit.Rect.Y += originY
		yield(hit)
	}
}

// Pending visits the bounded set of newly discovered file and diff candidates.
// Callers must pass each candidate through ResolutionIndex.BeginClassified;
// cached frames may be read repeatedly while the first request is in flight.
func (f PreparedFrame) Pending(yield func(contentlink.Pending)) {
	if f.data == nil || yield == nil {
		return
	}
	for _, candidate := range f.data.pending {
		yield(candidate)
	}
}

// BeginResolutions passes this frame's unresolved candidates through the
// shared root-aware index and yields only newly accepted work. Re-reading a
// cached frame while work is in flight therefore schedules nothing.
func BeginResolutions(index *contentlink.ResolutionIndex, root string, frame PreparedFrame, yield func(contentlink.ResolutionRequest)) {
	if index == nil || yield == nil {
		return
	}
	frame.Pending(func(candidate contentlink.Pending) {
		request, outcome := index.BeginClassified(root, candidate)
		switch outcome {
		case contentlink.BeginRequested:
			terminalperf.Record(terminalperf.DocumentResolutionRequest)
			yield(request)
		case contentlink.BeginReady:
			terminalperf.Record(terminalperf.DocumentResolutionCacheHit)
		}
	})
}

// PreparedFrame returns the most recently prepared visible frame. Pane hosts
// call PrepareFrame from their layout/SetSize path before consuming this in
// View.
func (m *Model) PreparedFrame() PreparedFrame {
	if m == nil {
		return PreparedFrame{}
	}
	return m.preparedFrame
}

// PrepareFrame builds the visible document and link metadata once per visual
// identity. Pane origin is deliberately absent from the key; hits are replayed
// at the current origin by AppendHitsAt.
func (m *Model) PrepareFrame(opts PrepareOptions) PreparedFrame {
	if m == nil {
		return PreparedFrame{}
	}
	if opts.AllowedKinds == nil {
		opts.AllowedKinds = ContentLinkKinds()
	}
	key := m.currentPreparedFrameKey(opts)
	if m.preparedValid && m.preparedKey == key {
		terminalperf.Record(terminalperf.DocumentFrameCacheHit)
		return m.preparedFrame
	}

	body := m.View()
	// View settles derived selection/search state against the current layout.
	// Record that settled identity so the next unchanged prepare is a hit.
	key = m.currentPreparedFrameKey(opts)
	frame := ContentLinkFrame{Output: body}
	if opts.Links {
		frame = m.scanContentLinksRelative(body, contentlink.FrameOptions{
			Ready:              opts.Resolution,
			Matchers:           opts.Matchers,
			InternalNamespaces: opts.InternalNamespaces,
			AllowedKinds:       opts.AllowedKinds,
			Decorate:           opts.Decorate,
		})
	}
	data := &preparedFrameData{output: frame.Output, hits: frame.Hits, pending: frame.Pending}
	m.preparedKey = key
	m.preparedFrame = PreparedFrame{data: data}
	m.preparedValid = true
	terminalperf.Record(terminalperf.DocumentFrameBuilt)
	return m.preparedFrame
}

func (m *Model) currentPreparedFrameKey(opts PrepareOptions) preparedFrameKey {
	return preparedFrameKey{
		visualRevision:      m.visualRevision,
		width:               m.width,
		height:              m.height,
		styleKey:            m.renderer.StyleKey(),
		presentationStyle:   docPresentationStyleKey(),
		root:                opts.Root,
		resolution:          opts.Resolution.Generation(),
		matcherGeneration:   opts.MatcherGeneration,
		matchers:            matcherKey(opts.MatcherGeneration, opts.Matchers),
		allowedKinds:        kindSetKey(opts.AllowedKinds),
		internalNamespaces:  namespaceKey(opts.InternalNamespaces),
		namespaceGeneration: opts.NamespaceGeneration,
		decorate:            opts.Decorate,
		links:               opts.Links,
	}
}

func kindSetKey(kinds contentlink.KindSet) uint64 {
	var key uint64
	for kind := range kinds {
		switch kind {
		case contentlink.KindURL:
			key |= 1 << 0
		case contentlink.KindFile:
			key |= 1 << 1
		case contentlink.KindIssue:
			key |= 1 << 2
		case contentlink.KindDiff:
			key |= 1 << 3
		case contentlink.KindResource:
			key |= 1 << 4
		case contentlink.KindInternal:
			key |= 1 << 5
		case contentlink.KindSession:
			key |= 1 << 6
		}
	}
	return key
}

func namespaceKey(namespaces map[string]contentlink.URIOptions) uint64 {
	if len(namespaces) == 0 {
		return 0
	}
	names := make([]string, 0, len(namespaces))
	for name := range namespaces {
		names = append(names, name)
	}
	sort.Strings(names)
	h := xxhash.New()
	var pointerBytes [8]byte
	for _, name := range names {
		_, _ = h.WriteString(name)
		_, _ = h.WriteString("\x00")
		opts := namespaces[name]
		keys := make([]string, 0, len(opts.AllowedQuery))
		for key := range opts.AllowedQuery {
			keys = append(keys, key)
		}
		sort.Strings(keys)
		for _, key := range keys {
			_, _ = h.WriteString(key)
			_, _ = h.WriteString("\x01")
		}
		if opts.ValidateID != nil {
			binary.LittleEndian.PutUint64(pointerBytes[:], uint64(reflect.ValueOf(opts.ValidateID).Pointer()))
			_, _ = h.Write(pointerBytes[:])
		}
		_, _ = h.WriteString("\x02")
	}
	return h.Sum64()
}

// docPresentationStyleKey covers colors docview paints after the Markdown
// renderer has produced its rows. Those colors deliberately do not all belong
// to markdown.ThemeSnapshot.StyleKey.
func docPresentationStyleKey() uint64 {
	c := styles.GetCurrentTheme().Colors
	selection := c.SelectionBg
	if selection == "" {
		selection = c.BgTertiary
	}
	track, thumb := c.ScrollbarTrack, c.ScrollbarThumb
	if track == "" {
		track = c.TextSubtle
	}
	if thumb == "" {
		thumb = c.TextMuted
	}
	h := xxhash.New()
	for _, value := range []string{
		selection,
		c.Warning, c.OnWarning,
		c.Primary, c.OnPrimary,
		c.TextPrimary,
		track, thumb,
	} {
		_, _ = h.WriteString(value)
		_, _ = h.WriteString("\x00")
	}
	return h.Sum64()
}

func resourceMatchersKey(matchers []contentlink.ResourceMatcher) string {
	var b strings.Builder
	for _, matcher := range matchers {
		b.WriteString(matcher.Provider)
		b.WriteByte(0)
		b.WriteString(matcher.ID)
		b.WriteByte(0)
		if matcher.Re != nil {
			b.WriteString(matcher.Re.String())
		}
		b.WriteByte(0)
		hosts := append([]string(nil), matcher.ClaimHosts...)
		sort.Strings(hosts)
		b.WriteString(strings.Join(hosts, "\x01"))
		b.WriteByte(0)
	}
	return b.String()
}

func matcherKey(generation uint64, matchers []contentlink.ResourceMatcher) string {
	if generation != 0 {
		return ""
	}
	return resourceMatchersKey(matchers)
}

// relativeContentLinkRect is ContentLinkRect in body-local coordinates.
func (m *Model) relativeContentLinkRect() mouse.Rect {
	rect := m.ContentLinkRect()
	rect.X -= m.originX
	rect.Y -= m.originY
	return rect
}
