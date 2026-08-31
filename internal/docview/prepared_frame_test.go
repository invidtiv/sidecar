package docview

import (
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/markdown"
	"github.com/marcus/sidecar/internal/styles"
	"github.com/marcus/sidecar/internal/terminalperf"
)

func TestPreparedFrameCachesUnchangedBodyAndVisibleLinkScan(t *testing.T) {
	m := newSelectableModel(t, 60, 4, "see README.md and td-22f35f")
	index := contentlink.NewResolutionIndex(8)
	opts := PrepareOptions{
		Root:         "/project",
		Resolution:   index.SnapshotForRoot("/project"),
		AllowedKinds: ContentLinkKinds(),
		Decorate:     true,
		Links:        true,
	}
	counters := &terminalperf.Counters{}
	restore := terminalperf.Install(counters)
	t.Cleanup(restore)

	first := m.PrepareFrame(opts)
	for range 99 {
		if got := m.PrepareFrame(opts); got.data != first.data {
			t.Fatal("unchanged prepare replaced the immutable frame")
		}
	}
	snapshot := counters.Snapshot()
	if snapshot.DocumentFramesBuilt != 1 || snapshot.DocumentFrameCacheHits != 99 || snapshot.DocumentLinkScans != 1 {
		t.Fatalf("100 unchanged prepares = %+v, want one build/scan and 99 hits", snapshot)
	}
}

