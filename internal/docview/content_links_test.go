package docview

import (
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/marcus/sidecar/internal/contentlink"
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
