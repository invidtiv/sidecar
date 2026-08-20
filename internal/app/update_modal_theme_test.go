package app

import (
	"testing"

	"github.com/marcus/sidecar/internal/markdown"
)

// The changelog modal caches its rendered release notes, so the cache must
// name the markdown style it was rendered under.
func TestChangelogModalCacheTracksMarkdownStyle(t *testing.T) {
	m := &Model{width: 100, height: 40, updateChangelog: "# Release\n\nnotes body\n"}
	m.ensureChangelogModal()
	if m.changelogModal == nil {
		t.Fatal("expected a changelog modal")
	}
	want := markdown.CurrentThemeSnapshot().StyleKey()
	if m.changelogModalStyleKey != want {
		t.Fatalf("changelogModalStyleKey = %q, want %q", m.changelogModalStyleKey, want)
	}

	m.changelogRenderedLines = []string{"stale ansi"}
	m.changelogModalStyleKey = "stale-theme"
	m.ensureChangelogModal()
	if len(m.changelogRenderedLines) == 1 && m.changelogRenderedLines[0] == "stale ansi" {
		t.Fatal("stale release notes survived a markdown style change")
	}
	if m.changelogModalStyleKey != want {
		t.Fatalf("changelogModalStyleKey = %q after rebuild, want %q", m.changelogModalStyleKey, want)
	}
}
