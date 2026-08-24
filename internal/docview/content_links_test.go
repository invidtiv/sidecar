package docview

import (
	"regexp"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/marcus/sidecar/internal/contentlink"
	"github.com/marcus/sidecar/internal/markdown"
)

func TestContentLinkRectExcludesGutterAndMatchesVisibleSourceRows(t *testing.T) {
	m := newSelectableModel(t, 40, 4, "td-22f35f", "README.md", "abcdef0 extra")
	rect := m.ContentLinkRect()
	gutter := m.display().gutterWidth
	if rect.X != selectionOriginX+gutter {
		t.Fatalf("rect.X = %d, want origin+gutter %d", rect.X, selectionOriginX+gutter)
	}
	if rect.Y != selectionOriginY {
		t.Fatalf("rect.Y = %d, want origin %d", rect.Y, selectionOriginY)
	}
	if rect.H != 3 {
		t.Fatalf("rect.H = %d, want 3 source rows", rect.H)
	}
	if rect.W != m.contentWidth()-gutter {
		t.Fatalf("rect.W = %d, want content minus gutter %d", rect.W, m.contentWidth()-gutter)
	}
}

func TestScanContentLinksFindsIssueFileAndDiff(t *testing.T) {
	m := newSelectableModel(t, 60, 4, "see README.md then td-22f35f and abcdef0")
	idx := contentlink.NewResolutionIndex(contentlink.MaxPendingResolutions)
	if !idx.Put(
		contentlink.Pending{Kind: contentlink.KindFile, Raw: "README.md"},
		contentlink.Ref{Kind: contentlink.KindFile, Value: "README.md"},
		true,
	) {
		t.Fatal("file resolution was rejected")
	}
	if !idx.Put(
		contentlink.Pending{Kind: contentlink.KindDiff, Raw: "abcdef0"},
		contentlink.Ref{Kind: contentlink.KindDiff, Value: "c:abcdef0"},
		true,
	) {
		t.Fatal("diff resolution was rejected")
	}

	body := m.View()
	frame := m.ScanContentLinks(body, contentlink.FrameOptions{
		Ready:        idx.Snapshot(),
		AllowedKinds: ContentLinkKinds(),
		Decorate:     true,
	})
	if !strings.Contains(frame.Output, "\x1b[4m") {
		t.Fatalf("decorated body has no underline: %q", ansi.Strip(frame.Output))
	}
	got := map[contentlink.Kind]contentlink.Ref{}
	for _, hit := range frame.Hits {
		got[hit.Ref.Kind] = hit.Ref
		if hit.Rect.H != 1 || hit.Rect.W < 1 {
			t.Fatalf("hit rect = %+v", hit.Rect)
		}
		if hit.Rect.X < m.ContentLinkRect().X {
			t.Fatalf("hit started in the gutter: %+v vs %+v", hit.Rect, m.ContentLinkRect())
		}
	}
	if got[contentlink.KindIssue].Value != "td-22f35f" {
		t.Fatalf("issue hit = %+v", got[contentlink.KindIssue])
	}
	if got[contentlink.KindFile].Value != "README.md" {
		t.Fatalf("file hit = %+v", got[contentlink.KindFile])
	}
	if got[contentlink.KindDiff].Value != "c:abcdef0" {
		t.Fatalf("diff hit = %+v", got[contentlink.KindDiff])
	}
}

func TestScanContentLinksOptsOutOfPlaceholders(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(40, 4)
	m.path = "missing.txt"
	m.loading = true
	if m.ContentLinksSafe() {
		t.Fatal("loading placeholder exposed a content-link surface")
	}
	frame := m.ScanContentLinks(m.View(), contentlink.FrameOptions{Decorate: true})
	if len(frame.Hits) != 0 || strings.Contains(frame.Output, "\x1b[4m") {
		t.Fatalf("placeholder was scanned: %+v output=%q", frame.Hits, frame.Output)
	}
}

