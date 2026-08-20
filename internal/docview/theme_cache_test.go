package docview

import "testing"

// The rendered markdown and the laid-out rows must both name the markdown
// renderer's style identity, so a live theme change repaints an open document
// without a resize or a reload.
func TestLayoutKeyTracksMarkdownStyle(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(50, 6)
	if !m.SetResult(loadFixture(t, m, "# Heading\n\nbody text\n", 0)) {
		t.Fatal("current result was rejected")
	}
	m.display()

	before := m.layoutBuilds
	m.display()
	if m.layoutBuilds != before {
		t.Fatal("unchanged state still rebuilt the layout")
	}

	// Simulate the theme moving underneath an already-laid-out document.
	m.layoutKey.styleKey = "stale-theme"
	m.display()
	if m.layoutBuilds == before {
		t.Fatal("a style-key change did not rebuild the layout")
	}
	if m.layoutKey.styleKey != m.renderer.StyleKey() {
		t.Fatalf("layout key style = %q, want %q", m.layoutKey.styleKey, m.renderer.StyleKey())
	}
}

func TestRenderedLinesRebuildOnStyleChange(t *testing.T) {
	m := newTestModel(t)
	m.SetSize(50, 6)
	if !m.SetResult(loadFixture(t, m, "# Heading\n\nbody text\n", 0)) {
		t.Fatal("current result was rejected")
	}
	m.display()
	if m.renderStyle == "" {
		t.Fatal("rendered markdown did not record a style key")
	}

	m.renderStyle = "stale-theme"
	m.renderedLines = []string{"stale ansi"}
	m.layoutValid = false
	m.display()
	if len(m.renderedLines) == 1 && m.renderedLines[0] == "stale ansi" {
		t.Fatal("stale rendered markdown survived a style change")
	}
	if m.renderStyle != m.renderer.StyleKey() {
		t.Fatalf("renderStyle = %q, want %q", m.renderStyle, m.renderer.StyleKey())
	}
}
