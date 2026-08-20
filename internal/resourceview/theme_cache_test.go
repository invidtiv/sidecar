package resourceview

import (
	"testing"

	"github.com/marcus/sidecar/internal/resource"
)

// The sanitized provider body is rendered through the shared markdown
// renderer, so its cache must name the renderer's theme identity.
func TestBodyRebuildsOnMarkdownStyleChange(t *testing.T) {
	rec := &recorder{}
	m := New(nil, rec.resolver())
	m.SetSize(60, 20)
	m.Load(1, ref("CASH-1"), 0)
	id, gen, _ := rec.last()
	d := doc("CASH-1", "A title")
	d.Body = &resource.Body{Format: resource.FormatMarkdown, Text: "# Heading\n\nbody text\n"}
	if !m.Apply(ResolvedMsg{ModelID: id, Generation: gen, Document: d}) {
		t.Fatal("Apply rejected the answer to its own request")
	}

	if got := m.renderedBody(); len(got) == 0 {
		t.Fatal("expected a rendered body")
	}
	if m.bodyForStyle != m.renderer.StyleKey() {
		t.Fatalf("bodyForStyle = %q, want %q", m.bodyForStyle, m.renderer.StyleKey())
	}

	m.body = []string{"stale ansi"}
	m.bodyForStyle = "stale-theme"
	got := m.renderedBody()
	if len(got) == 1 && got[0] == "stale ansi" {
		t.Fatal("stale body survived a markdown style change")
	}
	if m.bodyForStyle != m.renderer.StyleKey() {
		t.Fatalf("bodyForStyle = %q after rebuild, want %q", m.bodyForStyle, m.renderer.StyleKey())
	}
}