// End-to-end proof that Glamour emits OSC-8 for a Markdown link and that a
// rendered document is the trust domain where a claiming provider may reclaim
// its own key from the label. The same document shown as raw source is not.
func TestScanContentLinksYieldsMarkdownLinkLabelsOnlyWhenRendered(t *testing.T) {
	const source = "See [ZMS-37161](https://avalara.atlassian.net/browse/ZMS-37161) for the ticket.\n"
	matchers := []contentlink.ResourceMatcher{{
		Provider:   "jira-work",
		ID:         "issue-key",
		Re:         regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]+\b`),
		ClaimHosts: []string{"avalara.atlassian.net"},
	}}
	scan := func(m *Model) []contentlink.Ref {
		frame := m.ScanContentLinks(m.View(), contentlink.FrameOptions{
			Matchers:     matchers,
			AllowedKinds: ContentLinkKinds(),
			Decorate:     true,
		})
		var refs []contentlink.Ref
		for _, hit := range frame.Hits {
			refs = append(refs, hit.Ref)
		}
		return refs
	}

	m := newSelectableModel(t, 80, 8, strings.Split(source, "\n")...)
	m.SetRendered(true)
	rendered := scan(m)
	found := false
	for _, ref := range rendered {
		if ref.Kind == contentlink.KindResource {
			found = true
			if ref.Value != "ZMS-37161" || ref.Provider != "jira-work" || ref.Matcher != "issue-key" {
				t.Fatalf("resource ref = %+v, want the label as the jira-work locator", ref)
			}
		}
	}
	if !found {
		t.Fatalf("rendered Markdown link label did not become a resource: %+v", rendered)
	}

	// Raw source rows are the document's own bytes. A file that writes an OSC-8
	// sequence itself is not Sidecar's renderer, so its explicit destination is
	// still never reclassified.
	raw := newSelectableModel(t, 80, 8,
		"\x1b]8;id=13-1;https://avalara.atlassian.net/browse/ZMS-37161\x1b\\ZMS-37161\x1b]8;;\x1b\\")
	refs := scan(raw)
	if len(refs) != 1 || refs[0].Kind != contentlink.KindURL {
		t.Fatalf("raw source claimed a resource from an explicit destination: %+v", refs)
	}
}

// Below MinWidthForMarkdown the "rendered" view is internal/markdown's
// plain-wrap fallback over the document's own bytes, so an OSC-8 sequence in it
// is authored content and must keep the terminal rule even though m.rendered is
// true. Without the width gate this is the hole that lets a file hand itself a
// renderer-owned label.
func TestScanContentLinksTreatsTheNarrowFallbackAsSourceNotRenderer(t *testing.T) {
	const authored = "\x1b]8;id=13-1;https://avalara.atlassian.net/browse/ZMS-37161\x1b\\ZMS-37161\x1b]8;;\x1b\\"
	matchers := []contentlink.ResourceMatcher{{
		Provider:   "jira-work",
		ID:         "issue-key",
		Re:         regexp.MustCompile(`\b[A-Z][A-Z0-9]+-[0-9]+\b`),
		ClaimHosts: []string{"avalara.atlassian.net"},
	}}

	m := newSelectableModel(t, markdown.MinWidthForMarkdown-1, 6, authored)
	m.SetRendered(true)
	if markdown.RendersMarkdownAt(m.contentWidth()) {
		t.Fatalf("content width %d still renders markdown; widen the gap", m.contentWidth())
	}
	frame := m.ScanContentLinks(m.View(), contentlink.FrameOptions{
		Matchers:     matchers,
		AllowedKinds: ContentLinkKinds(),
		Decorate:     true,
	})
	for _, hit := range frame.Hits {
		if hit.Ref.Kind == contentlink.KindResource {
			t.Fatalf("the narrow plain-wrap fallback claimed a resource from an authored OSC-8: %+v", hit.Ref)
		}
	}
}

// URL yield reclassifies a claimed URL span into KindResource before
// AllowedKinds filtering, so a scanning surface that advertised one kind
// without the other would turn claimed URLs into inert text. Every surface
// shares this set; the pair must move together or someone has to decide what a
// claimed URL means on that surface first.
func TestContentLinkKindsAlwaysPairURLWithResource(t *testing.T) {
	kinds := ContentLinkKinds()
	if !kinds.Allows(contentlink.KindURL) || !kinds.Allows(contentlink.KindResource) {
		t.Fatalf("ContentLinkKinds = %+v, want url and resource together (claimed URLs are resource spans)", kinds)
	}
}