func TestPreparedFrameInvalidatesVisualAndHostIdentityOnce(t *testing.T) {
	m := newSelectableModel(t, 60, 4, "see README.md and RES-1")
	index := contentlink.NewResolutionIndex(8)
	matcher := contentlink.ResourceMatcher{Provider: "fixture", ID: "resource", Re: regexp.MustCompile(`RES-[0-9]+`)}
	opts := PrepareOptions{Root: "/one", Resolution: index.SnapshotForRoot("/one"), Matchers: []contentlink.ResourceMatcher{matcher}, MatcherGeneration: 1, AllowedKinds: ContentLinkKinds(), Decorate: true, Links: true}

	wantNew := func(name string, change func()) {
		t.Helper()
		before := m.PrepareFrame(opts)
		change()
		after := m.PrepareFrame(opts)
		if after.data == before.data {
			t.Fatalf("%s did not rebuild the prepared frame", name)
		}
		if again := m.PrepareFrame(opts); again.data != after.data {
			t.Fatalf("%s rebuilt more than once", name)
		}
	}

	wantNew("size", func() { m.SetSize(61, 4) })
	wantNew("selection", func() {
		m.HandleSelectionMouse(selectPress(m.contentX(0), selectionOriginY))
		m.HandleSelectionMouse(selectDrag(m.contentX(3), selectionOriginY))
	})
	wantNew("search", m.StartSearch)
	wantNew("style", func() {
		renderer, err := markdown.NewRenderer(markdown.CompactDocument)
		if err != nil {
			t.Fatal(err)
		}
		m.renderer = renderer
	})
	wantNew("root", func() {
		opts.Root = "/two"
		opts.Resolution = index.SnapshotForRoot("/two")
	})
	wantNew("matcher generation", func() { opts.MatcherGeneration++ })

	candidate := contentlink.Pending{Kind: contentlink.KindFile, Raw: "README.md"}
	request, ok := index.Begin("/two", candidate)
	if !ok {
		t.Fatal("resolution request was not accepted")
	}
	if _, accepted := index.Apply(contentlink.ResolutionResult{Request: request, Ref: contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"}, Found: true}); !accepted {
		t.Fatal("resolution result was not accepted")
	}
	wantNew("resolution generation", func() { opts.Resolution = index.SnapshotForRoot("/two") })
}

func TestPreparedFrameReplaysRelativeHitsAtCurrentOrigin(t *testing.T) {
	m := newSelectableModel(t, 60, 4, "open td-22f35f")
	frame := m.PrepareFrame(PrepareOptions{Root: "/project", AllowedKinds: ContentLinkKinds(), Decorate: true, Links: true})
	one := frame.AppendHitsAt(nil, 0, 0)
	two := frame.AppendHitsAt(nil, 17, 9)
	if len(one) != 1 || len(two) != 1 {
		t.Fatalf("replayed hits = %d and %d, want one", len(one), len(two))
	}
	if two[0].Rect.X-one[0].Rect.X != 17 || two[0].Rect.Y-one[0].Rect.Y != 9 {
		t.Fatalf("origin shift = (%d,%d), want (17,9)", two[0].Rect.X-one[0].Rect.X, two[0].Rect.Y-one[0].Rect.Y)
	}
	if m.PrepareFrame(PrepareOptions{Root: "/project", AllowedKinds: ContentLinkKinds(), Decorate: true, Links: true}).data != frame.data {
		t.Fatal("moving hit replay invalidated the prepared frame")
	}
}

func TestPreparedFrameResolutionIsRootAwareAndNegativeExpiryRequeues(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	index := contentlink.NewResolutionIndexWithClock(8, func() time.Time { return now })
	m := newSelectableModel(t, 60, 4, "open missing.go")
	prepare := func(root string) PreparedFrame {
		return m.PrepareFrame(PrepareOptions{Root: root, Resolution: index.SnapshotForRoot(root), AllowedKinds: ContentLinkKinds(), Decorate: true, Links: true})
	}

	var one []contentlink.ResolutionRequest
	docviewFrame := prepare("/one")
	BeginResolutions(index, "/one", docviewFrame, func(request contentlink.ResolutionRequest) { one = append(one, request) })
	BeginResolutions(index, "/one", docviewFrame, func(request contentlink.ResolutionRequest) { one = append(one, request) })
	if len(one) != 1 {
		t.Fatalf("same-root in-flight requests = %d, want one", len(one))
	}
	if _, accepted := index.Apply(contentlink.ResolutionResult{Request: one[0], Found: false}); !accepted {
		t.Fatal("negative result was not accepted")
	}
	var cached int
	BeginResolutions(index, "/one", prepare("/one"), func(contentlink.ResolutionRequest) { cached++ })
	if cached != 0 {
		t.Fatal("fresh negative result scheduled duplicate work")
	}

	var two []contentlink.ResolutionRequest
	BeginResolutions(index, "/two", prepare("/two"), func(request contentlink.ResolutionRequest) { two = append(two, request) })
	if len(two) != 1 || two[0].Root != "/two" {
		t.Fatalf("cross-root request = %+v, want independent /two request", two)
	}

	now = now.Add(3 * time.Second)
	var expired []contentlink.ResolutionRequest
	BeginResolutions(index, "/one", prepare("/one"), func(request contentlink.ResolutionRequest) { expired = append(expired, request) })
	if len(expired) != 1 || expired[0].Token == one[0].Token {
		t.Fatalf("expired negative request = %+v, want one fresh token", expired)
	}
}

func TestPreparedFrameInvalidatesSelectionOnlyPaletteChangeOnce(t *testing.T) {
	useDocviewTestTheme(t)
	m := newSelectableModel(t, 50, 3, "alpha beta")
	m.HandleSelectionMouse(selectPress(m.contentX(0), selectionOriginY))
	m.HandleSelectionMouse(selectDrag(m.contentX(5), selectionOriginY))
	opts := PrepareOptions{Links: false}
	before := m.PrepareFrame(opts)
	markdownKey := m.renderer.StyleKey()

	applyDocviewTestTheme(t, func(c *styles.ColorPalette) { c.SelectionBg = differentTestColor(c.SelectionBg, "#5b214e") })
	if got := m.renderer.StyleKey(); got != markdownKey {
		t.Fatalf("selection-only change moved Markdown key: %q -> %q", markdownKey, got)
	}
	after := m.PrepareFrame(opts)
	if after.data == before.data || after.Output() == before.Output() {
		t.Fatal("selection-only palette change reused the old frame/color")
	}
	if again := m.PrepareFrame(opts); again.data != after.data {
		t.Fatal("selection-only palette change rebuilt more than once")
	}
}

func TestPreparedFrameInvalidatesSearchOnlyPaletteChangeOnce(t *testing.T) {
	useDocviewTestTheme(t)
	m := newSearchModel(t, 50, 4, "alpha alpha")
	typeSearch(m, "alpha")
	pressSearch(m, "enter")
	opts := PrepareOptions{Links: false}
	before := m.PrepareFrame(opts)
	markdownKey := m.renderer.StyleKey()

	applyDocviewTestTheme(t, func(c *styles.ColorPalette) {
		c.Warning = differentTestColor(c.Warning, "#2a9d8f")
		c.OnWarning = differentTestColor(c.OnWarning, "#101010")
	})
	if got := m.renderer.StyleKey(); got != markdownKey {
		t.Fatalf("search-only change moved Markdown key: %q -> %q", markdownKey, got)
	}
	after := m.PrepareFrame(opts)
	if after.data == before.data || after.Output() == before.Output() {
		t.Fatal("search-only palette change reused the old frame/color")
	}
	if again := m.PrepareFrame(opts); again.data != after.data {
		t.Fatal("search-only palette change rebuilt more than once")
	}
}

func TestPreparedFrameInvalidatesScrollbarOnlyPaletteChangeOnce(t *testing.T) {
	useDocviewTestTheme(t)
	m := newSelectableModel(t, 50, 3, "one", "two", "three", "four", "five", "six")
	opts := PrepareOptions{Links: false}
	before := m.PrepareFrame(opts)
	markdownKey := m.renderer.StyleKey()

	applyDocviewTestTheme(t, func(c *styles.ColorPalette) {
		c.ScrollbarTrack = differentTestColor(c.ScrollbarTrack, "#8b3a3a")
		c.ScrollbarThumb = differentTestColor(c.ScrollbarThumb, "#3a8b62")
	})
	if got := m.renderer.StyleKey(); got != markdownKey {
		t.Fatalf("scrollbar-only change moved Markdown key: %q -> %q", markdownKey, got)
	}
	after := m.PrepareFrame(opts)
	if after.data == before.data || after.Output() == before.Output() {
		t.Fatal("scrollbar-only palette change reused the old frame/color")
	}
	if again := m.PrepareFrame(opts); again.data != after.data {
		t.Fatal("scrollbar-only palette change rebuilt more than once")
	}
}

func TestPreparedFrameInvalidatesInternalNamespaceAllowlistOnce(t *testing.T) {
	m := newSelectableModel(t, 70, 3, "open sidecar://note/nt-1?view=full")
	opts := PrepareOptions{
		AllowedKinds: contentlink.NewKindSet(contentlink.KindInternal),
		InternalNamespaces: map[string]contentlink.URIOptions{
			"note": {},
		},
		Decorate: true,
		Links:    true,
	}
	rejected := m.PrepareFrame(opts)
	if len(rejected.data.hits) != 0 {
		t.Fatalf("disallowed query produced hits: %+v", rejected.data.hits)
	}

	opts.InternalNamespaces["note"] = contentlink.URIOptions{AllowedQuery: map[string]struct{}{"view": {}}}
	allowed := m.PrepareFrame(opts)
	if allowed.data == rejected.data || len(allowed.data.hits) != 1 || !strings.Contains(allowed.Output(), "\x1b[4m") {
		t.Fatalf("allowlist change did not rebuild an activatable frame: %+v", allowed.data.hits)
	}
	if again := m.PrepareFrame(opts); again.data != allowed.data {
		t.Fatal("allowlist change rebuilt more than once")
	}
}

func TestPreparedFrameInvalidatesInternalNamespaceValidatorIdentityOnce(t *testing.T) {
	m := newSelectableModel(t, 70, 3, "open sidecar://note/nt-1")
	opts := PrepareOptions{
		AllowedKinds: contentlink.NewKindSet(contentlink.KindInternal),
		InternalNamespaces: map[string]contentlink.URIOptions{
			"note": {ValidateID: func(string) bool { return true }},
		},
		Decorate: true,
		Links:    true,
	}
	allowed := m.PrepareFrame(opts)
	if len(allowed.data.hits) != 1 {
		t.Fatalf("accepting validator produced hits: %+v", allowed.data.hits)
	}

	opts.InternalNamespaces["note"] = contentlink.URIOptions{ValidateID: func(string) bool { return false }}
	rejected := m.PrepareFrame(opts)
	if rejected.data == allowed.data || len(rejected.data.hits) != 0 {
		t.Fatalf("validator identity change did not reject the cached link: %+v", rejected.data.hits)
	}
	if again := m.PrepareFrame(opts); again.data != rejected.data {
		t.Fatal("validator identity change rebuilt more than once")
	}
}

func TestPreparedFrameInvalidatesInternalNamespaceMutableValidatorGenerationOnce(t *testing.T) {
	accept := true
	validator := func(string) bool { return accept }
	m := newSelectableModel(t, 70, 3, "open sidecar://note/nt-1")
	opts := PrepareOptions{
		AllowedKinds: contentlink.NewKindSet(contentlink.KindInternal),
		InternalNamespaces: map[string]contentlink.URIOptions{
			"note": {ValidateID: validator},
		},
		Decorate: true,
		Links:    true,
	}
	allowed := m.PrepareFrame(opts)
	if len(allowed.data.hits) != 1 {
		t.Fatalf("accepting mutable validator produced hits: %+v", allowed.data.hits)
	}

	accept = false
	opts.NamespaceGeneration++
	rejected := m.PrepareFrame(opts)
	if rejected.data == allowed.data || len(rejected.data.hits) != 0 {
		t.Fatalf("validator generation did not reject the cached link: %+v", rejected.data.hits)
	}
	if again := m.PrepareFrame(opts); again.data != rejected.data {
		t.Fatal("validator generation rebuilt more than once")
	}
}

func applyDocviewTestTheme(t *testing.T, mutate func(*styles.ColorPalette)) {
	t.Helper()
	original := styles.GetCurrentTheme()
	t.Cleanup(func() { styles.ApplyThemeColors(original) })
	changed := original
	mutate(&changed.Colors)
	styles.ApplyThemeColors(changed)
}

func useDocviewTestTheme(t *testing.T) {
	t.Helper()
	originalTheme := styles.GetCurrentTheme()
	originalSearchMatch := styles.SearchMatch
	originalSearchMatchCurrent := styles.SearchMatchCurrent
	originalModalTitle := styles.ModalTitle
	originalScrollbarTrack := styles.ScrollbarTrackColor
	originalScrollbarThumb := styles.ScrollbarThumbColor
	t.Cleanup(func() {
		// Applying a theme rebuilds style globals. Restore both the caller's
		// actual theme value and the exact docview-relevant globals it had on
		// entry, including direct test overrides not represented by the theme.
		styles.ApplyThemeColors(originalTheme)
		styles.SearchMatch = originalSearchMatch
		styles.SearchMatchCurrent = originalSearchMatchCurrent
		styles.ModalTitle = originalModalTitle
		styles.ScrollbarTrackColor = originalScrollbarTrack
		styles.ScrollbarThumbColor = originalScrollbarThumb
	})
}

func differentTestColor(current, candidate string) string {
	if strings.EqualFold(current, candidate) {
		return "#7d4e9f"
	}
	return candidate
}
