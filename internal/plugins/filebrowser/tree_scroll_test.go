package filebrowser

import (
	"strings"
	"testing"
)

// A tree that fits the viewport must draw in full from the top, whatever stale
// scroll offset a restored session or a shrunken tree left behind. This is the
// "the files list looks like directories are missing" bug: the pane rendered
// starting at an out-of-range offset and silently hid the top of the list.
func TestFittingTreeDrawsFromTopDespiteStaleOffset(t *testing.T) {
	p := newDropTestPlugin(t) // height 30; the 5-row tree fits comfortably
	p.treeScrollOff = 3       // stale: saved by a session with less room

	out := p.renderView()

	if p.treeScrollOff != 0 {
		t.Fatalf("render left treeScrollOff at %d, want 0 for a tree that fits", p.treeScrollOff)
	}
	for _, node := range p.tree.FlatList {
		if !strings.Contains(out, node.Name) {
			t.Errorf("rendered view is missing %q; the top of the tree was truncated", node.Name)
		}
	}
}

// ensureTreeCursorVisible runs during state restore, before any render, so it
// must drop a stale offset itself rather than do cursor math against it.
func TestEnsureTreeCursorVisibleClampsStaleOffset(t *testing.T) {
	p := newDropTestPlugin(t)
	p.treeScrollOff = 4 // stale
	p.ensureTreeCursorVisible()
	if p.treeScrollOff != 0 {
		t.Errorf("treeScrollOff = %d after ensureTreeCursorVisible on a fitting tree, want 0", p.treeScrollOff)
	}
}

// Scrolling still truncates when the tree overflows the viewport: the clamp
// bounds the offset to [0, len-visible] without pinning it to the top.
func TestClampKeepsUserScrollWhenTreeOverflows(t *testing.T) {
	p := newDropTestPlugin(t)
	p.height = p.tree.Len() + 1 // treeItemRows == Len-3 < Len, so it overflows

	maxOff := p.tree.Len() - p.treeItemRows()
	if maxOff < 1 {
		t.Fatalf("test setup: expected an overflowing tree, maxOff = %d", maxOff)
	}

	p.treeScrollOff = 100 // stale, far past the end
	p.clampTreeScroll()
	if p.treeScrollOff != maxOff {
		t.Errorf("treeScrollOff = %d after clamping past the end, want %d", p.treeScrollOff, maxOff)
	}

	p.treeScrollOff = 1 // a real user scroll inside the range
	p.clampTreeScroll()
	if p.treeScrollOff != 1 {
		t.Errorf("clamp moved an in-range offset to %d, want 1", p.treeScrollOff)
	}

	p.treeScrollOff = -3
	p.clampTreeScroll()
	if p.treeScrollOff != 0 {
		t.Errorf("treeScrollOff = %d after clamping below zero, want 0", p.treeScrollOff)
	}
}
