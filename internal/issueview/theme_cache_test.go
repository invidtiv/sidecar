package issueview

import "testing"

// Issue rows carry rendered markdown (description, acceptance criteria), so
// they must rebuild when the markdown renderer's theme identity moves.
func TestRowsRebuildOnMarkdownStyleChange(t *testing.T) {
	m := loadedCard(t, sample())
	m.ensureRows()

	if m.buildStyle != m.renderer.StyleKey() {
		t.Fatalf("buildStyle = %q, want %q", m.buildStyle, m.renderer.StyleKey())
	}

	m.buildStyle = "stale-theme"
	m.rows = []row{{kind: rowText, text: "stale ansi", cursor: -1}}
	got := m.ensureRows()
	if len(got) == 1 && got[0].text == "stale ansi" {
		t.Fatal("stale rows survived a markdown style change")
	}
	if m.buildStyle != m.renderer.StyleKey() {
		t.Fatalf("buildStyle = %q after rebuild, want %q", m.buildStyle, m.renderer.StyleKey())
	}
}
